package apply

import (
	"math"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/ripspec"
)

func avProbe(videoStart, audioStart string) *ffprobe.Result {
	return &ffprobe.Result{Streams: []ffprobe.Stream{
		{CodecType: "video", StartTime: videoStart},
		{CodecType: "audio", StartTime: audioStart, Disposition: map[string]int{"default": 1}},
	}}
}

func TestCompareAVStartTimes(t *testing.T) {
	tests := []struct {
		name      string
		source    *ffprobe.Result
		output    *ffprobe.Result
		wantDrift float64
		wantPass  bool
		wantErr   string
	}{
		{
			name:      "offset preserved",
			source:    avProbe("0", "0.501"),
			output:    avProbe("0", "0.5005"),
			wantDrift: -0.5,
			wantPass:  true,
		},
		{
			name:      "audio moved earlier",
			source:    avProbe("0.000000", "0.501000"),
			output:    avProbe("0.000000", "0.000000"),
			wantDrift: -501,
		},
		{
			name:      "audio moved later",
			source:    avProbe("0", "0"),
			output:    avProbe("0", "0.240"),
			wantDrift: 240,
		},
		{
			name:      "drift at the tolerance passes",
			source:    avProbe("0", "0"),
			output:    avProbe("0", "0.100"),
			wantDrift: 100,
			wantPass:  true,
		},
		{
			name:    "source without video",
			source:  &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "audio", StartTime: "0"}}},
			output:  avProbe("0", "0"),
			wantErr: "source: video stream unavailable",
		},
		{
			name:    "output without audio",
			source:  avProbe("0", "0"),
			output:  &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "video", StartTime: "0"}}},
			wantErr: "output: primary audio stream unavailable",
		},
		{
			name:    "unparsable start time",
			source:  avProbe("0", "N/A"),
			output:  avProbe("0", "0"),
			wantErr: "source: primary audio start time unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := compareAVStartTimes(&ripspec.AVSyncCheck{}, tt.source, tt.output, -1)
			if check.Error != tt.wantErr {
				t.Fatalf("error = %q, want %q", check.Error, tt.wantErr)
			}
			if tt.wantErr != "" {
				return
			}
			if math.Abs(check.DriftMilliseconds-tt.wantDrift) > 0.001 {
				t.Fatalf("drift = %.3fms, want %.3fms", check.DriftMilliseconds, tt.wantDrift)
			}
			if check.Passed != tt.wantPass {
				t.Fatalf("passed = %v, want %v", check.Passed, tt.wantPass)
			}
		})
	}
}

func TestPrimaryAVStartTimesSelectsRequestedAudioOrdinal(t *testing.T) {
	result := &ffprobe.Result{Streams: []ffprobe.Stream{
		{CodecType: "video", StartTime: "0"},
		{CodecType: "audio", StartTime: "0.100"},
		{CodecType: "audio", StartTime: "0.200", Disposition: map[string]int{"default": 1}},
	}}

	_, audio, err := primaryAVStartTimes(result, 0)
	if err != nil || math.Abs(audio-0.1) > 0.001 {
		t.Fatalf("ordinal 0 audio = %v (err %v), want 0.100", audio, err)
	}
	// A negative ordinal prefers the default-flagged stream.
	_, audio, err = primaryAVStartTimes(result, -1)
	if err != nil || math.Abs(audio-0.2) > 0.001 {
		t.Fatalf("default audio = %v (err %v), want 0.200", audio, err)
	}
}

func subtitleEnv(source string) *ripspec.Envelope {
	return &ripspec.Envelope{Attributes: ripspec.EnvelopeAttributes{
		SubtitleGenerationResults: []ripspec.SubtitleGenRecord{
			{EpisodeKey: "main", Source: source, Language: "eng"},
		},
	}}
}

func subtitleProbe(stream ffprobe.Stream) *ffprobe.Result {
	stream.CodecType = "subtitle"
	return &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "video"}, {CodecType: "audio"}, stream}}
}

