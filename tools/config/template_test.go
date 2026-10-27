package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

const upstreamSample = `
[node]
working_dir = "/tmp/x"
rpc_bind = "0.0.0.0:20443"
bootstrap_node = "abc@seed.example:20444"
miner = false
stacker = false
pox_5_bond_admin = "ST1"

[burnchain]
mode = "krypton"
peer_host = "127.0.0.1"
rpc_port = 1
pox_prepare_length = 100

[[events_observer]]
endpoint = "localhost:3700"
events_keys = ["*"]

[[ustx_balance]]
address = "ST2"
amount = 1

[[burnchain.epochs]]
epoch_name = "4.0"
start_height = 2702
`

func TestTemplateKeepsNetworkDropsDeployment(t *testing.T) {
	deployment := "[node]\nworking_dir = \"/root/stacks-blockchain/data\"\n[burnchain]\npeer_host = \"btc.example\"\nrpc_port = 18443\n"
	out, err := Template([]byte(upstreamSample), []byte(deployment), "stacks-core@abc sample/conf/x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), TemplateMarker+" from stacks-core@abc") {
		t.Fatalf("missing provenance header:\n%s", out)
	}
	m := map[string]any{}
	if err := toml.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	node, burn := m["node"].(map[string]any), m["burnchain"].(map[string]any)
	// network definition from upstream
	if node["bootstrap_node"] != "abc@seed.example:20444" || node["pox_5_bond_admin"] != "ST1" ||
		burn["mode"] != "krypton" || burn["pox_prepare_length"] != int64(100) {
		t.Errorf("network keys lost: node=%v burnchain=%v", node, burn)
	}
	if len(m["ustx_balance"].([]any)) != 1 || len(burn["epochs"].([]any)) != 1 {
		t.Errorf("balances/epochs lost")
	}
	// deployment keys: upstream values dropped, repo layer wins
	if node["working_dir"] != "/root/stacks-blockchain/data" || burn["peer_host"] != "btc.example" || burn["rpc_port"] != int64(18443) {
		t.Errorf("deployment layer not applied: node=%v burnchain=%v", node, burn)
	}
	for _, k := range []string{"miner", "stacker", "rpc_bind"} {
		if _, ok := node[k]; ok {
			t.Errorf("node.%s is a deployment key and must not come from upstream", k)
		}
	}
	if _, ok := m["events_observer"]; ok {
		t.Errorf("upstream observers must be dropped (the renderer adds them per profile)")
	}
}

// The committed samples must be exactly what tools/sync-upstream-conf.sh generates from the
// committed upstream files and deployment layers: catches hand edits and partial syncs.
func TestCommittedSamplesAreGenerated(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(repoConf, "upstream", "SOURCE"))
	if err != nil {
		t.Skipf("no conf/upstream/SOURCE yet: %v", err)
	}
	var sha string
	for _, l := range strings.Split(string(source), "\n") {
		if v, ok := strings.CutPrefix(l, "commit="); ok {
			sha = v
		}
	}
	for _, net := range []string{"mainnet", "testnet"} {
		upstream, err := os.ReadFile(filepath.Join(repoConf, "upstream", net+"-follower-conf.toml"))
		if err != nil {
			t.Fatal(err)
		}
		deployment, err := os.ReadFile(filepath.Join(repoConf, net, "Config.deployment.toml"))
		if err != nil {
			t.Fatal(err)
		}
		want, err := Template(upstream, deployment, "stacks-core@"+sha+" sample/conf/"+net+"-follower-conf.toml")
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(repoConf, net, "Config.toml.sample"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("conf/%s/Config.toml.sample is not generated from conf/upstream + Config.deployment.toml: run tools/sync-upstream-conf.sh", net)
		}
	}
}
