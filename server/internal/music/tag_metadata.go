package music

import (
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// The legacy parser removes interior NULs. Read v2.4 TCON before that
// transformation; separators in ordinary genre prose remain literal.
func readID3v24Genres(reader io.ReadSeeker, length int64) ([]string, error) {
	invalid := errors.New("unsupported or malformed ID3v2.4 genre frame")
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	var header [10]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	if string(header[:3]) != "ID3" || header[3] != 4 || header[5] != 0 {
		return nil, invalid
	}
	syncSize := func(data []byte) (int64, bool) {
		var size int64
		for _, b := range data {
			if b&0x80 != 0 {
				return 0, false
			}
			size = size<<7 | int64(b)
		}
		return size, true
	}
	size, ok := syncSize(header[6:10])
	if !ok || size > maxMetadataReadBytes || size > length-10 {
		return nil, invalid
	}
	end := size + 10
	values := []string{}
	for pos := int64(10); pos+10 <= end; {
		if _, err := reader.Seek(pos, io.SeekStart); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			return nil, err
		}
		if header[0] == 0 {
			break
		}
		size, ok = syncSize(header[4:8])
		if !ok || size > end-pos-10 {
			return nil, invalid
		}
		pos += size + 10
		if string(header[:4]) != "TCON" {
			continue
		}
		if size < 1 || size > 64<<10 || header[8] != 0 || header[9] != 0 {
			return nil, invalid
		}
		data := make([]byte, int(size))
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, err
		}
		var text string
		switch data[0] {
		case 0:
			runes := make([]rune, len(data)-1)
			for i, b := range data[1:] {
				runes[i] = rune(b)
			}
			text = string(runes)
		case 3:
			if !utf8.Valid(data[1:]) {
				return nil, invalid
			}
			text = string(data[1:])
		case 1, 2:
			payload := data[1:]
			var order binary.ByteOrder = binary.BigEndian
			if data[0] == 1 {
				if len(payload) < 2 {
					return nil, invalid
				}
				switch {
				case payload[0] == 0xff && payload[1] == 0xfe:
					order = binary.LittleEndian
				case payload[0] == 0xfe && payload[1] == 0xff:
				default:
					return nil, invalid
				}
				payload = payload[2:]
			}
			if len(payload)%2 != 0 {
				return nil, invalid
			}
			runes := make([]uint16, len(payload)/2)
			for i := range runes {
				runes[i] = order.Uint16(payload[i*2:])
			}
			for i := 0; i < len(runes); i++ {
				if runes[i] >= 0xd800 && runes[i] <= 0xdbff {
					if i+1 >= len(runes) || runes[i+1] < 0xdc00 || runes[i+1] > 0xdfff {
						return nil, invalid
					}
					i++
				} else if runes[i] >= 0xdc00 && runes[i] <= 0xdfff {
					return nil, invalid
				}
			}
			text = string(utf16.Decode(runes))
		default:
			return nil, invalid
		}
		for _, value := range strings.Split(text, "\x00") {
			if value = cleanMetadataText(value); value != "" {
				if len(values) >= 64 || len(value) > 1024 {
					return nil, invalid
				}
				values = append(values, value)
			}
		}
	}
	return values, nil
}
