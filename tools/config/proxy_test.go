package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderProxy(t *testing.T) {
	kv := baseEnv("mainnet", "node,api,proxy")
	kv["STACKS_BLOCKCHAIN_API_PORT"] = "3998"
	s, err := SettingsFromEnv(env(kv))
	if err != nil {
		t.Fatal(err)
	}
	out := string(RenderProxy(s))
	for _, want := range []string{"server stacks-blockchain-api:3998;", "listen 80;", "location /status", "proxy_pass http://stacks-blockchain-api/;"} {
		if !strings.Contains(out, want) {
			t.Errorf("proxy lacks %q:\n%s", want, out)
		}
	}
	// HTTPS is terminated in front of the proxy (owner, 2026-11-06)
	if strings.Contains(out, "443") || strings.Contains(out, "ssl") {
		t.Errorf("no TLS in the proxy:\n%s", out)
	}
}

func TestRunWritesProxyOnlyWithTheProfile(t *testing.T) {
	for _, profiles := range []string{"node,api", "node,api,proxy"} {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "mainnet"), 0o755)
		b, _ := os.ReadFile(filepath.Join(repoConf, "mainnet", "Config.toml.sample"))
		os.WriteFile(filepath.Join(root, "mainnet", "Config.toml.sample"), b, 0o644)
		if err := run(root, env(baseEnv("mainnet", profiles))); err != nil {
			t.Fatal(err)
		}
		_, err := os.Stat(filepath.Join(root, "nginx", "proxy.conf"))
		if strings.Contains(profiles, "proxy") != (err == nil) {
			t.Errorf("%s: proxy.conf written=%v", profiles, err == nil)
		}
	}
}

func TestPostgresLocalSwitch(t *testing.T) {
	kv := baseEnv("mainnet", "node,api")
	for _, ok := range []string{"", "0", "1"} {
		kv["POSTGRES_LOCAL"] = ok
		if _, err := SettingsFromEnv(env(kv)); err != nil {
			t.Errorf("POSTGRES_LOCAL=%q: %v", ok, err)
		}
	}
	kv["POSTGRES_LOCAL"] = "true"
	if _, err := SettingsFromEnv(env(kv)); err == nil {
		t.Errorf("POSTGRES_LOCAL must be 0 or 1")
	}
}
