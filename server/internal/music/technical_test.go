package music

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestFLACStreamInfoTechnicalFallback(t *testing.T) {
	const rate, channels, depth, seconds = 96000, 2, 24, 3
	stream := make([]byte, 34)
	value := uint64(rate)<<44 | uint64(channels-1)<<41 | uint64(depth-1)<<36 | uint64(rate*seconds)
	binary.BigEndian.PutUint64(stream[10:18], value)
	data := append([]byte{'f', 'L', 'a', 'C', 0x80, 0, 0, 34}, stream...)
	path := filepath.Join(t.TempDir(), "song.flac")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	actual := basicTechnical(path)
	if actual.SampleRate != rate || actual.Channels != channels || actual.BitsPerSample != depth || actual.DurationMS != seconds*1000 {
		t.Fatalf("incorrect FLAC STREAMINFO: %+v", actual)
	}
}

func TestProbeOutputLimit(t *testing.T) {
	var output limitedBuffer
	_, _ = output.Write(make([]byte, 4<<20))
	_, _ = output.Write([]byte("extra"))
	if !output.overflow || len(output.data) > 4<<20 {
		t.Fatalf("probe output was not bounded: bytes=%d overflow=%v", len(output.data), output.overflow)
	}
}

func TestFFprobeAudioWithPictureStream(t *testing.T) {
	// ffprobe emits quoted bits_per_raw_sample for attached-picture streams.
	// Rejecting that representation used to discard the entire audio result.
	data := []byte(`{"streams":[{"codec_type":"audio","duration":"252.757302","bit_rate":"320000","sample_rate":"44100","channels":2,"bits_per_sample":0},{"codec_type":"video","bits_per_raw_sample":"8"}],"format":{"duration":"252.757302","bit_rate":"321897"}}`)
	result, ok := parseFFprobeOutput(data)
	if !ok || result.DurationMS != 252757 || result.BitrateKbps != 320 || result.SampleRate != 44100 || result.Channels != 2 || result.BitsPerSample != 0 {
		t.Fatalf("ffprobe audio fields not parsed: %+v ok=%v", result, ok)
	}
	for _, data := range [][]byte{[]byte("invalid"), []byte(`{"streams":[{"codec_type":"video","bits_per_raw_sample":"8"}]}`)} {
		if _, ok := parseFFprobeOutput(data); ok {
			t.Fatal("invalid/non-audio probe must permit a fallback")
		}
	}
}

func TestDurationRejectsNonFinite(t *testing.T) {
	for _, value := range []string{"NaN", "+Inf", "-1", "1e30"} {
		if durationMilliseconds(value) != 0 {
			t.Fatalf("unsafe duration accepted: %q", value)
		}
	}
	if durationMilliseconds("1.2345") != 1235 || math.IsNaN(float64(durationMilliseconds("NaN"))) {
		t.Fatal("duration rounding incorrect")
	}
}

func TestLocalTechnicalMetadataProbe(t *testing.T) {
	base := os.Getenv("MUSIC_SERVER_METADATA_SAMPLES")
	probe := os.Getenv("MUSIC_SERVER_TEST_FFPROBE")
	if base == "" || probe == "" {
		t.Skip("set sample root and ffprobe path to run the real-file probe")
	}
	for _, name := range []string{"暂存/麻雀 - 李荣浩.mp3", "[1]默认列表/她是什么样的 - 傅梦彤、陈麒名.flac", "[1]默认列表/Reply.wav"} {
		t.Run(name, func(t *testing.T) {
			result := technicalMetadata(filepath.Join(base, filepath.FromSlash(name)), probe)
			if result.DurationMS <= 0 || result.BitrateKbps <= 0 || result.SampleRate <= 0 || result.Channels <= 0 {
				t.Fatalf("missing real-file technical parameters: %+v", result)
			}
			t.Logf("technical parameters: %+v", result)
		})
	}
}
