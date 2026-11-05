package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

func TestAppScrapeIntervalAndKeepLists(t *testing.T) {
	kv := baseEnv("mainnet", "node,api,signer,monitoring")
	s, err := SettingsFromEnv(env(kv))
	if err != nil {
		t.Fatal(err)
	}
	out := string(RenderPrometheus(s))
	if !strings.Contains(out, "global:\n  scrape_interval: 30s\n  evaluation_interval: 30s\n") {
		t.Errorf("infrastructure jobs and rules stay at 30 s:\n%s", out)
	}
	for _, job := range []string{"stacks-node", "stacks-api", "stacks-signer"} {
		if !strings.Contains(out, "- job_name: "+job+"\n    scrape_interval: 15s\n") {
			t.Errorf("%s must use the default app interval 15s:\n%s", job, out)
		}
	}
	for _, job := range []string{"prometheus", "node-exporter", "cadvisor", "postgres"} {
		block := out[strings.Index(out, "- job_name: "+job+"\n"):]
		if next := strings.Index(block[1:], "- job_name:"); next > 0 {
			block = block[:next+1]
		}
		if strings.Contains(block, "scrape_interval") || !strings.Contains(block, "action: keep") {
			t.Errorf("%s: fixed 30 s and a keep-list expected:\n%s", job, block)
		}
	}
	if strings.Count(out, "action: keep") != 4 {
		t.Errorf("only the 4 infrastructure jobs are filtered:\n%s", out)
	}

	kv["PROMETHEUS_APP_SCRAPE_INTERVAL"] = "5s"
	s, _ = SettingsFromEnv(env(kv))
	if !strings.Contains(string(RenderPrometheus(s)), "- job_name: stacks-api\n    scrape_interval: 5s\n") {
		t.Errorf("PROMETHEUS_APP_SCRAPE_INTERVAL must reach the app jobs")
	}
	for _, bad := range []string{"15", "0s", "fast", "15 s"} {
		kv["PROMETHEUS_APP_SCRAPE_INTERVAL"] = bad
		if _, err := SettingsFromEnv(env(kv)); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

// Every infrastructure metric a rule or dashboard panel uses must survive its job's keep-list.
func TestKeepListsCoverRulesAndDashboards(t *testing.T) {
	text, err := os.ReadFile(filepath.Join(repoConf, "monitoring", "rules", "stacks.yml"))
	if err != nil {
		t.Fatal(err)
	}
	dashboards, _ := filepath.Glob(filepath.Join(repoConf, "monitoring", "grafana", "dashboards", "*.json"))
	if len(dashboards) == 0 {
		t.Fatal("no dashboards found")
	}
	for _, f := range dashboards {
		b, _ := os.ReadFile(f)
		text = append(text, b...)
	}
	lists := map[string][]string{"prometheus_": keepPrometheus, "process_": keepPrometheus, "node_": keepNodeExporter,
		"container_": keepCadvisor, "machine_": keepCadvisor, "pg_": keepPostgres}
	seen := 0
	// Not after ':' (the tail of a recording rule such as stacks:node_tip_height); container_label_* are labels
	for _, m := range regexp.MustCompile(`(?:^|[^:a-zA-Z0-9_])((?:prometheus|process|node|container|machine|pg)_[a-zA-Z0-9_]+)`).FindAllStringSubmatch(string(text), -1) {
		tok := m[1]
		if strings.HasPrefix(tok, "node_exporter") || strings.HasPrefix(tok, "container_label_") {
			continue
		}
		for prefix, keep := range lists {
			if strings.HasPrefix(tok, prefix) {
				seen++
				if !slices.Contains(keep, tok) {
					t.Errorf("%s is used by a rule or dashboard but dropped by the %s keep-list", tok, prefix)
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("found no infrastructure metrics in the rules and dashboards: the test isn't reading them")
	}
}
