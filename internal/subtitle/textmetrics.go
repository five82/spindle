package subtitle

import "unicode"

func lexicalTokens(text string) []string {
	var (
		tokens []string
		buf    []rune
	)
	flush := func() {
		if len(buf) == 0 {
			return
		}
		tokens = append(tokens, string(buf))
		buf = buf[:0]
	}
	for _, r := range text {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			buf = append(buf, unicode.ToLower(r))
		case (r == '\'' || r == '-') && len(buf) > 0:
			buf = append(buf, r)
		default:
			flush()
		}
	}
	flush()
	return tokens
}

func lexicalWordCount(text string) int {
	return len(lexicalTokens(text))
}
