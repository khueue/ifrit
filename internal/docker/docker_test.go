package docker

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/khueue/ifrit/internal/check"
	"github.com/khueue/ifrit/internal/config"
	"github.com/khueue/ifrit/internal/fakedocker"
)

func TestMain(m *testing.M) {
	fakedocker.RunIfInvoked()
	os.Exit(m.Run())
}

// setup installs the fake docker and returns a manager for two projects,
// "api" and "web", whose compose files exist on disk.
func setup(t *testing.T, implicit bool) (*Manager, *fakedocker.Fake, string) {
	t.Helper()
	fake := fakedocker.Install(t)

	root := t.TempDir()
	projects := map[string]config.Project{}
	for _, name := range []string{"api", "web"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		projects[name] = config.Project{Path: dir, ComposeFiles: []string{"compose.yml"}}
	}

	cfg := &config.Config{NamePrefix: "t-", ImplicitNetworking: &implicit, Projects: projects}
	return NewManager(cfg, false), fake, root
}

// compose strips the "--file ..." arguments from a recorded call, leaving
// "compose --project-name <name> <command...>".
func compose(call string) string {
	fields := strings.Fields(call)
	var out []string
	for i := 0; i < len(fields); i++ {
		if fields[i] == "--file" {
			i++
			continue
		}
		out = append(out, fields[i])
	}
	return strings.Join(out, " ")
}

func composeCalls(fake *fakedocker.Fake) []string {
	var out []string
	for _, c := range fake.Calls() {
		out = append(out, compose(c))
	}
	return out
}

