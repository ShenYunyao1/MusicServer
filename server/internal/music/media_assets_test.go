package music

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestAlbumArtworkRequiresMatchingTags(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "AlbumCover - Singer - Album.PNG"), tinyPNG(), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for name, metadata := range map[string]audioMetadata{
		"absent tags": {}, "different artist": {Artist: "Other", Album: "Album"},
		"different album": {Artist: "Singer", Album: "Other"},
	} {
		t.Run(name, func(t *testing.T) {
			data, _, _ := readExternalCoverForMetadata(root, "song.mp3", metadata)
			if len(data) != 0 {
				t.Fatal("album artwork was assigned to an unrelated track")
			}
		})
	}
	data, mime, source := readExternalCoverForMetadata(root, "song.mp3", audioMetadata{Artist: "singer", Album: "album"})
	if len(data) == 0 || mime != "image/png" || source != "external" {
		t.Fatalf("matching artwork not found: %q %q %d", mime, source, len(data))
	}
}

func TestExternalLyricsCaseAndEncoding(t *testing.T) {
	const expected = "[00:01.00]中文歌词\n[00:02.00]下一行"
	legacy, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(strings.ReplaceAll(expected, "\n", "\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"UTF-8": []byte(expected), "UTF-8 BOM": append([]byte{0xef, 0xbb, 0xbf}, []byte(expected)...),
		"UTF-16 LE": testLyricsUTF16(expected, binary.LittleEndian), "UTF-16 BE": testLyricsUTF16(expected, binary.BigEndian),
		"GB18030": legacy,
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, "SONG.LRC"), data, 0o644); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			actual, source := readExternalLyrics(root, "song.mp3")
			if actual != expected || source != "sidecar" {
				t.Fatalf("incorrect lyrics: %q source=%q", actual, source)
			}
		})
	}
}

func TestEmptyExternalLyricsPermitEmbeddedFallback(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "song.lrc"), []byte("\xef\xbb\xbf \r\n\t"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if text, source := readExternalLyrics(root, "song.mp3"); text != "" || source != "" {
		t.Fatalf("blank lyrics must not suppress embedded lyrics: %q %q", text, source)
	}
	for _, invalid := range [][]byte{{0xff, 0xfe, 0}, {0xff, 0xfe, 0, 0xd8}, {0xff}} {
		if _, err := decodeLyricsFile(invalid); err == nil {
			t.Fatalf("invalid lyrics encoding was accepted: %x", invalid)
		}
	}
}

func testLyricsUTF16(text string, order binary.AppendByteOrder) []byte {
	data := order.AppendUint16(nil, 0xfeff)
	for _, unit := range utf16.Encode([]rune(text)) {
		data = order.AppendUint16(data, unit)
	}
	return data
}

func TestLocalExternalAssetProbe(t *testing.T) {
	base := os.Getenv("MUSIC_SERVER_METADATA_SAMPLES")
	if base == "" {
		t.Skip("set sample root to run the read-only external asset probe")
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	text, source := readExternalLyrics(root, "[1]默认列表/爱殇.mp3")
	if source != "sidecar" || !strings.Contains(text, "[") || strings.ContainsRune(text, '\ufffd') {
		t.Fatalf("real external lyrics were not decoded: source=%q bytes=%d", source, len(text))
	}
	metadata := audioMetadata{Artist: "livetune;かぐや(cv.夏吉ゆうこ)", Album: "超かぐや姫！"}
	data, mime, source := readExternalCoverForMetadata(root, "[1]默认列表/Reply.wav", metadata)
	if len(data) == 0 || mime != "image/jpeg" || source != "external" {
		t.Fatalf("real album artwork was not matched: %q %q bytes=%d", mime, source, len(data))
	}
	if data, _, _ := readExternalCoverForMetadata(root, "[1]默认列表/unrelated-track-no-artwork.mp3", audioMetadata{Artist: "Other", Album: "Other"}); len(data) != 0 {
		t.Fatal("real album artwork was assigned to an unrelated song")
	}
	t.Logf("external lyrics: %d bytes; matched Reply artwork without applying it to unrelated songs", len(text))
}

func TestExternalCoverPrecedenceAndValidation(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.WriteFile(filepath.Join(directory, "song.mp3"), []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "Cover.PNG"), tinyPNG(), 0o644); err != nil {
		t.Fatal(err)
	}
	data, mime, source := readExternalCover(root, "song.mp3")
	if len(data) == 0 || mime != "image/png" || source != "external" {
		t.Fatalf("external cover not found: mime=%q source=%q bytes=%d", mime, source, len(data))
	}
	if err := os.WriteFile(filepath.Join(directory, "song.jpg"), tinyJPEG(), 0o644); err != nil {
		t.Fatal(err)
	}
	data, mime, _ = readExternalCover(root, "song.mp3")
	if mime != "image/jpeg" || !bytes.Equal(data, tinyJPEG()) {
		t.Fatalf("same-basename cover did not win: mime=%q bytes=%d", mime, len(data))
	}
}

