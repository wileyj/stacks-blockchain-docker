package main

import (
	"strings"
	"testing"
)

const sampleTOC = `;
; Archive created at 2026-10-27
;
215; 1262 16384 DATABASE - stacks_blockchain_api postgres
6123; 1259 412345 INDEX stacks_blockchain_api nft_events_asset_identifier_index postgres
6124; 1259 412346 INDEX stacks_blockchain_api nft_events_block_height_index postgres
7001; 0 0 TABLE DATA stacks_blockchain_api nft_events postgres
`

func TestFilterTOC(t *testing.T) {
	kept, removed, err := FilterTOC(sampleTOC, []string{"nft_events_asset_identifier_index"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(kept, "nft_events_asset_identifier_index") || !strings.Contains(kept, "nft_events_block_height_index") || !strings.Contains(kept, "DATABASE") {
		t.Errorf("kept list wrong:\n%s", kept)
	}
	if len(removed) != 1 || !strings.HasPrefix(removed[0], "6123;") {
		t.Errorf("removed: %q", removed)
	}
	if _, _, err := FilterTOC(sampleTOC, []string{"nft_events_typo"}); err == nil || !strings.Contains(err.Error(), "no such index") {
		t.Errorf("an unknown index must be refused, got %v", err)
	}
	// TABLE DATA with the same name must not match an index name
	if _, _, err := FilterTOC(sampleTOC, []string{"nft_events"}); err == nil {
		t.Errorf("a table name is not an index")
	}
}

func TestIndexDefinitionsAndConcurrently(t *testing.T) {
	sql := `SET statement_timeout = 0;
CREATE INDEX nft_events_asset_identifier_index ON stacks_blockchain_api.nft_events USING hash (asset_identifier) WHERE (asset_event_type_id = 2);
`
	defs := IndexDefinitions(sql, "public")
	want := `CREATE INDEX nft_events_asset_identifier_index ON "public".nft_events USING hash (asset_identifier) WHERE (asset_event_type_id = 2);`
	if len(defs) != 1 || defs[0] != want {
		t.Fatalf("defs = %q", defs)
	}
	if got := Concurrently(defs[0]); !strings.HasPrefix(got, "CREATE INDEX CONCURRENTLY nft_events_asset_identifier_index ON") {
		t.Errorf("Concurrently: %s", got)
	}
	if got := Concurrently("CREATE UNIQUE INDEX x ON s.t (a);"); got != "CREATE UNIQUE INDEX CONCURRENTLY x ON s.t (a);" {
		t.Errorf("unique: %s", got)
	}
	if got := onIndexName(defs[0]); got != `"public".nft_events_asset_identifier_index` {
		t.Errorf("onIndexName: %s", got)
	}
	if got := onIndexName(IndexDefinitions("CREATE INDEX i ON ONLY stacks_blockchain_api.t (a);", "my schema")[0]); got != `"my schema".i` {
		t.Errorf("onIndexName with ONLY and a quoted schema: %s", got)
	}
}

func TestRecordDeferredSQL(t *testing.T) {
	sql := recordDeferredSQL([]string{"i1"}, []string{"CREATE INDEX i1 ON \"public\".t (a);"})
	for _, want := range []string{"CREATE SCHEMA IF NOT EXISTS sbd_seed;", "$sbd$i1$sbd$", "$sbd$CREATE INDEX i1 ON \"public\".t (a);$sbd$", "ON CONFLICT (name)"} {
		if !strings.Contains(sql, want) {
			t.Errorf("record SQL lacks %q:\n%s", want, sql)
		}
	}
}

func TestCreateDatabaseStatement(t *testing.T) {
	script := "--\n-- Name: stacks_blockchain_api; Type: DATABASE\n--\n\n" +
		"CREATE DATABASE stacks_blockchain_api WITH TEMPLATE = template0 ENCODING = 'UTF8' LOCALE_PROVIDER = libc LOCALE = 'en_US.UTF-8';\n\n" +
		"\\connect stacks_blockchain_api\n\nALTER DATABASE stacks_blockchain_api SET search_path TO 'stacks_blockchain_api', 'public';\n" +
		"ALTER ROLE hirosystems IN DATABASE stacks_blockchain_api SET search_path TO 'stacks_blockchain_api', 'public';\n"
	got, err := CreateDatabaseStatement(script)
	want := "CREATE DATABASE stacks_blockchain_api WITH TEMPLATE = template0 ENCODING = 'UTF8' LOCALE_PROVIDER = libc LOCALE = 'en_US.UTF-8';"
	if err != nil || got != want {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := CreateDatabaseStatement("CREATE DATABASE other;\n"); err == nil {
		t.Errorf("a dump of another database must fail")
	}
}
