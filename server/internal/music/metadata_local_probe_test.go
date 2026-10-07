package music

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLocalAudioMetadataProbe(t *testing.T) {
	base := os.Getenv("MUSIC_SERVER_METADATA_SAMPLES")
	if base == "" {
		t.Skip("set MUSIC_SERVER_METADATA_SAMPLES to run the read-only real-file probe")
	}
	for _, sample := range []struct {
		name, title, artist string
		hasLyrics           bool
	}{
		{"暂存/麻雀 - 李荣浩.mp3", "麻雀", "李荣浩", true},
		{"暂存/The Phantom Of the Opera - Evil&Neuro.mp3", "The Phantom Of the Opera", "Evil&Neuro", true},
		{"[1]默认列表/她是什么样的 - 傅梦彤、陈麒名.flac", "她是什么样的", "傅梦彤、陈麒名", true},
		{"[1]默认列表/Adele - Rolling In The Deep [mqms2].flac", "Rolling In The Deep", "Adele", false},
		{"[2]国风女声/邓寓君(等什么君)-慕夏.flac", "慕夏", "邓寓君(等什么君)", false},
		{"[2]国风女声/添儿呗-烟火人间.flac", "烟火人间", "添儿呗", false},
		{"[1]默认列表/Reply.wav", "Reply", "livetune;かぐや(cv.夏吉ゆうこ)", true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			file, err := os.Open(filepath.Join(base, filepath.FromSlash(sample.name)))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			metadata, err := readAudioMetadata(file)
			if err != nil {
				t.Fatal(err)
			}
			if metadata.Title != sample.title || metadata.Artist != sample.artist || (metadata.Lyrics != "") != sample.hasLyrics {
				t.Errorf("unexpected real-file title=%q artist=%q embedded=%v", metadata.Title, metadata.Artist, metadata.Lyrics != "")
			}
			lines := 0
			if metadata.Lyrics != "" {
				lines = strings.Count(metadata.Lyrics, "\n") + 1
			}
			summary, _ := json.Marshal(struct {
				Title, Artist, Album, Source               string
				LyricsCharacters, LyricsBytes, LyricsLines int
			}{metadata.Title, metadata.Artist, metadata.Album, metadata.LyricsSource, utf8.RuneCountInString(metadata.Lyrics), len(metadata.Lyrics), lines})
			t.Log(string(summary))
		})
	}
}
