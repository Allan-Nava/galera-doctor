package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clusters.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// load writes a config and reads it back, which is what most of these tests
// mean by "this configuration".
func load(t *testing.T, body string) (File, error) {
	t.Helper()
	return Load(write(t, body))
}

func mustLoad(t *testing.T, body string) File {
	t.Helper()
	f, err := load(t, body)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return f
}

func TestLoadExpandsEnvironmentReferences(t *testing.T) {
	t.Setenv("GD_TEST_PASS", "s3cret")
	path := write(t, `{"clusters":{"compress":{"nodes":[
	  {"name":"sg-01","dsn":"audit:${GD_TEST_PASS}@tcp(10.11.1.5:3306)/"}],
	  "expect_nodes":3}}}`)
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Clusters["compress"].Nodes[0].DSN; !strings.Contains(got, "s3cret") {
		t.Fatalf("dsn = %q", got)
	}
	if f.Clusters["compress"].ExpectNodes != 3 {
		t.Fatal("expect_nodes lost")
	}
}

// Expanding a missing variable to an empty string produces "access denied",
// which is a terrible way to learn that an environment variable is unset.
func TestUnsetEnvironmentVariableIsAnError(t *testing.T) {
	path := write(t, `{"clusters":{"c":{"nodes":[{"name":"a","dsn":"u:${GD_NOT_SET_ANYWHERE}@tcp(h:3306)/"}]}}}`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "GD_NOT_SET_ANYWHERE") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidation(t *testing.T) {
	cases := map[string]string{
		"no cluster":     `{"clusters":{}}`,
		"no node":        `{"clusters":{"c":{"nodes":[]}}}`,
		"no name":        `{"clusters":{"c":{"nodes":[{"dsn":"u@tcp(h:3306)/"}]}}}`,
		"no dsn":         `{"clusters":{"c":{"nodes":[{"name":"a"}]}}}`,
		"duplicate name": `{"clusters":{"c":{"nodes":[{"name":"a","dsn":"x"},{"name":"a","dsn":"y"}]}}}`,
		"typo in a key":  `{"clusters":{"c":{"noeds":[{"name":"a","dsn":"x"}]}}}`,
	}
	for what, body := range cases {
		if _, err := Load(write(t, body)); err == nil {
			t.Fatalf("%s must be rejected", what)
		}
	}
}

func TestNamesAreSorted(t *testing.T) {
	path := write(t, `{"clusters":{
	  "sslazio":{"nodes":[{"name":"a","dsn":"x"}]},
	  "compress":{"nodes":[{"name":"b","dsn":"y"}]}}}`)
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := f.Names()
	if got[0] != "compress" || got[1] != "sslazio" {
		t.Fatalf("names = %v, want sorted", got)
	}
}

// GD-14 — backup freshness, declared rather than guessed.
//
// A dump on disk is not a backup that left the building, and this tool cannot
// see either: it speaks read-only SQL to the nodes. What it can do is grade a
// query *the operator writes*, against a table their backups already record
// into — so nothing here invents a schema, and the read-only gate still
// guarantees the query cannot write.
func TestBackupBlockIsParsed(t *testing.T) {
	f := mustLoad(t, `{
	  "clusters": {
	    "compress": {
	      "nodes": [{"name": "sg-01", "dsn": "u:p@tcp(h:3306)/"}],
	      "backup": {
	        "query": "SELECT MAX(finished_at) FROM ops.backups WHERE status = 'ok' AND offsite = 1",
	        "warn_after": "26h",
	        "bad_after": "50h"
	      }
	    }
	  }
	}`)
	c := f.Clusters["compress"]
	if c.Backup == nil {
		t.Fatal("the backup block was dropped")
	}
	if !strings.HasPrefix(c.Backup.Query, "SELECT MAX(finished_at)") {
		t.Fatalf("query = %q", c.Backup.Query)
	}
	if c.Backup.WarnAfter() != 26*time.Hour || c.Backup.BadAfter() != 50*time.Hour {
		t.Fatalf("thresholds = %s / %s", c.Backup.WarnAfter(), c.Backup.BadAfter())
	}
}

// Defaults that suit a daily backup, so the common case needs two lines rather
// than four.
func TestBackupThresholdsHaveDefaults(t *testing.T) {
	f := mustLoad(t, `{"clusters": {"c": {
	  "nodes": [{"name": "n", "dsn": "u:p@tcp(h:3306)/"}],
	  "backup": {"query": "SELECT MAX(t) FROM b"}
	}}}`)
	b := f.Clusters["c"].Backup
	if b.WarnAfter() != 26*time.Hour || b.BadAfter() != 50*time.Hour {
		t.Fatalf("defaults = %s / %s", b.WarnAfter(), b.BadAfter())
	}
}

func TestABackupBlockWithoutAQueryIsRejected(t *testing.T) {
	if _, err := load(t, `{"clusters": {"c": {
	  "nodes": [{"name": "n", "dsn": "u:p@tcp(h:3306)/"}],
	  "backup": {"warn_after": "26h"}
	}}}`); err == nil {
		t.Fatal("a backup block with nothing to run is a configuration error")
	}
}

// A duration nobody can parse must fail at load, not silently become zero —
// which would grade every backup as overdue.
func TestABadBackupDurationIsRejected(t *testing.T) {
	_, err := load(t, `{"clusters": {"c": {
	  "nodes": [{"name": "n", "dsn": "u:p@tcp(h:3306)/"}],
	  "backup": {"query": "SELECT 1", "warn_after": "twenty six hours"}
	}}}`)
	if err == nil {
		t.Fatal("an unparseable duration must be a configuration error")
	}
	if !strings.Contains(err.Error(), "warn_after") {
		t.Fatalf("the error must name the field: %v", err)
	}
}

// Thresholds that are the wrong way round grade nothing correctly and would
// never be noticed.
func TestBackupThresholdsHaveToBeInOrder(t *testing.T) {
	if _, err := load(t, `{"clusters": {"c": {
	  "nodes": [{"name": "n", "dsn": "u:p@tcp(h:3306)/"}],
	  "backup": {"query": "SELECT 1", "warn_after": "50h", "bad_after": "26h"}
	}}}`); err == nil {
		t.Fatal("bad_after must be at least warn_after")
	}
}

// The query is the operator's, so it is checked at load against the same rule
// the query gate enforces at run time: a config cannot smuggle a write past a
// tool whose whole promise is that it does not write.
func TestAWritingBackupQueryIsRejectedAtLoad(t *testing.T) {
	for _, q := range []string{
		"DELETE FROM ops.backups",
		"UPDATE ops.backups SET status = 'ok'",
		"SELECT 1; DROP TABLE ops.backups",
	} {
		if _, err := load(t, `{"clusters": {"c": {
		  "nodes": [{"name": "n", "dsn": "u:p@tcp(h:3306)/"}],
		  "backup": {"query": "`+q+`"}
		}}}`); err == nil {
			t.Fatalf("a backup query that writes must be refused at load: %q", q)
		}
	}
}
