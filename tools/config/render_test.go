package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// The real templates: tests fail if a sample stops rendering.
var repoConf = filepath.Join("..", "..", "conf")

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func baseEnv(network, profiles string) map[string]string {
	return map[string]string{
		"NETWORK": network, "COMPOSE_PROFILES": profiles,
		"BTC_HOST": "btc.example", "BTC_RPC_USER": "u", "BTC_RPC_PASS": "p", "BTC_RPC_PORT": "8332", "BTC_P2P_PORT": "8333",
		"TBTC_HOST": "tbtc.example", "TBTC_RPC_PORT": "18443", "TBTC_P2P_PORT": "18444",
		"AUTH_TOKEN": "tok", "NODE_METRICS_PORT": "9153", "STACKS_SIGNER_PORT": "30000", "SIGNER_METRICS_PORT": "9154",
		"SIGNER_PRIVATE_KEY": "aa", "TESTNET_SIGNER_PRIVATE_KEY": "bb",
	}
}

func renderNode(t *testing.T, kv map[string]string, overrides string) map[string]any {
	t.Helper()
	s, err := SettingsFromEnv(env(kv))
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := os.ReadFile(filepath.Join(repoConf, s.Network, "Config.toml.sample"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := RenderNode(tmpl, []byte(overrides), s, "Config.toml.sample")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), Marker) {
		t.Fatalf("rendered file must start with the marker")
	}
	m := map[string]any{}
	if err := toml.Unmarshal(out, &m); err != nil {
		t.Fatalf("rendered TOML doesn't parse: %v\n%s", err, out)
	}
	return m
}

func endpoints(m map[string]any) []string {
	var eps []string
	obs, _ := m["events_observer"].([]any)
	for _, o := range obs {
		eps = append(eps, o.(map[string]any)["endpoint"].(string))
	}
	return eps
}

func TestObserversFollowProfiles(t *testing.T) {
	for _, tc := range []struct {
		profiles string
		want     []string
		stacker  bool
	}{
		{"node", nil, false},
		{"node,api", []string{"stacks-blockchain-api:3700"}, false},
		{"node,api,signer", []string{"stacks-blockchain-api:3700", "stacks-signer:30000"}, true},
		{"node,signer", []string{"stacks-signer:30000"}, true},
		{" Node , API ", []string{"stacks-blockchain-api:3700"}, false},
		{"*", []string{"stacks-blockchain-api:3700", "stacks-signer:30000"}, true},
	} {
		for _, network := range []string{"mainnet", "testnet"} {
			m := renderNode(t, baseEnv(network, tc.profiles), "")
			if got := strings.Join(endpoints(m), ","); got != strings.Join(tc.want, ",") {
				t.Errorf("%s %q: observers %q, want %q", network, tc.profiles, got, tc.want)
			}
			if got := m["node"].(map[string]any)["stacker"]; got != tc.stacker {
				t.Errorf("%s %q: stacker=%v, want %v", network, tc.profiles, got, tc.stacker)
			}
		}
	}
}

func TestDeploymentKeys(t *testing.T) {
	m := renderNode(t, baseEnv("mainnet", "node,api"), "")
	burn := m["burnchain"].(map[string]any)
	if burn["peer_host"] != "btc.example" || burn["rpc_port"] != int64(8332) || burn["username"] != "u" {
		t.Errorf("mainnet burnchain: %v", burn)
	}
	if m["connection_options"].(map[string]any)["auth_token"] != "tok" {
		t.Errorf("auth_token not set")
	}
	obs := m["events_observer"].([]any)[0].(map[string]any)
	if obs["timeout_ms"] != int64(60000) {
		t.Errorf("API observer timeout_ms = %v, want 60000 (D55)", obs["timeout_ms"])
	}

	// testnet uses TBTC_* and keeps the template's network definition
	m = renderNode(t, baseEnv("testnet", "node"), "")
	burn = m["burnchain"].(map[string]any)
	if burn["peer_host"] != "tbtc.example" || burn["mode"] != "krypton" {
		t.Errorf("testnet burnchain: %v", burn)
	}
	if _, ok := burn["username"]; ok {
		t.Errorf("testnet has no TBTC_RPC_USER: username must be absent")
	}
	if epochs, _ := burn["epochs"].([]any); len(epochs) == 0 {
		t.Errorf("testnet epochs lost")
	}
}

func TestFollowerWithoutRPCCredentials(t *testing.T) {
	kv := baseEnv("mainnet", "node")
	kv["BTC_RPC_USER"], kv["BTC_RPC_PASS"] = "", ""
	burn := renderNode(t, kv, "")["burnchain"].(map[string]any)
	if _, ok := burn["username"]; ok {
		t.Errorf("username must be dropped when BTC_RPC_USER is empty")
	}
	if _, ok := burn["password"]; ok {
		t.Errorf("password must be dropped when BTC_RPC_PASS is empty")
	}
}

