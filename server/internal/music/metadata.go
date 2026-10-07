package music

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/dhowden/tag"
)

const maxEmbeddedLyricsBytes = 2 << 20
const maxMetadataReadBytes = 32 << 20

type audioMetadata struct {
	Title, Artist, Album, Genre, Comment, Lyrics string
	LyricsSource                                 string
	// Cover contains a validated image selected from embedded metadata. The
	// store persists it as a content-addressed asset and exposes only its ID.
	// CoverSource is embedded or external and is useful for diagnostics/UI.
	Cover       []byte
	CoverMIME   string
	CoverSource string
}

const (
	maxEmbeddedCoverBytes = 8 << 20
	maxCoverPixels        = 20_000_000
)

// readAudioMetadata reads tags without decoding audio or invoking external programs.
// Missing or malformed tags are nonfatal to callers, which can use filename defaults.
// The reader's original position is preserved, including after a prior content hash.
func readAudioMetadata(reader io.ReadSeeker) (result audioMetadata, err error) {
	original, err := reader.Seek(0, io.SeekCurrent)
	if err != nil {
		return result, err
	}
	defer func() {
		_, _ = reader.Seek(original, io.SeekStart)
		// Some malformed tag values cause type assertions or bounds checks in the
		// third-party parser to panic. Preserve filename fallback for those files.
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("malformed audio metadata: %v", recovered)
		}
	}()
	length, err := reader.Seek(0, io.SeekEnd)
	if err != nil {
		return result, err
	}
	if _, err = reader.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	bounded := &metadataReader{ReadSeeker: reader, length: length, remaining: maxMetadataReadBytes}
	var signature [8]byte
	if _, signatureErr := io.ReadFull(bounded, signature[:]); signatureErr == nil {
		if string(signature[:4]) == "RIFF" {
			return readWAVMetadata(bounded, length)
		}
		if string(signature[4:]) == "ftyp" {
			if err = validateMP4Atoms(bounded, 0, length, 0); err != nil {
				return result, err
			}
		}
		switch string(signature[:4]) {
		case "fLaC":
			err = validateFLACMetadata(bounded, length)
		case "OggS":
			err = validateOggMetadata(bounded)
		}
		if err != nil {
			return result, err
		}
	}
	if _, err = bounded.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	metadata, err := tag.ReadFrom(bounded)
	if metadata == nil {
		return result, err
	}
	result.Title = cleanMetadataText(metadata.Title())
	result.Artist = cleanMetadataText(metadata.Artist())
	result.Album = cleanMetadataText(metadata.Album())
	result.Genre = cleanMetadataText(metadata.Genre())
	result.Comment = cleanMetadataText(metadata.Comment())
	if picture := metadata.Picture(); picture != nil {
		if data, mime, ok := validateCover(picture.Data, picture.MIMEType); ok {
			result.Cover, result.CoverMIME, result.CoverSource = data, mime, "embedded"
		}
	}
	raw := metadata.Raw()
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	setLyrics := func(value, source string) bool {
		value = cleanMetadataText(value)
		if value == "" || len(value) > maxEmbeddedLyricsBytes {
			return false
		}
		result.Lyrics = strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
		result.LyricsSource = source
		return true
	}
	switch metadata.Format() {
	case tag.ID3v2_2, tag.ID3v2_3, tag.ID3v2_4:
		// COMM's description names the comment; its Text is the actual value.
		// tag.Comment() may otherwise return the description for named comments.
		for _, key := range keys {
			if frameNamed(key, "COMM") || frameNamed(key, "COM") {
				if value := cleanMetadataText(rawMetadataText(raw[key])); value != "" {
					result.Comment = value
					break
				}
			}
		}
		for _, frame := range []string{"USLT", "ULT"} {
			for _, key := range keys {
				if frameNamed(key, frame) && setLyrics(rawMetadataText(raw[key]), "ID3v2/"+frame) {
					return result, err
				}
			}
		}
		for _, key := range keys {
			if frameNamed(key, "SYLT") || frameNamed(key, "SLT") {
				if data, ok := raw[key].([]byte); ok && setLyrics(synchronizedLyrics(data), "ID3v2/"+strings.SplitN(key, "_", 2)[0]) {
					return result, err
				}
			}
		}
		for _, key := range keys {
			if frameNamed(key, "TXXX") || frameNamed(key, "TXX") {
				if text, ok := raw[key].(*tag.Comm); ok && isLyricsField(text.Description) && setLyrics(text.Text, "ID3v2/"+strings.SplitN(key, "_", 2)[0]+":"+text.Description) {
					return result, err
				}
			}
		}
	case tag.VORBIS:
		for _, field := range []string{"LYRICS", "UNSYNCEDLYRICS", "UNSYNCHRONIZEDLYRICS"} {
			for _, key := range keys {
				if strings.EqualFold(key, field) && setLyrics(rawMetadataText(raw[key]), "Vorbis/"+field) {
					return result, err
				}
			}
		}
	case tag.MP4:
		if setLyrics(rawMetadataText(raw["\xa9lyr"]), "MP4/©lyr") {
			return result, err
		}
		for _, key := range keys {
			value := rawMetadataText(raw[key])
			// The pinned tag parser's freeform data includes its four locale bytes.
			if len(value) >= 4 {
				value = value[4:]
			}
			if isLyricsField(key) && setLyrics(value, "MP4/----:com.apple.iTunes:"+key) {
				return result, err
			}
		}
	}
	return result, err
}