func TestTechnicalWAVFallback(t *testing.T) {
	const sampleRate, channels, bits = 8000, 1, 16
	const samples = 800
	dataBytes := samples * channels * bits / 8
	fmtBody := make([]byte, 16)
	binary.LittleEndian.PutUint16(fmtBody[0:2], 1)
	binary.LittleEndian.PutUint16(fmtBody[2:4], channels)
	binary.LittleEndian.PutUint32(fmtBody[4:8], sampleRate)
	binary.LittleEndian.PutUint32(fmtBody[8:12], sampleRate*channels*bits/8)
	binary.LittleEndian.PutUint16(fmtBody[12:14], channels*bits/8)
	binary.LittleEndian.PutUint16(fmtBody[14:16], bits)
	wav := append([]byte("RIFF"), make([]byte, 4)...)
	wav = append(wav, []byte("WAVEfmt ")...)
	wav = binary.LittleEndian.AppendUint32(wav, uint32(len(fmtBody)))
	wav = append(wav, fmtBody...)
	wav = append(wav, []byte("data")...)
	wav = binary.LittleEndian.AppendUint32(wav, uint32(dataBytes))
	wav = append(wav, make([]byte, dataBytes)...)
	binary.LittleEndian.PutUint32(wav[4:8], uint32(len(wav)-8))
	path := filepath.Join(t.TempDir(), "tone.wav")
	if err := os.WriteFile(path, wav, 0o644); err != nil {
		t.Fatal(err)
	}
	technical := technicalMetadata(path, "")
	if technical.SampleRate != sampleRate || technical.Channels != channels || technical.BitsPerSample != bits || technical.DurationMS != 100 {
		t.Fatalf("unexpected WAV technical metadata: %+v", technical)
	}
}

func TestWebPDimensionsAndLimits(t *testing.T) {
	for _, dimensions := range []struct{ w, h int }{{1, 1}, {512, 256}} {
		body := make([]byte, 10)
		for offset, value := range map[int]int{4: dimensions.w - 1, 7: dimensions.h - 1} {
			body[offset], body[offset+1], body[offset+2] = byte(value), byte(value>>8), byte(value>>16)
		}
		webp := testWebPChunk("VP8X", body)
		w, h, ok := webpDimensions(webp)
		if !ok || w != dimensions.w || h != dimensions.h {
			t.Fatalf("incorrect VP8X dimensions: %dx%d want %dx%d", w, h, dimensions.w, dimensions.h)
		}
		if _, mime, ok := validateCover(webp, ""); !ok || mime != "image/webp" {
			t.Fatalf("valid WebP header rejected: %q %v", mime, ok)
		}
	}
	// Lossless dimensions share bytes; both dimensions cross their low-bit
	// boundary to exercise carry propagation in the width/height formulas.
	value := uint32(512-1) | uint32(256-1)<<14
	body := binary.LittleEndian.AppendUint32([]byte{0x2f}, value)
	w, h, ok := webpDimensions(testWebPChunk("VP8L", body))
	if !ok || w != 512 || h != 256 {
		t.Fatalf("incorrect VP8L dimensions: %dx%d ok=%v", w, h, ok)
	}
	for _, invalid := range [][]byte{[]byte("RIFFxxxxWEBP"), testWebPChunk("VP8X", []byte{1, 2}), append(testWebPChunk("VP8X", make([]byte, 10)), 0)} {
		if _, _, ok := validateCover(invalid, "image/webp"); ok {
			t.Fatal("invalid WebP must be rejected")
		}
	}
	oversized := make([]byte, 10)
	oversized[4], oversized[5], oversized[7], oversized[8] = 0x87, 0x13, 0x87, 0x13 // 5000 x 5000.
	if _, _, ok := validateCover(testWebPChunk("VP8X", oversized), "image/webp"); ok {
		t.Fatal("oversized WebP must be rejected")
	}
}

func testWebPChunk(name string, body []byte) []byte {
	data := append([]byte("RIFF"), make([]byte, 4)...)
	data = append(data, []byte("WEBP"+name)...)
	data = binary.LittleEndian.AppendUint32(data, uint32(len(body)))
	data = append(data, body...)
	if len(body)%2 != 0 {
		data = append(data, 0)
	}
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	return data
}

func tinyPNG() []byte {
	data, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	return data
}

func tinyJPEG() []byte {
	data, _ := base64.StdEncoding.DecodeString("/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAP//////////////////////////////////////////////////////////////////////////////////////2wBDAf//////////////////////////////////////////////////////////////////////////////////////wAARCAABAAEDASIAAhEBAxEB/8QAFQABAQAAAAAAAAAAAAAAAAAAAAX/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/9oADAMBAAIQAxAAAAH/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/9oACAEBAAEFAqf/xAAUEQEAAAAAAAAAAAAAAAAAAAAA/9oACAEDAQE/AYf/xAAUEQEAAAAAAAAAAAAAAAAAAAAA/9oACAECAQE/Aaf/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/9oACAEBAAY/Aqf/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/9oACAEBAAE/Iaf/2gAMAwEAAgADAAAAEP/EABQRAQAAAAAAAAAAAAAAAAAAABD/2gAIAQMBAT8QH//EABQRAQAAAAAAAAAAAAAAAAAAABD/2gAIAQIBAT8QH//EABQQAQAAAAAAAAAAAAAAAAAAABD/2gAIAQEAAT8QH//Z")
	return data
}
