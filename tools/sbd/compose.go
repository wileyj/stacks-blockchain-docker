package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
)

// projectName is the top-level `name:` of the repo's compose.yaml; it identifies the project dir.
const projectName = "stacks-blockchain-docker"

// FindProject returns the repo root: the explicit dir if given, else the first directory upwards
// from start whose compose.yaml declares the project name.
func FindProject(explicit, start string) (string, error) {
	if explicit != "" {
		dir, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		if !isProject(dir) {
			return "", fmt.Errorf("%s: no compose.yaml for %s there", dir, projectName)
		}
		return dir, nil
	}
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if isProject(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not inside a %s checkout (no compose.yaml with `name: %s` here or above); use --project-dir", projectName, projectName)
		}
		dir = parent
	}
}

func isProject(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		return false
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "name: "+projectName {
			return true
		}
	}
	return false
}

// Docker runs `docker` in the project dir. Run passes the terminal through (prompts, progress,
// Ctrl-C reach the child); Output captures stdout for read-only queries.
type Docker interface {
	Run(args []string, stdout io.Writer) (exitCode int, err error)
	Output(args []string) ([]byte, error)
}

type execDocker struct {
	dir string
	env []string // extra environment, e.g. DOCKER_HOST
}

func (d execDocker) cmd(args []string) *exec.Cmd {
	cmd := exec.Command("docker", args...)
	cmd.Dir = d.dir
	cmd.Env = append(os.Environ(), d.env...)
	return cmd
}

func (d execDocker) Run(args []string, stdout io.Writer) (int, error) {
	cmd := d.cmd(args)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, os.Stderr
	// The child gets Ctrl-C itself (same process group) and decides how to stop; sbd just waits
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

func (d execDocker) Output(args []string) ([]byte, error) {
	cmd := d.cmd(args)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// Container is one row of `docker compose ps --format json`.
type Container struct {
	Service string `json:"Service"`
	Name    string `json:"Name"`
	State   string `json:"State"`
	Health  string `json:"Health"`
	Status  string `json:"Status"`
}

// ParsePS reads `docker compose ps --format json`: one object per line (Compose ≥ 2.21) or a JSON
// array (older releases).
func ParsePS(out []byte) ([]Container, error) {
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil, nil
	}
	if out[0] == '[' {
		var cs []Container
		return cs, json.Unmarshal(out, &cs)
	}
	var cs []Container
	for _, line := range bytes.Split(out, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var c Container
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, err
		}
		cs = append(cs, c)
	}
	return cs, nil
}

// psArgs lists every container of the project, in every profile, including stopped ones.
var psArgs = []string{"compose", "--profile", "*", "ps", "--all", "--format", "json"}

func containers(d Docker) ([]Container, error) {
	out, err := d.Output(psArgs)
	if err != nil {
		return nil, err
	}
	return ParsePS(out)
}

// running returns the project's running containers (one-shot jobs that exited don't count).
func running(d Docker) ([]Container, error) {
	cs, err := containers(d)
	if err != nil {
		return nil, err
	}
	var up []Container
	for _, c := range cs {
		if c.State == "running" || c.State == "restarting" || c.State == "paused" {
			up = append(up, c)
		}
	}
	return up, nil
}

// Services maps each service of the project to its profiles (none for always-on services such as
// `config`), from `docker compose config`, so nothing here hard-codes the compose file.
func Services(d Docker) (map[string][]string, error) {
	out, err := d.Output([]string{"compose", "--profile", "*", "config", "--format", "json"})
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Services map[string]struct {
			Profiles []string `json:"profiles"`
		} `json:"services"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		return nil, fmt.Errorf("docker compose config: %w", err)
	}
	m := map[string][]string{}
	for name, s := range cfg.Services {
		m[name] = s.Profiles
	}
	return m, nil
}
