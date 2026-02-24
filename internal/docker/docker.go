package docker

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/khueue/ifrit/internal/config"
	"github.com/khueue/ifrit/internal/ui"
)

// composeCommand creates an exec.Cmd for "docker compose" with the given args.
func composeCommand(args ...string) *exec.Cmd {
	return exec.Command("docker", append([]string{"compose"}, args...)...)
}

// Manager handles Docker Compose operations.
type Manager struct {
	config          *config.Config
	verbose         bool
	networkVerified bool
	overrideFile    string // temp compose override for implicit networking
}

// NewManager creates a new Docker manager.
func NewManager(cfg *config.Config, verbose bool) *Manager {
	return &Manager{
		config:  cfg,
		verbose: verbose,
	}
}

// getProject looks up a project by name, returning an error if not found.
func (m *Manager) getProject(projectName string) (config.Project, error) {
	project, ok := m.config.Projects[projectName]
	if !ok {
		return config.Project{}, fmt.Errorf("project %s not found in config", projectName)
	}
	return project, nil
}

// logCommand prints the full command line when verbose mode is enabled.
func (m *Manager) logCommand(cmd *exec.Cmd) {
	if !m.verbose {
		return
	}
	fmt.Fprintf(os.Stderr, "\033[90m$ %s\033[0m\n", strings.Join(cmd.Args, " "))
}

// composeEnv returns the current process environment with IFRIT_SHARED_NETWORK injected.
// When not in verbose mode, BUILDKIT_PROGRESS is set to "quiet" to suppress
// noisy BuildKit output during image builds.
func (m *Manager) composeEnv() []string {
	env := append(os.Environ(), fmt.Sprintf("IFRIT_SHARED_NETWORK=%s", m.config.SharedNetwork))
	if !m.verbose {
		env = append(env, "BUILDKIT_PROGRESS=quiet")
	}
	return env
}

// ensureOverrideFile creates (once) a temp compose override file that sets the
// default network to the shared external network. The file lives in the OS temp
// dir and is cleaned up automatically on reboot.
func (m *Manager) ensureOverrideFile() (string, error) {
	if m.overrideFile != "" {
		return m.overrideFile, nil
	}

	content := fmt.Sprintf(`networks:
  default:
    external: true
    name: %s
`, m.config.SharedNetwork)

	f, err := os.CreateTemp("", "ifrit-network-override-*.yml")
	if err != nil {
		return "", fmt.Errorf("failed to create network override file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("failed to write network override: %w", err)
	}

	m.overrideFile = f.Name()
	return m.overrideFile, nil
}

// composeArgs returns the common prefix args for a compose invocation:
//
//	--file <file1> --file <file2> ... --project-name <project_prefix>_<name>
//
// It also validates that every compose file exists on disk.
// When implicit networking is enabled, a generated override file is appended
// last so that the default network is set to the shared external network.
func (m *Manager) composeArgs(project config.Project, projectName string) ([]string, error) {
	var args []string
	for _, cf := range project.ComposeFiles {
		composePath := filepath.Join(project.Path, cf)
		if _, err := os.Stat(composePath); err != nil {
			return nil, fmt.Errorf("compose file not found at %s: %w", composePath, err)
		}
		args = append(args, "--file", composePath)
	}

	if *m.config.ImplicitNetworking {
		overridePath, err := m.ensureOverrideFile()
		if err != nil {
			return nil, err
		}
		args = append(args, "--file", overridePath)
	}

	args = append(args, "--project-name", fmt.Sprintf("%s_%s", m.config.NamePrefix, projectName))
	return args, nil
}

// EnsureNetwork creates the shared network if it doesn't exist.
func (m *Manager) networkExists() (bool, error) {
	cmd := exec.Command("docker", "network", "ls", "--format", "{{.Name}}")
	m.logCommand(cmd)
	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to list docker networks: %w", err)
	}

	for network := range strings.SplitSeq(string(output), "\n") {
		if strings.TrimSpace(network) == m.config.SharedNetwork {
			return true, nil
		}
	}

	return false, nil
}