// validateCover bounds both the bytes retained in the database and the image
// dimensions. DecodeConfig reads headers only, so a malicious cover cannot
// force a full decoded bitmap into memory. MIME is derived from magic bytes;
// the tag supplied MIME is used only for formats whose signature is known.
func validateCover(data []byte, declaredMIME string) ([]byte, string, bool) {
	if len(data) == 0 || len(data) > maxEmbeddedCoverBytes {
		return nil, "", false
	}
	mime := strings.ToLower(strings.TrimSpace(strings.SplitN(declaredMIME, ";", 2)[0]))
	if detected := http.DetectContentType(data); detected != "application/octet-stream" {
		mime = detected
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		if w, h, ok := webpDimensions(data); ok && int64(w)*int64(h) <= maxCoverPixels {
			return append([]byte(nil), data...), "image/webp", true
		}
		return nil, "", false
	}
	if mime != "image/jpeg" && mime != "image/png" && mime != "image/gif" {
		return nil, "", false
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxCoverPixels {
		return nil, "", false
	}
	return append([]byte(nil), data...), mime, true
}

func webpDimensions(data []byte) (int, int, bool) {
	if len(data) < 30 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0, false
	}
	chunk := 12
	for chunk+8 <= len(data) {
		typ := string(data[chunk : chunk+4])
		size := int(binary.LittleEndian.Uint32(data[chunk+4 : chunk+chunk+8]))
		if size < 0 || chunk+8+size > len(data) {
			return 0, 0, false
		}
		body := data[chunk+8 : chunk+8+size]
		switch typ {
		case "VP8X":
			if len(body) < 10 {
				return 0, 0, false
			}
			w := 1 + int(body[4]) | int(body[5])<<8 | int(body[6])<<16
			h := 1 + int(body[7]) | int(body[8])<<8 | int(body[9])<<16
			return w, h, true
		case "VP8 ":
			if len(body) < 10 || body[3] != 0x9d || body[4] != 0x01 || body[5] != 0x2a {
				return 0, 0, false
			}
			return int(binary.LittleEndian.Uint16(body[6:8]) & 0x3fff), int(binary.LittleEndian.Uint16(body[8:10]) & 0x3fff), true
		case "VP8L":
			if len(body) < 5 || body[0] != 0x2f {
				return 0, 0, false
			}
			w := 1 + int(body[1]) | int(body[2]&0x3f)<<8
			h := 1 + int(body[2]>>6) | int(body[3])<<2 | int(body[4]&0xf)<<10
			return w, h, true
		}
		chunk += 8 + size + size%2
	}
	return 0, 0, false
}

