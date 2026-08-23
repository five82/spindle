package makemkv

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func observeAll(o *ripOutcome, lines []string) {
	for _, line := range lines {
		o.observe(line)
	}
}

func discardSlog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Lines shared by the verdict scenarios: the saving phase starts, progress
// reaches 43%, and no "Copy complete" summary is ever printed.
var stalledRipLines = []string{
	`PRGT:5017,0,"Scanning CD-ROM content"`,
	`PRGV:100,900,1000`, // scan-phase progress must not publish
	`PRGT:5024,0,"Saving to MKV file"`,
	`PRGV:0,100,1000`,
	`PRGV:0,430,1000`,
	`MSG:5010,1,0,"Failed to open disc","Failed to open disc"`,
}

func TestRipOutcomeVerdict(t *testing.T) {
	completeLines := []string{
		`PRGT:5024,0,"Saving to MKV file"`,
		`PRGV:0,990,1000`,
		`MSG:5036,0,2,"Copy complete. 1 titles saved, 0 failed.","Copy complete. %1 titles saved, %2 failed.","1","0"`,
	}
	zeroSavedLines := []string{
		`PRGT:5024,0,"Saving to MKV file"`,
		`MSG:5003,1,0,"Error while reading title","Error while reading title"`,
		`MSG:5036,0,2,"Copy complete. 0 titles saved, 1 failed.","Copy complete. %1 titles saved, %2 failed.","0","1"`,
	}

	tests := []struct {
		name     string
		lines    []string
		waitErr  error
		newFiles []string
		wantErr  []string // substrings; empty means success
	}{
		{
			name:     "clean rip",
			lines:    completeLines,
			newFiles: []string{"title_t00.mkv"},
		},
		{
			name:     "new file without summary line still succeeds",
			lines:    []string{`PRGT:5024,0,"Saving to MKV file"`},
			newFiles: []string{"title_t00.mkv"},
		},
		{
			name:    "nonzero exit surfaces message diagnostics",
			lines:   stalledRipLines,
			waitErr: errors.New("exit status 1"),
			wantErr: []string{"exit status 1", "error_messages=1", `last="Failed to open disc"`},
		},
		{
			name:    "exit 0 with no new file is unreadable sectors",
			lines:   stalledRipLines,
			wantErr: []string{"exited 0 but produced no output", "stalled at 43.0%", "saved=-1", `last="Failed to open disc"`},
		},
		{
			name:     "summary reporting zero saved fails despite new file",
			lines:    zeroSavedLines,
			newFiles: []string{"title_t00.mkv"},
			wantErr:  []string{"summary reports zero saved", "failed=1", "errors=1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := newRipOutcome(0, func(RipProgress) {}, discardSlog())
			observeAll(o, tt.lines)
			err := o.verdict(tt.waitErr, tt.newFiles, "/staging")
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("verdict: %v, want success", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("verdict succeeded, want error containing %q", tt.wantErr)
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("verdict error %q missing %q", err.Error(), want)
				}
			}
		})
	}
}

func TestRipOutcomeObserveSummaryCounts(t *testing.T) {
	o := newRipOutcome(0, nil, discardSlog())
	if o.savedCount != -1 || o.failedCount != -1 {
		t.Fatalf("initial counts = %d/%d, want -1/-1", o.savedCount, o.failedCount)
	}
	o.observe(`MSG:5036,0,2,"Copy complete. 2 titles saved, 1 failed.","Copy complete. %1 titles saved, %2 failed.","2","1"`)
	if o.savedCount != 2 || o.failedCount != 1 {
		t.Fatalf("counts = %d/%d, want 2/1", o.savedCount, o.failedCount)
	}
}

func TestRipOutcomeProgressPublishing(t *testing.T) {
	var published []float64
	o := newRipOutcome(3, func(p RipProgress) { published = append(published, p.Percent) }, discardSlog())
	observeAll(o, []string{
		`PRGV:0,500,1000`, // before any PRGT: scan/open phase, dropped
		`PRGT:5017,0,"Scanning CD-ROM content"`,
		`PRGV:0,900,1000`, // scan phase, dropped
		`PRGT:5024,0,"Saving to MKV file"`,
		`PRGV:0,100,1000`,  // 10%, published
		`PRGV:0,100,1000`,  // repeat, throttled
		`PRGV:0,50,1000`,   // regression, throttled
		`PRGV:0,430,1000`,  // 43%, published
		`PRGV:0,1000,1000`, // 100% is reserved for completion, dropped
	})
	want := []float64{10, 43}
	if len(published) != len(want) {
		t.Fatalf("published %v, want %v", published, want)
	}
	for i := range want {
		if published[i] != want[i] {
			t.Fatalf("published %v, want %v", published, want)
		}
	}
	if o.publishedPercent != 43 {
		t.Fatalf("publishedPercent = %v, want 43", o.publishedPercent)
	}
}
