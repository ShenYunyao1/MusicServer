package music

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestReadAudioMetadataID3UTF16(t *testing.T) {
	reader := bytes.NewReader(testID3AudioMetadata())
	_, _ = reader.Seek(17, 0) // Callers may have already hashed/read the file.
	metadata, err := readAudioMetadata(reader)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Title != "中文歌名" || metadata.Artist != "中文歌手" || metadata.Album != "标签专辑" || metadata.Genre != "流行" || metadata.Comment != "实际备注" {
		t.Fatalf("incorrect ID3 metadata: %+v", metadata)
	}
	if metadata.Lyrics != "[00:01.00]第一行\n[00:02.50]第二行" || metadata.LyricsSource != "ID3v2/USLT" {
		t.Fatalf("incorrect ID3 lyrics: %+v", metadata)
	}
	if position, err := reader.Seek(0, 1); err != nil || position != 17 {
		t.Fatalf("reader position was not restored: %d, %v", position, err)
	}
}

func TestReadAudioMetadataFLAC(t *testing.T) {
	for _, field := range []string{"LYRICS", "UNSYNCEDLYRICS"} {
		t.Run(field, func(t *testing.T) {
			data := testFLACMetadata("TITLE=FLAC 歌名", "ARTIST=FLAC 歌手", "ALBUM=FLAC 专辑", "GENRE=Pop", "COMMENT=备注", field+"=[00:03.00]中文歌词\n[00:04.00]下一行")
			metadata, err := readAudioMetadata(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if metadata.Title != "FLAC 歌名" || metadata.Artist != "FLAC 歌手" || metadata.Album != "FLAC 专辑" || metadata.Genre != "Pop" || metadata.Comment != "备注" || metadata.Lyrics != "[00:03.00]中文歌词\n[00:04.00]下一行" || metadata.LyricsSource != "Vorbis/"+field {
				t.Fatalf("incorrect FLAC metadata: %+v", metadata)
			}
		})
	}
}

func TestReadAudioMetadataOgg(t *testing.T) {
	metadata, err := readAudioMetadata(bytes.NewReader(testOggComments("TITLE=Opus 歌名", "ARTIST=Opus 歌手", "UNSYNCEDLYRICS=[00:03.00]中文歌词")))
	if err != nil || metadata.Title != "Opus 歌名" || metadata.Artist != "Opus 歌手" || metadata.Lyrics != "[00:03.00]中文歌词" || metadata.LyricsSource != "Vorbis/UNSYNCEDLYRICS" {
		t.Fatalf("incorrect Ogg Opus metadata: %+v, %v", metadata, err)
	}
}

func TestReadAudioMetadataOggContinuation(t *testing.T) {
	lyrics := "[00:03.00]" + strings.Repeat("续页", 12000)
	metadata, err := readAudioMetadata(bytes.NewReader(testOggComments("TITLE=跨页歌名", "UNSYNCEDLYRICS="+lyrics)))
	if err != nil || metadata.Title != "跨页歌名" || metadata.Lyrics != lyrics {
		t.Fatalf("Ogg continued packet was not read: title=%q lyrics bytes=%d, %v", metadata.Title, len(metadata.Lyrics), err)
	}
}

func TestReadAudioMetadataMalformedPicture(t *testing.T) {
	// Deliberately small enough to run against the old parser without exhausting
	// memory: it still requests 1 MiB before noticing that no picture bytes exist.
	picture := binary.BigEndian.AppendUint32(nil, 3)
	picture = append(picture, make([]byte, 24)...)
	picture = binary.BigEndian.AppendUint32(picture, 1<<20)
	flac := append([]byte{'f', 'L', 'a', 'C', 0x86, 0, 0, byte(len(picture))}, picture...)
	reader := &metadataReadObserver{Reader: bytes.NewReader(flac)}
	if _, err := readAudioMetadata(reader); err == nil || reader.maxRead > len(flac) {
		t.Fatalf("malformed FLAC picture must fail before its declared allocation: err=%v, largest read=%d", err, reader.maxRead)
	}
	for _, key := range []string{"METADATA_BLOCK_PICTURE", "METADATA_BLOCK_PICTURE"} {
		comment := key + "=" + base64.StdEncoding.EncodeToString(picture)
		for name, data := range map[string][]byte{"FLAC comment": testFLACMetadata("TITLE=歌名", comment), "Ogg comment": testOggComments("TITLE=歌名", comment)} {
			t.Run(name+"/"+key, func(t *testing.T) {
				if _, err := readAudioMetadata(bytes.NewReader(data)); err == nil {
					t.Fatal("malformed base64 picture must be rejected")
				}
			})
		}
	}
}

type metadataReadObserver struct {
	*bytes.Reader
	maxRead int
}

func (reader *metadataReadObserver) Read(data []byte) (int, error) {
	if len(data) > reader.maxRead {
		reader.maxRead = len(data)
	}
	return reader.Reader.Read(data)
}

func TestReadAudioMetadataWAV(t *testing.T) {
	id3 := testID3AudioMetadata()
	var chunks []byte
	for _, chunk := range []struct {
		name string
		data []byte
	}{{"fmt ", make([]byte, 16)}, {"data", []byte{1, 2, 3}}, {"ID3 ", id3}} {
		chunks = append(chunks, []byte(chunk.name)...)
		chunks = binary.LittleEndian.AppendUint32(chunks, uint32(len(chunk.data)))
		chunks = append(chunks, chunk.data...)
		if len(chunk.data)%2 != 0 {
			chunks = append(chunks, 0)
		}
	}
	wav := binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(len(chunks)+4))
	wav = append(append(wav, []byte("WAVE")...), chunks...)
	metadata, err := readAudioMetadata(bytes.NewReader(wav))
	if err != nil || metadata.Title != "中文歌名" || metadata.Artist != "中文歌手" || metadata.Lyrics != "[00:01.00]第一行\n[00:02.50]第二行" || metadata.LyricsSource != "WAV/ID3v2/USLT" {
		t.Fatalf("WAV ID3 chunk was not read: %+v, %v", metadata, err)
	}
}