func TestComposeUpCreatesNetworkOnce(t *testing.T) {
	m, fake, _ := setup(t, false)

	if err := m.Up(m.config.GetProjects(), UpOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"network ls --format {{.Name}}",
		"network create t-shared",
		"compose --project-name t-api up --remove-orphans --detach",
		"compose --project-name t-web up --remove-orphans --detach",
	}
	if got := composeCalls(fake); !slices.Equal(got, want) {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestComposeUpKeepsExistingNetwork(t *testing.T) {
	m, fake, _ := setup(t, false)
	// "t-shared-2" must not be mistaken for "t-shared".
	t.Setenv(fakedocker.NetworksEnv, "bridge t-shared-2 t-shared host")

	if err := m.Up([]string{"api"}, UpOptions{Recreate: true}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"network ls --format {{.Name}}",
		"compose --project-name t-api up --remove-orphans --build --force-recreate --always-recreate-deps --detach",
	}
	if got := composeCalls(fake); !slices.Equal(got, want) {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// fileArgs returns the values of the "--file" arguments of a recorded call.
func fileArgs(call string) []string {
	fields := strings.Fields(call)
	var files []string
	for i, field := range fields[:len(fields)-1] {
		if field == "--file" {
			files = append(files, fields[i+1])
		}
	}
	return files
}

func TestComposeFilesAndImplicitNetworking(t *testing.T) {
	m, fake, root := setup(t, true)

	check.NoError(t, m.Down([]string{"api"}, DownOptions{RemoveVolumes: true}))
	calls := fake.Calls()
	check.Slice(t, "calls", composeCalls(fake), []string{"compose --project-name t-api down --volumes"})
	if len(calls) != 1 {
		t.FailNow()
	}

	// The project's own file first, the generated override last.
	override, err := m.ensureOverrideFile()
	check.NoError(t, err)
	check.Slice(t, "compose files", fileArgs(calls[0]), []string{filepath.Join(root, "api", "compose.yml"), override})

	content, err := os.ReadFile(override)
	check.NoError(t, err)
	check.Equal(t, "override content", string(content), "networks:\n  default:\n    external: true\n    name: \"t-shared\"\n")
}

// The override must sit in a private directory of the user's, never in a
// shared temp dir where someone else could plant it. Its path must be stable
// across runs.
func TestOverrideFileLocation(t *testing.T) {
	m, _, _ := setup(t, true)

	override, err := m.ensureOverrideFile()
	check.NoError(t, err)
	check.Equal(t, "under the user's home", strings.HasPrefix(override, os.Getenv("HOME")), true)

	info, err := os.Stat(filepath.Dir(override))
	if err != nil {
		t.Fatal(err)
	}
	check.Equal(t, "permissions for others", info.Mode().Perm()&0o077, 0)

	again, err := NewManager(m.config, false).ensureOverrideFile()
	check.NoError(t, err)
	check.Equal(t, "path from a second manager", again, override)
}

func TestMissingComposeFile(t *testing.T) {
	m, fake, root := setup(t, false)
	if err := os.Remove(filepath.Join(root, "web", "compose.yml")); err != nil {
		t.Fatal(err)
	}
	_, err := m.Project("web")
	check.ErrorContains(t, err, "compose file not found")
	check.Slice(t, "docker calls", fake.Calls(), nil)
}

func TestUnknownProject(t *testing.T) {
	m, fake, _ := setup(t, false)

	_, err := m.Project("nope")
	check.ErrorContains(t, err, "project nope not found")
	check.ErrorContains(t, m.Up([]string{"api", "nope", "web"}, UpOptions{}), "project nope not found")

	// Starting stops at the first failure.
	check.Slice(t, "calls", composeCalls(fake), []string{
		"network ls --format {{.Name}}",
		"network create t-shared",
		"compose --project-name t-api up --remove-orphans --detach",
	})
}

// When docker fails on a command whose output we capture, its explanation
// must make it into the error, not just the exit status.
func TestNetworkFailureCarriesStderr(t *testing.T) {
	m, _, _ := setup(t, false)

	t.Setenv(fakedocker.FailEnv, "network ls")
	check.ErrorContains(t, m.EnsureNetwork(), "fake docker: boom", "exit status 3")
}

// One project failing to stop must not keep the rest running, and must not
// go unreported either.
func TestDownContinuesAndReportsFailures(t *testing.T) {
	m, fake, _ := setup(t, false)
	t.Setenv(fakedocker.FailEnv, "t-api down")

	check.ErrorContains(t, m.Down(m.config.GetProjects(), DownOptions{}), "failed to stop project api")
	check.Slice(t, "calls", composeCalls(fake), []string{"compose --project-name t-api down", "compose --project-name t-web down"})

	t.Setenv(fakedocker.FailEnv, "")
	check.NoError(t, m.Down(m.config.GetProjects(), DownOptions{}))
}

func TestRemoveNetwork(t *testing.T) {
	m, fake, _ := setup(t, false)

	if err := m.RemoveNetwork(); err != nil {
		t.Fatal(err)
	}
	if got, want := fake.Calls(), []string{"network ls --format {{.Name}}"}; !slices.Equal(got, want) {
		t.Errorf("calls = %q, want %q (nothing to remove)", got, want)
	}

	fake.Reset()
	t.Setenv(fakedocker.NetworksEnv, "t-shared")
	if err := m.RemoveNetwork(); err != nil {
		t.Fatal(err)
	}
	if got, want := fake.Calls(), []string{"network ls --format {{.Name}}", "network rm t-shared"}; !slices.Equal(got, want) {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

func TestNetworkStatus(t *testing.T) {
	m, fake, _ := setup(t, false)

	check.NoError(t, m.NetworkStatus())
	check.Slice(t, "calls", fake.Calls(), []string{"network ls --filter name=^t-shared$"})

	t.Setenv(fakedocker.FailEnv, "network ls")
	check.ErrorContains(t, m.NetworkStatus(), "failed to check network t-shared")
}

// A network that cannot be created must stop projects from being started.
func TestNetworkCreateFailure(t *testing.T) {
	m, fake, _ := setup(t, false)
	t.Setenv(fakedocker.FailEnv, "network create")

	check.ErrorContains(t, m.Up([]string{"api"}, UpOptions{}), "failed to create network t-shared")
	check.Slice(t, "calls", fake.Calls(), []string{"network ls --format {{.Name}}", "network create t-shared"})
}

// In verbose mode BuildKit is left to print its progress.
func TestVerboseEnv(t *testing.T) {
	m, _, _ := setup(t, false)
	check.Equal(t, "quiet by default", slices.Contains(m.composeEnv(), "BUILDKIT_PROGRESS=quiet"), true)

	verbose := NewManager(m.config, true)
	check.Equal(t, "quiet when verbose", slices.Contains(verbose.composeEnv(), "BUILDKIT_PROGRESS=quiet"), false)
	check.NoError(t, verbose.NetworkStatus()) // echoes the command
}
