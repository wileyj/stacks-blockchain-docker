package main

import (
	"fmt"
	"strings"
)

// scrapeTarget is one Prometheus job: a compose service reached by its service name over the
// Docker network (container names are not used: they may be overridden).
type scrapeTarget struct {
	job, target string
}

// ScrapeTargets lists what Prometheus scrapes for the enabled profiles, so a service that isn't
// running (signer off, node-only) never shows up as a down target (refactor/12).
func (s Settings) ScrapeTargets() []scrapeTarget {
	targets := []scrapeTarget{{"prometheus", "localhost:9090"}, {"node-exporter", "node-exporter:9100"}}
	if s.ContainerMetrics {
		targets = append(targets, scrapeTarget{"cadvisor", "cadvisor:8080"})
	}
	if s.Profiles["node"] {
		targets = append(targets, scrapeTarget{"stacks-node", "stacks-blockchain:" + orDefault(s.NodeMetricsPort, "9153")})
	}
	if s.Profiles["api"] {
		// The API serves Prometheus metrics on a fixed port 9153 with NODE_ENV=production (refactor/15 #11a)
		targets = append(targets,
			scrapeTarget{"stacks-api", "stacks-blockchain-api:9153"},
			scrapeTarget{"postgres", "postgres-exporter:9187"})
	}
	if s.Profiles["signer"] {
		targets = append(targets, scrapeTarget{"stacks-signer", "stacks-signer:" + orDefault(s.SignerMetricsPort, "9154")})
	}
	return targets
}

// RenderPrometheus renders conf/monitoring/prometheus.yml. Every value is a fixed hostname or a
// port validated as a number, so plain text formatting is safe here.
func RenderPrometheus(s Settings) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "%s. Do not edit: it is rewritten on every `docker compose up`.\n", Marker)
	b.WriteString("# Scrape targets follow COMPOSE_PROFILES; rules are in conf/monitoring/rules/.\n")
	b.WriteString("global:\n  scrape_interval: 30s\n  evaluation_interval: 30s\n")
	fmt.Fprintf(&b, "  external_labels:\n    network: %s\n", s.Network)
	b.WriteString("rule_files:\n  - /etc/prometheus/rules/*.yml\n")
	b.WriteString("scrape_configs:\n")
	for _, t := range s.ScrapeTargets() {
		fmt.Fprintf(&b, "  - job_name: %s\n    static_configs:\n      - targets: [\"%s\"]\n", t.job, t.target)
	}
	return []byte(b.String())
}
