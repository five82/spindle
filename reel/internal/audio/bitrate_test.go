package audio

import "testing"

func TestCalculateBitrate(t *testing.T) {
	tests := []struct {
		channels uint32
		want     uint32
	}{
		{channels: 0, want: 0},
		{channels: 1, want: 76},
		{channels: 2, want: 128},
		{channels: 3, want: 132},
		{channels: 4, want: 177},
		{channels: 5, want: 219},
		{channels: 6, want: 258},
		{channels: 7, want: 295},
		{channels: 8, want: 331},
		{channels: 10, want: 427},
	}

	for _, tt := range tests {
		if got := CalculateBitrate(tt.channels); got != tt.want {
			t.Fatalf("CalculateBitrate(%d) = %d, want %d", tt.channels, got, tt.want)
		}
	}
}
