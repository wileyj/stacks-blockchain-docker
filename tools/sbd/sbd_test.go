package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const sampleEnv = `## comment kept
NETWORK=mainnet
COMPOSE_PROFILES=node,api
SIGNER_PRIVATE_KEY=
TESTNET_SIGNER_PRIVATE_KEY=
`

// fakeDocker answers the read-only queries and records what would have run.
type fakeDocker struct {
	ps      string // docker compose ps --format json output
	version string
	config  string // docker compose config --format json output
	exit    int
	ran     []string
}

func (f *fakeDocker) Run(args []string, _ io.Writer) (int, error) {
	f.ran = append(f.ran, strings.Join(args, " "))
	return f.exit, nil
}

func (f *fakeDocker) Output(args []string) ([]byte, error) {
	switch strings.Join(args, " ") {
	case strings.Join(psArgs, " "):
		return []byte(f.ps), nil
	case "compose version --short":
		return []byte(f.version + "\n"), nil
	case "info --format {{.ServerVersion}}":
		return []byte("28.0.0\n"), nil
	case "compose --profile * config --format json":
		return []byte(f.config), nil
	}
	return nil, nil
}

// project makes a repo checkout with sample.env and, unless env is "-", a .env.
func project(t *testing.T, env string) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("name: stacks-blockchain-docker\nservices: {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "sample.env"), []byte(sampleEnv), 0o644)
	if env != "-" {
		os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600)
	}
	return dir
}

func sbd(t *testing.T, dir string, d Docker, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"-C", dir}, args...), &out, &errb, d)
	return code, out.String(), errb.String()
}

const runningPS = `{"Service":"stacks-blockchain","Name":"stacks-blockchain","State":"running","Health":"healthy","Status":"Up 2 hours"}
{"Service":"config","Name":"stacks-blockchain-docker-config-1","State":"exited","Status":"Exited (0)"}`

func TestPlans(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		args      []string
		want      string
		code      int
	}{
		{"start", sampleEnv, []string{"start"}, "docker compose up -d\n", 0},
		{"up alias", sampleEnv, []string{"up"}, "docker compose up -d\n", 0},
		{"start creates .env", "-", []string{"start"}, "cp sample.env .env\ndocker compose up -d\n", 0},
		{"start testnet with monitoring", sampleEnv, []string{"start", "--network", "TESTNET", "--with", "monitoring"},
			"edit .env: NETWORK=testnet\nedit .env: COMPOSE_PROFILES=node,api,monitoring\ndocker compose up -d\n", 0},
		{"start node only", sampleEnv, []string{"start", "--without=api"}, "edit .env: COMPOSE_PROFILES=node\ndocker compose up -d\n", 0},
		{"start exact profiles", sampleEnv, []string{"start", "--profiles", "node,proxy"}, "edit .env: COMPOSE_PROFILES=node,proxy\ndocker compose up -d\n", 0},
		{"start signer without key", sampleEnv, []string{"start", "--with", "signer"}, "", 2},
		{"start signer with key", strings.Replace(sampleEnv, "SIGNER_PRIVATE_KEY=\n", "SIGNER_PRIVATE_KEY=ab01\n", 1), []string{"start", "--with", "signer"},
			"edit .env: COMPOSE_PROFILES=node,api,signer\ndocker compose up -d\n", 0},
		{"start testnet signer needs the testnet key", strings.Replace(sampleEnv, "SIGNER_PRIVATE_KEY=\n", "SIGNER_PRIVATE_KEY=ab01\n", 1), []string{"start", "--network", "testnet", "--with", "signer"}, "", 2},
		{"start bad network", sampleEnv, []string{"start", "--network", "mocknet"}, "", 2},
		{"start bad profile", sampleEnv, []string{"start", "--with", "seed"}, "", 2},
		{"start nothing selected", sampleEnv, []string{"start", "--without", "node,api"}, "", 2},
		{"stop", sampleEnv, []string{"stop"}, "docker compose --profile '*' down\n", 0},
		{"restart", sampleEnv, []string{"restart", "--with", "proxy"}, "docker compose --profile '*' down\nedit .env: COMPOSE_PROFILES=node,api,proxy\ndocker compose up -d\n", 0},
		{"logs", sampleEnv, []string{"logs"}, "docker compose logs -f --tail 100\n", 0},
		{"logs service", sampleEnv, []string{"logs", "stacks-blockchain", "--tail", "10", "--no-follow"}, "docker compose logs --tail 10 stacks-blockchain\n", 0},
		{"logs export", sampleEnv, []string{"logs", "export"},
			"docker compose --profile '*' logs --no-color --timestamps stacks-blockchain > exported-logs/stacks-blockchain.log\n" +
				"docker compose --profile '*' logs --no-color --timestamps config > exported-logs/stacks-blockchain-docker-config-1.log\n", 0},
		{"pull", sampleEnv, []string{"pull"}, "docker compose pull --ignore-buildable\n", 0},
		{"upgrade alias", sampleEnv, []string{"upgrade"}, "docker compose pull --ignore-buildable\n", 0},
		{"seed", sampleEnv, []string{"seed"}, "docker compose run --rm seed\n", 0},
		{"seed passes its options through", sampleEnv, []string{"seed", "--dry-run", "--chainstate", "a.tar.zst", "--yes"},
			"docker compose run --rm seed --dry-run --chainstate a.tar.zst --yes\n", 0},
		{"global yes reaches the seed job", sampleEnv, []string{"--yes", "seed"}, "docker compose run --rm seed --yes\n", 0},
		{"reset", sampleEnv, []string{"reset"}, "docker compose run --rm reset\n", 0},
		{"reset --yes", sampleEnv, []string{"reset", "--yes"}, "docker compose run --rm reset --yes\n", 0},
		{"bns", sampleEnv, []string{"bns"}, "docker compose run --rm bns\n", 0},
		{"config render", sampleEnv, []string{"config", "render"}, "docker compose run --rm config\n", 0},
		{"config without render", sampleEnv, []string{"config"}, "", 2},
		{"env set", sampleEnv, []string{"env", "set", "NETWORK=Testnet", "STACKS_LOG_DEBUG=1"}, "edit .env: NETWORK=testnet\nedit .env: STACKS_LOG_DEBUG=1\n", 0},
		{"env set bad profiles", sampleEnv, []string{"env", "set", "COMPOSE_PROFILES=node,seed"}, "", 2},
		{"unknown command", sampleEnv, []string{"frobnicate"}, "", 2},
		{"stop takes no arguments", sampleEnv, []string{"stop", "now"}, "", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := project(t, tc.env)
			d := &fakeDocker{ps: runningPS}
			code, out, errOut := sbd(t, dir, d, append([]string{"plan"}, tc.args...)...)
			if code != tc.code {
				t.Fatalf("exit %d, want %d (stderr: %s)", code, tc.code, errOut)
			}
			if tc.code == 0 && out != tc.want {
				t.Errorf("plan:\n%s\nwant:\n%s", out, tc.want)
			}
			if len(d.ran) > 0 {
				t.Errorf("plan must not run anything, ran %v", d.ran)
			}
		})
	}
}

