package main

import (
	"strings"
	"unicode/utf8"
)

type columnAlignment int

const (
	alignLeft columnAlignment = iota
	alignRight
)

func renderTable(headers []string, rows [][]string, aligns []columnAlignment) string {
	columns := len(headers)
	if columns == 0 {
		return ""
	}

	colWidths := make([]int, columns)
	for i := 0; i < columns; i++ {
		maxLen := utf8.RuneCountInString(headers[i])
		for _, row := range rows {
			if i >= len(row) {
				continue
			}
			length := utf8.RuneCountInString(row[i])
			if length > maxLen {
				maxLen = length
			}
		}
		colWidths[i] = maxLen + 2
	}

	var b strings.Builder
	writeBorder(&b, '┏', '┳', '┓', '━', colWidths)
	writeRow(&b, '┃', headers, colWidths, aligns, true)
	writeBorder(&b, '┡', '╇', '┩', '━', colWidths)
	for _, row := range rows {
		writeRow(&b, '│', row, colWidths, aligns, false)
	}
	writeBorder(&b, '└', '┴', '┘', '─', colWidths)
	return b.String()
}

func writeBorder(b *strings.Builder, left, mid, right, fill rune, widths []int) {
	b.WriteRune(left)
	for i, width := range widths {
		if width < 1 {
			width = 1
		}
		b.WriteString(strings.Repeat(string(fill), width))
		if i == len(widths)-1 {
			b.WriteRune(right)
			b.WriteRune('\n')
		} else {
			b.WriteRune(mid)
		}
	}
}

func writeRow(b *strings.Builder, edge rune, cells []string, widths []int, aligns []columnAlignment, header bool) {
	b.WriteRune(edge)
	for i, width := range widths {
		if width < 1 {
			width = 1
		}

		content := ""
		if i < len(cells) {
			content = cells[i]
		}
		textWidth := utf8.RuneCountInString(content)
		padWidth := width - 2
		if padWidth < 0 {
			padWidth = 0
		}
		if textWidth > padWidth {
			textWidth = padWidth
		}

		leftPad := 1
		rightPad := padWidth - textWidth + 1
		alignment := alignLeft
		if i < len(aligns) {
			alignment = aligns[i]
		}
		if !header && alignment == alignRight {
			leftPad = padWidth - textWidth + 1
			rightPad = 1
		}
		if leftPad < 1 {
			leftPad = 1
		}
		if rightPad < 1 {
			rightPad = 1
		}

		b.WriteString(strings.Repeat(" ", leftPad))
		if textWidth < utf8.RuneCountInString(content) {
			runes := []rune(content)
			b.WriteString(string(runes[:padWidth]))
		} else {
			b.WriteString(content)
		}
		b.WriteString(strings.Repeat(" ", rightPad))
		if i == len(widths)-1 {
			b.WriteRune(edge)
			b.WriteRune('\n')
		} else {
			b.WriteRune(edge)
		}
	}
}
