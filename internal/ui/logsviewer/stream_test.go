package logsviewer

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/khueue/ifrit/internal/check"
)

// readAllLines reads input to its end through readLine.
func readAllLines(t *testing.T, input string) []string {
	t.Helper()
	r := bufio.NewReaderSize(strings.NewReader(input), readBufferSize)
	var lines []string
	for {
		line, err := readLine(r)
		if errors.Is(err, io.EOF) {
			return lines
		}
		check.NoError(t, err)
		lines = append(lines, line)
	}
}

func TestReadLine(t *testing.T) {
	long := strings.Repeat("x", 3*readBufferSize+17)

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"plain lines", "one\ntwo\n", []string{"one", "two"}},
		{"crlf", "one\r\ntwo\r\n", []string{"one", "two"}},
		{"empty lines are kept", "one\n\ntwo\n", []string{"one", "", "two"}},
		{"no final newline", "one\ntwo", []string{"one", "two"}},
		{"longer than the buffer stays whole", "a\n" + long + "\nb\n", []string{"a", long, "b"}},
		{"long line cut off by EOF", long, []string{long}},
		{"empty", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check.Slice(t, "lines", readAllLines(t, tt.input), tt.want)
		})
	}
}

// Beyond maxLineBytes a line is split, but nothing is lost and reading
// carries on with the lines after it.
func TestReadLineSplitsOversizedLine(t *testing.T) {
	huge := strings.Repeat("y", maxLineBytes+readBufferSize+5)

	got := readAllLines(t, huge+"\nafter\n")
	if len(got) < 3 {
		t.Fatalf("got %d lines, want the oversized line in pieces plus one more", len(got))
	}
	pieces, last := got[:len(got)-1], got[len(got)-1]

	check.Equal(t, "last line", last, "after")
	check.Equal(t, "bytes in pieces", len(strings.Join(pieces, "")), len(huge))
	for i, piece := range pieces {
		if len(piece) > maxLineBytes+readBufferSize {
			t.Errorf("piece %d is %d bytes, over the limit", i, len(piece))
		}
	}
}

// nextBatch calls readBatch, failing the test if it blocks.
func nextBatch(t *testing.T, r *bufio.Reader) []string {
	t.Helper()
	done := make(chan []string, 1)
	go func() {
		lines, _ := readBatch(r)
		done <- lines
	}()
	select {
	case lines := <-done:
		return lines
	case <-time.After(5 * time.Second):
		t.Fatal("readBatch blocked although lines were available")
		return nil
	}
}

// readBatch must hand over everything that is already available in one go,
// but never wait for more once it has something to deliver.
func TestReadBatch(t *testing.T) {
	pr, pw := io.Pipe()
	r := bufio.NewReaderSize(pr, readBufferSize)

	// Three complete lines and the start of a fourth, in a single write.
	go pw.Write([]byte("one\ntwo\nthree\npart"))
	check.Slice(t, "first batch", nextBatch(t, r), []string{"one", "two", "three"})

	// The rest of the fourth line arrives later.
	go pw.Write([]byte("ial\n"))
	check.Slice(t, "second batch", nextBatch(t, r), []string{"partial"})

	pw.Close()
	lines, err := readBatch(r)
	check.Equal(t, "lines at end of stream", len(lines), 0)
	check.Equal(t, "error at end of stream", err, io.EOF)
}

// cmdModel returns a sized model with one tab per command, cleaned up when
// the test ends.
func cmdModel(t *testing.T, cmds ...*exec.Cmd) *Model {
	t.Helper()
	tabs := make([]TabInfo, len(cmds))
	for i := range cmds {
		tabs[i] = TabInfo{Name: fmt.Sprintf("p/s%d", i), Group: "p"}
	}
	next := 0
	m, err := New(tabs, func(string) (*exec.Cmd, error) {
		next++
		return cmds[next-1], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.killAll)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	return m
}

// shModel returns a model with one tab per shell script.
func shModel(t *testing.T, scripts ...string) *Model {
	t.Helper()
	cmds := make([]*exec.Cmd, len(scripts))
	for i, script := range scripts {
		cmds[i] = exec.Command("sh", "-c", script)
	}
	return cmdModel(t, cmds...)
}

// runtime stands in for the Bubble Tea runtime: it runs commands on
// goroutines of their own and collects the messages they produce.
type runtime struct {
	t    *testing.T
	msgs chan tea.Msg
}

func newRuntime(t *testing.T) *runtime {
	return &runtime{t: t, msgs: make(chan tea.Msg, 1024)}
}

// run executes cmd in the background, unpacking batches.
func (r *runtime) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				r.run(c)
			}
			return
		}
		r.msgs <- msg
	}()
}

