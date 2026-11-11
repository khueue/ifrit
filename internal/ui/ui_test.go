package ui

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func TestEmitPlain(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want string
	}{
		{"simple", "hello\n", "$ hello\n"},
		{"no trailing newline", "hello", "$ hello"},
		{"leading newlines stay before the prefix", "\n\nhello\n", "\n\n$ hello\n"},
		{"inner newlines are kept", "one\ntwo\n", "$ one\ntwo\n"},
		{"only newlines", "\n", "\n"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			emit(&buf, orange, tt.msg)
			if got := buf.String(); got != tt.want {
				t.Errorf("emit(%q) = %q, want %q", tt.msg, got, tt.want)
			}
		})
	}
}

func TestFprintfFormats(t *testing.T) {
	var buf bytes.Buffer
	Fprintf(&buf, "\n=== Project: %s (%d) ===\n", "backend", 2)
	if got, want := buf.String(), "\n$ === Project: backend (2) ===\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Output that is not a terminal (a pipe, a file, a buffer) must never contain
// escape codes, or they end up in scripts and log files.
func TestNoColorWhenNotATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if useColor(w) {
		t.Error("useColor(pipe) = true")
	}
	Fprintf(w, "ifrit %s\n", "1.2.3")
	w.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if want := "$ ifrit 1.2.3\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// captureStdout runs fn with os.Stdout redirected to a pipe.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestPrintln(t *testing.T) {
	got := captureStdout(t, func() {
		Println("one")
		Println()
		Println("\ntwo")
		Printf("three %d\n", 3)
	})
	if want := "$ one\n\n\n$ two\n$ three 3\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
