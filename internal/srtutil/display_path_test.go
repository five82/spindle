package srtutil

import "testing"

func TestDisplaySubtitlePathUsesISO2AndDefaultsEnglish(t *testing.T) {
	for _, tc := range []struct{ video, lang, want string }{{"/library/movie.mkv", "eng", "/library/movie.en.srt"}, {"/library/movie.mkv", "fr", "/library/movie.fr.srt"}, {"/library/movie.mkv", "", "/library/movie.en.srt"}} {
		if got := DisplaySubtitlePath(tc.video, tc.lang); got != tc.want {
			t.Errorf("%q %q: %q want %q", tc.video, tc.lang, got, tc.want)
		}
	}
}
