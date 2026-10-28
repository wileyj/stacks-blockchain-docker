package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func jobs(s Settings) string {
	var j []string
	for _, t := range s.ScrapeTargets() {
		j = append(j, t.job+"="+t.target)
	}
	return strings.Join(j, " ")
}

func TestScrapeTargetsFollowProfiles(t *testing.T) {
	for _, tc := range []struct {
		profiles, extra string
		want            string
	}{
		{"node,monitoring", "", "prometheus=localhost:9090 node-exporter=node-exporter:9100 cadvisor=cadvisor:8080 stacks-node=stacks-blockchain:9153"},
		{"node,api,monitoring", "", "prometheus=localhost:9090 node-exporter=node-exporter:9100 cadvisor=cadvisor:8080 stacks-node=stacks-blockchain:9153 stacks-api=stacks-blockchain-api:9153 postgres=postgres-exporter:9187"},
		{"node,signer,monitoring", "", "prometheus=localhost:9090 node-exporter=node-exporter:9100 cadvisor=cadvisor:8080 stacks-node=stacks-blockchain:9153 stacks-signer=stacks-signer:9154"},
		{"node,monitoring", "0", "prometheus=localhost:9090 node-exporter=node-exporter:9100 stacks-node=stacks-blockchain:9153"},
	} {
		kv := baseEnv("mainnet", tc.profiles)
		kv["MONITORING_CONTAINER_METRICS"] = tc.extra
		s, err := SettingsFromEnv(env(kv))
		if err != nil {
			t.Fatal(err)
		}
		if got := jobs(s); got != tc.want {
			t.Errorf("%s (cadvisor=%q):\n got  %s\n want %s", tc.profiles, tc.extra, got, tc.want)
		}
	}
}

func TestScrapePortsFollowEnv(t *testing.T) {
	kv := baseEnv("testnet", "node,signer,monitoring")
	kv["NODE_METRICS_PORT"], kv["SIGNER_METRICS_PORT"] = "19153", "19154"
	s, _ := SettingsFromEnv(env(kv))
	out := string(RenderPrometheus(s))
	for _, want := range []string{`"stacks-blockchain:19153"`, `"stacks-signer:19154"`, "network: testnet", "/etc/prometheus/rules/*.yml"} {
		if !strings.Contains(out, want) {
			t.Errorf("prometheus.yml lacks %s:\n%s", want, out)
		}
	}
}

func TestContainerMetricsSwitch(t *testing.T) {
	kv := baseEnv("mainnet", "node")
	kv["MONITORING_CONTAINER_METRICS"] = "false"
	if _, err := SettingsFromEnv(env(kv)); err == nil || !strings.Contains(err.Error(), "0 or 1") {
		t.Errorf("a non 0/1 value must fail clearly, got %v", err)
	}
}

func TestRunWritesPrometheusOnlyWithMonitoring(t *testing.T) {
	for _, profiles := range []string{"node,api", "node,api,monitoring"} {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "mainnet"), 0o755)
		b, _ := os.ReadFile(filepath.Join(repoConf, "mainnet", "Config.toml.sample"))
		os.WriteFile(filepath.Join(root, "mainnet", "Config.toml.sample"), b, 0o644)
		if err := run(root, env(baseEnv("mainnet", profiles))); err != nil {
			t.Fatal(err)
		}
		_, err := os.Stat(filepath.Join(root, "monitoring", "prometheus.yml"))
		if strings.Contains(profiles, "monitoring") != (err == nil) {
			t.Errorf("%s: prometheus.yml written=%v", profiles, err == nil)
		}
	}
}