func TestValuesAreEscaped(t *testing.T) {
	kv := baseEnv("mainnet", "node")
	kv["AUTH_TOKEN"] = `a"b'c\d` + "\nx"
	m := renderNode(t, kv, "")
	if got := m["connection_options"].(map[string]any)["auth_token"]; got != kv["AUTH_TOKEN"] {
		t.Errorf("auth_token round trip: %q", got)
	}
}

func TestOverrides(t *testing.T) {
	overrides := `
[node]
txindex = true
[burnchain]
peer_host = "my-btc"
[[events_observer]]
endpoint = "my-indexer:9999"
events_keys = ["*"]
`
	m := renderNode(t, baseEnv("mainnet", "node,api"), overrides)
	if m["node"].(map[string]any)["txindex"] != true {
		t.Errorf("override key not merged")
	}
	if m["node"].(map[string]any)["rpc_bind"] == nil {
		t.Errorf("template keys must survive a table merge")
	}
	if m["burnchain"].(map[string]any)["peer_host"] != "my-btc" {
		t.Errorf("override must win over .env")
	}
	if got := strings.Join(endpoints(m), ","); got != "stacks-blockchain-api:3700,my-indexer:9999" {
		t.Errorf("extra observers must be appended, got %q", got)
	}
}

func TestSigner(t *testing.T) {
	kv := baseEnv("testnet", "node,signer")
	s, _ := SettingsFromEnv(env(kv))
	tmpl, _ := os.ReadFile(filepath.Join(repoConf, "testnet", "Signer.toml.sample"))
	out, err := RenderSigner(tmpl, nil, s, "Signer.toml.sample")
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	if err := toml.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["stacks_private_key"] != "bb" || m["auth_password"] != "tok" || m["node_host"] != "stacks-blockchain:20443" || m["network"] != "testnet" {
		t.Errorf("signer: %v", m)
	}

	kv["TESTNET_SIGNER_PRIVATE_KEY"] = ""
	s, _ = SettingsFromEnv(env(kv))
	if _, err := RenderSigner(tmpl, nil, s, ""); err == nil || !strings.Contains(err.Error(), "TESTNET_SIGNER_PRIVATE_KEY") {
		t.Errorf("missing key must fail naming the variable, got %v", err)
	}
}

func TestSettingsErrors(t *testing.T) {
	for name, kv := range map[string]map[string]string{
		"no .env":         {},
		"mocknet":         {"NETWORK": "mocknet"},
		"bad port":        {"NETWORK": "mainnet", "BTC_RPC_PORT": "83a2"},
		"bad timeout":     {"NETWORK": "mainnet", "API_OBSERVER_TIMEOUT_MS": "1m"},
		"negative metric": {"NETWORK": "mainnet", "NODE_METRICS_PORT": "-1"},
	} {
		if _, err := SettingsFromEnv(env(kv)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := SettingsFromEnv(env(map[string]string{})); !strings.Contains(err.Error(), "cp sample.env .env") {
		t.Errorf("missing NETWORK must tell the user to create .env, got %v", err)
	}
}

func TestRunMovesLegacyFileOnce(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "mainnet")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpl, _ := os.ReadFile(filepath.Join(repoConf, "mainnet", "Config.toml.sample"))
	os.WriteFile(filepath.Join(dir, "Config.toml.sample"), tmpl, 0o644)
	os.WriteFile(filepath.Join(dir, "Config.toml"), []byte("# my hand edits\n"), 0o644)

	kv := baseEnv("mainnet", "node,api")
	for i := 0; i < 2; i++ {
		if err := run(root, env(kv)); err != nil {
			t.Fatal(err)
		}
	}
	legacy, err := os.ReadFile(filepath.Join(dir, "Config.toml.legacy"))
	if err != nil || string(legacy) != "# my hand edits\n" {
		t.Fatalf("hand-edited file must be kept as Config.toml.legacy: %v %q", err, legacy)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "Config.toml.legacy*"))
	if len(matches) != 1 {
		t.Errorf("a rendered file must not be moved aside again: %v", matches)
	}
	if _, err := os.Stat(filepath.Join(dir, "Signer.toml")); err == nil {
		t.Errorf("Signer.toml must not be written without the signer profile")
	}
}

func TestRunSignerFileMode(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "mainnet")
	os.MkdirAll(dir, 0o755)
	for _, f := range []string{"Config.toml.sample", "Signer.toml.sample"} {
		b, _ := os.ReadFile(filepath.Join(repoConf, "mainnet", f))
		os.WriteFile(filepath.Join(dir, f), b, 0o644)
	}
	if err := run(root, env(baseEnv("mainnet", "node,signer"))); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "Signer.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("Signer.toml holds the private key: mode %v, want 0600", fi.Mode().Perm())
	}
}
