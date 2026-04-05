package makemkv

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"1:30:00", 90 * time.Minute},
		{"0:45:30", 45*time.Minute + 30*time.Second},
		{"2:00:00", 2 * time.Hour},
		{"0:00:00", 0},
		{"0:01:05", time.Minute + 5*time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseDuration(tt.input)
			if got != tt.want {
				t.Errorf("parseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseDurationMalformed(t *testing.T) {
	tests := []string{
		"",
		"invalid",
		"1:30",
		"a:b:c",
		"1:2:3:4",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			got := parseDuration(input)
			if got != 0 {
				t.Errorf("parseDuration(%q) = %v, want 0", input, got)
			}
		})
	}
}

func TestParseRobotOutputTINFO(t *testing.T) {
	lines := []string{
		`TINFO:0,2,0,"Main Feature"`,
		`TINFO:0,8,0,"25"`,
		`TINFO:0,9,0,"1:30:00"`,
		`TINFO:0,10,0,"15000000000"`,
		`TINFO:0,16,0,"00800.mpls"`,
		`TINFO:0,25,0,"3"`,
		`TINFO:0,26,0,"00001.m2ts,00002.m2ts,00003.m2ts"`,
		`TINFO:1,2,0,"Bonus"`,
		`TINFO:1,9,0,"0:10:00"`,
	}

	info := parseRobotOutput(lines)

	if len(info.Titles) != 2 {
		t.Fatalf("got %d titles, want 2", len(info.Titles))
	}

	title0 := info.Titles[0]
	if title0.ID != 0 {
		t.Errorf("title 0 ID = %d, want 0", title0.ID)
	}
	if title0.Name != "Main Feature" {
		t.Errorf("title 0 Name = %q, want %q", title0.Name, "Main Feature")
	}
	if title0.Duration != 90*time.Minute {
		t.Errorf("title 0 Duration = %v, want %v", title0.Duration, 90*time.Minute)
	}
	if title0.Chapters != 25 {
		t.Errorf("title 0 Chapters = %d, want 25", title0.Chapters)
	}
	if title0.SizeBytes != 15000000000 {
		t.Errorf("title 0 SizeBytes = %d, want 15000000000", title0.SizeBytes)
	}
	if title0.Playlist != "00800.mpls" {
		t.Errorf("title 0 Playlist = %q, want %q", title0.Playlist, "00800.mpls")
	}
	if title0.SegmentCount != 3 {
		t.Errorf("title 0 SegmentCount = %d, want 3", title0.SegmentCount)
	}
	if title0.SegmentMap != "00001.m2ts,00002.m2ts,00003.m2ts" {
		t.Errorf("title 0 SegmentMap = %q, want m2ts segment references", title0.SegmentMap)
	}

	title1 := info.Titles[1]
	if title1.ID != 1 {
		t.Errorf("title 1 ID = %d, want 1", title1.ID)
	}
	if title1.Name != "Bonus" {
		t.Errorf("title 1 Name = %q, want %q", title1.Name, "Bonus")
	}
	if title1.Duration != 10*time.Minute {
		t.Errorf("title 1 Duration = %v, want %v", title1.Duration, 10*time.Minute)
	}
}

func TestParseRobotOutputCINFO(t *testing.T) {
	lines := []string{
		`CINFO:2,0,"My Disc Name"`,
		`CINFO:1,0,"something"`,
	}

	info := parseRobotOutput(lines)

	if info.Name != "My Disc Name" {
		t.Errorf("disc Name = %q, want %q", info.Name, "My Disc Name")
	}
}

func TestParsePRGV(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		titleID int
		wantOK  bool
		wantPct float64
		wantCur int
		wantTot int
	}{
		{
			name:    "normal progress",
			line:    "PRGV:50,100,200",
			titleID: 3,
			wantOK:  true,
			wantPct: 25.0,
			wantCur: 50,
			wantTot: 100,
		},
		{
			name:    "complete",
			line:    "PRGV:200,200,200",
			titleID: 0,
			wantOK:  true,
			wantPct: 100.0,
			wantCur: 200,
			wantTot: 200,
		},
		{
			name:    "zero max",
			line:    "PRGV:0,0,0",
			titleID: 0,
			wantOK:  true,
			wantPct: 0,
			wantCur: 0,
			wantTot: 0,
		},
		{
			name:   "not prgv",
			line:   "TINFO:0,2,0,\"test\"",
			wantOK: false,
		},
		{
			name:   "malformed",
			line:   "PRGV:abc",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := parsePRGV(tt.line, tt.titleID)
			if ok != tt.wantOK {
				t.Fatalf("parsePRGV ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if p.TitleID != tt.titleID {
				t.Errorf("TitleID = %d, want %d", p.TitleID, tt.titleID)
			}
			if p.Current != tt.wantCur {
				t.Errorf("Current = %d, want %d", p.Current, tt.wantCur)
			}
			if p.Total != tt.wantTot {
				t.Errorf("Total = %d, want %d", p.Total, tt.wantTot)
			}
			if p.Percent != tt.wantPct {
				t.Errorf("Percent = %f, want %f", p.Percent, tt.wantPct)
			}
		})
	}
}

