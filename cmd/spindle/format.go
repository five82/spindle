package main

import "github.com/fatih/color"

var (
	headerStyle  = color.New(color.Bold).SprintFunc()
	labelStyle   = color.New(color.Bold).SprintFunc()
	successStyle = color.New(color.FgGreen).SprintFunc()
	failStyle    = color.New(color.FgRed).SprintFunc()
	warnStyle    = color.New(color.FgYellow).SprintFunc()
	dimStyle     = color.New(color.Faint).SprintFunc()
)
