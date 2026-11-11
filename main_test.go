package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/khueue/ifrit/internal/fakedocker"
)

// The test binary doubles as the programs under test: as the fake docker
// when found on PATH under that name, and as ifrit itself when asked to.
func TestMain(m *testing.M) {
	fakedocker.RunIfInvoked()
	if os.Getenv("IFRIT_TEST_RUN_MAIN") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type result struct {
	stdout, stderr string
	code           int
}

// env is a directory to run ifrit in, with a fake docker on PATH.
type env struct {
	t    *testing.T
	dir  string
	fake *fakedocker.Fake
}

const testConfig = `name_prefix: t-
implicit_networking: false
projects:
  api:
    path: ./api
  web:
    path: ./web
`

// newEnv creates a working directory holding the projects "api" and "web".
// With config set, it is written to ifrit.yml.
func newEnv(t *testing.T, config string) *env {
	t.Helper()
	e := &env{t: t, fake: fakedocker.Install(t)}

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.dir = dir
	for _, name := range []string{"api", "web"} {
		e.write(filepath.Join(name, "compose.yml"), "services: {}\n")
	}
	if config != "" {
		e.write("ifrit.yml", config)
	}
	return e
}

func (e *env) write(name, content string) {
	e.t.Helper()
	path := filepath.Join(e.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// run executes ifrit with the given arguments. Its output goes to pipes, so
// ifrit sees no terminal.
func (e *env) run(args ...string) result {
	e.t.Helper()
	exe, err := os.Executable()
	if err != nil {
		e.t.Fatal(err)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = e.dir
	cmd.Env = append(os.Environ(), "IFRIT_TEST_RUN_MAIN=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	res := result{}
	if err := cmd.Run(); err != nil {
		exitErr, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			e.t.Fatal(err)
		}
		res.code = exitErr.ExitCode()
	}
	res.stdout, res.stderr = stdout.String(), stderr.String()
	return res
}

// calls returns the docker calls so far, without "--file <path>" arguments,
// and forgets them.
func (e *env) calls() []string {
	var out []string
	for _, call := range e.fake.Calls() {
		fields := strings.Fields(call)
		var kept []string
		for i := 0; i < len(fields); i++ {
			if fields[i] == "--file" {
				i++
				continue
			}
			kept = append(kept, fields[i])
		}
		out = append(out, strings.Join(kept, " "))
	}
	e.fake.Reset()
	return out
}

func (e *env) wantCalls(want ...string) {
	e.t.Helper()
	if got := e.calls(); !slices.Equal(got, want) {
		e.t.Errorf("docker calls:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func (r result) want(t *testing.T, code int) result {
	t.Helper()
	if r.code != code {
		t.Errorf("exit code = %d, want %d\nstdout: %s\nstderr: %s", r.code, code, r.stdout, r.stderr)
	}
	return r
}

func wantContains(t *testing.T, what, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("%s lacks %q:\n%s", what, want, got)
		}
	}
}

// Commands that do not need a config must work without one.
func TestWorksWithoutConfig(t *testing.T) {
	e := newEnv(t, "")
	for _, args := range [][]string{
		{"help"}, {"help", "up"}, {"--help"}, {"up", "--help"}, {"version"}, {"completion", "bash"}, {},
	} {
		res := e.run(args...).want(t, 0)
		if res.stdout == "" || res.stderr != "" {
			t.Errorf("ifrit %v: stdout %d bytes, stderr %q", args, len(res.stdout), res.stderr)
		}
	}

	res := e.run("status").want(t, 1)
	wantContains(t, "stderr", res.stderr, "Error: failed to load config", "ifrit.yml")
	e.wantCalls()
}

// Piped output is for scripts and files: no escape codes.
func TestOutputIsPlainWhenPiped(t *testing.T) {
	e := newEnv(t, testConfig)
	if got := e.run("version").want(t, 0).stdout; !strings.HasPrefix(got, "$ ifrit ") || !strings.HasSuffix(got, "\n") {
		t.Errorf("version output = %q", got)
	}

	res := e.run("-v", "status").want(t, 0)
	if strings.Contains(res.stdout+res.stderr, "\033") {
		t.Errorf("escape codes in piped output:\n%q\n%q", res.stdout, res.stderr)
	}
	wantContains(t, "stderr", res.stderr, "$ docker compose --file")
}

func TestInit(t *testing.T) {
	e := newEnv(t, "")

	res := e.run("init").want(t, 0)
	wantContains(t, "stdout", res.stdout, "Created ifrit.yml")
	e.run("status").want(t, 0) // what init writes must load

	res = e.run("init").want(t, 1)
	wantContains(t, "stderr", res.stderr, "already exists")

	// --config names the file to create.
	res = e.run("--config", "other.yml", "init").want(t, 0)
	wantContains(t, "stdout", res.stdout, "Created other.yml", "Edit other.yml")
	if _, err := os.Stat(filepath.Join(e.dir, "other.yml")); err != nil {
		t.Errorf("other.yml was not created: %v", err)
	}

	e.run("init", "stray").want(t, 1)
}

func TestUpAndDown(t *testing.T) {
	e := newEnv(t, testConfig)

	e.run("up").want(t, 0)
	e.wantCalls(
		"network ls --format {{.Name}}",
		"network create t-shared",
		"compose --project-name t-api up --remove-orphans --detach",
		"compose --project-name t-web up --remove-orphans --detach",
	)

	t.Setenv(fakedocker.NetworksEnv, "t-shared")
	e.run("up", "--recreate", "web").want(t, 0)
	e.wantCalls(
		"network ls --format {{.Name}}",
		"compose --project-name t-web up --remove-orphans --build --force-recreate --always-recreate-deps --detach",
	)

	// Stopping single projects leaves the network alone.
	e.run("down", "--volumes", "api").want(t, 0)
	e.wantCalls("compose --project-name t-api down --volumes")

	e.run("down").want(t, 0)
	e.wantCalls(
		"compose --project-name t-api down",
		"compose --project-name t-web down",
		"network ls --format {{.Name}}",
		"network rm t-shared",
	)

	res := e.run("up", "nope").want(t, 1)
	wantContains(t, "stderr", res.stderr, "project nope not found")
}

// A project that fails to stop must show in the exit code, must not keep the
// others from being stopped, and leaves the network (still in use) in place.
func TestDownReportsFailure(t *testing.T) {
	e := newEnv(t, testConfig)
	t.Setenv(fakedocker.NetworksEnv, "t-shared")
	t.Setenv(fakedocker.FailEnv, "t-api down")

	res := e.run("down").want(t, 1)
	wantContains(t, "stderr", res.stderr, "Error: failed to stop project api")
	e.wantCalls(
		"compose --project-name t-api down",
		"compose --project-name t-web down",
	)
}

// Relative project paths are relative to the config file, wherever ifrit is
// run from.
func TestConfigElsewhere(t *testing.T) {
	e := newEnv(t, testConfig)
	root := e.dir
	e.dir = t.TempDir()

	e.run("--config", filepath.Join(root, "ifrit.yml"), "down", "api").want(t, 0)
	calls := e.fake.Calls()
	if len(calls) != 1 || !strings.Contains(calls[0], "--file "+filepath.Join(root, "api", "compose.yml")) {
		t.Errorf("docker calls = %q, want the compose file under %s", calls, root)
	}
}

func TestEnvOverrides(t *testing.T) {
	e := newEnv(t, testConfig)
	t.Setenv("IFRIT_NAME_PREFIX", "env-")
	t.Setenv("IFRIT_SHARED_NETWORK", "custom")

	e.run("up", "api").want(t, 0)
	e.wantCalls(
		"network ls --format {{.Name}}",
		"network create custom",
		"compose --project-name env-api up --remove-orphans --detach",
	)
}

func TestStatus(t *testing.T) {
	e := newEnv(t, testConfig)
	t.Setenv(fakedocker.ServicesEnv, "app db")

	res := e.run("status").want(t, 0)
	wantContains(t, "stdout", res.stdout, "=== Project: api ===", "- app", "- db", "=== Project: web ===", "=== Network: t-shared ===")
	e.wantCalls(
		"compose --project-name t-api config --services",
		"compose --project-name t-api ps",
		"compose --project-name t-web config --services",
		"compose --project-name t-web ps",
		"network ls --filter name=^t-shared$",
	)

	// Docker's own explanation of a failure must reach the user.
	t.Setenv(fakedocker.FailEnv, "config --services")
	res = e.run("status").want(t, 0)
	wantContains(t, "stdout", res.stdout, "Error listing services", "fake docker: boom")

	e.run("status", "stray").want(t, 1)
}

func TestNoProjects(t *testing.T) {
	e := newEnv(t, "name_prefix: t-\nimplicit_networking: true\n")
	for _, cmd := range []string{"up", "down", "status", "logs"} {
		res := e.run(cmd).want(t, 0)
		wantContains(t, "ifrit "+cmd, res.stdout, "No projects defined.")
	}
	e.wantCalls()
}

func TestLogs(t *testing.T) {
	e := newEnv(t, testConfig)

	// No terminal: plain logs instead of the TUI, every line of them.
	res := e.run("logs").want(t, 0)
	wantContains(t, "stdout", res.stdout, "=== Logs: api ===", "=== Logs: web ===", "a log line")
	e.wantCalls(
		"compose --project-name t-api logs",
		"compose --project-name t-web logs",
	)

	e.run("logs", "--no-tui", "--follow", "--tail", "20", "web").want(t, 0)
	e.wantCalls("compose --project-name t-web logs --follow --tail 20")

	e.run("logs", "--tail", "all", "web").want(t, 0)
	e.wantCalls("compose --project-name t-web logs --tail all")

	// Following several projects would only ever show the first.
	res = e.run("logs", "--no-tui", "--follow").want(t, 1)
	wantContains(t, "stderr", res.stderr, "one project at a time")
	e.wantCalls()

	res = e.run("logs", "nope").want(t, 1)
	wantContains(t, "stderr", res.stderr, "project nope not found")
}

func TestShell(t *testing.T) {
	e := newEnv(t, testConfig)
	t.Setenv(fakedocker.ServicesEnv, "app db")
	t.Setenv(fakedocker.NetworksEnv, "t-shared")
	prelude := []string{
		"compose --project-name t-api config --services",
		"network ls --format {{.Name}}",
		"compose --project-name t-api up --detach app",
	}
	exec := "compose --project-name t-api exec --no-TTY app "

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"default shell falls back to sh", []string{"shell", "api", "app"}, exec + "sh -c if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi"},
		{"nothing after --", []string{"shell", "api", "app", "--"}, exec + "sh -c if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi"},
		{"single argument goes through sh", []string{"shell", "api", "app", "--", "ls && echo done"}, exec + "sh -c ls && echo done"},
		{"several arguments run as they are", []string{"shell", "api", "app", "--", "ls", "-al", "--color"}, exec + "ls -al --color"},
		{"forced interactive", []string{"shell", "--interactive=true", "api", "app", "--", "top", "-b"}, "compose --project-name t-api exec app top -b"},
		{"global flags first", []string{"-c", "ifrit.yml", "shell", "-i=false", "api", "app", "--", "id", "-u"}, exec + "id -u"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.run(tt.args...).want(t, 0)
			e.wantCalls(append(slices.Clone(prelude), tt.want)...)
		})
	}
}

func TestShellUsageErrors(t *testing.T) {
	e := newEnv(t, testConfig)
	t.Setenv(fakedocker.ServicesEnv, "app db")

	tests := []struct {
		name    string
		args    []string
		wantErr string
		wantOut string
	}{
		{"no arguments", []string{"shell"}, "requires a project and service name", "ifrit shell api app"},
		{"no service", []string{"shell", "web"}, "requires a service name (project: web)", "ifrit shell web db"},
		{"unknown project", []string{"shell", "nope", "app"}, `project "nope" not found`, "Available services:"},
		{"unknown service", []string{"shell", "api", "nope"}, `service "nope" not found in project "api"`, "ifrit shell api db"},
		{"command without --", []string{"shell", "api", "app", "ls", "-al"}, "ifrit shell api app -- ls -al", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := e.run(tt.args...).want(t, 1)
			wantContains(t, "stderr", res.stderr, tt.wantErr)
			wantContains(t, "stdout", res.stdout, tt.wantOut)
			for _, call := range e.calls() {
				if !strings.HasSuffix(call, "config --services") {
					t.Errorf("unexpected docker call: %s", call)
				}
			}
		})
	}
}

