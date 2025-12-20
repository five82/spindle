package identification

import "testing"

func TestDeriveShowHintSeasonFallback(t *testing.T) {
	hint, season := deriveShowHint("South Park Season 5 Disc 1")
	if hint != "South Park" {
		t.Fatalf("expected show hint, got %q", hint)
	}
	if season != 5 {
		t.Fatalf("expected season 5, got %d", season)
	}
}

func TestExtractSeasonNumberVariants(t *testing.T) {
	cases := []struct {
		input []string
		want  int
	}{
		{[]string{"South Park Season 5"}, 5},
		{[]string{"South Park S03"}, 3},
		{[]string{"Season 12"}, 12},
	}
	for _, tc := range cases {
		got, ok := extractSeasonNumber(tc.input...)
		if !ok || got != tc.want {
			t.Fatalf("extractSeasonNumber(%v)=%d,%v want %d,true", tc.input, got, ok, tc.want)
		}
	}
}