func (m *Manager) EnsureNetwork() error {
	if m.networkVerified {
		return nil
	}

	exists, err := m.networkExists()
	if err != nil {
		return err
	}
	if exists {
		if m.verbose {
			ui.Printf("Network %s already exists\n", m.config.SharedNetwork)
		}
		m.networkVerified = true
		return nil
	}

	// Create network.
	ui.Printf("Creating shared network: %s\n", m.config.SharedNetwork)
	cmd := exec.Command("docker", "network", "create", m.config.SharedNetwork)
	m.logCommand(cmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create network %s: %w", m.config.SharedNetwork, err)
	}

	m.networkVerified = true
	return nil
}

// NetworkStatus runs docker network ls filtered by the shared network name,
// printing the result directly to stdout.
func (m *Manager) NetworkStatus() error {
	cmd := exec.Command("docker", "network", "ls", "--filter", fmt.Sprintf("name=^%s$", m.config.SharedNetwork))
	m.logCommand(cmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to check network %s: %w", m.config.SharedNetwork, err)
	}

	return nil
}

// RemoveNetwork removes the shared network if it exists.
func (m *Manager) RemoveNetwork() error {
	exists, err := m.networkExists()
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	ui.Printf("Removing shared network: %s\n", m.config.SharedNetwork)
	cmd := exec.Command("docker", "network", "rm", m.config.SharedNetwork)
	m.logCommand(cmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		ui.Printf("Warning: failed to remove network %s: %v\n", m.config.SharedNetwork, err)
	}

	return nil
}

// ComposeUp runs docker compose up for a project.
func (m *Manager) ComposeUp(projectName string, forceRecreate bool) error {
	project, err := m.getProject(projectName)
	if err != nil {
		return err
	}

	if err := m.EnsureNetwork(); err != nil {
		return err
	}

	baseArgs, err := m.composeArgs(project, projectName)
	if err != nil {
		return err
	}

	ui.Printf("Starting project: %s\n", projectName)

	args := append(baseArgs,
		"up",
		"--remove-orphans",
	)

	if forceRecreate {
		args = append(args, "--build", "--force-recreate", "--always-recreate-deps")
	}

	args = append(args, "--detach")

	cmd := composeCommand(args...)
	cmd.Dir = project.Path
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = m.composeEnv()

	m.logCommand(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start project %s: %w", projectName, err)
	}

	return nil
}

// ComposeDown runs docker compose down for a project.
func (m *Manager) ComposeDown(projectName string, removeVolumes bool) error {
	project, err := m.getProject(projectName)
	if err != nil {
		return err
	}

	baseArgs, err := m.composeArgs(project, projectName)
	if err != nil {
		return err
	}

	ui.Printf("Stopping project: %s\n", projectName)

	args := append(baseArgs, "down")

	if removeVolumes {
		args = append(args, "--volumes")
	}

	cmd := composeCommand(args...)
	cmd.Dir = project.Path
	cmd.Env = m.composeEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	m.logCommand(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop project %s: %w", projectName, err)
	}

	return nil
}

// ComposeStatus shows the status of a project.
func (m *Manager) ComposeStatus(projectName string) error {
	project, err := m.getProject(projectName)
	if err != nil {
		return err
	}

	baseArgs, err := m.composeArgs(project, projectName)
	if err != nil {
		return err
	}

	args := append(baseArgs, "ps")

	cmd := composeCommand(args...)
	cmd.Dir = project.Path
	cmd.Env = m.composeEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	m.logCommand(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to get status for project %s: %w", projectName, err)
	}

	return nil
}

// ComposeLogs shows logs for a project.
func (m *Manager) ComposeLogs(projectName string, follow bool, tail string) error {
	project, err := m.getProject(projectName)
	if err != nil {
		return err
	}

	baseArgs, err := m.composeArgs(project, projectName)
	if err != nil {
		return err
	}

	args := append(baseArgs, "logs")

	if follow {
		args = append(args, "--follow")
	}

	if tail != "" {
		args = append(args, "--tail", tail)
	}

	cmd := composeCommand(args...)
	cmd.Dir = project.Path
	cmd.Env = m.composeEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	m.logCommand(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to get logs for project %s: %w", projectName, err)
	}

	return nil
}

