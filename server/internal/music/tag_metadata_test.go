package music

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

func TestTagMetadataPreservesRepeatedVorbisGenresAndLiteralSeparators(t *testing.T) {
	for _, data := range [][]byte{
		testFLACMetadata("GENRE=Rock", "GENRE=AC/DC", "GENRE=Pop;Jazz"),
		testOggComments("GENRE=Rock", "GENRE=AC/DC", "GENRE=Pop;Jazz"),
	} {
		m, err := readAudioMetadata(bytes.NewReader(data))
		if err != nil || !reflect.DeepEqual(m.GenreValues, []string{"Rock", "AC/DC", "Pop;Jazz"}) {
			t.Fatalf("genres=%v err=%v", m.GenreValues, err)
		}
		if m.Genre != "Pop;Jazz" {
			t.Fatal("legacy genre changed", m.Genre)
		}
	}
}

func TestTagMetadataDistinguishesAbsentAndMalformed(t *testing.T) {
	valid, err := readAudioMetadata(bytes.NewReader(testFLACMetadata("TITLE=No genre")))
	if err != nil || valid.GenreValues == nil || len(valid.GenreValues) != 0 {
		t.Fatal(valid, err)
	}
	malformed, err := readAudioMetadata(bytes.NewReader([]byte("fLaCbad")))
	if err == nil || malformed.GenreValues != nil {
		t.Fatal(malformed, err)
	}
}

func TestTagMetadataID3v24UsesOnlyDefinedMultiValueSeparator(t *testing.T) {
	data := testID3Tag(testID3Frame("TCON", append([]byte{3}, []byte("Rock\x00AC/DC\x00Pop;Jazz")...)))
	data[3] = 4
	m, err := readAudioMetadata(bytes.NewReader(data))
	if err != nil || !reflect.DeepEqual(m.GenreValues, []string{"Rock", "AC/DC", "Pop;Jazz"}) {
		t.Fatal(m.GenreValues, err)
	}
}

func TestTagMetadataRejectsBrokenUTF16AndKeepsWAVUnreadableState(t *testing.T) {
	t.Run("UTF16", func(t *testing.T) {
		for _, body := range [][]byte{{2, 0xd8, 0}, {1, 0xfe, 0xff, 0xd8, 0}, {2, 0xdc, 0}} {
			data := testID3Tag(testID3Frame("TCON", body))
			data[3] = 4
			if _, err := readID3v24Genres(bytes.NewReader(data), int64(len(data))); err == nil {
				t.Fatal("broken UTF16 accepted")
			}
		}
	})
	t.Run("WAV", func(t *testing.T) {
		frame := testID3Frame("TCON", append([]byte{3}, []byte("Rock")...))
		frame[8] = 0x10
		data := testID3Tag(frame)
		data[3] = 4
		chunk := append([]byte("WAVEid3 "), binary.LittleEndian.AppendUint32(nil, uint32(len(data)))...)
		chunk = append(chunk, data...)
		if len(data)%2 != 0 {
			chunk = append(chunk, 0)
		}
		wav := append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(len(chunk)))...)
		wav = append(wav, chunk...)
		m, err := readAudioMetadata(bytes.NewReader(wav))
		if err != nil || m.Genre != "Rock" || m.GenreValues != nil {
			t.Fatal("WAV lost unreadable source state", m, err)
		}
	})
}