// The exit status of the command run in the container becomes ifrit's own,
// with nothing added to the command's output. Failing to get that far is
// different: that the user must be told about.
func TestShellExitStatus(t *testing.T) {
	e := newEnv(t, testConfig)
	t.Setenv(fakedocker.ServicesEnv, "app")
	t.Setenv(fakedocker.NetworksEnv, "t-shared")

	t.Setenv(fakedocker.FailEnv, " exec ")
	res := e.run("shell", "api", "app", "--", "false").want(t, 3)
	if strings.Contains(res.stderr, "Error:") {
		t.Errorf("ifrit added to the command's output: %q", res.stderr)
	}

	t.Setenv(fakedocker.FailEnv, "up --detach")
	res = e.run("shell", "api", "app", "--", "ls").want(t, 1)
	wantContains(t, "stderr", res.stderr, "fake docker: boom", "Error: failed to start service app in project api")

	// For instance: the docker daemon is not running.
	t.Setenv(fakedocker.FailEnv, "network ls")
	res = e.run("shell", "api", "app", "--", "ls").want(t, 1)
	wantContains(t, "stderr", res.stderr, "Error: failed to list docker networks", "fake docker: boom")
}

func TestInvalidConfig(t *testing.T) {
	tests := []struct {
		name, config, want string
	}{
		{"unknown field", testConfig + "extra: 1\n", "extra"},
		{"no prefix", "implicit_networking: true\n", "name_prefix is required"},
		{"project without path", "name_prefix: t-\nimplicit_networking: true\nprojects:\n  api:\n", "path is required for project api"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, tt.config)
			res := e.run("up").want(t, 1)
			wantContains(t, "stderr", res.stderr, "Error: failed to load config", tt.want)
			e.wantCalls()
		})
	}
}