func TestStartEditsEnvAndRefusesWhileRunning(t *testing.T) {
	dir := project(t, sampleEnv)
	d := &fakeDocker{ps: runningPS}
	if code, _, errOut := sbd(t, dir, d, "start", "--network", "testnet"); code != 1 || !strings.Contains(errOut, "already running") {
		t.Fatalf("start while running: exit %d, %s", code, errOut)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".env")); string(b) != sampleEnv {
		t.Errorf(".env must be untouched when start refuses")
	}

	d.ps = `{"Service":"config","Name":"c","State":"exited"}`
	if code, _, errOut := sbd(t, dir, d, "start", "--network", "testnet", "--with", "monitoring"); code != 0 {
		t.Fatalf("start: exit %d: %s", code, errOut)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".env"))
	want := strings.Replace(strings.Replace(sampleEnv, "NETWORK=mainnet", "NETWORK=testnet", 1), "COMPOSE_PROFILES=node,api", "COMPOSE_PROFILES=node,api,monitoring", 1)
	if string(b) != want {
		t.Errorf(".env after start:\n%s\nwant:\n%s", b, want)
	}
	if prev, _ := os.ReadFile(filepath.Join(dir, ".env.save")); string(prev) != sampleEnv {
		t.Errorf(".env.save must hold .env as it was before the command:\n%s", prev)
	}
	if len(d.ran) != 1 || d.ran[0] != "compose up -d" {
		t.Errorf("ran %v", d.ran)
	}
}