func TestCheckSubtitleLayout(t *testing.T) {
	adopted := ffprobe.Stream{
		CodecName:   "subrip",
		Tags:        map[string]string{"language": "eng", "title": "English"},
		Disposition: map[string]int{},
	}

	tests := []struct {
		name        string
		env         *ripspec.Envelope
		muxed       bool
		output      *ffprobe.Result
		wantFailure string
	}{
		{name: "adopted and muxed", env: subtitleEnv("opensubtitles"), muxed: true, output: subtitleProbe(adopted)},
		{
			name:        "adopted but stream missing",
			env:         subtitleEnv("opensubtitles"),
			muxed:       true,
			output:      &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "video"}}},
			wantFailure: "adopted subtitle expects 1 subtitle stream, found 0",
		},
		{
			name:   "adopted with a second stream",
			env:    subtitleEnv("opensubtitles"),
			muxed:  true,
			output: &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "subtitle"}, {CodecType: "subtitle"}}},
			// Two streams is a layout failure regardless of their labels.
			wantFailure: "found 2",
		},
		{
			name:        "pgs instead of srt",
			env:         subtitleEnv("opensubtitles"),
			muxed:       true,
			output:      subtitleProbe(ffprobe.Stream{CodecName: "hdmv_pgs_subtitle", Tags: map[string]string{"language": "eng", "title": "English"}}),
			wantFailure: "is not subrip",
		},
		{
			name:        "missing language tag",
			env:         subtitleEnv("opensubtitles"),
			muxed:       true,
			output:      subtitleProbe(ffprobe.Stream{CodecName: "subrip", Tags: map[string]string{"title": "English"}}),
			wantFailure: "no language tag",
		},
		{
			name:        "label does not name the language",
			env:         subtitleEnv("opensubtitles"),
			muxed:       true,
			output:      subtitleProbe(ffprobe.Stream{CodecName: "subrip", Tags: map[string]string{"language": "eng", "title": "Track 1"}}),
			wantFailure: "does not identify language",
		},
		{
			name:        "forced flag",
			env:         subtitleEnv("opensubtitles"),
			muxed:       true,
			output:      subtitleProbe(ffprobe.Stream{CodecName: "subrip", Tags: map[string]string{"language": "eng", "title": "English (Forced)"}, Disposition: map[string]int{"forced": 1}}),
			wantFailure: "flagged forced",
		},
		{
			name:        "default flag",
			env:         subtitleEnv("opensubtitles"),
			muxed:       true,
			output:      subtitleProbe(ffprobe.Stream{CodecName: "subrip", Tags: map[string]string{"language": "eng", "title": "English"}, Disposition: map[string]int{"default": 1}}),
			wantFailure: "flagged default",
		},
		{
			name:   "adopted but never muxed is judged by the mux failure instead",
			env:    subtitleEnv("opensubtitles"),
			muxed:  false,
			output: &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "video"}}},
		},
		{name: "skipped title has no subtitle", env: subtitleEnv("none"), output: &ffprobe.Result{}},
		{
			name:        "skipped title with a stray subtitle",
			env:         subtitleEnv("none"),
			output:      subtitleProbe(adopted),
			wantFailure: "1 subtitle stream(s) present for a skipped subtitle",
		},
		{name: "no record at all", env: &ripspec.Envelope{}, output: subtitleProbe(adopted)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failures := checkSubtitleLayout(tt.env, "main", tt.muxed, tt.output)
			if tt.wantFailure == "" {
				if len(failures) != 0 {
					t.Fatalf("failures = %v, want none", failures)
				}
				return
			}
			if !strings.Contains(strings.Join(failures, "; "), tt.wantFailure) {
				t.Fatalf("failures = %v, want one containing %q", failures, tt.wantFailure)
			}
		})
	}
}

func audioProbe(streams ...ffprobe.Stream) *ffprobe.Result {
	out := &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "video"}}}
	for _, s := range streams {
		s.CodecType = "audio"
		out.Streams = append(out.Streams, s)
	}
	return out
}

func TestCheckCommentaryLabels(t *testing.T) {
	primary := ffprobe.Stream{Tags: map[string]string{"language": "eng", "title": "Surround"}, Disposition: map[string]int{"default": 1}}
	labeled := ffprobe.Stream{Tags: map[string]string{"language": "eng", "title": "Stereo (Commentary)"}, Disposition: map[string]int{"comment": 1}}

	tests := []struct {
		name        string
		expected    []int
		output      *ffprobe.Result
		wantFailure string
	}{
		{name: "labeled commentary", expected: []int{1}, output: audioProbe(primary, labeled)},
		{name: "no commentary expected", output: audioProbe(primary)},
		{
			name:        "commentary missing the comment flag",
			expected:    []int{1},
			output:      audioProbe(primary, ffprobe.Stream{Tags: map[string]string{"title": "Commentary"}}),
			wantFailure: "commentary track 1 is missing the comment flag",
		},
		{
			name:        "commentary missing the label",
			expected:    []int{1},
			output:      audioProbe(primary, ffprobe.Stream{Tags: map[string]string{"title": "Stereo"}, Disposition: map[string]int{"comment": 1}}),
			wantFailure: "lacks a Commentary label",
		},
		{
			name:        "comment flag on a non-commentary track",
			output:      audioProbe(primary, labeled),
			wantFailure: "audio track 1 carries the comment flag but is not commentary",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failures := checkCommentaryLabels(tt.expected, tt.output)
			if tt.wantFailure == "" {
				if len(failures) != 0 {
					t.Fatalf("failures = %v, want none", failures)
				}
				return
			}
			if !strings.Contains(strings.Join(failures, "; "), tt.wantFailure) {
				t.Fatalf("failures = %v, want one containing %q", failures, tt.wantFailure)
			}
		})
	}
}

