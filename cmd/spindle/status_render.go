package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
)

type statusKind int

const (
	statusInfo statusKind = iota
	statusOK
	statusWarn
	statusError
)

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiBlue   = "\x1b[34m"
)

const (
	statusLabelWidth = 20
	statusIndent     = "  "
)

func renderStatusLine(label string, kind statusKind, message string, colorize bool) string {
	statusText := statusKindLabel(kind)
	if message != "" {
		statusText = fmt.Sprintf("[%s] %s", statusText, message)
	} else {
		statusText = fmt.Sprintf("[%s]", statusText)
	}
	base := fmt.Sprintf("%s%-*s %s", statusIndent, statusLabelWidth, label+":", statusText)
	if colorize {
		if color := statusKindColor(kind); color != "" {
			return color + base + ansiReset
		}
	}
	return base
}

func statusKindLabel(kind statusKind) string {
	switch kind {
	case statusOK:
		return "OK"
	case statusWarn:
		return "WARN"
	case statusError:
		return "ERROR"
	default:
		return "INFO"
	}
}

func statusKindColor(kind statusKind) string {
	switch kind {
	case statusOK:
		return ansiGreen
	case statusWarn:
		return ansiYellow
	case statusError:
		return ansiRed
	case statusInfo:
		return ansiBlue
	default:
		return ""
	}
}

func renderSectionHeader(title string, colorize bool) []string {
	line := fmt.Sprintf("== %s ==", strings.TrimSpace(title))
	rule := strings.Repeat("-", len(line))
	if colorize {
		line = ansiBlue + line + ansiReset
		rule = ansiBlue + rule + ansiReset
	}
	return []string{line, rule}
}

func shouldColorize(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	fd := file.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}
