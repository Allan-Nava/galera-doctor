package cluster

import (
	"context"
	"strings"
	"testing"
)

func snap() Snapshot {
	return Snapshot{
		Node: "sg-01",
		Status: map[string]string{
			"wsrep_cluster_size":        "3",
			"wsrep_local_state_comment": "Synced",
			"wsrep_ready":               "ON",
			"threads_connected":         "not-a-number",
		},
		Vars: map[string]string{
			"read_only":              "OFF",
			"wsrep_node_address":     "10.11.1.5",
			"wsrep_provider_options": "base_dir = /var/lib/mysql/; gcache.size = 512M; gcs.fc_limit = 16;",
		},
	}
}

func TestFloatRefusesToInventAZero(t *testing.T) {
	s := snap()
	if _, ok := s.Float("wsrep_cluster_size"); !ok {
		t.Fatal("a numeric value must parse")
	}
	if v, ok := s.Float("threads_connected"); ok {
		t.Fatalf("an unparseable value must not become %v", v)
	}
	if _, ok := s.Float("wsrep_absent_counter"); ok {
		t.Fatal("a missing counter is not a zero — a build without the metric is not a healthy one")
	}
}

func TestBoolReadsBothSpellingsAndBothMaps(t *testing.T) {
	s := snap()
	if v, ok := s.Bool("wsrep_ready"); !ok || !v {
		t.Fatal("ON must read as true from the status map")
	}
	if v, ok := s.Bool("read_only"); !ok || v {
		t.Fatal("OFF must read as false from the variables map")
	}
	if _, ok := s.Bool("wsrep_cluster_size"); ok {
		t.Fatal("a number that is not a boolean must report not-a-boolean")
	}
}

func TestProviderOption(t *testing.T) {
	s := snap()
	v, ok := s.ProviderOption("gcache.size")
	if !ok || v != "512M" {
		t.Fatalf("got %q, %v", v, ok)
	}
	if _, ok := s.ProviderOption("gcache.page_size"); ok {
		t.Fatal("an absent option must not be found")
	}
}

func TestBytesSuffixes(t *testing.T) {
	cases := map[string]int64{"512M": 512 << 20, "1G": 1 << 30, "128": 128, "2k": 2 << 10}
	for in, want := range cases {
		got, ok := Bytes(in)
		if !ok || got != want {
			t.Fatalf("Bytes(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	if _, ok := Bytes("plenty"); ok {
		t.Fatal("a non-size must not parse")
	}
}

// A fingerprint has to be stable whatever order the server returned the rows
// in, or every run reports drift.
func TestFingerprintIsOrderIndependent(t *testing.T) {
	a := Fingerprint([]string{"id|1|int|NO|PRI|~none~", "name|2|varchar(64)|YES||~none~"})
	b := Fingerprint([]string{"name|2|varchar(64)|YES||~none~", "id|1|int|NO|PRI|~none~"})
	if a != b {
		t.Fatal("the same columns in a different order are the same definition")
	}
	c := Fingerprint([]string{"id|1|bigint|NO|PRI|~none~", "name|2|varchar(64)|YES||~none~"})
	if a == c {
		t.Fatal("a changed column type must change the fingerprint")
	}
}

// The proxy's server list is written by a human: match on every spelling the
// node answers to, or a node that is right there gets reported as missing.
func TestAddressesCoversEverySpelling(t *testing.T) {
	s := snap()
	got := strings.Join(s.Addresses(), ",")
	for _, want := range []string{"sg-01", "10.11.1.5"} {
		if !strings.Contains(got, want) {
			t.Fatalf("addresses %q must contain %q", got, want)
		}
	}
	s.Vars["wsrep_node_address"] = "10.11.1.5:4567"
	if !strings.Contains(strings.Join(s.Addresses(), ","), "10.11.1.5") {
		t.Fatal("a port must be dropped from the address")
	}
}

// The promise that this tool cannot change anything is mechanical.
func TestQueryRefusesEverythingButShowAndSelect(t *testing.T) {
	for _, q := range []string{
		"UPDATE mysql.user SET x = 1",
		"DELETE FROM t",
		"SET GLOBAL wsrep_desync = ON",
		"FLUSH STATUS",
		"DROP TABLE t",
		"  \n INSERT INTO t VALUES (1)",
	} {
		if _, err := Query(context.Background(), nil, q); err == nil {
			t.Fatalf("Query must refuse %q", q)
		} else if !strings.Contains(err.Error(), "only issues SHOW and SELECT") {
			t.Fatalf("unexpected error for %q: %v", q, err)
		}
	}
	for _, q := range []string{"SHOW GLOBAL STATUS", "  select 1", "\n\tSELECT * FROM information_schema.COLUMNS"} {
		if !readOnly.MatchString(q) {
			t.Fatalf("Query must accept %q", q)
		}
	}
}

// An error message ends up in a ticket. A DSN must not.
func TestRedactKeepsThePasswordOut(t *testing.T) {
	dsn := "audit:sup3rs3cret@tcp(10.11.1.5:3306)/"
	msg := redact(`dial error for dsn "audit:sup3rs3cret@tcp(10.11.1.5:3306)/": timeout`, dsn)
	if strings.Contains(msg, "sup3rs3cret") {
		t.Fatalf("the password survived redaction: %q", msg)
	}
}