func TestCheckAudioLayout(t *testing.T) {
	english := ffprobe.Stream{Tags: map[string]string{"language": "eng"}, Disposition: map[string]int{"default": 1}}
	italian := ffprobe.Stream{Tags: map[string]string{"language": "ita"}}

	tests := []struct {
		name        string
		keptAudio   int
		output      *ffprobe.Result
		wantFailure string
	}{
		{name: "single english default", keptAudio: 1, output: audioProbe(english)},
		{name: "count not enforced without a plan", output: audioProbe(english, italian)},
		{
			name:        "count differs from the plan",
			keptAudio:   2,
			output:      audioProbe(english),
			wantFailure: "audio stream count 1 does not match the refinement plan's 2",
		},
		{
			name:        "no audio at all",
			output:      &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "video"}}},
			wantFailure: "output has no audio streams",
		},
		{
			name:        "first stream not default",
			output:      audioProbe(ffprobe.Stream{Tags: map[string]string{"language": "eng"}}),
			wantFailure: "first audio stream is not the default track",
		},
		{
			name:        "second stream also default",
			output:      audioProbe(english, ffprobe.Stream{Tags: map[string]string{"language": "eng"}, Disposition: map[string]int{"default": 1}}),
			wantFailure: "non-primary audio track 1 is also default",
		},
		{
			name: "non-english default despite an english track",
			output: audioProbe(
				ffprobe.Stream{Tags: map[string]string{"language": "ita"}, Disposition: map[string]int{"default": 1}},
				ffprobe.Stream{Tags: map[string]string{"language": "eng"}},
			),
			wantFailure: "default audio track is not English despite an English track being present",
		},
		{
			name: "non-english default with no english track",
			output: audioProbe(
				ffprobe.Stream{Tags: map[string]string{"language": "ita"}, Disposition: map[string]int{"default": 1}},
				ffprobe.Stream{Tags: map[string]string{"language": "ger"}},
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failures := checkAudioLayout(tt.keptAudio, tt.output)
			if tt.wantFailure == "" {
				if len(failures) != 0 {
					t.Fatalf("failures = %v, want none", failures)
				}
				return
			}
			if !strings.Contains(strings.Join(failures, "; "), tt.wantFailure) {
				t.Fatalf("failures = %v, want one containing %q", failures, tt.wantFailure)
			}
		})
	}
}

func TestCheckMuxDuration(t *testing.T) {
	probe := func(duration string) *ffprobe.Result {
		return &ffprobe.Result{Format: ffprobe.Format{Duration: duration}}
	}
	tests := []struct {
		name            string
		encodedDuration float64
		output          *ffprobe.Result
		wantFailure     bool
	}{
		{name: "unchanged", encodedDuration: 7813, output: probe("7813.000000")},
		{name: "within tolerance", encodedDuration: 7813, output: probe("7812.400000")},
		{name: "truncated mux", encodedDuration: 7813, output: probe("291.530000"), wantFailure: true},
		{name: "unknown encoded duration", output: probe("7813.000000")},
		{name: "unknown output duration", encodedDuration: 7813, output: probe("")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failures := checkMuxDuration(tt.encodedDuration, tt.output)
			if tt.wantFailure != (len(failures) > 0) {
				t.Fatalf("failures = %v, wantFailure = %v", failures, tt.wantFailure)
			}
		})
	}
}

func TestSubtitleLabelCorrect(t *testing.T) {
	tests := []struct {
		name   string
		lang   string
		title  string
		forced bool
		want   bool
	}{
		{name: "language name", lang: "eng", title: "English", want: true},
		{name: "language code", lang: "eng", title: "eng", want: true},
		{name: "empty title", lang: "eng", want: false},
		{name: "unrelated title", lang: "eng", title: "Track 1", want: false},
		{name: "no language tag", title: "Anything", want: true},
		{name: "forced without the word", lang: "eng", title: "English", forced: true, want: false},
		{name: "forced with the word", lang: "eng", title: "English (Forced)", forced: true, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := subtitleLabelCorrect(tt.lang, tt.title, tt.forced); got != tt.want {
				t.Fatalf("subtitleLabelCorrect(%q, %q, %v) = %v, want %v", tt.lang, tt.title, tt.forced, got, tt.want)
			}
		})
	}
}
