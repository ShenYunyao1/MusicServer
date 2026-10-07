package music

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

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

func tinyPNG() []byte {
	data, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	return data
}

func tinyJPEG() []byte {
	data, _ := base64.StdEncoding.DecodeString("/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAP//////////////////////////////////////////////////////////////////////////////////////2wBDAf//////////////////////////////////////////////////////////////////////////////////////wAARCAABAAEDASIAAhEBAxEB/8QAFQABAQAAAAAAAAAAAAAAAAAAAAX/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/9oADAMBAAIQAxAAAAH/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/9oACAEBAAEFAqf/xAAUEQEAAAAAAAAAAAAAAAAAAAAA/9oACAEDAQE/AYf/xAAUEQEAAAAAAAAAAAAAAAAAAAAA/9oACAECAQE/Aaf/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/9oACAEBAAY/Aqf/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/9oACAEBAAE/Iaf/2gAMAwEAAgADAAAAEP/EABQRAQAAAAAAAAAAAAAAAAAAABD/2gAIAQMBAT8QH//EABQRAQAAAAAAAAAAAAAAAAAAABD/2gAIAQIBAT8QH//EABQQAQAAAAAAAAAAAAAAAAAAABD/2gAIAQEAAT8QH//Z")
	return data
}
