package docker

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/khueue/ifrit/internal/check"
	"github.com/khueue/ifrit/internal/fakedocker"
)

// project returns the named project, stopping the test if it cannot.
func project(t *testing.T, m *Manager, name string) *Project {
	t.Helper()
	p, err := m.Project(name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A project with several compose files passes all of them on, in order.
func TestProjectComposeFiles(t *testing.T) {
	m, fake, root := setup(t, false)
	dir := filepath.Join(root, "api")
	check.NoError(t, os.WriteFile(filepath.Join(dir, "compose.dev.yml"), []byte("services: {}\n"), 0o644))

	api := m.config.Projects["api"]
	api.ComposeFiles = []string{"compose.yml", "compose.dev.yml"}
	m.config.Projects["api"] = api

	check.NoError(t, project(t, m, "api").Status())
	calls := fake.Calls()
	check.Slice(t, "calls", composeCalls(fake), []string{"compose --project-name t-api ps"})
	if len(calls) != 1 {
		t.FailNow()
	}
	check.Slice(t, "compose files", fileArgs(calls[0]), []string{
		filepath.Join(dir, "compose.yml"),
		filepath.Join(dir, "compose.dev.yml"),
	})

	// Every one of them has to exist.
	check.NoError(t, os.Remove(filepath.Join(dir, "compose.dev.yml")))
	_, err := m.Project("api")
	check.ErrorContains(t, err, "compose file not found", "compose.dev.yml")
}

// Each operation names the project in its error when docker fails.
func TestProjectFailures(t *testing.T) {
	tests := []struct {
		name    string
		fail    string
		run     func(*Project) error
		wantErr string
	}{
		{"up", " up ", func(p *Project) error { return p.Up(UpOptions{}) }, "failed to start project web"},
		{"down", " down", func(p *Project) error { return p.Down(DownOptions{}) }, "failed to stop project web"},
		{"status", " ps", func(p *Project) error { return p.Status() }, "failed to get status for project web"},
		{"logs", " logs", func(p *Project) error { return p.Logs(LogsOptions{}) }, "failed to get logs for project web"},
		{"services", "config --services", func(p *Project) error { _, err := p.Services(); return err }, "fake docker: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _, _ := setup(t, false)
			t.Setenv(fakedocker.NetworksEnv, "t-shared")
			t.Setenv(fakedocker.FailEnv, tt.fail)

			check.ErrorContains(t, tt.run(project(t, m, "web")), tt.wantErr, "exit status 3")
		})
	}
}

func TestProjectUpAndDown(t *testing.T) {
	m, fake, _ := setup(t, false)
	t.Setenv(fakedocker.NetworksEnv, "t-shared")
	web := project(t, m, "web")

	check.NoError(t, web.Up(UpOptions{}))
	check.NoError(t, web.Up(UpOptions{Recreate: true}))
	check.NoError(t, web.Down(DownOptions{}))
	check.NoError(t, web.Down(DownOptions{RemoveVolumes: true}))

	// The network is only looked up once per manager.
	check.Slice(t, "calls", composeCalls(fake), []string{
		"network ls --format {{.Name}}",
		"compose --project-name t-web up --remove-orphans --detach",
		"compose --project-name t-web up --remove-orphans --build --force-recreate --always-recreate-deps --detach",
		"compose --project-name t-web down",
		"compose --project-name t-web down --volumes",
	})
}

func TestProjectServices(t *testing.T) {
	m, _, _ := setup(t, false)
	api := project(t, m, "api")

	t.Setenv(fakedocker.ServicesEnv, "api db worker")
	got, err := api.Services()
	check.NoError(t, err)
	check.Slice(t, "services", got, []string{"api", "db", "worker"})

	t.Setenv(fakedocker.ServicesEnv, "")
	got, err = api.Services()
	check.NoError(t, err)
	check.Slice(t, "services", got, nil)
}

func TestProjectExec(t *testing.T) {
	m, fake, _ := setup(t, false)
	t.Setenv(fakedocker.NetworksEnv, "t-shared")
	api := project(t, m, "api")

	check.NoError(t, api.Exec(ExecOptions{Service: "db", Command: []string{"psql", "-c", "select 1"}}))
	check.Slice(t, "calls", composeCalls(fake), []string{
		"network ls --format {{.Name}}",
		"compose --project-name t-api up --detach db",
		"compose --project-name t-api exec --no-TTY db psql -c select 1",
	})

	fake.Reset()
	check.NoError(t, api.Exec(ExecOptions{Service: "db", Command: []string{"bash"}, Interactive: true}))
	check.Slice(t, "interactive calls", composeCalls(fake), []string{
		"compose --project-name t-api up --detach db",
		"compose --project-name t-api exec db bash",
	})
}

// Only the exit status of the command itself is an ExecExitError. Not getting
// as far as running it is an ordinary error, to be shown to the user.
func TestProjectExecErrors(t *testing.T) {
	tests := []struct {
		name     string
		fail     string
		wantExit bool
		wantErr  string
	}{
		{"command fails", " exec ", true, "exit status 3"},
		{"service fails to start", "up --detach", false, "failed to start service db"},
		{"daemon unreachable", "network ls", false, "fake docker: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _, _ := setup(t, false)
			t.Setenv(fakedocker.FailEnv, tt.fail)

			err := project(t, m, "api").Exec(ExecOptions{Service: "db", Command: []string{"ls"}})
			check.ErrorContains(t, err, tt.wantErr)
			_, isExit := errors.AsType[*ExecExitError](err)
			check.Equal(t, "is ExecExitError", isExit, tt.wantExit)
		})
	}
}

func TestProjectLogs(t *testing.T) {
	m, fake, _ := setup(t, false)
	web := project(t, m, "web")

	check.NoError(t, web.Logs(LogsOptions{}))
	check.NoError(t, web.Logs(LogsOptions{Follow: true, Tail: "50"}))
	check.Slice(t, "calls", composeCalls(fake), []string{
		"compose --project-name t-web logs",
		"compose --project-name t-web logs --follow --tail 50",
	})

	cmd := web.LogsCmd(LogsOptions{Follow: true, Tail: "100", Service: "nginx"})
	check.Equal(t, "service logs cmd", compose(strings.Join(cmd.Args[1:], " ")), "compose --project-name t-web logs --follow --tail 100 nginx")
	check.Equal(t, "dir", cmd.Dir, web.dir)
	check.Equal(t, "env has shared network", slices.Contains(cmd.Env, "IFRIT_SHARED_NETWORK=t-shared"), true)
	check.Equal(t, "env quiets buildkit", slices.Contains(cmd.Env, "BUILDKIT_PROGRESS=quiet"), true)
}
