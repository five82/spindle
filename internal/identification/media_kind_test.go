package identification

import "testing"

func TestExtractDiscNumberFromArabicAndRoman(t *testing.T) {
	cases := []struct {
		input []string
		want  int
	}{
		{[]string{"SOUTHPARK5_DISC1"}, 1},
		{[]string{"Disc II"}, 2},
		{[]string{"South Park Season 5 Disc 3 of 4"}, 3},
	}
	for _, tc := range cases {
		got, ok := extractDiscNumber(tc.input...)
		if !ok || got != tc.want {
			t.Fatalf("extractDiscNumber(%v) = %d,%v want %d,true", tc.input, got, ok, tc.want)
		}
	}
}

func TestExtractDiscNumberMissing(t *testing.T) {
	if _, ok := extractDiscNumber("South Park"); ok {
		t.Fatalf("expected no disc number")
	}
}
