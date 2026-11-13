package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func caddyfile(t *testing.T, extra map[string]string) string {
	t.Helper()
	kv := baseEnv("mainnet", "node,api,proxy")
	kv["STACKS_BLOCKCHAIN_API_PORT"] = "3998"
	for k, v := range extra {
		kv[k] = v
	}
	s, err := SettingsFromEnv(env(kv))
	if err != nil {
		t.Fatal(err)
	}
	return string(RenderProxy(s))
}

// HTTP only unless PROXY_TLS=1 (owner, 2026-11-13)
func TestRenderProxy(t *testing.T) {
	out := caddyfile(t, nil)
	for _, want := range []string{Marker, "http:// {", "reverse_proxy stacks-blockchain-api:3998", "handle /status", "protocols h1 h2"} {
		if !strings.Contains(out, want) {
			t.Errorf("HTTP proxy lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "https://") || strings.Contains(out, "tls") || strings.Contains(out, "email") {
		t.Errorf("no TLS by default:\n%s", out)
	}
	// A domain: Let's Encrypt through Caddy's automatic HTTPS, with the contact email if given
	out = caddyfile(t, map[string]string{"PROXY_TLS": "1", "PROXY_DOMAIN": "API.Example.org", "PROXY_ACME_EMAIL": "ops@example.org"})
	for _, want := range []string{"\napi.example.org {\n\timport api", "\temail ops@example.org"} {
		if !strings.Contains(out, want) {
			t.Errorf("Let's Encrypt proxy lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "http:// {") || strings.Contains(out, "tls internal") {
		t.Errorf("a domain uses Let's Encrypt, not plain HTTP or a self-signed certificate:\n%s", out)
	}
	// No domain: self-signed, and port 80 redirects
	out = caddyfile(t, map[string]string{"PROXY_TLS": "1"})
	for _, want := range []string{"https:// {\n\ttls internal", "on_demand", "redir https://{host}{uri} 308"} {
		if !strings.Contains(out, want) {
			t.Errorf("self-signed proxy lacks %q:\n%s", want, out)
		}
	}
}

func TestProxySettingsErrors(t *testing.T) {
	for name, extra := range map[string]map[string]string{
		"PROXY_TLS=true":           {"PROXY_TLS": "true"},
		"domain without TLS":       {"PROXY_DOMAIN": "api.example.org"},
		"domain with a scheme":     {"PROXY_TLS": "1", "PROXY_DOMAIN": "https://api.example.org"},
		"domain with a port":       {"PROXY_TLS": "1", "PROXY_DOMAIN": "api.example.org:443"},
		"an IP is not a domain":    {"PROXY_TLS": "1", "PROXY_DOMAIN": "203.0.113.7"},
		"bad email":                {"PROXY_TLS": "1", "PROXY_DOMAIN": "api.example.org", "PROXY_ACME_EMAIL": "ops"},
		"email without the domain": {"PROXY_TLS": "1", "PROXY_ACME_EMAIL": "ops@example.org"},
	} {
		kv := baseEnv("mainnet", "node,api,proxy")
		for k, v := range extra {
			kv[k] = v
		}
		if _, err := SettingsFromEnv(env(kv)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRunWritesProxyOnlyWithTheProfile(t *testing.T) {
	for _, profiles := range []string{"node,api", "node,api,proxy"} {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "mainnet"), 0o755)
		b, _ := os.ReadFile(filepath.Join(repoConf, "mainnet", "Config.toml.sample"))
		os.WriteFile(filepath.Join(root, "mainnet", "Config.toml.sample"), b, 0o644)
		// The nginx proxy's file from before 2026-11-13 goes when it is ours, stays when it isn't
		os.MkdirAll(filepath.Join(root, "nginx"), 0o755)
		os.WriteFile(filepath.Join(root, "nginx", "proxy.conf"), []byte(Marker+". old\n"), 0o644)
		os.WriteFile(filepath.Join(root, "nginx", "postgres-local.conf"), []byte("stream {}\n"), 0o644)
		if err := run(root, env(baseEnv("mainnet", profiles))); err != nil {
			t.Fatal(err)
		}
		_, err := os.Stat(filepath.Join(root, "proxy", "Caddyfile"))
		if strings.Contains(profiles, "proxy") != (err == nil) {
			t.Errorf("%s: Caddyfile written=%v", profiles, err == nil)
		}
		if _, err := os.Stat(filepath.Join(root, "nginx", "proxy.conf")); err == nil {
			t.Errorf("%s: the old generated nginx/proxy.conf is kept", profiles)
		}
		if _, err := os.Stat(filepath.Join(root, "nginx", "postgres-local.conf")); err != nil {
			t.Errorf("%s: postgres-local.conf removed", profiles)
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