func TestStartCreatesEnv(t *testing.T) {
	dir := project(t, "-")
	d := &fakeDocker{}
	if code, _, errOut := sbd(t, dir, d, "start", "--without", "api"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if !strings.Contains(string(b), "## comment kept\n") || !strings.Contains(string(b), "COMPOSE_PROFILES=node\n") {
		t.Errorf(".env:\n%s", b)
	}
}

func TestJobsRefuseWhileRunning(t *testing.T) {
	for _, args := range [][]string{{"reset", "--yes"}, {"seed"}, {"bns"}} {
		dir := project(t, sampleEnv)
		d := &fakeDocker{ps: runningPS}
		code, _, errOut := sbd(t, dir, d, args...)
		if code != 1 || !strings.Contains(errOut, "sbd stop") || !strings.Contains(errOut, "mainnet") {
			t.Errorf("%v: exit %d, %q", args, code, errOut)
		}
		if len(d.ran) > 0 {
			t.Errorf("%v ran %v", args, d.ran)
		}
	}
}

func TestDockerExitCodePassesThrough(t *testing.T) {
	d := &fakeDocker{exit: 2}
	if code, _, _ := sbd(t, project(t, sampleEnv), d, "reset"); code != 2 {
		t.Errorf("a job's exit 2 (needs --yes) must reach the caller, got %d", code)
	}
}

func TestStatus(t *testing.T) {
	dir := project(t, sampleEnv)
	code, out, _ := sbd(t, dir, &fakeDocker{ps: runningPS}, "status")
	if code != 0 || !strings.Contains(out, "mainnet (node,api): running") || !strings.Contains(out, "healthy") {
		t.Errorf("running: exit %d\n%s", code, out)
	}
	code, out, _ = sbd(t, dir, &fakeDocker{ps: `[{"Service":"config","Name":"c","State":"exited"}]`}, "status", "--json")
	var st struct {
		Network    string
		Running    bool
		Containers []Container
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil || code != 1 || st.Running || st.Network != "mainnet" || len(st.Containers) != 1 {
		t.Errorf("stopped (old compose array format): exit %d, %+v, %v\n%s", code, st, err, out)
	}
}

func TestDoctor(t *testing.T) {
	cfg := `{"services":{"config":{},"stacks-blockchain":{"profiles":["node"]},"grafana":{"profiles":["monitoring"]}}}`
	ps := runningPS + "\n" + `{"Service":"grafana","Name":"stacks-grafana","State":"running"}`
	code, out, _ := sbd(t, project(t, sampleEnv), &fakeDocker{version: "2.39.1", ps: ps, config: cfg}, "doctor")
	if code != 0 || !strings.Contains(out, "stacks-grafana running but not in COMPOSE_PROFILES") {
		t.Errorf("exit %d\n%s", code, out)
	}
	code, out, _ = sbd(t, project(t, sampleEnv), &fakeDocker{version: "2.20.2"}, "doctor")
	if code != 1 || !strings.Contains(out, "2.24.0 or newer is required") {
		t.Errorf("old compose: exit %d\n%s", code, out)
	}
	code, out, _ = sbd(t, project(t, strings.Replace(sampleEnv, "node,api", "node,api,signer", 1)), &fakeDocker{version: "5.1.4"}, "doctor")
	if code != 1 || !strings.Contains(out, "SIGNER_PRIVATE_KEY is empty") {
		t.Errorf("signer without key: exit %d\n%s", code, out)
	}
}

func TestEnvEditing(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte("# c\nA=1\nexport B = 'x y' # not a comment inside quotes\nC=plain # trailing\nA=2\n"), 0o600)
	env, err := LoadEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"A": "2", "B": "x y", "C": "plain"} {
		if v, _ := env.Get(k); v != want {
			t.Errorf("%s = %q, want %q", k, v, want)
		}
	}
	if env.Set("C", "plain") {
		t.Errorf("setting the same value is not a change")
	}
	env.Set("A", "three words")
	env.Set("D", `it's`)
	env.Save(true)
	b, _ := os.ReadFile(path)
	want := "# c\nA=1\nexport B = 'x y' # not a comment inside quotes\nC=plain # trailing\nA='three words'\nD=\"it's\"\n"
	if string(b) != want {
		t.Errorf("saved:\n%s\nwant:\n%s", b, want)
	}
	again, _ := LoadEnv(path)
	if v, _ := again.Get("D"); v != "it's" {
		t.Errorf("round trip: %q", v)
	}
}

func TestFindProject(t *testing.T) {
	dir := project(t, sampleEnv)
	sub := filepath.Join(dir, "a", "b")
	os.MkdirAll(sub, 0o755)
	if got, err := FindProject("", sub); err != nil || got != dir {
		t.Errorf("from a subdir: %s, %v", got, err)
	}
	if _, err := FindProject("", t.TempDir()); err == nil {
		t.Errorf("outside a checkout must fail")
	}
}

