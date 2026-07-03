package deps

import "testing"

func TestCheckRequirements(t *testing.T) {
	tests := []struct {
		name      string
		req       Requirement
		wantAvail bool
	}{
		{
			name: "known binary is available",
			req: Requirement{
				Name:        "go",
				Command:     "go",
				Description: "Go toolchain",
			},
			wantAvail: true,
		},
		{
			name: "unknown binary is not available",
			req: Requirement{
				Name:        "nonexistent",
				Command:     "spindle-nonexistent-binary-xyz",
				Description: "should not exist",
			},
			wantAvail: false,
		},
		{
			name: "optional unknown binary is not available",
			req: Requirement{
				Name:        "optional-missing",
				Command:     "spindle-nonexistent-optional-xyz",
				Description: "optional missing binary",
				Optional:    true,
			},
			wantAvail: false,
		},
		{
			name: "unknown library is not available",
			req: Requirement{
				Name:        "missing-lib",
				Command:     "libspindle-nonexistent-xyz.so",
				Description: "missing library",
				Library:     true,
			},
			wantAvail: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := CheckRequirements([]Requirement{tt.req})
			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}
			s := results[0]
			if s.Available != tt.wantAvail {
				t.Errorf("Available = %v, want %v (detail: %s)", s.Available, tt.wantAvail, s.Detail)
			}
			if s.Name != tt.req.Name {
				t.Errorf("Name = %q, want %q", s.Name, tt.req.Name)
			}
			if s.Available && s.Detail == "" {
				t.Error("expected non-empty Detail for available binary")
			}
			if !s.Available && s.Detail == "" {
				t.Error("expected non-empty Detail for unavailable binary")
			}
		})
	}
}

func TestParseLDConfig(t *testing.T) {
	output := `
	libavformat.so.61 (libc6,x86-64) => /lib/x86_64-linux-gnu/libavformat.so.61
	libopusenc.so.0 (libc6,x86-64) => /lib/x86_64-linux-gnu/libopusenc.so.0
`
	if got := parseLDConfig("libavformat.so", output); got != "/lib/x86_64-linux-gnu/libavformat.so.61" {
		t.Fatalf("parseLDConfig libavformat.so = %q", got)
	}
	if got := parseLDConfig("libopusenc.so", output); got != "/lib/x86_64-linux-gnu/libopusenc.so.0" {
		t.Fatalf("parseLDConfig libopusenc.so = %q", got)
	}
	if got := parseLDConfig("libmissing.so", output); got != "" {
		t.Fatalf("parseLDConfig libmissing.so = %q", got)
	}
}

func TestCheckRequirements_preservesOrder(t *testing.T) {
	reqs := []Requirement{
		{Name: "first", Command: "go", Description: "Go"},
		{Name: "second", Command: "spindle-nonexistent-xyz", Description: "missing"},
	}
	results := CheckRequirements(reqs)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Name != "first" || results[1].Name != "second" {
		t.Error("result order does not match input order")
	}
	if !results[0].Available {
		t.Error("expected go to be available")
	}
	if results[1].Available {
		t.Error("expected missing binary to be unavailable")
	}
}
