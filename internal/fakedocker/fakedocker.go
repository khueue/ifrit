// Package fakedocker lets tests run ifrit against a stand-in for the docker
// CLI. The stand-in is the test binary itself, put on PATH under the name
// "docker": it records how it was called and answers with canned output.
package fakedocker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Environment variables steering the fake.
const (
	logEnv      = "FAKE_DOCKER_LOG"      // file that calls are appended to
	NetworksEnv = "FAKE_DOCKER_NETWORKS" // space-separated, listed by "network ls"
	ServicesEnv = "FAKE_DOCKER_SERVICES" // space-separated, listed by "config --services"
	FailEnv     = "FAKE_DOCKER_FAIL"     // calls containing this text fail with status 3
)

// RunIfInvoked turns the process into the fake docker when it was started
// under that name. Call it first thing in TestMain.
func RunIfInvoked() {
	if name := filepath.Base(os.Args[0]); name != "docker" {
		return
	}
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	line := strings.Join(args, " ")

	if path := os.Getenv(logEnv); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fake docker:", err)
			return 99
		}
		entry, _ := json.Marshal(args)
		fmt.Fprintf(f, "%s\n", entry)
		f.Close()
	}

	if fail := os.Getenv(FailEnv); fail != "" && strings.Contains(line, fail) {
		fmt.Fprintln(os.Stderr, "fake docker: boom")
		return 3
	}

	switch {
	case strings.HasPrefix(line, "network ls --format"):
		printLines(os.Getenv(NetworksEnv))
	case strings.HasSuffix(line, "config --services"):
		printLines(os.Getenv(ServicesEnv))
	case strings.Contains(line, " logs"):
		fmt.Println("svc-1  | a log line")
	}
	return 0
}

func printLines(words string) {
	for w := range strings.FieldsSeq(words) {
		fmt.Println(w)
	}
}

// Fake is an installed fake docker.
type Fake struct {
	log string
}

// Install puts the fake docker first on PATH for the duration of the test,
// and points the user's home and cache directories at a temp dir so nothing
// is written outside of it.
func Install(t testing.TB) *Fake {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake docker needs symlinks")
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(exe, filepath.Join(dir, "docker")); err != nil {
		t.Fatal(err)
	}

	f := &Fake{log: filepath.Join(dir, "calls.log")}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(logEnv, f.log)
	t.Setenv("HOME", dir)
	// Under -race, a process lingers for a second on exit unless told not to.
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	for _, name := range []string{NetworksEnv, ServicesEnv, FailEnv, "IFRIT_NAME_PREFIX", "IFRIT_SHARED_NETWORK", "NO_COLOR"} {
		t.Setenv(name, "")
	}
	return f
}

// Calls returns the arguments of every docker call so far, one string per
// call.
func (f *Fake) Calls() []string {
	data, err := os.ReadFile(f.log)
	if err != nil {
		return nil
	}
	var calls []string
	for line := range strings.Lines(string(data)) {
		var args []string
		if err := json.Unmarshal([]byte(line), &args); err == nil {
			calls = append(calls, strings.Join(args, " "))
		}
	}
	return calls
}

// Reset forgets the calls recorded so far.
func (f *Fake) Reset() {
	os.Remove(f.log)
}
