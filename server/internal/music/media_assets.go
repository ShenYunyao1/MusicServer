package music

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// externalCoverNames returns candidates in the documented precedence order.
// A same-basename image wins over conventional folder artwork.
func externalCoverNames(relativeAudioPath string) []string {
	dir := filepath.ToSlash(filepath.Dir(relativeAudioPath))
	base := strings.TrimSuffix(filepath.Base(relativeAudioPath), filepath.Ext(relativeAudioPath))
	var names []string
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".gif", ".webp"} {
		name := base + ext
		if dir != "." {
			name = filepath.ToSlash(filepath.Join(dir, name))
		}
		names = append(names, name)
	}
	for _, stem := range []string{"cover", "folder", "albumcover"} {
		for _, ext := range []string{".jpg", ".jpeg", ".png", ".gif", ".webp"} {
			name := stem + ext
			if dir != "." {
				name = filepath.ToSlash(filepath.Join(dir, name))
			}
			names = append(names, name)
		}
	}
	return names
}

// readExternalCover looks beside an audio file for common artwork names. It
// performs a case-insensitive directory lookup because media collections often
// use Cover.JPG or Folder.png. Files are bounded before validation.
func readExternalCover(root *os.Root, relativeAudioPath string) (data []byte, mime, source string) {
	return readExternalCoverForMetadata(root, relativeAudioPath, audioMetadata{})
}

func externalCoverNamesForMetadata(relativeAudioPath string, metadata audioMetadata) []string {
	candidates := externalCoverNames(relativeAudioPath)
	// Download tools may name artwork "AlbumCover - artist - album.jpg".
	// Only exact tag matches qualify; prefixes alone are unsafe in mixed folders.
	if artist, album := strings.TrimSpace(metadata.Artist), strings.TrimSpace(metadata.Album); artist != "" && album != "" {
		dir := filepath.ToSlash(filepath.Dir(relativeAudioPath))
		albumNames := make([]string, 0, 5)
		for _, ext := range []string{".jpg", ".jpeg", ".png", ".gif", ".webp"} {
			name := "AlbumCover - " + artist + " - " + album + ext
			// Tag strings must remain filenames, including on Windows.
			if strings.ContainsAny(name, "/\\") {
				continue
			}
			albumNames = append(albumNames, filepath.ToSlash(filepath.Join(dir, name)))
		}
		candidates = append(append(append([]string{}, candidates[:5]...), albumNames...), candidates[5:]...)
	}
	return candidates
}

func readExternalCoverForMetadata(root *os.Root, relativeAudioPath string, metadata audioMetadata, indices ...*assetDirectoryIndex) (data []byte, mime, source string) {
	if root == nil {
		return nil, "", ""
	}
	candidates := externalCoverNamesForMetadata(relativeAudioPath, metadata)
	index := assetLookup(indices)
	for _, candidate := range candidates {
		for attempt := 0; attempt < 2; attempt++ {
			actual := candidate
			if attempt == 1 {
				actual = index.find(root, candidate)
				if actual == candidate {
					continue
				}
			}
			if actual == "" {
				continue
			}
			file, err := root.Open(actual)
			if err != nil {
				continue
			}
			info, statErr := file.Stat()
			if statErr != nil || info.IsDir() || info.Size() <= 0 || info.Size() > maxEmbeddedCoverBytes {
				_ = file.Close()
				continue
			}
			content, readErr := io.ReadAll(io.LimitReader(file, maxEmbeddedCoverBytes+1))
			_ = file.Close()
			if readErr != nil || len(content) > maxEmbeddedCoverBytes {
				continue
			}
			if content, mime, ok := validateCover(content, ""); ok {
				return content, mime, "external"
			}
		}
	}
	return nil, "", ""
}

func readExternalLyrics(root *os.Root, relativeAudioPath string, indices ...*assetDirectoryIndex) (text, source string) {
	if root == nil {
		return "", ""
	}
	base := strings.TrimSuffix(relativeAudioPath, filepath.Ext(relativeAudioPath))
	index := assetLookup(indices)
	for _, ext := range []string{".lrc", ".elrc", ".txt"} {
		candidate := index.find(root, base+ext)
		if candidate == "" {
			continue
		}
		file, err := root.Open(candidate)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(file, maxEmbeddedLyricsBytes+1))
		_ = file.Close()
		if err != nil || len(data) > maxEmbeddedLyricsBytes {
			continue
		}
		decoded, err := decodeLyricsFile(data)
		if err == nil && strings.TrimSpace(decoded) != "" {
			return decoded, "sidecar"
		}
	}
	return "", ""
}

// decodeLyricsFile supports common sidecar encodings without relying on the
// host's locale. Invalid input is rejected so embedded lyrics remain usable.
func decodeLyricsFile(data []byte) (string, error) {
	if len(data) > maxEmbeddedLyricsBytes {
		return "", errors.New("lyrics file exceeds size limit")
	}
	var result string
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xfe}), bytes.HasPrefix(data, []byte{0xfe, 0xff}):
		var order binary.ByteOrder = binary.LittleEndian
		if data[0] == 0xfe {
			order = binary.BigEndian
		}
		data = data[2:]
		if len(data)%2 != 0 {
			return "", errors.New("truncated UTF-16 lyrics")
		}
		units := make([]uint16, len(data)/2)
		for i := range units {
			units[i] = order.Uint16(data[i*2 : i*2+2])
		}
		for i := 0; i < len(units); i++ {
			if units[i] >= 0xd800 && units[i] <= 0xdbff {
				if i+1 == len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
					return "", errors.New("invalid UTF-16 lyrics surrogate")
				}
				i++
			} else if units[i] >= 0xdc00 && units[i] <= 0xdfff {
				return "", errors.New("invalid UTF-16 lyrics surrogate")
			}
		}
		result = string(utf16.Decode(units))
	default:
		utf8BOM := bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf})
		data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
		if utf8.Valid(data) {
			result = string(data)
		} else {
			if utf8BOM {
				return "", errors.New("invalid UTF-8 lyrics")
			}
			decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data)
			if err != nil || bytes.Contains(decoded, []byte(string(utf8.RuneError))) {
				return "", errors.New("invalid lyrics encoding")
			}
			result = string(decoded)
		}
	}
	if strings.ContainsRune(result, '\x00') {
		return "", errors.New("lyrics contain NUL bytes")
	}
	return strings.ReplaceAll(strings.ReplaceAll(result, "\r\n", "\n"), "\r", "\n"), nil
}

func findCaseInsensitive(root *os.Root, relative string) string {
	return (&assetDirectoryIndex{}).find(root, relative)
}

// selectCover preserves embedded artwork precedence while allowing a caller
// to fill in an external fallback. It is deliberately independent of Store so
// upload and scan code can apply the same policy.
func selectCover(root *os.Root, relativeAudioPath string, metadata audioMetadata, indices ...*assetDirectoryIndex) audioMetadata {
	if len(metadata.Cover) > 0 {
		return metadata
	}
	data, mime, source := readExternalCoverForMetadata(root, relativeAudioPath, metadata, indices...)
	if len(data) > 0 {
		metadata.Cover, metadata.CoverMIME, metadata.CoverSource = data, mime, source
	}
	return metadata
}