// ComposeLogsCmd builds and returns an *exec.Cmd for tailing logs of a project
// without executing it. The caller is responsible for managing the process
// lifecycle. This is used by the interactive TUI logs viewer.
func (m *Manager) ComposeLogsCmd(projectName string, tail string) (*exec.Cmd, error) {
	project, err := m.getProject(projectName)
	if err != nil {
		return nil, err
	}

	baseArgs, err := m.composeArgs(project, projectName)
	if err != nil {
		return nil, err
	}

	args := append(baseArgs, "logs", "--follow")

	if tail != "" {
		args = append(args, "--tail", tail)
	}

	cmd := composeCommand(args...)
	cmd.Dir = project.Path
	cmd.Env = m.composeEnv()

	return cmd, nil
}

// UpAll starts all projects in sorted order.
func (m *Manager) UpAll(forceRecreate bool) error {
	if err := m.EnsureNetwork(); err != nil {
		return err
	}

	for _, name := range m.config.GetProjects() {
		if err := m.ComposeUp(name, forceRecreate); err != nil {
			return err
		}
	}

	return nil
}

// DownAll stops all projects in sorted order.
func (m *Manager) DownAll(removeVolumes bool) error {
	for _, name := range m.config.GetProjects() {
		if err := m.ComposeDown(name, removeVolumes); err != nil {
			// Continue stopping other projects even if one fails.
			ui.Printf("Warning: %v\n", err)
		}
	}

	return nil
}

// ensureServiceRunning ensures a specific service is up and running in a project.
// This is idempotent: if the service is already running, it's a no-op.
func (m *Manager) ensureServiceRunning(projectName, serviceName string) error {
	project, err := m.getProject(projectName)
	if err != nil {
		return err
	}

	if err := m.EnsureNetwork(); err != nil {
		return err
	}

	baseArgs, err := m.composeArgs(project, projectName)
	if err != nil {
		return err
	}

	args := append(baseArgs, "up", "--detach", serviceName)

	cmd := composeCommand(args...)
	cmd.Dir = project.Path
	cmd.Env = m.composeEnv()

	// Capture output instead of printing directly — only show on error.
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	m.logCommand(cmd)
	if err := cmd.Run(); err != nil {
		// Show the captured output so the user can diagnose the failure.
		if outBuf.Len() > 0 {
			os.Stdout.Write(outBuf.Bytes())
		}
		if errBuf.Len() > 0 {
			os.Stderr.Write(errBuf.Bytes())
		}
		return fmt.Errorf("failed to start service %s in project %s: %w", serviceName, projectName, err)
	}

	return nil
}

// ComposeExec executes a command in a running container.
func (m *Manager) ComposeExec(projectName, serviceName string, command []string, interactive bool) error {
	project, err := m.getProject(projectName)
	if err != nil {
		return err
	}

	baseArgs, err := m.composeArgs(project, projectName)
	if err != nil {
		return err
	}

	// Ensure the service is up and running before exec.
	if err := m.ensureServiceRunning(projectName, serviceName); err != nil {
		return err
	}

	args := append(baseArgs, "exec")

	if !interactive {
		args = append(args, "--no-TTY")
	}

	args = append(args, serviceName)
	args = append(args, command...)

	cmd := composeCommand(args...)
	cmd.Dir = project.Path
	cmd.Env = m.composeEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if interactive {
		cmd.Stdin = os.Stdin
	}

	m.logCommand(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to exec into service %s in project %s: %w", serviceName, projectName, err)
	}

	return nil
}

// ComposeServices lists all services in a project.
func (m *Manager) ComposeServices(projectName string) ([]string, error) {
	project, err := m.getProject(projectName)
	if err != nil {
		return nil, err
	}

	baseArgs, err := m.composeArgs(project, projectName)
	if err != nil {
		return nil, err
	}

	args := append(baseArgs, "config", "--services")

	cmd := composeCommand(args...)
	cmd.Dir = project.Path
	cmd.Env = m.composeEnv()
	m.logCommand(cmd)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list services for project %s: %w", projectName, err)
	}

	services := []string{}
	lines := strings.SplitSeq(strings.TrimSpace(string(output)), "\n")
	for line := range lines {
		if line != "" {
			services = append(services, line)
		}
	}

	return services, nil
}