// Every compose command sbd runs is documented for plain-Compose users (refactor/14 R4): each plan
// line, with service and container names replaced by placeholders, appears in docs/usage.md.
func TestPlansAreDocumented(t *testing.T) {
	doc, err := os.ReadFile("../../docs/usage.md")
	if err != nil {
		t.Fatal(err)
	}
	dir := project(t, strings.Replace(sampleEnv, "SIGNER_PRIVATE_KEY=\n", "SIGNER_PRIVATE_KEY=ab\n", 1))
	os.MkdirAll(filepath.Join(dir, "conf", "mainnet"), 0o755) // a hand-edited config, so migrate has a step
	os.WriteFile(filepath.Join(dir, "conf", "mainnet", "Config.toml"), []byte("[node]\n"), 0o644)
	placeholders := strings.NewReplacer(
		"--timestamps stacks-blockchain > exported-logs/stacks-blockchain.log", "--timestamps <service> > exported-logs/<container>.log",
		"--tail 100 stacks-blockchain", "--tail 100 <service>")
	seen := map[string]bool{}
	for _, args := range [][]string{
		{"start"}, {"stop"}, {"logs"}, {"logs", "stacks-blockchain"}, {"logs", "export"},
		{"pull"}, {"seed"}, {"reset", "--yes"}, {"bns"}, {"config", "render"}, {"migrate"},
	} {
		_, out, _ := sbd(t, dir, &fakeDocker{ps: strings.SplitN(runningPS, "\n", 2)[0]}, append([]string{"plan"}, args...)...)
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			line = placeholders.Replace(line)
			if !strings.HasPrefix(line, "docker ") || seen[line] {
				continue
			}
			seen[line] = true
			if !regexp.MustCompile(`(?m)` + regexp.QuoteMeta(line) + "(`|\\s*$|\\s+#)").Match(doc) {
				t.Errorf("docs/usage.md doesn't show `%s` (sbd %s)", line, strings.Join(args, " "))
			}
		}
	}
}

// An install from before the compose-first layout: no COMPOSE_PROFILES, an event-replay setting, a
// hand-edited Config.toml with the signer on, and an event-replay data dir.
func TestMigrate(t *testing.T) {
	dir := project(t, "NETWORK=mainnet\nSIGNER_PRIVATE_KEY=ab01\nSTACKS_EXPORT_EVENTS_FILE=/tmp/events.tsv\n")
	os.MkdirAll(filepath.Join(dir, "conf", "mainnet"), 0o755)
	os.WriteFile(filepath.Join(dir, "conf", "mainnet", "Config.toml"), []byte("[node]\n# stacker = false\nstacker = true\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "persistent-data", "mainnet", "event-replay"), 0o755)
	code, out, errOut := sbd(t, dir, &fakeDocker{}, "plan", "migrate")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{
		"edit .env: COMPOSE_PROFILES=node,api,signer\n",
		"# STACKS_EXPORT_EVENTS_FILE in .env is no longer used",
		"docker compose run --rm config suggest\n",
		"event-replay is no longer used",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan lacks %q:\n%s", want, out)
		}
	}

	// The signer stays off without its key; a suggestion already written is pointed to, not redone
	os.WriteFile(filepath.Join(dir, ".env"), []byte("NETWORK=mainnet\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "conf", "mainnet", "Config.overrides.toml.suggested"), []byte("[node]\n"), 0o644)
	_, out, _ = sbd(t, dir, &fakeDocker{}, "plan", "migrate")
	if !strings.Contains(out, "COMPOSE_PROFILES=node,api\n") || !strings.Contains(out, "SIGNER_PRIVATE_KEY is empty") ||
		strings.Contains(out, "config suggest") || !strings.Contains(out, "review conf/mainnet/Config.overrides.toml.suggested") {
		t.Errorf("no key / suggestion pending:\n%s", out)
	}
	os.Remove(filepath.Join(dir, "conf", "mainnet", "Config.overrides.toml.suggested"))

	// Up to date: a generated Config.toml and COMPOSE_PROFILES set
	os.WriteFile(filepath.Join(dir, "conf", "mainnet", "Config.toml"), []byte("# Generated by the config job from x\n"), 0o644)
	os.RemoveAll(filepath.Join(dir, "persistent-data"))
	os.WriteFile(filepath.Join(dir, ".env"), []byte(sampleEnv), 0o600)
	if _, out, _ := sbd(t, dir, &fakeDocker{}, "plan", "migrate"); out != "# nothing to migrate\n" {
		t.Errorf("up to date:\n%s", out)
	}
}
