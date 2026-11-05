package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrafanaAlertingFromTheRules(t *testing.T) {
	rules, err := os.ReadFile(filepath.Join(repoConf, "monitoring", "rules", "stacks.yml"))
	if err != nil {
		t.Fatal(err)
	}
	alerts := strings.Count(string(rules), "- alert: ")
	out, err := RenderGrafanaAlerting(rules, Settings{Alerts: true, AlertWebhook: true})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	var doc struct {
		Groups []struct {
			Rules []struct {
				UID, Title, For string
				Data            []struct {
					Model map[string]any
				}
				Annotations map[string]string
			}
		}
		ContactPoints []struct {
			Receivers []struct {
				Type     string
				Settings map[string]string
			}
		} `json:"contactPoints"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	n := 0
	for _, g := range doc.Groups {
		for _, r := range g.Rules {
			n++
			if !strings.HasPrefix(r.UID, "stacks-") || len(r.UID) > 40 || r.Data[0].Model["expr"] == "" || r.Data[1].Model["expression"] != "$A == $A" {
				t.Errorf("%s: %+v", r.Title, r)
			}
			for _, a := range r.Annotations {
				if strings.Contains(a, "$value }}") {
					t.Errorf("%s: $value must become $values.A.Value: %q", r.Title, a)
				}
			}
		}
	}
	if n != alerts {
		t.Errorf("%d Grafana rules for %d Prometheus alerts", n, alerts)
	}
	if !strings.Contains(s, `"$__env{GRAFANA_ALERT_WEBHOOK_URL}"`) {
		t.Errorf("the webhook URL must stay an env reference")
	}
	// Grafana expands only $__env{...}: everything else must be plain (a $$ is not unescaped)
	if strings.Contains(s, "$$") {
		t.Errorf("no $$ escaping: Grafana keeps it literally")
	}
	for _, raw := range []string{`"$A == $A"`, `{{ $labels.`, `{{ $values.A.Value }}`} {
		if !strings.Contains(s, raw) {
			t.Errorf("%s missing", raw)
		}
	}
	if r := doc.ContactPoints[0].Receivers; len(r) != 1 || r[0].Type != "webhook" {
		t.Errorf("only the configured receivers: %+v", r)
	}
	both, _ := RenderGrafanaAlerting(rules, Settings{Alerts: true, AlertWebhook: true, AlertEmail: true})
	if !strings.Contains(string(both), `"$__env{GRAFANA_ALERT_EMAIL}"`) {
		t.Errorf("email receiver missing")
	}
}

func TestAlertingSettings(t *testing.T) {
	kv := baseEnv("mainnet", "node,api,monitoring")
	if s, err := SettingsFromEnv(env(kv)); err != nil || s.Alerts {
		t.Errorf("off by default: %+v %v", s.Alerts, err)
	}
	kv["MONITORING_ALERTS"] = "1"
	if _, err := SettingsFromEnv(env(kv)); err == nil || !strings.Contains(err.Error(), "contact point") {
		t.Errorf("alerts without a contact point must fail: %v", err)
	}
	kv["GRAFANA_ALERT_EMAIL"] = "ops@example.org"
	kv["MONITORING_GRAFANA"] = "0"
	if _, err := SettingsFromEnv(env(kv)); err == nil || !strings.Contains(err.Error(), "MONITORING_GRAFANA=1") {
		t.Errorf("alerts without Grafana must fail: %v", err)
	}
	kv["MONITORING_GRAFANA"] = "1"
	if s, err := SettingsFromEnv(env(kv)); err != nil || !s.Alerts || !s.AlertEmail {
		t.Errorf("alerts with email: %+v %v", s, err)
	}
	kv["MONITORING_ALERTS"] = "yes"
	if _, err := SettingsFromEnv(env(kv)); err == nil {
		t.Errorf("MONITORING_ALERTS must be 0 or 1")
	}
}

func TestRunWritesAndRemovesAlerting(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "mainnet"), 0o755)
	os.MkdirAll(filepath.Join(root, "monitoring", "rules"), 0o755)
	b, _ := os.ReadFile(filepath.Join(repoConf, "mainnet", "Config.toml.sample"))
	os.WriteFile(filepath.Join(root, "mainnet", "Config.toml.sample"), b, 0o644)
	r, _ := os.ReadFile(filepath.Join(repoConf, "monitoring", "rules", "stacks.yml"))
	os.WriteFile(filepath.Join(root, "monitoring", "rules", "stacks.yml"), r, 0o644)
	dest := filepath.Join(root, "monitoring", "grafana", "provisioning", "alerting", "stacks.json")
	kv := baseEnv("mainnet", "node,api,monitoring")
	kv["MONITORING_ALERTS"], kv["GRAFANA_ALERT_WEBHOOK_URL"] = "1", "http://hook"
	if err := run(root, env(kv)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("alerting on: %v", err)
	}
	kv["MONITORING_ALERTS"] = "0"
	if err := run(root, env(kv)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Errorf("alerting off must remove the file")
	}
}
