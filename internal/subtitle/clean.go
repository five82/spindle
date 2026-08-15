package subtitle

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/five82/spindle/internal/srtutil"
)

// cleanStats summarizes downloaded-subtitle cleanup decisions.
type cleanStats struct {
	OriginalCues int
	CleanedCues  int
	SpamCues     int
	EmptiedCues  int
}

// spamCuePatterns match uploader signatures, promotional lines, and
// subtitle-site advertising that OpenSubtitles uploads commonly carry. A cue
// with any matching line is dropped whole: these lines are never dialogue.
var spamCuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)opensubtitles|addic7ed|subscene|podnapisi|yts\.|yify|tvsubtitles|subdivx|\bsubrip\b`),
	regexp.MustCompile(`(?i)www\.[^\s]+\.[a-z]{2,}|https?://`),
	regexp.MustCompile(`(?i)\b[\w.+-]+@[\w-]+(\.[\w-]+)+\b`),
	regexp.MustCompile(`(?i)\b(subtitles?|captions?)\s+(by|:)`),
	regexp.MustCompile(`(?i)^\s*[-*♪#\s]*(subtitle[sd]?|subs|caption(s|ed|ing)?|sync(ed|hronized)?|corrected|resync(ed)?|encoded|ripped|verified|provided|uploaded|translat(ed|ion)|transcri(bed|pt)|edited|improved|creat(ed|or)|timings?)([\s&,+/]+(and\s+)?\w+)*\s*(by\b|:)`),
	regexp.MustCompile(`(?i)^\s*downloaded\s+from\b|caption(s|ing)? (paid for|sponsored) by`),
	regexp.MustCompile(`(?i)advertise your product|remove all ads|become (a )?vip( member)?|support us and|rate this subtitle|please rate|watch (any video )?online|free from ads`),
}

// lineBreakMarkerPattern matches literal line-break markers ([br], <br>) that
// converted uploads carry; they must become real newlines before bracket and
// tag stripping would otherwise glue the surrounding words together.
var lineBreakMarkerPattern = regexp.MustCompile(`(?i)\[br\]|<br\s*/?>`)

// assOverrideTagPattern matches {\an8}-style ASS override blocks that some
// converted subtitles carry.
var assOverrideTagPattern = regexp.MustCompile(`\{[^}]*\}`)

// markupTagPattern matches any HTML-style tag; keepMarkupTags lists the
// display formatting Jellyfin renders in SRT and therefore survives cleanup.
var markupTagPattern = regexp.MustCompile(`</?([a-zA-Z][a-zA-Z0-9]*)[^>]*>`)

var keepMarkupTags = map[string]bool{"i": true, "b": true}

// SDH cleanup: bracketed/parenthetical sound descriptions, ALL-CAPS speaker
// labels, and music-symbol-only lines are annotation, not dialogue.
var (
	sdhBracketPattern    = regexp.MustCompile(`\[[^\]]*\]`)
	sdhParenPattern      = regexp.MustCompile(`\([^)]*\)`)
	sdhSpeakerPattern    = regexp.MustCompile(`^(-\s*)?[A-Z][A-Z0-9 .'&-]{1,24}:\s*`)
	musicSymbolOnlyLine  = regexp.MustCompile(`^[\s\x{00B6}\x{266A}\x{266B}#*♪♫]+$`)
	whitespaceRunPattern = regexp.MustCompile(`[ \t]+`)
	punctuationOnlyLine  = regexp.MustCompile(`^[\s\p{P}\p{S}]*$`)
	windows1252HighMap   = [32]rune{
		0x20AC, 0xFFFD, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
		0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0xFFFD, 0x017D, 0xFFFD,
		0xFFFD, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
		0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0xFFFD, 0x017E, 0x0178,
	}
)

// decodeSubtitleText interprets downloaded subtitle bytes as UTF-8 (with or
// without BOM), falling back to Windows-1252 for the legacy uploads that are
// not valid UTF-8.
func decodeSubtitleText(data []byte) string {
	data = bytesTrimBOM(data)
	if utf8.Valid(data) {
		return string(data)
	}
	var b strings.Builder
	b.Grow(len(data))
	for _, c := range data {
		switch {
		case c < 0x80:
			b.WriteByte(c)
		case c >= 0x80 && c <= 0x9F:
			b.WriteRune(windows1252HighMap[c-0x80])
		default:
			b.WriteRune(rune(c))
		}
	}
	return b.String()
}

func bytesTrimBOM(data []byte) []byte {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:]
	}
	return data
}

// cleanDownloadedSubtitle parses raw downloaded subtitle bytes and returns
// display-ready cues: spam cues dropped, ASS/HTML markup reduced to the
// italic/bold tags Jellyfin renders, SDH annotations stripped, and cues that
// end up empty removed. Cue timing is preserved; indexes are renumbered.
func cleanDownloadedSubtitle(data []byte) ([]srtutil.Cue, cleanStats) {
	cues := srtutil.Parse(decodeSubtitleText(data))
	stats := cleanStats{OriginalCues: len(cues)}

	out := make([]srtutil.Cue, 0, len(cues))
	for _, cue := range cues {
		raw := lineBreakMarkerPattern.ReplaceAllString(cue.Text, "\n")
		if cueIsSpam(raw) {
			stats.SpamCues++
			continue
		}
		text := cleanCueText(raw)
		if text == "" {
			stats.EmptiedCues++
			continue
		}
		cue.Text = text
		cue.Index = len(out) + 1
		out = append(out, cue)
	}
	stats.CleanedCues = len(out)
	return out, stats
}

func cueIsSpam(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		for _, pattern := range spamCuePatterns {
			if pattern.MatchString(line) {
				return true
			}
		}
	}
	return false
}

// cleanCueText strips markup and SDH annotation from one cue, returning ""
// when nothing displayable remains.
func cleanCueText(text string) string {
	text = assOverrideTagPattern.ReplaceAllString(text, "")
	text = markupTagPattern.ReplaceAllStringFunc(text, func(tag string) string {
		name := strings.ToLower(markupTagPattern.FindStringSubmatch(tag)[1])
		if !keepMarkupTags[name] {
			return ""
		}
		if strings.HasPrefix(tag, "</") {
			return "</" + name + ">"
		}
		return "<" + name + ">"
	})
	text = sdhBracketPattern.ReplaceAllString(text, "")
	text = sdhParenPattern.ReplaceAllString(text, "")

	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = sdhSpeakerPattern.ReplaceAllString(line, "$1")
		line = strings.TrimSpace(whitespaceRunPattern.ReplaceAllString(line, " "))
		if line == "" || musicSymbolOnlyLine.MatchString(line) || lineIsEmptyDialogue(line) {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// lineIsEmptyDialogue reports lines that carry no words once annotation is
// stripped: bare dashes, punctuation, or empty formatting tag pairs.
func lineIsEmptyDialogue(line string) bool {
	bare := markupTagPattern.ReplaceAllString(line, "")
	return punctuationOnlyLine.MatchString(bare)
}

// stripMarkup removes all markup tags for text-similarity comparison.
func stripMarkup(text string) string {
	text = assOverrideTagPattern.ReplaceAllString(text, "")
	return markupTagPattern.ReplaceAllString(text, "")
}
