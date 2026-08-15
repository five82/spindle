package subtitle

import (
	"fmt"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/srtutil"
)

func srtBytes(cues ...string) []byte {
	var b strings.Builder
	for i, text := range cues {
		start := float64(i * 4)
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1,
			srtutil.FormatTimestamp(start), srtutil.FormatTimestamp(start+3), text)
	}
	return []byte(b.String())
}

func TestCleanDownloadedSubtitleDropsSpamCues(t *testing.T) {
	spam := []string{
		"Downloaded from www.OpenSubtitles.org",
		"Subtitles by explosiveskull",
		"Synced & corrected by PopcornAWH",
		"Advertise your product or brand here",
		"Support us and become VIP member",
		"Visit https://example.com today",
		"Ripped with SubRip 1.17 and Verified by CdinT\ncdint@hotmail.com",
		"Creator: Deluce\nhappy x-mas, y'all.",
		"Translation/timings/creator by: Deluce",
		"Captioned by Media Access\nGroup at WGBH access.wgbh.org",
		"Subtitled by Acme Media",
		"English Subtitles by\nGELULA & CO., INC.",
	}
	for _, line := range spam {
		cues, stats := cleanDownloadedSubtitle(srtBytes(line, "Real dialogue stays right here."))
		if stats.SpamCues != 1 {
			t.Fatalf("spam line %q: SpamCues = %d, want 1", line, stats.SpamCues)
		}
		if len(cues) != 1 || cues[0].Text != "Real dialogue stays right here." {
			t.Fatalf("spam line %q: cues = %+v", line, cues)
		}
	}
}

func TestCleanDownloadedSubtitleKeepsDialogue(t *testing.T) {
	lines := []string{
		"I corrected the report this morning.",
		"We can sync our watches later.",
		"He was ripped apart by the critics.",
	}
	cues, stats := cleanDownloadedSubtitle(srtBytes(lines...))
	if stats.SpamCues != 0 || len(cues) != len(lines) {
		t.Fatalf("dialogue was dropped: stats=%+v cues=%d", stats, len(cues))
	}
}

func TestCleanDownloadedSubtitleConvertsLineBreakMarkers(t *testing.T) {
	cues, _ := cleanDownloadedSubtitle(srtBytes(
		"And now, a fireside chat[br]with the creators of South Park:",
		"It's in the bedroom, ladies.<br/>Come on in.",
	))
	if len(cues) != 2 {
		t.Fatalf("cues = %+v", cues)
	}
	if cues[0].Text != "And now, a fireside chat\nwith the creators of South Park:" {
		t.Fatalf("[br] not converted: %q", cues[0].Text)
	}
	if cues[1].Text != "It's in the bedroom, ladies.\nCome on in." {
		t.Fatalf("<br/> not converted: %q", cues[1].Text)
	}
}

func TestCleanDownloadedSubtitleStripsMarkup(t *testing.T) {
	cues, _ := cleanDownloadedSubtitle(srtBytes(
		`{\an8}<font color="#ffff00"><i>He is coming.</i></font>`,
	))
	if len(cues) != 1 || cues[0].Text != "<i>He is coming.</i>" {
		t.Fatalf("cues = %+v", cues)
	}
}

func TestCleanDownloadedSubtitleStripsSDH(t *testing.T) {
	cues, stats := cleanDownloadedSubtitle(srtBytes(
		"[door slams]",
		"MAN: Get out of here!",
		"(whispering) It's behind you.",
		"♪ ♪",
		"- SARAH: Run!\n- Where?",
	))
	if len(cues) != 3 {
		t.Fatalf("cues = %+v (stats %+v)", cues, stats)
	}
	if cues[0].Text != "Get out of here!" {
		t.Fatalf("speaker label not stripped: %q", cues[0].Text)
	}
	if cues[1].Text != "It's behind you." {
		t.Fatalf("parenthetical not stripped: %q", cues[1].Text)
	}
	if cues[2].Text != "- Run!\n- Where?" {
		t.Fatalf("dialogue-dash speaker label mishandled: %q", cues[2].Text)
	}
	if stats.EmptiedCues != 2 {
		t.Fatalf("EmptiedCues = %d, want 2", stats.EmptiedCues)
	}
}

func TestCleanDownloadedSubtitleRenumbers(t *testing.T) {
	cues, _ := cleanDownloadedSubtitle(srtBytes(
		"Subtitles by nobody",
		"First line kept.",
		"Second line kept.",
	))
	if len(cues) != 2 || cues[0].Index != 1 || cues[1].Index != 2 {
		t.Fatalf("cues = %+v", cues)
	}
	if cues[0].Start != 4 || cues[1].Start != 8 {
		t.Fatalf("timing changed: %+v", cues)
	}
}

func TestDecodeSubtitleTextWindows1252(t *testing.T) {
	raw := append([]byte("It"), 0x92)
	raw = append(raw, []byte("s fine \x85 really")...)
	got := decodeSubtitleText(raw)
	if got != "It’s fine … really" {
		t.Fatalf("decoded = %q", got)
	}
}

func TestDecodeSubtitleTextUTF8BOM(t *testing.T) {
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte("Café dialogue")...)
	if got := decodeSubtitleText(raw); got != "Café dialogue" {
		t.Fatalf("decoded = %q", got)
	}
}
