package music

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func validateAudio(path, configured string) error {
	_, err := validatedAudioContext(context.Background(), path, configured)
	return err
}
func validatedAudioContext(ctx context.Context, path, configured string) (audioTechnical, error) {
	candidates := []string{configured, os.Getenv("MUSIC_SERVER_FFPROBE")}
	if found, err := exec.LookPath("ffprobe"); err == nil {
		candidates = append(candidates, found)
	}
	available := false
	seen := map[string]bool{}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		available = true
		if technical, ok := probeWithFFprobeContext(ctx, path, candidate); ok {
			return technical, nil
		}
		if ctx.Err() != nil {
			return audioTechnical{}, ctx.Err()
		}
	}
	if !available {
		return audioTechnical{}, errors.New("FFPROBE_REQUIRED: configure ffprobe to validate uploads")
	}
	return audioTechnical{}, errors.New("AUDIO_INVALID: no valid audio stream found or ffprobe failed; full decoding has not been performed")
}

// audioTechnical contains container/codec facts useful to clients. Zero
// values mean that the available parser could not determine a field.
type audioTechnical struct {
	DurationMS    int64
	BitrateKbps   int
	SampleRate    int
	Channels      int
	BitsPerSample int
}

type ffprobeOutput struct {
	Streams []struct {
		CodecType     string      `json:"codec_type"`
		Duration      string      `json:"duration"`
		BitRate       string      `json:"bit_rate"`
		SampleRate    string      `json:"sample_rate"`
		Channels      int         `json:"channels"`
		BitsPerSample int         `json:"bits_per_sample"`
		BitsPerRaw    json.Number `json:"bits_per_raw_sample"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
		BitRate  string `json:"bit_rate"`
	} `json:"format"`
}

// technicalMetadata probes ffprobe when configured, then falls back to the
// compact WAV/FLAC stream headers. A missing probe is nonfatal to scanning.
func technicalMetadata(path, ffprobePath string) audioTechnical {
	return technicalMetadataContext(context.Background(), path, ffprobePath)
}

func technicalMetadataContext(ctx context.Context, path, ffprobePath string) audioTechnical {
	candidates := []string{ffprobePath, os.Getenv("MUSIC_SERVER_FFPROBE")}
	if found, err := exec.LookPath("ffprobe"); err == nil {
		candidates = append(candidates, found)
	}
	seen := make(map[string]bool)
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		if result, ok := probeWithFFprobeContext(ctx, path, candidate); ok {
			return result
		}
	}
	return basicTechnical(path)
}

func probeWithFFprobe(path, ffprobePath string) (audioTechnical, bool) {
	return probeWithFFprobeContext(context.Background(), path, ffprobePath)
}

func probeWithFFprobeContext(parent context.Context, path, ffprobePath string) (audioTechnical, bool) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, ffprobePath, "-v", "error", "-print_format", "json", "-show_entries", "stream=codec_type,duration,bit_rate,sample_rate,channels,bits_per_sample,bits_per_raw_sample:format=duration,bit_rate", path)
	var output limitedBuffer
	command.Stdout = &output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil || output.overflow || ctx.Err() != nil {
		return audioTechnical{}, false
	}
	return parseFFprobeOutput(output.data)
}

func parseFFprobeOutput(data []byte) (audioTechnical, bool) {
	var parsed ffprobeOutput
	if json.Unmarshal(data, &parsed) != nil {
		return audioTechnical{}, false
	}
	result := audioTechnical{}
	audioFound := false
	for _, stream := range parsed.Streams {
		if stream.CodecType != "audio" {
			continue
		}
		audioFound = true
		result.DurationMS = durationMilliseconds(stream.Duration)
		result.BitrateKbps = bitrateKbps(stream.BitRate)
		result.SampleRate = positiveInt(stream.SampleRate)
		result.Channels = stream.Channels
		result.BitsPerSample = stream.BitsPerSample
		if result.BitsPerSample == 0 {
			result.BitsPerSample = positiveInt(stream.BitsPerRaw.String())
		}
		break
	}
	if result.DurationMS == 0 {
		result.DurationMS = durationMilliseconds(parsed.Format.Duration)
	}
	if result.BitrateKbps == 0 {
		result.BitrateKbps = bitrateKbps(parsed.Format.BitRate)
	}
	return result, audioFound && result.SampleRate > 0 && result.Channels > 0
}

func durationMilliseconds(value string) int64 {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds > float64(math.MaxInt64)/1000 {
		return 0
	}
	return int64(seconds*1000 + 0.5)
}

func bitrateKbps(value string) int {
	bits, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || bits <= 0 {
		return 0
	}
	return int((bits + 500) / 1000)
}

func positiveInt(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

type limitedBuffer struct {
	data     []byte
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	const max = 4 << 20
	if len(b.data)+len(p) > max {
		b.overflow = true
		return len(p), nil
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

func basicTechnical(path string) audioTechnical {
	file, err := os.Open(path)
	if err != nil {
		return audioTechnical{}
	}
	defer file.Close()
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".wav":
		return wavTechnical(file)
	case ".flac":
		return flacTechnical(file)
	default:
		return audioTechnical{}
	}
}

func wavTechnical(file *os.File) audioTechnical {
	var header [12]byte
	if _, err := io.ReadFull(file, header[:]); err != nil || string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" {
		return audioTechnical{}
	}
	var result audioTechnical
	for {
		var chunk [8]byte
		if _, err := io.ReadFull(file, chunk[:]); err != nil {
			return result
		}
		size := int64(binary.LittleEndian.Uint32(chunk[4:]))
		if string(chunk[:4]) == "fmt " && size >= 16 && size <= 1<<20 {
			body := make([]byte, size)
			if _, err := io.ReadFull(file, body); err != nil {
				return result
			}
			result.Channels = int(binary.LittleEndian.Uint16(body[2:4]))
			result.SampleRate = int(binary.LittleEndian.Uint32(body[4:8]))
			bits := int(binary.LittleEndian.Uint16(body[14:16]))
			result.BitsPerSample = bits
			byteRate := int64(binary.LittleEndian.Uint32(body[8:12]))
			if byteRate > 0 {
				result.BitrateKbps = int((byteRate*8 + 500) / 1000)
			}
		} else if string(chunk[:4]) == "data" {
			if result.Channels > 0 && result.SampleRate > 0 && result.BitsPerSample > 0 {
				bytesPerSecond := int64(result.Channels*result.SampleRate*result.BitsPerSample) / 8
				if bytesPerSecond > 0 {
					result.DurationMS = (size * 1000) / bytesPerSecond
				}
			}
			return result
		} else if size > 0 {
			if _, err := file.Seek(size, io.SeekCurrent); err != nil {
				return result
			}
		}
		if size%2 != 0 {
			_, _ = file.Seek(1, io.SeekCurrent)
		}
	}
}

func flacTechnical(file *os.File) audioTechnical {
	var header [4]byte
	if _, err := io.ReadFull(file, header[:]); err != nil || string(header[:]) != "fLaC" {
		return audioTechnical{}
	}
	for {
		var block [4]byte
		if _, err := io.ReadFull(file, block[:]); err != nil {
			return audioTechnical{}
		}
		size := int64(block[1])<<16 | int64(block[2])<<8 | int64(block[3])
		if block[0]&0x7f == 0 && size >= 34 {
			stream := make([]byte, 34)
			if _, err := io.ReadFull(file, stream); err != nil {
				return audioTechnical{}
			}
			// STREAMINFO packs rate (20), channels (3), depth (5), and total
			// samples (36) into eight big-endian bytes.
			value := binary.BigEndian.Uint64(stream[10:18])
			sampleRate := int(value >> 44)
			channels := int((value>>41)&7) + 1
			bits := int((value>>36)&31) + 1
			totalSamples := value & ((uint64(1) << 36) - 1)
			result := audioTechnical{SampleRate: sampleRate, Channels: channels, BitsPerSample: bits}
			if sampleRate > 0 {
				result.DurationMS = int64((totalSamples*1000 + uint64(sampleRate)/2) / uint64(sampleRate))
			}
			if result.DurationMS > 0 {
				if info, err := file.Stat(); err == nil {
					result.BitrateKbps = int((info.Size()*8 + result.DurationMS/2) / result.DurationMS)
				}
			}
			return result
		}
		if _, err := file.Seek(size, io.SeekCurrent); err != nil {
			return audioTechnical{}
		}
		if block[0]&0x80 != 0 {
			return audioTechnical{}
		}
	}
}
