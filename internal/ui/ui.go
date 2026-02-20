package ui

import (
	"fmt"
	"io"
)

const (
	color  = "\033[38;5;215m"
	reset  = "\033[0m"
	prefix = "$ "
)

// Printf formats and prints a colored, prefixed message to stdout.
// Leading newlines in the format string are emitted before the colored prefix.
func Printf(format string, a ...any) {
	leading, rest := splitLeadingNewlines(format)
	if leading != "" {
		fmt.Print(leading)
	}
	msg := fmt.Sprintf(rest, a...)
	fmt.Print(color + prefix + msg + reset)
}

// Println prints a colored, prefixed message to stdout.
// Leading newlines are emitted before the colored prefix.
// If called with no arguments (or empty content), prints a blank line.
func Println(a ...any) {
	s := fmt.Sprint(a...)
	if s == "" {
		fmt.Println()
		return
	}
	leading, rest := splitLeadingNewlines(s)
	if leading != "" {
		fmt.Print(leading)
	}
	fmt.Println(color + prefix + rest + reset)
}

// Fprintf formats and prints a colored, prefixed message to the given writer.
// Leading newlines in the format string are emitted before the colored prefix.
func Fprintf(w io.Writer, format string, a ...any) {
	leading, rest := splitLeadingNewlines(format)
	if leading != "" {
		fmt.Fprint(w, leading)
	}
	msg := fmt.Sprintf(rest, a...)
	fmt.Fprint(w, color+prefix+msg+reset)
}

// splitLeadingNewlines splits s into leading newlines and the remainder.
func splitLeadingNewlines(s string) (string, string) {
	i := 0
	for i < len(s) && s[i] == '\n' {
		i++
	}
	return s[:i], s[i:]
}
