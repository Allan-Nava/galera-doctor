package cluster

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestCollectAgainstARealServer runs the actual SQL. It is skipped unless
// GD_TEST_DSN points at a throwaway server, because the queries are the one
// part of this package that unit tests cannot check: an information_schema
// join that is subtly wrong returns rows on some builds and an error on
// others, and both look fine from a fixture.
//
//	docker run -d --rm --name gd-test -e MARIADB_ROOT_PASSWORD=testpw -p 13306:3306 mariadb:11.4
//	GD_TEST_DSN='root:testpw@tcp(127.0.0.1:13306)/' go test ./internal/cluster/ -run Real -v
func TestCollectAgainstARealServer(t *testing.T) {
	dsn := os.Getenv("GD_TEST_DSN")
	if dsn == "" {
		t.Skip("set GD_TEST_DSN to run this against a throwaway MariaDB/MySQL")
	}
	snap := Collector{Timeout: 15 * time.Second}.Collect(context.Background(), Node{Name: "local", DSN: dsn})
	if !snap.OK() {
		t.Fatalf("collect failed: %s", snap.Err)
	}
	if len(snap.Status) == 0 || len(snap.Vars) == 0 {
		t.Fatal("SHOW GLOBAL STATUS and SHOW GLOBAL VARIABLES must both return rows")
	}
	if _, ok := snap.Var("version"); !ok {
		t.Fatal("no version variable — the variables read did not work")
	}
	// Every supported server has these in the mysql schema; a fingerprint map
	// that came back empty means the information_schema query failed silently.
	if len(snap.SysTables) == 0 {
		t.Fatal("no system table fingerprints: the information_schema.COLUMNS query returned nothing")
	}
	if _, ok := snap.SysTables["user"]; !ok {
		if _, ok := snap.SysTables["global_priv"]; !ok {
			t.Fatalf("neither mysql.user nor mysql.global_priv was fingerprinted: %d tables", len(snap.SysTables))
		}
	}
	// The primary key query must not report the server's own tables.
	for _, table := range snap.TablesNoPK {
		for _, schema := range SystemSchemas {
			if len(table) > len(schema) && table[:len(schema)+1] == schema+"." {
				t.Fatalf("system schema %q leaked into the missing-primary-key list: %s", schema, table)
			}
		}
	}
	t.Logf("read %d status, %d variables, %d system tables, %d tables without a primary key",
		len(snap.Status), len(snap.Vars), len(snap.SysTables), len(snap.TablesNoPK))
}
