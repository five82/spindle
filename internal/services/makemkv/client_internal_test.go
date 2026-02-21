package makemkv

import "testing"

func TestParseMSGCode(t *testing.T) {
	tests := []struct {
		name string
		line string
		want int
	}{
		{"error code", "MSG:5010,0,1,\"SCSI error\",\"format\"", 5010},
		{"info code", "MSG:1001,0,1,\"info message\",\"format\"", 1001},
		{"zero code", "MSG:0,0,1,\"msg\",\"fmt\"", 0},
		{"non-MSG line", "PRGV:0,50,100", -1},
		{"empty line", "", -1},
		{"MSG prefix only", "MSG:", -1},
		{"MSG no comma", "MSG:abc", -1},
		{"MSG non-numeric code", "MSG:abc,0,1,\"msg\"", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseMSGCode(tt.line)
			if got != tt.want {
				t.Errorf("parseMSGCode(%q) = %d, want %d", tt.line, got, tt.want)
			}
		})
	}
}

func TestParseMSGText(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			"standard error",
			`MSG:5010,0,3,"SCSI error - Loss of streaming","format"`,
			"SCSI error - Loss of streaming",
		},
		{
			"info message",
			`MSG:1001,0,1,"Operation successfully completed","fmt"`,
			"Operation successfully completed",
		},
		{
			"unquoted field",
			"MSG:1001,0,1,some text,fmt",
			"some text",
		},
		{
			"non-MSG line",
			"PRGV:0,50,100",
			"",
		},
		{
			"empty line",
			"",
			"",
		},
		{
			"too few fields",
			"MSG:5010,0",
			"",
		},
		{
			"MSG prefix only",
			"MSG:",
			"",
		},
		{
			"quoted field with comma inside earlier field",
			`MSG:5010,0,3,"Error reading, disc scratched","fmt"`,
			"Error reading, disc scratched",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseMSGText(tt.line)
			if got != tt.want {
				t.Errorf("parseMSGText(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}