func TestReadAudioMetadataMP4(t *testing.T) {
	for _, custom := range []bool{false, true} {
		name := "standard"
		if custom {
			name = "iTunes freeform"
		}
		t.Run(name, func(t *testing.T) {
			lyricsAtom := testMP4TextAtom("\xa9lyr", "[00:05.00]M4A 歌词")
			source := "MP4/©lyr"
			if custom {
				lyricsAtom = testMP4Atom("----", bytes.Join([][]byte{
					testMP4Atom("mean", append(make([]byte, 4), []byte("com.apple.iTunes")...)),
					testMP4Atom("name", append(make([]byte, 4), []byte("Lyrics")...)),
					testMP4Atom("data", append([]byte{0, 0, 0, 1, 0, 0, 0, 0}, []byte("[00:05.00]M4A 歌词")...)),
				}, nil))
				source = "MP4/----:com.apple.iTunes:Lyrics"
			}
			items := bytes.Join([][]byte{testMP4TextAtom("\xa9nam", "M4A 歌名"), testMP4TextAtom("\xa9ART", "M4A 歌手"), lyricsAtom}, nil)
			data := append(testMP4Atom("ftyp", []byte("M4A \x00\x00\x00\x00isom")), testMP4Atom("moov", testMP4Atom("udta", testMP4Atom("meta", append(make([]byte, 4), testMP4Atom("ilst", items)...))))...)
			metadata, err := readAudioMetadata(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if metadata.Title != "M4A 歌名" || metadata.Artist != "M4A 歌手" || metadata.Lyrics != "[00:05.00]M4A 歌词" || metadata.LyricsSource != source {
				t.Fatalf("incorrect MP4 metadata: %+v", metadata)
			}
		})
	}
}

func TestReadAudioMetadataSynchronizedLyrics(t *testing.T) {
	data := []byte{3, 'e', 'n', 'g', 2, 1, 0} // UTF-8, milliseconds, lyrics, empty description.
	for _, entry := range []struct {
		text string
		ms   uint32
	}{{"第一行", 1250}, {"第二行", 61500}} {
		data = append(data, []byte(entry.text)...)
		data = append(data, 0)
		data = binary.BigEndian.AppendUint32(data, entry.ms)
	}
	metadata, err := readAudioMetadata(bytes.NewReader(testID3Tag(testID3Frame("SYLT", data))))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Lyrics != "[00:01.25]第一行\n[01:01.50]第二行" || metadata.LyricsSource != "ID3v2/SYLT" {
		t.Fatalf("incorrect synchronized lyrics: %+v", metadata)
	}
	data[4] = 1 // MPEG frames are not milliseconds and must not be rendered as LRC.
	metadata, err = readAudioMetadata(bytes.NewReader(testID3Tag(testID3Frame("SYLT", data))))
	if err != nil || metadata.Lyrics != "" || metadata.LyricsSource != "" {
		t.Fatalf("MPEG-frame timestamps must remain unsupported: %+v, %v", metadata, err)
	}
}

func TestReadAudioMetadataMultipleUSLT(t *testing.T) {
	data := testID3Tag(testID3Frame("USLT", []byte{3, 'e', 'n', 'g', 0}), testID3Frame("USLT", []byte("\x03zho\x00[00:01.00]非空歌词")))
	metadata, err := readAudioMetadata(bytes.NewReader(data))
	if err != nil || metadata.Lyrics != "[00:01.00]非空歌词" || metadata.LyricsSource != "ID3v2/USLT" {
		t.Fatalf("nonempty duplicate USLT was not selected: %+v, %v", metadata, err)
	}
}

func TestReadAudioMetadataMalformed(t *testing.T) {
	for name, data := range map[string][]byte{
		"untagged":          []byte("fake audio used by store tests"),
		"truncated ID3":     []byte("ID3\x03\x00\x00\x00\x00\x00\x40"),
		"truncated FLAC":    []byte("fLaC\x84\x00\x00\x60garbage"),
		"truncated WAV":     []byte("RIFF\xff\xff\xff\x7fWAVE"),
		"invalid WAV chunk": []byte("RIFF\x0c\x00\x00\x00WAVEID3 \xff\xff\xff\x7f"),
		"invalid MP4 atom":  append(testMP4Atom("ftyp", []byte("M4A \x00\x00\x00\x00isom")), []byte("\x00\x00\x00\x01moov")...),
	} {
		t.Run(name, func(t *testing.T) {
			metadata, err := readAudioMetadata(bytes.NewReader(data))
			if err == nil || metadata.Lyrics != "" || metadata.LyricsSource != "" {
				t.Fatalf("malformed/untagged data must permit fallback: %+v, %v", metadata, err)
			}
		})
	}
}

func testID3AudioMetadata() []byte {
	text := func(value string) []byte { return append([]byte{1}, testUTF16(value)...) }
	comment := append([]byte{1, 'z', 'h', 'o'}, testUTF16("说明")...)
	comment = append(comment, 0, 0)
	comment = append(comment, testUTF16("实际备注")...)
	lyrics := append([]byte{1, 'z', 'h', 'o'}, testUTF16("")...)
	lyrics = append(lyrics, 0, 0)
	lyrics = append(lyrics, testUTF16("[00:01.00]第一行\n[00:02.50]第二行")...)
	return testID3Tag(testID3Frame("TIT2", text("中文歌名")), testID3Frame("TPE1", text("中文歌手")), testID3Frame("TALB", text("标签专辑")), testID3Frame("TCON", text("流行")), testID3Frame("COMM", comment), testID3Frame("USLT", lyrics))
}

func testUTF16(value string) []byte {
	data := []byte{0xff, 0xfe}
	for _, unit := range utf16.Encode([]rune(value)) {
		data = binary.LittleEndian.AppendUint16(data, unit)
	}
	return data
}

func testID3Frame(name string, body []byte) []byte {
	data := binary.BigEndian.AppendUint32([]byte(name), uint32(len(body)))
	return append(append(data, 0, 0), body...)
}

func testID3Tag(frames ...[]byte) []byte {
	body := append(bytes.Join(frames, nil), make([]byte, 16)...)
	size := len(body)
	header := []byte{'I', 'D', '3', 3, 0, 0, byte(size >> 21), byte(size >> 14 & 127), byte(size >> 7 & 127), byte(size & 127)}
	return append(header, body...)
}

func testFLACMetadata(comments ...string) []byte {
	body := binary.LittleEndian.AppendUint32(nil, 4)
	body = append(body, []byte("test")...)
	body = binary.LittleEndian.AppendUint32(body, uint32(len(comments)))
	for _, comment := range comments {
		body = binary.LittleEndian.AppendUint32(body, uint32(len(comment)))
		body = append(body, []byte(comment)...)
	}
	header := []byte{'f', 'L', 'a', 'C', 0x84, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	return append(header, body...)
}

func testMP4Atom(name string, body []byte) []byte {
	data := binary.BigEndian.AppendUint32(nil, uint32(len(body)+8))
	data = append(data, []byte(name)...)
	return append(data, body...)
}

func testMP4TextAtom(name, value string) []byte {
	return testMP4Atom(name, testMP4Atom("data", append([]byte{0, 0, 0, 1, 0, 0, 0, 0}, []byte(value)...)))
}

func testOggComments(comments ...string) []byte {
	packet := append([]byte("OpusTags"), testFLACMetadata(comments...)[8:]...)
	var segments []byte
	for size := len(packet); size >= 255; size -= 255 {
		segments = append(segments, 255)
	}
	segments = append(segments, byte(len(packet)%255))
	var pages []byte
	for offset, sequence := 0, uint32(0); len(segments) > 0; sequence++ {
		pageSegments := segments[:min(255, len(segments))]
		size := 0
		for _, segment := range pageSegments {
			size += int(segment)
		}
		page := append([]byte("OggS"), make([]byte, 23)...)
		page[5] = 2
		if sequence > 0 {
			page[5] = 1
		}
		binary.LittleEndian.PutUint32(page[18:22], sequence)
		page[26] = byte(len(pageSegments))
		page = append(append(page, pageSegments...), packet[offset:offset+size]...)
		binary.LittleEndian.PutUint32(page[22:26], testOggCRC(page))
		pages = append(pages, page...)
		offset += size
		segments = segments[len(pageSegments):]
	}
	return pages
}

func testOggCRC(page []byte) uint32 {
	var crc uint32
	for _, value := range page {
		crc ^= uint32(value) << 24
		for bit := 0; bit < 8; bit++ {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