// WAV stores ID3v2 in a RIFF chunk, often after a large audio payload. Seek over
// other chunks instead of loading or decoding audio; RIFF sizes include padding.
func readWAVMetadata(reader io.ReadSeeker, length int64) (audioMetadata, error) {
	var result audioMetadata
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	var header [12]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return result, err
	}
	if string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" {
		return result, errors.New("unsupported RIFF audio container")
	}
	end := int64(binary.LittleEndian.Uint32(header[4:8])) + 8
	if end < 12 || end > length {
		return result, errors.New("invalid WAV RIFF size")
	}
	for position := int64(12); position < end; {
		if end-position < 8 {
			return result, errors.New("truncated WAV chunk header")
		}
		if _, err := reader.Seek(position, io.SeekStart); err != nil {
			return result, err
		}
		var chunk [8]byte
		if _, err := io.ReadFull(reader, chunk[:]); err != nil {
			return result, err
		}
		size := int64(binary.LittleEndian.Uint32(chunk[4:]))
		paddedSize := size + size%2
		if paddedSize > end-position-8 {
			return result, errors.New("invalid WAV chunk size")
		}
		if strings.EqualFold(string(chunk[:4]), "id3 ") {
			if size < 10 || size > maxMetadataReadBytes {
				return result, errors.New("invalid or oversized WAV ID3 chunk")
			}
			data := make([]byte, size)
			if _, err := io.ReadFull(reader, data); err != nil {
				return result, err
			}
			if string(data[:3]) != "ID3" {
				return result, errors.New("invalid WAV ID3 chunk signature")
			}
			result, err := readAudioMetadata(bytes.NewReader(data))
			if result.LyricsSource != "" {
				result.LyricsSource = "WAV/" + result.LyricsSource
			}
			return result, err
		}
		position += 8 + paddedSize
	}
	return result, tag.ErrNoTagsFound
}

// The tag library allocates FLAC pictures using their internal data length
// before reading. Validate both native and base64 pictures before that allocation.
func validateFLACMetadata(reader io.ReadSeeker, length int64) error {
	for position := int64(4); ; {
		if _, err := reader.Seek(position, io.SeekStart); err != nil {
			return err
		}
		var header [4]byte
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			return err
		}
		start := position + 4
		size := int64(header[1])<<16 | int64(header[2])<<8 | int64(header[3])
		if size > length-start {
			return errors.New("FLAC metadata block outside file")
		}
		end := start + size
		switch header[0] & 0x7f {
		case 4:
			if err := validateVorbisComments(reader, start, end, true); err != nil {
				return err
			}
		case 6:
			if err := validatePictureBlock(reader, start, end); err != nil {
				return err
			}
		}
		if header[0]&0x80 != 0 {
			return nil
		}
		position = end
	}
}

func readMetadataUint32(reader io.ReadSeeker, end int64, order binary.ByteOrder) (uint32, error) {
	position, err := reader.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	if end-position < 4 {
		return 0, errors.New("truncated audio metadata length")
	}
	var data [4]byte
	if _, err := io.ReadFull(reader, data[:]); err != nil {
		return 0, err
	}
	return order.Uint32(data[:]), nil
}

func skipMetadataBytes(reader io.ReadSeeker, end, size int64) error {
	position, err := reader.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if size < 0 || size > end-position {
		return errors.New("audio metadata field outside block")
	}
	_, err = reader.Seek(position+size, io.SeekStart)
	return err
}

func validatePictureBlock(reader io.ReadSeeker, start, end int64) error {
	if _, err := reader.Seek(start, io.SeekStart); err != nil {
		return err
	}
	if _, err := readMetadataUint32(reader, end, binary.BigEndian); err != nil { // Picture type.
		return err
	}
	for field := 0; field < 2; field++ { // MIME type and description.
		size, err := readMetadataUint32(reader, end, binary.BigEndian)
		if err != nil {
			return err
		}
		if err := skipMetadataBytes(reader, end, int64(size)); err != nil {
			return err
		}
	}
	if err := skipMetadataBytes(reader, end, 16); err != nil { // Dimensions, depth, colors.
		return err
	}
	size, err := readMetadataUint32(reader, end, binary.BigEndian)
	if err != nil {
		return err
	}
	position, err := reader.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if int64(size) != end-position || size > maxMetadataReadBytes {
		return errors.New("invalid or oversized embedded picture length")
	}
	return skipMetadataBytes(reader, end, int64(size))
}

