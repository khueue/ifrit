package docker

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/khueue/ifrit/internal/config"
	"github.com/khueue/ifrit/internal/ui"
)

// UpOptions adjusts how a project is started.
type UpOptions struct {
	// Recreate rebuilds images and force-recreates all containers and their
	// dependencies.
	Recreate bool
}

// DownOptions adjusts how a project is stopped.
type DownOptions struct {
	RemoveVolumes bool
}

// LogsOptions selects which logs to show.
type LogsOptions struct {
	Follow  bool
	Tail    string // number of lines from the end, or "all"; empty for docker's default
	Service string // limit the logs to one service; empty for all
}

// ExecOptions describes a command to run in a service's container.
type ExecOptions struct {
	Service     string
	Command     []string
	Interactive bool // attach stdin and allocate a TTY
}

// Project is a Docker Compose project from the config, ready for compose
// commands to be run against it.
type Project struct {
	manager *Manager
	name    string
	dir     string

	// baseArgs is the prefix of every compose invocation:
	//
	//	--file <file1> --file <file2> ... --project-name <name_prefix><name>
	baseArgs []string
}

// Project looks up a project by name. It fails if the project is not in the
// config, or if one of its compose files is missing.
func (m *Manager) Project(name string) (*Project, error) {
	project, ok := m.config.Projects[name]
	if !ok {
		return nil, fmt.Errorf("project %s not found in config", name)
	}

	args, err := m.composeFileArgs(project)
	if err != nil {
		return nil, err
	}

	return &Project{
		manager:  m,
		name:     name,
		dir:      project.Path,
		baseArgs: append(args, "--project-name", m.config.NamePrefix+name),
	}, nil
}

// composeFileArgs returns a "--file" argument for each of the project's
// compose files. When implicit networking is enabled, a generated override
// file is appended last so that the default network is set to the shared
// external network.
func (m *Manager) composeFileArgs(project config.Project) ([]string, error) {
	var args []string
	for _, composeFile := range project.ComposeFiles {
		path := filepath.Join(project.Path, composeFile)
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("compose file not found at %s: %w", path, err)
		}
		args = append(args, "--file", path)
	}

	if !*m.config.ImplicitNetworking {
		return args, nil
	}
	override, err := m.ensureOverrideFile()
	if err != nil {
		return nil, err
	}
	return append(args, "--file", override), nil
}

// command returns a "docker compose" command for the project, set to run in
// the project's directory.
func (p *Project) command(args ...string) *exec.Cmd {
	cmd := exec.Command("docker", slices.Concat([]string{"compose"}, p.baseArgs, args)...)
	cmd.Dir = p.dir
	cmd.Env = p.manager.composeEnv()
	return cmd
}

// Up runs docker compose up for the project.
func (p *Project) Up(opts UpOptions) error {
	if err := p.manager.EnsureNetwork(); err != nil {
		return err
	}

	ui.Printf("Starting project: %s\n", p.name)

	args := []string{"up", "--remove-orphans"}
	if opts.Recreate {
		args = append(args, "--build", "--force-recreate", "--always-recreate-deps")
	}

	cmd := p.command(append(args, "--detach")...)
	cmd.Stdin = os.Stdin
	if err := p.manager.run(cmd); err != nil {
		return fmt.Errorf("failed to start project %s: %w", p.name, err)
	}
	return nil
}

// Down runs docker compose down for the project.
func (p *Project) Down(opts DownOptions) error {
	ui.Printf("Stopping project: %s\n", p.name)

	args := []string{"down"}
	if opts.RemoveVolumes {
		args = append(args, "--volumes")
	}

	if err := p.manager.run(p.command(args...)); err != nil {
		return fmt.Errorf("failed to stop project %s: %w", p.name, err)
	}
	return nil
}

// Status shows the status of the project's containers.
func (p *Project) Status() error {
	if err := p.manager.run(p.command("ps")); err != nil {
		return fmt.Errorf("failed to get status for project %s: %w", p.name, err)
	}
	return nil
}

// LogsCmd builds the command that prints the project's logs, without running
// it. The caller is responsible for managing the process lifecycle.
func (p *Project) LogsCmd(opts LogsOptions) *exec.Cmd {
	args := []string{"logs"}
	if opts.Follow {
		args = append(args, "--follow")
	}
	if opts.Tail != "" {
		args = append(args, "--tail", opts.Tail)
	}
	if opts.Service != "" {
		args = append(args, opts.Service)
	}
	return p.command(args...)
}

// Logs prints the project's logs.
func (p *Project) Logs(opts LogsOptions) error {
	cmd := p.LogsCmd(opts)
	cmd.Stdin = os.Stdin
	if err := p.manager.run(cmd); err != nil {
		return fmt.Errorf("failed to get logs for project %s: %w", p.name, err)
	}
	return nil
}

// ensureServiceRunning ensures a specific service is up and running.
// This is idempotent: if the service is already running, it's a no-op.
func (p *Project) ensureServiceRunning(service string) error {
	if err := p.manager.EnsureNetwork(); err != nil {
		return err
	}

	// Capture output instead of printing directly — only show on error.
	var output bytes.Buffer
	cmd := p.command("up", "--detach", service)
	cmd.Stdout = &output
	cmd.Stderr = &output

	p.manager.logCommand(cmd)
	if err := cmd.Run(); err != nil {
		// Show the captured output so the user can diagnose the failure.
		os.Stderr.Write(output.Bytes())
		return fmt.Errorf("failed to start service %s in project %s: %w", service, p.name, err)
	}
	return nil
}

// Exec runs a command in a service's container, starting the service first
// if need be. A command that runs but fails is reported as an ExecExitError.
func (p *Project) Exec(opts ExecOptions) error {
	if err := p.ensureServiceRunning(opts.Service); err != nil {
		return err
	}

	args := []string{"exec"}
	if !opts.Interactive {
		args = append(args, "--no-TTY")
	}
	args = append(args, opts.Service)

	cmd := p.command(append(args, opts.Command...)...)
	if opts.Interactive {
		cmd.Stdin = os.Stdin
	}

	err := p.manager.run(cmd)
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return &ExecExitError{Code: exitErr.ExitCode()}
	}
	if err != nil {
		return fmt.Errorf("failed to exec into service %s in project %s: %w", opts.Service, p.name, err)
	}
	return nil
}

// Services lists all services in the project.
func (p *Project) Services() ([]string, error) {
	cmd := p.command("config", "--services")
	p.manager.logCommand(cmd)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list services for project %s: %w", p.name, outputError(err))
	}

	// One service name per line; names cannot contain whitespace.
	return strings.Fields(string(output)), nil
}