// next waits for the next message.
func (r *runtime) next() tea.Msg {
	r.t.Helper()
	select {
	case msg := <-r.msgs:
		return msg
	case <-time.After(30 * time.Second):
		r.t.Fatal("timed out waiting for a message")
		return nil
	}
}

// deliver feeds the next message to the model, runs the command that comes
// back, and returns the message.
func (r *runtime) deliver(m *Model) tea.Msg {
	r.t.Helper()
	msg := r.next()
	_, cmd := m.Update(msg)
	r.run(cmd)
	return msg
}

// awaitExits waits for n processes to exit, without involving the model (as
// after a quit). Lines read before the exit may still be on their way.
func (r *runtime) awaitExits(n int) {
	r.t.Helper()
	for n > 0 {
		switch msg := r.next().(type) {
		case logLinesMsg:
			r.run(msg.next)
		case processExitedMsg:
			n--
		}
	}
}

// pump starts the model and delivers messages until all processes exited.
func pump(t *testing.T, m *Model) {
	t.Helper()
	rt := newRuntime(t)
	rt.run(m.Init())
	for running := len(m.tabs); running > 0; {
		if _, ok := rt.deliver(m).(processExitedMsg); ok {
			running--
		}
	}
}

func TestStreamsProcessOutput(t *testing.T) {
	m := shModel(t,
		`echo out-1; echo err-1 >&2; echo out-2`,
		`printf 'no newline at end'`,
		`exit 3`,
	)
	pump(t, m)

	// stdout and stderr share one stream.
	check.Slice(t, "tab 0", m.tabs[0].lines, []string{"out-1", "err-1", "out-2", " [process exited]"})
	check.Slice(t, "tab 1", m.tabs[1].lines, []string{"no newline at end", " [process exited]"})
	check.Slice(t, "tab 2", m.tabs[2].lines, []string{" [process exited: exit status 3]"})
	check.Equal(t, "tab 1 unread", m.tabs[1].hasUnread, true)
	check.Equal(t, "tab 2 unread", m.tabs[2].hasUnread, true)
}

// A line longer than any buffer must not stall the stream: everything after
// it still has to arrive, and the process has to be able to finish.
func TestVeryLongLineDoesNotStallStream(t *testing.T) {
	m := shModel(t, `echo first; head -c 3000000 /dev/zero | tr '\0' 'a'; echo; echo last`)
	pump(t, m)

	lines := m.tabs[0].lines
	if len(lines) < 4 {
		t.Fatalf("got %d lines", len(lines))
	}
	check.Equal(t, "first line", lines[0], "first")
	check.Slice(t, "end of stream", lines[len(lines)-2:], []string{"last", " [process exited]"})
	check.Equal(t, "bytes of long line", len(strings.Join(lines[1:len(lines)-2], "")), 3000000)
}

func TestProcessThatCannotStart(t *testing.T) {
	m := cmdModel(t, exec.Command("/nonexistent/ifrit-test-binary"))
	pump(t, m)

	check.Equal(t, "line count", len(m.tabs[0].lines), 1)
	check.Contains(t, "exit notice", strings.Join(m.tabs[0].lines, "\n"), "process exited:")
	m.killAll() // must cope with a process that never started
}

func TestBuilderErrorFailsNew(t *testing.T) {
	_, err := New([]TabInfo{{Name: "p/s", Group: "p"}}, func(string) (*exec.Cmd, error) {
		return nil, errors.New("boom")
	})
	check.ErrorContains(t, err, "p/s", "boom")
}

func TestRunWithoutTabs(t *testing.T) {
	check.ErrorContains(t, Run(nil, nil), "no tabs")
}

// Quitting must end the log processes, the ones busy writing as well as the
// silent ones, and must be safe while they are being read. Run with -race:
// quitting used to race with the processes being started.
func TestQuitKillsProcesses(t *testing.T) {
	m := shModel(t,
		`while true; do echo busy; done`,
		`sleep 600`,
	)
	rt := newRuntime(t)
	rt.run(m.Init())

	// Let some output through, then quit mid-stream.
	for len(m.tabs[0].lines) < 100 {
		rt.deliver(m)
	}
	_, quit := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if quit == nil {
		t.Fatal("q should return a quit cmd")
	}

	// Both readers must now come back with an exit.
	rt.awaitExits(2)
	for i, cmd := range m.cmds {
		check.Equal(t, fmt.Sprintf("process %d reaped", i), cmd.ProcessState != nil, true)
	}

	m.killAll() // a second call (Run defers one) must be harmless
}