func TestParseRobotOutputMalformedLines(t *testing.T) {
	lines := []string{
		"",
		"garbage without colon",
		"TINFO:",
		"TINFO:not,enough",
		"TINFO:abc,2,0,\"value\"",
		"CINFO:",
		"CINFO:abc,0,\"value\"",
		`TINFO:0,2,0,"Valid Title"`,
		"MSG:1234,0,1,\"Some message\",\"format\"",
		"DRV:0,2,999,1,\"Blu-ray\",\"/dev/sr0\",\"disc:0\"",
	}

	info := parseRobotOutput(lines)

	// Should still parse the valid TINFO line despite malformed ones.
	if len(info.Titles) != 1 {
		t.Fatalf("got %d titles, want 1", len(info.Titles))
	}
	if info.Titles[0].Name != "Valid Title" {
		t.Errorf("title Name = %q, want %q", info.Titles[0].Name, "Valid Title")
	}
}

func TestParseRobotOutputRawLines(t *testing.T) {
	lines := []string{"line1", "line2"}
	info := parseRobotOutput(lines)
	if len(info.RawLines) != 2 {
		t.Errorf("RawLines length = %d, want 2", len(info.RawLines))
	}
}

func TestNormalizeDevice(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "disc:0"},
		{"/dev/sr0", "dev:/dev/sr0"},
		{"disc:0", "disc:0"},
		{"disc:1", "disc:1"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeDevice(tt.input)
			if got != tt.want {
				t.Errorf("normalizeDevice(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestHasForcedEnglishSubtitles(t *testing.T) {
	tests := []struct {
		name     string
		rawLines []string
		want     bool
	}{
		{
			name: "forced english subtitle",
			rawLines: []string{
				`SINFO:0,0,1,6209,"Video"`,
				`SINFO:0,1,1,6210,"Audio"`,
				`SINFO:0,1,3,6210,"eng"`,
				`SINFO:0,2,1,6211,"Subtitle"`,
				`SINFO:0,2,3,6211,"eng"`,
				`SINFO:0,2,30,6211,"PGS English (forced only)"`,
			},
			want: true,
		},
		{
			name: "forced non-english subtitle",
			rawLines: []string{
				`SINFO:0,0,1,6209,"Subtitle"`,
				`SINFO:0,0,3,6209,"spa"`,
				`SINFO:0,0,30,6209,"PGS Spanish (forced only)"`,
			},
			want: false,
		},
		{
			name: "forced subtitle in second title",
			rawLines: []string{
				`SINFO:0,0,1,6209,"Video"`,
				`SINFO:1,0,1,6209,"Video"`,
				`SINFO:1,1,1,6210,"Subtitle"`,
				`SINFO:1,1,3,6210,"eng"`,
				`SINFO:1,1,30,6210,"Subtitles (forced only)"`,
			},
			want: true,
		},
		{
			name: "no forced subtitles",
			rawLines: []string{
				`SINFO:0,0,1,6209,"Video"`,
				`SINFO:0,1,1,6210,"Subtitle"`,
				`SINFO:0,1,3,6210,"eng"`,
				`SINFO:0,1,30,6210,"PGS English"`,
			},
			want: false,
		},
		{
			name:     "no subtitle tracks",
			rawLines: []string{`SINFO:0,0,1,6209,"Video"`},
			want:     false,
		},
		{
			name:     "nil disc info",
			rawLines: nil,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var info *DiscInfo
			if tt.rawLines != nil {
				info = &DiscInfo{RawLines: tt.rawLines}
			}
			got := info.HasForcedEnglishSubtitles()
			if got != tt.want {
				t.Errorf("HasForcedEnglishSubtitles() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSplitFields(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		n      int
		wantN  int
		want0  string
		wantLn string // last field
	}{
		{
			name:   "simple",
			input:  `0,2,0,"Hello"`,
			n:      4,
			wantN:  4,
			want0:  "0",
			wantLn: `"Hello"`,
		},
		{
			name:   "quoted comma",
			input:  `0,16,0,"seg1,seg2,seg3"`,
			n:      4,
			wantN:  4,
			want0:  "0",
			wantLn: `"seg1,seg2,seg3"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitFields(tt.input, tt.n)
			if len(got) != tt.wantN {
				t.Fatalf("splitFields returned %d fields, want %d: %v", len(got), tt.wantN, got)
			}
			if got[0] != tt.want0 {
				t.Errorf("field[0] = %q, want %q", got[0], tt.want0)
			}
			if got[len(got)-1] != tt.wantLn {
				t.Errorf("last field = %q, want %q", got[len(got)-1], tt.wantLn)
			}
		})
	}
}

func TestParseMSG(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantOK     bool
		wantCode   int
		wantFlags  int
		wantMsg    string
		wantParams []string
		wantError  bool
	}{
		{
			name:      "startup info",
			line:      `MSG:1005,0,1,"MakeMKV v1.18.3 linux(x64-release) started","%1 started","MakeMKV v1.18.3 linux(x64-release)"`,
			wantOK:    true,
			wantCode:  1005,
			wantFlags: 0,
			wantMsg:   "MakeMKV v1.18.3 linux(x64-release) started",
			wantParams: []string{
				"MakeMKV v1.18.3 linux(x64-release)",
			},
		},
		{
			name:       "copy complete summary",
			line:       `MSG:5036,0,2,"Copy complete. 1 titles saved, 0 failed.","Copy complete. %1 titles saved, %2 failed.","1","0"`,
			wantOK:     true,
			wantCode:   5036,
			wantFlags:  0,
			wantMsg:    "Copy complete. 1 titles saved, 0 failed.",
			wantParams: []string{"1", "0"},
		},
		{
			name:      "error flag set",
			line:      `MSG:2024,1,1,"Failed to save title","Failed to save title %1","1"`,
			wantOK:    true,
			wantCode:  2024,
			wantFlags: 1,
			wantError: true,
			wantMsg:   "Failed to save title",
			wantParams: []string{
				"1",
			},
		},
		{
			name:     "non-MSG prefix ignored",
			line:     `PRGV:100,200,65536`,
			wantOK:   false,
		},
		{
			name:     "malformed",
			line:     `MSG:not,a,number`,
			wantOK:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok := parseMSG(tt.line)
			if ok != tt.wantOK {
				t.Fatalf("parseMSG ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if msg.code != tt.wantCode {
				t.Errorf("code = %d, want %d", msg.code, tt.wantCode)
			}
			if msg.flags != tt.wantFlags {
				t.Errorf("flags = %d, want %d", msg.flags, tt.wantFlags)
			}
			if msg.message != tt.wantMsg {
				t.Errorf("message = %q, want %q", msg.message, tt.wantMsg)
			}
			if msg.isError() != tt.wantError {
				t.Errorf("isError = %v, want %v", msg.isError(), tt.wantError)
			}
			if len(msg.params) != len(tt.wantParams) {
				t.Fatalf("params len = %d, want %d (%v)", len(msg.params), len(tt.wantParams), msg.params)
			}
			for i, p := range tt.wantParams {
				if msg.params[i] != p {
					t.Errorf("params[%d] = %q, want %q", i, msg.params[i], p)
				}
			}
		})
	}
}

func TestSplitAllFieldsQuotedCommas(t *testing.T) {
	// MSG lines can have many comma-separated params; splitAllFields
	// must preserve quoted commas as literal characters inside a field.
	fields := splitAllFields(`5036,0,2,"Copy complete. 1 titles saved, 0 failed.","%1 saved, %2 failed","1","0"`)
	if len(fields) != 7 {
		t.Fatalf("expected 7 fields, got %d: %v", len(fields), fields)
	}
	if fields[3] != `"Copy complete. 1 titles saved, 0 failed."` {
		t.Errorf("fields[3] = %q", fields[3])
	}
	if fields[5] != `"1"` || fields[6] != `"0"` {
		t.Errorf("param fields = %q, %q", fields[5], fields[6])
	}
}

func TestSnapshotAndNewMKVFiles(t *testing.T) {
	dir := t.TempDir()
	// Missing dir is fine.
	if got := snapshotMKVFiles(filepath.Join(dir, "does-not-exist")); len(got) != 0 {
		t.Errorf("expected empty snapshot for missing dir, got %v", got)
	}

	// Seed one existing file.
	if err := os.WriteFile(filepath.Join(dir, "title_t01.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Unrelated file must be ignored.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	before := snapshotMKVFiles(dir)
	if _, ok := before["title_t01.mkv"]; !ok || len(before) != 1 {
		t.Fatalf("expected 1 mkv in snapshot, got %v", before)
	}

	// No new files yet.
	if got := newMKVFiles(dir, before); len(got) != 0 {
		t.Errorf("expected no new files, got %v", got)
	}

	// Add a new mkv and verify it's detected.
	if err := os.WriteFile(filepath.Join(dir, "title_t00.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatalf("add: %v", err)
	}
	got := newMKVFiles(dir, before)
	if len(got) != 1 || got[0] != "title_t00.mkv" {
		t.Errorf("expected [title_t00.mkv], got %v", got)
	}
}
