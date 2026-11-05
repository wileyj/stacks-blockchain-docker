package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderProxy(t *testing.T) {
	kv := baseEnv("mainnet", "node,api,proxy")
	s, err := SettingsFromEnv(env(kv))
	if err != nil {
		t.Fatal(err)
	}
	http := string(RenderProxy(s))
	for _, want := range []string{"server stacks-blockchain-api:3999;", "listen 80;", "location /status", "proxy_pass http://stacks-blockchain-api/;"} {
		if !strings.Contains(http, want) {
			t.Errorf("HTTP proxy lacks %q:\n%s", want, http)
		}
	}
	if strings.Contains(http, "443") || strings.Contains(http, "return 301") {
		t.Errorf("no TLS unless PROXY_TLS=1:\n%s", http)
	}

	kv["PROXY_TLS"], kv["STACKS_BLOCKCHAIN_API_PORT"] = "1", "3998"
	s, _ = SettingsFromEnv(env(kv))
	tls := string(RenderProxy(s))
	for _, want := range []string{"server stacks-blockchain-api:3998;", "listen 443 ssl;", "/etc/nginx/tls/fullchain.pem", "/etc/nginx/tls/privkey.pem", "return 301 https://$host$request_uri;"} {
		if !strings.Contains(tls, want) {
			t.Errorf("TLS proxy lacks %q:\n%s", want, tls)
		}
	}
	// /status stays on plain HTTP (the healthcheck), before the redirect
	if i, j := strings.Index(tls, "location /status"), strings.Index(tls, "return 301"); i < 0 || i > j {
		t.Errorf("/status must be served on port 80:\n%s", tls)
	}
	kv["PROXY_TLS"] = "yes"
	if _, err := SettingsFromEnv(env(kv)); err == nil {
		t.Errorf("PROXY_TLS must be 0 or 1")
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
