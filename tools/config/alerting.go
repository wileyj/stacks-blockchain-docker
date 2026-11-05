package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Grafana alerting (refactor/12 M5; owner, 2026-11-05: Grafana rather than Alertmanager, off by
// default). With MONITORING_ALERTS=1 the config job renders
// conf/monitoring/grafana/provisioning/alerting/stacks.json from the alert rules in
// conf/monitoring/rules/stacks.yml, so the expressions stay in one place and stay promtool-tested;
// Grafana evaluates them against Prometheus and sends the notifications. Grafana's own evaluation and
// delivery can't be tested with promtool.
//
// Contact details stay out of the file: it refers to GRAFANA_ALERT_WEBHOOK_URL and GRAFANA_ALERT_EMAIL
// as $__env{...}, which Grafana expands from its environment when it loads the file. Grafana expands
// only that form in alerting files: other `$` (templates, math) are left as they are, and `$$` is not
// unescaped (checked on Grafana 13: escaping broke every rule).

// promRules is the part of a Prometheus rules file the conversion reads.
type promRules struct {
	Groups []struct {
		Name  string `yaml:"name"`
		Rules []struct {
			Alert       string            `yaml:"alert"`
			Expr        string            `yaml:"expr"`
			For         string            `yaml:"for"`
			Labels      map[string]string `yaml:"labels"`
			Annotations map[string]string `yaml:"annotations"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

const (
	grafanaDatasource = "prometheus" // uid in provisioning/datasources/prometheus.yml
	contactPoint      = "stacks"
)

var promValue = regexp.MustCompile(`\{\{\s*\$value\s*\}\}`)

// grafanaTemplate turns a Prometheus annotation template into Grafana's: $labels is the same, $value
// is the value of query A.
func grafanaTemplate(s string) string {
	return promValue.ReplaceAllLiteralString(s, "{{ $values.A.Value }}")
}

// RenderGrafanaAlerting returns the Grafana alerting provisioning file for the alert rules.
func RenderGrafanaAlerting(rules []byte, s Settings) ([]byte, error) {
	var pr promRules
	if err := yaml.Unmarshal(rules, &pr); err != nil {
		return nil, fmt.Errorf("reading the alert rules: %w", err)
	}
	var out []any
	for _, g := range pr.Groups {
		var grs []any
		for _, r := range g.Rules {
			if r.Alert == "" {
				continue // recording rules stay in Prometheus; alerts that use them query their results
			}
			forDur := r.For
			if forDur == "" {
				forDur = "0s"
			}
			ann := map[string]string{}
			for k, v := range r.Annotations {
				ann[k] = grafanaTemplate(v)
			}
			grs = append(grs, map[string]any{
				"uid":       "stacks-" + strings.ToLower(r.Alert),
				"title":     r.Alert,
				"condition": "B",
				"data": []any{
					map[string]any{"refId": "A", "datasourceUid": grafanaDatasource,
						"relativeTimeRange": map[string]int{"from": 600, "to": 0},
						"model":             map[string]any{"refId": "A", "expr": strings.TrimSpace(r.Expr), "instant": true}},
					// The expression already selects the series that alert; B is true for each of them
					map[string]any{"refId": "B", "datasourceUid": "__expr__",
						"relativeTimeRange": map[string]int{"from": 600, "to": 0},
						"model":             map[string]any{"refId": "B", "type": "math", "expression": "$A == $A"}},
				},
				"noDataState":  "OK",
				"execErrState": "Error",
				"for":          forDur,
				"labels":       r.Labels,
				"annotations":  ann,
			})
		}
		if len(grs) > 0 {
			out = append(out, map[string]any{"orgId": 1, "name": g.Name, "folder": "Stacks", "interval": "30s", "rules": grs})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no alert rules found")
	}
	var receivers []any
	if s.AlertWebhook {
		receivers = append(receivers, map[string]any{"uid": "stacks-webhook", "type": "webhook",
			"settings": map[string]string{"url": "$__env{GRAFANA_ALERT_WEBHOOK_URL}"}})
	}
	if s.AlertEmail {
		receivers = append(receivers, map[string]any{"uid": "stacks-email", "type": "email",
			"settings": map[string]string{"addresses": "$__env{GRAFANA_ALERT_EMAIL}"}})
	}
	doc := map[string]any{
		"apiVersion":    1,
		"groups":        out,
		"contactPoints": []any{map[string]any{"orgId": 1, "name": contactPoint, "receivers": receivers}},
		"policies": []any{map[string]any{"orgId": 1, "receiver": contactPoint,
			"group_by": []string{"alertname", "job"}, "repeat_interval": "4h"}},
	}
	return json.MarshalIndent(doc, "", "  ")
}
