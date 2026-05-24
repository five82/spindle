package subtitle

import (
	"reflect"
	"testing"
)

func TestBuildSubtitleMuxArgs(t *testing.T) {
	got := BuildSubtitleMuxArgs("/tmp/out.mkv", "/media/movie.mkv", MuxTrack{Path: "/tmp/movie.en.srt", Language: "en"}, true)
	want := []string{
		"-o", "/tmp/out.mkv", "--no-subtitles", "/media/movie.mkv",
		"--language", "0:eng", "--track-name", "0:English", "--default-track-flag", "0:no", "--forced-track", "0:no", "/tmp/movie.en.srt",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildSubtitleMuxArgs() = %#v, want %#v", got, want)
	}
}
