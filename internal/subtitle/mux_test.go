package subtitle

import (
	"reflect"
	"testing"
)

func TestBuildSubtitleMuxArgsRegularAndForced(t *testing.T) {
	got := BuildSubtitleMuxArgs("/tmp/out.mkv", "/media/movie.mkv", []MuxTrack{
		{Path: "/tmp/movie.en.srt", Language: "en"},
		{Path: "/tmp/movie.en.forced.srt", Language: "en", Forced: true},
	}, true)
	want := []string{
		"-o", "/tmp/out.mkv", "--no-subtitles", "/media/movie.mkv",
		"--language", "0:eng", "--track-name", "0:English", "--default-track-flag", "0:no", "--forced-track", "0:no", "/tmp/movie.en.srt",
		"--language", "0:eng", "--track-name", "0:English (Forced)", "--default-track-flag", "0:yes", "--forced-track", "0:yes", "/tmp/movie.en.forced.srt",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildSubtitleMuxArgs() = %#v, want %#v", got, want)
	}
}
