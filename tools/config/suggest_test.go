package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Config.toml from before the renderer (the d2e558c mainnet sample as manage.sh left it), with two
// user edits in [connection_options] and an extra observer.
func TestSuggestFromLegacyConfig(t *testing.T) {
	legacy, _ := os.ReadFile("testdata/legacy-mainnet-Config.toml")
	tmpl, err := os.ReadFile("../../conf/mainnet/Config.toml.sample")
	if err != nil {
		t.Fatal(err)
	}
	upstream, _ := os.ReadFile("../../conf/upstream/mainnet-follower-conf.toml")
	s, err := SettingsFromEnv(env(map[string]string{"NETWORK": "mainnet", "COMPOSE_PROFILES": "node,api",
		"BTC_HOST": "bitcoin.mainnet.stacks.org", "BTC_RPC_PORT": "8332", "BTC_P2P_PORT": "8333", "AUTH_TOKEN": "1234", "NODE_METRICS_PORT": "9153"}))
	if err != nil {
		t.Fatal(err)
	}
	sg, err := Suggest(legacy, tmpl, upstream, s)
	if err != nil {
		t.Fatal(err)
	}
	over, err := parse(sg.Out, "suggestion")
	if err != nil {
		t.Fatalf("the suggestion must be valid TOML: %v\n%s", err, sg.Out)
	}
	conn := over["connection_options"].(map[string]any)
	if conn["private_neighbors"] != true || conn["read_only_call_limit_runtime"] != int64(1000000000) {
		t.Errorf("user edits missing: %v", conn)
	}
	obs, _ := over["events_observer"].([]any)
	if len(obs) != 1 || obs[0].(map[string]any)["endpoint"] != "my-indexer:8080" {
		t.Errorf("only the user's own observer is carried over (the API/signer ones are rendered): %v", obs)
	}
	for _, k := range []string{"node", "burnchain"} {
		if _, ok := over[k]; ok {
			t.Errorf("[%s] must not be suggested (network definition or .env), got %v", k, over[k])
		}
	}
	if strings.Join(sg.Added, ",") != "connection_options.read_only_call_limit_runtime" {
		t.Errorf("added: %v", sg.Added)
	}
	fromEnv := strings.Join(sg.FromEnv, "\n")
	for _, want := range []string{"burnchain.username = stacks (now unset): set BTC_RPC_USER in .env if you still need it", "burnchain.password", "node.stacker = true (now false): set COMPOSE_PROFILES (signer) in .env"} {
		if !strings.Contains(fromEnv, want) {
			t.Errorf("FromEnv lacks %q:\n%s", want, fromEnv)
		}
	}
	if !strings.HasPrefix(string(sg.Out), "# Suggested by `config suggest`") {
		t.Errorf("header missing:\n%s", sg.Out)
	}

	// The same file with no edits has nothing to carry over
	plain, _ := os.ReadFile("testdata/legacy-mainnet-Config.toml")
	plain = []byte(strings.NewReplacer("private_neighbors = true\nread_only_call_limit_runtime = 1000000000", "private_neighbors = false",
		"\n[[events_observer]]\nendpoint = \"my-indexer:8080\"\nevents_keys = [\"*\"]\n", "").Replace(string(plain)))
	if sg, err := Suggest(plain, tmpl, upstream, s); err != nil || sg.Out != nil {
		t.Errorf("unedited legacy file: %v\n%s", err, sg.Out)
	}
}

func TestRunSuggestFindsTheLegacyFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "mainnet")
	os.MkdirAll(filepath.Join(root, "upstream"), 0o755)
	os.MkdirAll(dir, 0o755)
	for src, dst := range map[string]string{
		"../../conf/mainnet/Config.toml.sample":          filepath.Join(dir, "Config.toml.sample"),
		"../../conf/upstream/mainnet-follower-conf.toml": filepath.Join(root, "upstream", "mainnet-follower-conf.toml"),
		"testdata/legacy-mainnet-Config.toml":            filepath.Join(dir, "Config.toml.legacy"),
	} {
		b, _ := os.ReadFile(src)
		os.WriteFile(dst, b, 0o644)
	}
	// The renderer already moved the hand-edited file aside and wrote a generated one
	os.WriteFile(filepath.Join(dir, "Config.toml"), []byte(Marker+" from Config.toml.sample.\n"), 0o644)
	getenv := env(map[string]string{"NETWORK": "mainnet", "COMPOSE_PROFILES": "node,api"})
	if err := runSuggest(root, getenv); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, suggestedName)); err != nil {
		t.Errorf("no suggestion written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Config.overrides.toml")); err == nil {
		t.Errorf("suggest must never write Config.overrides.toml")
	}
}
