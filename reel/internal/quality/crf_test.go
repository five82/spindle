package quality

import "testing"

func TestFormatCRF(t *testing.T) {
	tests := map[float32]string{
		25:    "25",
		25.25: "25.25",
		25.5:  "25.5",
		25.75: "25.75",
	}
	for crf, want := range tests {
		if got := FormatCRF(crf); got != want {
			t.Fatalf("FormatCRF(%g) = %q, want %q", crf, got, want)
		}
	}
}

func TestParseCRF(t *testing.T) {
	valid := []string{"1", "24.25", "24.5", "24.75", "70"}
	for _, input := range valid {
		if _, err := ParseCRF(input); err != nil {
			t.Fatalf("ParseCRF(%q) returned error: %v", input, err)
		}
	}
	invalid := []string{"0", "70.25", "24.1"}
	for _, input := range invalid {
		if _, err := ParseCRF(input); err == nil {
			t.Fatalf("ParseCRF(%q) succeeded, want error", input)
		}
	}
}

func TestParseTargetQualityRange(t *testing.T) {
	low, high, target, tolerance, err := ParseTargetQualityRange("9.45-9.55")
	if err != nil {
		t.Fatal(err)
	}
	if low != 9.45 || high != 9.55 || target != 9.5 || tolerance < 0.049 || tolerance > 0.051 {
		t.Fatalf("unexpected target range: low=%g high=%g target=%g tolerance=%g", low, high, target, tolerance)
	}
	if _, _, _, _, err := ParseTargetQualityRange("9-11"); err == nil {
		t.Fatal("ParseTargetQualityRange accepted >10 high bound")
	}
}

func TestParseCRFSearchRange(t *testing.T) {
	low, high, err := ParseCRFSearchRange("4.25-63.75")
	if err != nil {
		t.Fatal(err)
	}
	if low != 4.25 || high != 63.75 {
		t.Fatalf("got %g-%g", low, high)
	}
	if _, _, err := ParseCRFSearchRange("4.1-63.75"); err == nil {
		t.Fatal("ParseCRFSearchRange accepted non-quarter bound")
	}
}