func validateVorbisComments(reader io.ReadSeeker, start, end int64, exact bool) error {
	if _, err := reader.Seek(start, io.SeekStart); err != nil {
		return err
	}
	vendorSize, err := readMetadataUint32(reader, end, binary.LittleEndian)
	if err != nil {
		return err
	}
	if err := skipMetadataBytes(reader, end, int64(vendorSize)); err != nil {
		return err
	}
	count, err := readMetadataUint32(reader, end, binary.LittleEndian)
	if err != nil {
		return err
	}
	position, err := reader.Seek(0, io.SeekCurrent)
	if err != nil || int64(count) > (end-position)/4 {
		return errors.New("invalid Vorbis comment count")
	}
	const pictureKey = "metadata_block_picture"
	for index := uint32(0); index < count; index++ {
		size, err := readMetadataUint32(reader, end, binary.LittleEndian)
		if err != nil {
			return err
		}
		position, err := reader.Seek(0, io.SeekCurrent)
		if err != nil || int64(size) > end-position {
			return errors.New("Vorbis comment outside block")
		}
		// Match the library's Unicode strings.ToLower, including Kelvin-sign K.
		// A key that lowers to this ASCII name has at most len(key) UTF-8 runes.
		var prefix [4*len(pictureKey) + 1]byte
		probeSize := min(int(size), len(prefix))
		if _, err := io.ReadFull(reader, prefix[:probeSize]); err != nil {
			return err
		}
		remaining := int64(size) - int64(probeSize)
		separator := bytes.IndexByte(prefix[:probeSize], '=')
		if separator < 0 || strings.ToLower(string(prefix[:separator])) != pictureKey {
			if err := skipMetadataBytes(reader, end, remaining); err != nil {
				return err
			}
			continue
		}
		valueSize := int64(size) - int64(separator+1)
		if valueSize > maxMetadataReadBytes {
			return errors.New("oversized base64 picture metadata")
		}
		encoded := make([]byte, valueSize)
		copied := copy(encoded, prefix[separator+1:probeSize])
		if _, err := io.ReadFull(reader, encoded[copied:]); err != nil {
			return err
		}
		picture, err := base64.StdEncoding.DecodeString(string(encoded))
		if err != nil {
			return err
		}
		if err := validatePictureBlock(bytes.NewReader(picture), 0, int64(len(picture))); err != nil {
			return err
		}
	}
	position, err = reader.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if exact && position != end {
		return errors.New("FLAC Vorbis block has trailing data")
	}
	return nil
}

// Reassemble only enough Ogg packets to validate comments. The library checks
// page CRCs afterward; the shared reader budget bounds pages and packet buffers.
func validateOggMetadata(reader io.ReadSeeker) error {
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return err
	}
	packets := make(map[uint32]*bytes.Buffer)
	for {
		var header [27]byte
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			return err
		}
		if string(header[:4]) != "OggS" || header[4] != 0 {
			return errors.New("invalid Ogg page header")
		}
		segments := make([]byte, int(header[26]))
		if _, err := io.ReadFull(reader, segments); err != nil {
			return err
		}
		size := 0
		for _, segment := range segments {
			size += int(segment)
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(reader, data); err != nil {
			return err
		}
		serial := binary.LittleEndian.Uint32(header[14:18])
		packet := packets[serial]
		if header[5]&1 == 0 {
			packet = &bytes.Buffer{}
		} else if packet == nil {
			return errors.New("Ogg continuation without packet")
		}
		position := 0
		for _, segment := range segments {
			packet.Write(data[position : position+int(segment)])
			position += int(segment)
			if segment == 255 {
				continue
			}
			contents := packet.Bytes()
			for _, prefix := range []string{"\x03vorbis", "OpusTags"} {
				if bytes.HasPrefix(contents, []byte(prefix)) {
					return validateVorbisComments(bytes.NewReader(contents), int64(len(prefix)), int64(len(contents)), false)
				}
			}
			packet = &bytes.Buffer{}
		}
		if _, exists := packets[serial]; !exists && len(packets) >= 128 {
			return errors.New("Ogg logical stream limit exceeded")
		}
		packets[serial] = packet
	}
}

// Validate container bounds before handing atoms to the library, whose legacy
// parser treats malformed container sizes as an ordinary EOF. Media payloads
// are skipped; unsupported extended-size atoms fall back to filename metadata.
func validateMP4Atoms(reader io.ReadSeeker, start, end int64, depth int) error {
	if depth > 16 {
		return errors.New("MP4 metadata nesting limit exceeded")
	}
	for position := start; position < end; {
		if end-position < 8 {
			return errors.New("truncated MP4 atom header")
		}
		if _, err := reader.Seek(position, io.SeekStart); err != nil {
			return err
		}
		var header [8]byte
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			return err
		}
		size := int64(binary.BigEndian.Uint32(header[:4]))
		if size < 8 || size > end-position {
			return errors.New("invalid or unsupported MP4 atom size")
		}
		bodyStart := position + 8
		switch string(header[4:]) {
		case "meta":
			bodyStart += 4 // Full-box version and flags.
			fallthrough
		case "moov", "udta", "ilst", "----":
			if bodyStart > position+size {
				return errors.New("truncated MP4 metadata container")
			}
			if err := validateMP4Atoms(reader, bodyStart, position+size, depth+1); err != nil {
				return err
			}
		}
		position += size
	}
	return nil
}

