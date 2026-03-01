package main

import (
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
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

	tw := table.NewWriter()
	tw.SetStyle(table.StyleRounded)

	header := make(table.Row, columns)
	for i := 0; i < columns; i++ {
		header[i] = headers[i]
	}
	tw.AppendHeader(header)

	for _, row := range rows {
		r := make(table.Row, columns)
		for i := 0; i < columns; i++ {
			if i < len(row) {
				r[i] = row[i]
			} else {
				r[i] = ""
			}
		}
		tw.AppendRow(r)
	}

	columnConfigs := make([]table.ColumnConfig, 0, columns)
	for i := 0; i < columns; i++ {
		align := text.AlignLeft
		if i < len(aligns) && aligns[i] == alignRight {
			align = text.AlignRight
		}
		columnConfigs = append(columnConfigs, table.ColumnConfig{
			Number:      i + 1,
			Align:       align,
			AlignHeader: text.AlignLeft,
		})
	}
	tw.SetColumnConfigs(columnConfigs)

	return tw.Render()
}
