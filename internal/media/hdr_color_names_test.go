package media

import "testing"

func TestHDRColorCodeNamesAndUnknownFallback(t *testing.T) {
	if !IsAvailable() {
		t.Fatal("native media support unexpectedly unavailable")
	}
	for _, tc := range []struct {
		code                        int32
		primaries, transfer, matrix string
	}{
		{1, "bt709", "bt709", "bt709"},
		{4, "bt470m", "gamma22", "fcc"},
		{5, "bt470bg", "gamma28", "bt470bg"},
		{6, "smpte170m", "smpte170m", "smpte170m"},
		{7, "smpte240m", "smpte240m", "smpte240m"},
		{9, "bt2020", "", "bt2020nc"},
		{10, "", "", "bt2020c"},
		{12, "smpte431", "", "chromaticity-derived-nc"},
		{13, "", "iec61966-2-1", "chromaticity-derived-c"},
		{14, "", "bt2020-10", "ictcp"},
		{15, "", "bt2020-12", ""},
		{16, "", "smpte2084", ""},
		{18, "", "arib-std-b67", ""},
		{22, "ebu3213", "", ""},
		{255, "", "", ""},
	} {
		if got := colorPrimariesName(tc.code); got != tc.primaries {
			t.Errorf("primaries %d = %q, want %q", tc.code, got, tc.primaries)
		}
		if got := colorTransferName(tc.code); got != tc.transfer {
			t.Errorf("transfer %d = %q, want %q", tc.code, got, tc.transfer)
		}
		if got := colorSpaceName(tc.code); got != tc.matrix {
			t.Errorf("matrix %d = %q, want %q", tc.code, got, tc.matrix)
		}
	}
}