// metadataReader bounds both reads and seeks while allowing MP4 to skip media
// payloads and ID3v1 to seek to the footer. Metadata need not load the audio file.
type metadataReader struct {
	io.ReadSeeker
	length, remaining int64
}

func (reader *metadataReader) Read(data []byte) (int, error) {
	if reader.remaining <= 0 {
		return 0, errors.New("audio metadata read limit exceeded")
	}
	if int64(len(data)) > reader.remaining {
		data = data[:reader.remaining]
	}
	n, err := reader.ReadSeeker.Read(data)
	reader.remaining -= int64(n)
	return n, err
}

func (reader *metadataReader) Seek(offset int64, whence int) (int64, error) {
	position := offset
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		current, err := reader.ReadSeeker.Seek(0, io.SeekCurrent)
		if err != nil {
			return 0, err
		}
		position += current
	case io.SeekEnd:
		position += reader.length
	default:
		return 0, errors.New("invalid metadata seek origin")
	}
	if position < 0 || position > reader.length {
		return 0, errors.New("audio metadata seek outside file")
	}
	return reader.ReadSeeker.Seek(position, io.SeekStart)
}

func cleanMetadataText(value string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.Trim(value, "\x00"), "\ufeff"))
}

func rawMetadataText(value interface{}) string {
	switch value := value.(type) {
	case string:
		return value
	case *tag.Comm:
		if value != nil {
			return value.Text
		}
	}
	return ""
}

func frameNamed(key, name string) bool {
	return key == name || strings.HasPrefix(key, name+"_")
}

func isLyricsField(field string) bool {
	switch strings.ToUpper(strings.TrimSpace(field)) {
	case "LYRICS", "UNSYNCEDLYRICS", "UNSYNCHRONIZEDLYRICS":
		return true
	}
	return false
}

// SYLT uses either MPEG-frame counts or milliseconds. Only milliseconds can
// become LRC timestamps without an audio decoder; unsupported data stays empty.
func synchronizedLyrics(data []byte) string {
	if len(data) < 7 || data[0] > 3 || data[4] != 2 || data[5] != 1 {
		return ""
	}
	encoding := data[0]
	// UTF-16 strings in one frame may share the descriptor's byte order.
	if encoding == 1 && len(data) >= 8 && data[6] == 0xfe && data[7] == 0xff {
		encoding = 2
	}
	data, _, ok := takeID3String(data[6:], encoding)
	if !ok {
		return ""
	}
	var lines []string
	for len(data) > 0 {
		remaining, text, ok := takeID3String(data, encoding)
		if !ok || len(remaining) < 4 {
			return ""
		}
		milliseconds := binary.BigEndian.Uint32(remaining[:4])
		data = remaining[4:]
		if text = cleanMetadataText(text); text != "" {
			lines = append(lines, fmt.Sprintf("[%02d:%02d.%02d]%s", milliseconds/60000, milliseconds/1000%60, milliseconds/10%100, text))
		}
	}
	return strings.Join(lines, "\n")
}

func takeID3String(data []byte, encoding byte) ([]byte, string, bool) {
	step := 1
	if encoding == 1 || encoding == 2 {
		step = 2
	}
	for end := 0; end+step <= len(data); end += step {
		if data[end] != 0 || (step == 2 && data[end+1] != 0) {
			continue
		}
		text := data[:end]
		if step == 1 {
			if encoding == 0 {
				units := make([]rune, len(text))
				for i, value := range text {
					units[i] = rune(value)
				}
				return data[end+step:], string(units), true
			}
			return data[end+step:], string(text), true
		}
		var order binary.ByteOrder = binary.LittleEndian
		if encoding == 2 {
			order = binary.BigEndian
		}
		if len(text) >= 2 && (text[0] == 0xff && text[1] == 0xfe || text[0] == 0xfe && text[1] == 0xff) {
			if text[0] == 0xfe {
				order = binary.BigEndian
			}
			text = text[2:]
		}
		units := make([]uint16, len(text)/2)
		for i := range units {
			units[i] = order.Uint16(text[i*2:])
		}
		return data[end+step:], string(utf16.Decode(units)), true
	}
	return nil, "", false
}
