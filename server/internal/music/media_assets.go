package music

import (
	"io"
	"os"
	"path/filepath"
	"strings"
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
	for _, stem := range []string{"cover", "folder"} {
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
	if root == nil {
		return nil, "", ""
	}
	candidates := externalCoverNames(relativeAudioPath)
	for _, candidate := range candidates {
		for _, actual := range []string{candidate, findCaseInsensitive(root, candidate)} {
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

func findCaseInsensitive(root *os.Root, relative string) string {
	dir, name := filepath.ToSlash(filepath.Dir(relative)), filepath.Base(relative)
	if dir == "." {
		dir = "."
	}
	directory, err := root.Open(dir)
	if err != nil {
		return ""
	}
	entries, err := directory.Readdir(-1)
	_ = directory.Close()
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), name) {
			if dir == "." {
				return entry.Name()
			}
			return filepath.ToSlash(filepath.Join(dir, entry.Name()))
		}
	}
	return ""
}

// selectCover preserves embedded artwork precedence while allowing a caller
// to fill in an external fallback. It is deliberately independent of Store so
// upload and scan code can apply the same policy.
func selectCover(root *os.Root, relativeAudioPath string, metadata audioMetadata) audioMetadata {
	if len(metadata.Cover) > 0 {
		return metadata
	}
	data, mime, source := readExternalCover(root, relativeAudioPath)
	if len(data) > 0 {
		metadata.Cover, metadata.CoverMIME, metadata.CoverSource = data, mime, source
	}
	return metadata
}
