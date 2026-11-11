// Package ui prints ifrit's own messages, set apart from the output of the
// commands it runs by a "$ " prefix and, on a terminal, by color.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
)

const (
	orange = "\033[38;5;215m"
	gray   = "\033[90m"
	reset  = "\033[0m"
	prefix = "$ "
)

// Printf formats and prints a prefixed message to stdout.
func Printf(format string, a ...any) {
	Fprintf(os.Stdout, format, a...)
}

// Println prints a prefixed message to stdout, followed by a newline.
// If called with no arguments (or empty content), prints a blank line.
func Println(a ...any) {
	emit(os.Stdout, orange, fmt.Sprint(a...)+"\n")
}

// Fprintf formats and prints a prefixed message to the given writer.
func Fprintf(w io.Writer, format string, a ...any) {
	emit(w, orange, fmt.Sprintf(format, a...))
}

// Debugf formats and prints a dimmed, prefixed message to stderr. It is used
// to echo the underlying commands in verbose mode.
func Debugf(format string, a ...any) {
	emit(os.Stderr, gray, fmt.Sprintf(format, a...))
}

// emit writes msg to w behind the prefix. Leading and trailing newlines stay
// outside the prefixed, colored span, so "\nfoo\n" prints a blank line and
// then "$ foo".
func emit(w io.Writer, color, msg string) {
	rest := strings.TrimLeft(msg, "\n")
	leading := msg[:len(msg)-len(rest)]
	body := strings.TrimRight(rest, "\n")
	trailing := rest[len(body):]

	switch {
	case body == "":
		// Nothing but newlines: no prefix to print.
	case useColor(w):
		body = color + prefix + body + reset
	default:
		body = prefix + body
	}
	fmt.Fprint(w, leading+body+trailing)
}

// useColor reports whether escape codes may be written to w: only when it is
// a terminal, and the user has not opted out (https://no-color.org).
func useColor(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && os.Getenv("NO_COLOR") == "" && isatty.IsTerminal(f.Fd())
}
