package daemonrun

import (
	"reflect"
	"testing"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

// encodeEnvelope encodes env and fails the test on error.
func encodeEnvelope(t *testing.T, env ripspec.Envelope) string {
	t.Helper()
	data, err := env.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return data
}

func TestContentIDClaims(t *testing.T) {
	cases := []struct {
		name        string
		ripSpecData string
		want        map[string]int
	}{
		{
			name:        "tv",
			ripSpecData: encodeEnvelope(t, ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: "tv"}}),
			want:        map[string]int{"gpu": 1},
		},
		{
			name:        "tv case-insensitive",
			ripSpecData: encodeEnvelope(t, ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: "TV"}}),
			want:        map[string]int{"gpu": 1},
		},
		{
			name:        "movie",
			ripSpecData: encodeEnvelope(t, ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: "movie"}}),
			want:        map[string]int{},
		},
		{
			name:        "missing media type",
			ripSpecData: encodeEnvelope(t, ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{}}),
			want:        map[string]int{},
		},
		{
			name:        "empty rip spec data",
			ripSpecData: "",
			want:        map[string]int{},
		},
		{
			name:        "malformed rip spec data",
			ripSpecData: "not json",
			want:        map[string]int{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := &queue.Item{RipSpecData: tc.ripSpecData}
			got := contentIDClaims(item)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("contentIDClaims() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestEncodeTierClaims(t *testing.T) {
	cases := []struct {
		name        string
		ripSpecData string
		want        map[string]int
	}{
		{
			name:        "uhd",
			ripSpecData: encodeEnvelope(t, ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{UHD: true}}),
			want:        map[string]int{"encode_4k": 1},
		},
		{
			name:        "non-uhd",
			ripSpecData: encodeEnvelope(t, ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{UHD: false}}),
			want:        map[string]int{"encode_1080p": 1},
		},
		{
			name:        "parse failure defaults to 1080p",
			ripSpecData: "not json",
			want:        map[string]int{"encode_1080p": 1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := &queue.Item{RipSpecData: tc.ripSpecData}
			got := encodeTierClaims(item)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("encodeTierClaims() = %#v, want %#v", got, tc.want)
			}
		})
	}
}
