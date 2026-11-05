package main

import (
	"fmt"
	"strings"
)

// scrapeTarget is one Prometheus job: a compose service reached by its service name over the
// Docker network (container names are not used: they may be overridden).
//   - app jobs (node, API, signer) are scraped every PROMETHEUS_APP_SCRAPE_INTERVAL and keep every
//     metric: they are small and the ones worth having at block resolution
//   - infrastructure jobs use the fixed global 30 s and keep only `keep`: the families the rules and
//     dashboards use plus a few health series (refactor/12 M7; everything else was 94% of the series)
type scrapeTarget struct {
	job, target string
	app         bool
	keep        []string
}

// Keep-lists for the infrastructure jobs. Adding a dashboard panel or rule over one of these jobs means
// adding its metric here (TestKeepListsCoverRulesAndDashboards checks it).
var (
	keepPrometheus = []string{"up", "prometheus_tsdb_head_series", "prometheus_tsdb_storage_blocks_bytes",
		"prometheus_tsdb_wal_storage_size_bytes", "prometheus_tsdb_compactions_failed_total",
		"prometheus_rule_evaluation_failures_total", "prometheus_config_last_reload_successful",
		"prometheus_notifications_dropped_total", "process_resident_memory_bytes"}
	keepNodeExporter = []string{"up", "node_boot_time_seconds", "node_cpu_seconds_total", "node_load1", "node_load5",
		"node_load15", "node_memory_MemAvailable_bytes", "node_memory_MemTotal_bytes", "node_filesystem_avail_bytes",
		"node_filesystem_size_bytes", "node_filesystem_readonly", "node_disk_read_bytes_total",
		"node_disk_written_bytes_total", "node_disk_io_time_seconds_total", "node_network_receive_bytes_total",
		"node_network_transmit_bytes_total"}
	keepCadvisor = []string{"up", "machine_memory_bytes", "container_start_time_seconds", "container_cpu_usage_seconds_total",
		"container_memory_working_set_bytes", "container_oom_events_total", "container_fs_reads_bytes_total",
		"container_fs_writes_bytes_total", "container_network_receive_bytes_total", "container_network_transmit_bytes_total"}
	// The API database: size, connections, transactions, deadlocks (a VACUUM/ANALYZE during a deferred
	// index build, refactor/15 "Phase 5"), locks and the oldest open transaction
	keepPostgres = []string{"up", "pg_up", "pg_database_size_bytes", "pg_stat_database_numbackends",
		"pg_stat_database_xact_commit", "pg_stat_database_xact_rollback", "pg_stat_database_deadlocks",
		"pg_locks_count", "pg_long_running_transactions", "pg_long_running_transactions_oldest_timestamp_seconds"}
)

// ScrapeTargets lists what Prometheus scrapes for the enabled profiles, so a service that isn't
// running (signer off, node-only) never shows up as a down target (refactor/12).
func (s Settings) ScrapeTargets() []scrapeTarget {
	targets := []scrapeTarget{{job: "prometheus", target: "localhost:9090", keep: keepPrometheus},
		{job: "node-exporter", target: "node-exporter:9100", keep: keepNodeExporter}}
	if s.ContainerMetrics {
		targets = append(targets, scrapeTarget{job: "cadvisor", target: "cadvisor:8080", keep: keepCadvisor})
	}
	if s.Profiles["node"] {
		targets = append(targets, scrapeTarget{job: "stacks-node", target: "stacks-blockchain:" + orDefault(s.NodeMetricsPort, "9153"), app: true})
	}
	if s.Profiles["api"] {
		// The API serves Prometheus metrics on a fixed port 9153 with NODE_ENV=production (refactor/15 #11a)
		targets = append(targets,
			scrapeTarget{job: "stacks-api", target: "stacks-blockchain-api:9153", app: true},
			scrapeTarget{job: "postgres", target: "postgres-exporter:9187", keep: keepPostgres})
	}
	if s.Profiles["signer"] {
		targets = append(targets, scrapeTarget{job: "stacks-signer", target: "stacks-signer:" + orDefault(s.SignerMetricsPort, "9154"), app: true})
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
	fmt.Fprintf(&b, "  # application jobs: PROMETHEUS_APP_SCRAPE_INTERVAL=%s\n", s.AppScrapeInterval)
	fmt.Fprintf(&b, "  external_labels:\n    network: %s\n", s.Network)
	b.WriteString("rule_files:\n  - /etc/prometheus/rules/*.yml\n")
	b.WriteString("scrape_configs:\n")
	for _, t := range s.ScrapeTargets() {
		fmt.Fprintf(&b, "  - job_name: %s\n", t.job)
		if t.app {
			fmt.Fprintf(&b, "    scrape_interval: %s\n", s.AppScrapeInterval)
		}
		fmt.Fprintf(&b, "    static_configs:\n      - targets: [\"%s\"]\n", t.target)
		if len(t.keep) > 0 {
			fmt.Fprintf(&b, "    metric_relabel_configs:\n      - source_labels: [__name__]\n        action: keep\n        regex: %s\n", strings.Join(t.keep, "|"))
		}
	}
	return []byte(b.String())
}
