// Package cluster is the data model of one read of a Galera cluster: one
// Snapshot per node, taken as close together as the connections allow.
//
// Everything the audit needs is in a Snapshot, and a Snapshot is plain data —
// no database handle, no clock, no network. That is what makes the checks
// testable against a cluster that never existed, which matters more here than
// usual: the interesting states (two clusters with the same name, a node that
// is Synced but desynced, a system table whose definition drifted) are states
// nobody can conjure on demand against a real server.
package cluster

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Node is one server to read.
type Node struct {
	Name string `json:"name"`
	// DSN is a go-sql-driver DSN: user:pass@tcp(host:3306)/. It is never
	// printed: a report identifies nodes by Name, so a password cannot reach a
	// terminal, a log or a ticket.
	DSN string `json:"dsn"`
}

// Snapshot is one read of one node.
type Snapshot struct {
	Node string    `json:"node"`
	At   time.Time `json:"at"`
	// Clock is the server's own clock, read in the same round trip as
	// everything else. The zero value means it was not read — which is not the
	// same as a server whose clock agrees.
	Clock time.Time `json:"clock,omitempty"`
	// Status and Vars are SHOW GLOBAL STATUS and SHOW GLOBAL VARIABLES, keyed
	// lowercase so a caller never has to guess the server's capitalisation.
	Status map[string]string `json:"status,omitempty"`
	Vars   map[string]string `json:"vars,omitempty"`
	// SysTables maps a table in the `mysql` schema to a fingerprint of its
	// column definitions. Galera does not replicate DDL on the system tables
	// the way it replicates application DDL, so a drift here is invisible to
	// every wsrep_* counter — which is exactly why it is collected.
	SysTables map[string]string `json:"sys_tables,omitempty"`
	// AppTables maps an application table — schema-qualified — to a
	// fingerprint of its column definitions. Galera *does* replicate
	// application DDL, so a difference here is not maintenance that never
	// travelled (that is SysTables) but a schema change that failed, was
	// applied by hand on one node, or landed while a node was desynced.
	//
	// A nil map means the schemas were not read; an empty non-nil map means
	// they were read and there are none. "No application tables" and "no
	// grants on information_schema" are different findings.
	AppTables map[string]string `json:"app_tables,omitempty"`
	// TablesNoPK lists application tables without a primary key. Galera's
	// row-based certification needs one.
	TablesNoPK []string `json:"tables_no_pk,omitempty"`
	// DataBytes is what a full state transfer would have to copy: the data and
	// index length of every application table. A pointer because nil means
	// "not read" — a cluster that holds no data at all is a different
	// statement, and a size of zero would make an SST look free.
	DataBytes *int64 `json:"data_bytes,omitempty"`
	// TablesNonInnoDB lists application tables on a storage engine Galera does
	// not replicate, each rendered "schema.table (ENGINE)". The writes
	// succeed, every counter stays green, and the rows exist on one node.
	TablesNonInnoDB []string `json:"tables_non_innodb,omitempty"`
	// Err is set when the node could not be read. Every cluster-wide statement
	// then rests on fewer nodes than it claims, so the audit reports it as an
	// ERROR rather than skipping the node quietly.
	Err string `json:"error,omitempty"`
}

// OK reports whether the node was read.
func (s Snapshot) OK() bool { return s.Err == "" }

// Get returns a status value.
func (s Snapshot) Get(key string) (string, bool) {
	v, ok := s.Status[strings.ToLower(key)]
	return v, ok
}

// Var returns a global variable.
func (s Snapshot) Var(key string) (string, bool) {
	v, ok := s.Vars[strings.ToLower(key)]
	return v, ok
}

// Float returns a numeric status value. The bool is false for a missing *or*
// unparseable value: a missing counter is not a zero, and treating it as one
// turns "this build has no such metric" into "this metric is fine".
func (s Snapshot) Float(key string) (float64, bool) {
	v, ok := s.Get(key)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// Bool reads MySQL's ON/OFF/1/0 spelling of a boolean.
func (s Snapshot) Bool(key string) (bool, bool) {
	v, ok := s.Get(key)
	if !ok {
		if v, ok = s.Var(key); !ok {
			return false, false
		}
	}
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "ON", "1", "YES", "TRUE":
		return true, true
	case "OFF", "0", "NO", "FALSE":
		return false, true
	}
	return false, false
}

// ProviderOption reads one key out of wsrep_provider_options, which the server
// exposes as one long "key = value; key = value" string. gcache.size lives in
// there and nowhere else.
func (s Snapshot) ProviderOption(name string) (string, bool) {
	blob, ok := s.Var("wsrep_provider_options")
	if !ok {
		return "", false
	}
	for _, part := range strings.Split(blob, ";") {
		k, v, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(k), name) {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

// ReplLatency parses wsrep_evs_repl_latency, which is the cluster's own
// measurement of the round trip between this node and the group. Galera prints
// it as min/avg/max/stddev/samples in seconds.
//
// The bool is false for a missing value, an unparseable one, *and* for zero
// samples: a provider that has not measured anything yet reports 0/0/0/0/0,
// and reading that as "no latency" would make an unmeasured cluster look like
// the fastest one in the fleet.
func (s Snapshot) ReplLatency() (avg, max time.Duration, ok bool) {
	v, found := s.Get("wsrep_evs_repl_latency")
	if !found {
		return 0, 0, false
	}
	parts := strings.Split(strings.TrimSpace(v), "/")
	if len(parts) < 5 {
		return 0, 0, false
	}
	samples, err := strconv.ParseFloat(strings.TrimSpace(parts[4]), 64)
	if err != nil || samples <= 0 {
		return 0, 0, false
	}
	seconds := func(i int) (time.Duration, bool) {
		f, err := strconv.ParseFloat(strings.TrimSpace(parts[i]), 64)
		if err != nil || f < 0 {
			return 0, false
		}
		return time.Duration(f * float64(time.Second)), true
	}
	a, ok1 := seconds(1)
	m, ok2 := seconds(2)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return a, m, true
}

// Segment is the node's gmcast.segment, or "" when the provider does not report
// one. Nodes in the same segment are meant to be near each other; that is the
// whole content of the setting.
func (s Snapshot) Segment() string {
	v, ok := s.ProviderOption("gmcast.segment")
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// Bytes parses a size written the way Galera writes it: a plain number of
// bytes, or a number with a K/M/G/T suffix.
func Bytes(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	mult := int64(1)
	switch last := s[len(s)-1]; last {
	case 'k', 'K':
		mult, s = 1<<10, s[:len(s)-1]
	case 'm', 'M':
		mult, s = 1<<20, s[:len(s)-1]
	case 'g', 'G':
		mult, s = 1<<30, s[:len(s)-1]
	case 't', 'T':
		mult, s = 1<<40, s[:len(s)-1]
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, false
	}
	return int64(f * float64(mult)), true
}

// Fingerprint is the stable hash of a table's column definitions. It is a hash
// rather than the definitions themselves so that a report can say *which*
// tables differ without printing a schema, and so two nodes can be compared
// with one string equality.
func Fingerprint(rows []string) string {
	sort.Strings(rows)
	h := sha256.New()
	for _, r := range rows {
		h.Write([]byte(r))
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// ColumnRow is one column definition, rendered into the canonical form that
// goes into a fingerprint.
func ColumnRow(column string, ordinal int, colType, nullable, key, def string) string {
	return fmt.Sprintf("%s|%d|%s|%s|%s|%s", column, ordinal, colType, nullable, key, def)
}

// Addresses is every name this node might be known by from the outside: the
// address it advertises to the group, the name it calls itself, and the name it
// was configured under here. A proxy's server list is written by whoever set
// the proxy up, and matching on one spelling only is how a node ends up
// reported as "missing from the proxy" when it is right there under its IP.
func (s Snapshot) Addresses() []string {
	out := []string{s.Node}
	for _, key := range []string{"wsrep_node_address", "wsrep_node_name"} {
		if v, ok := s.Var(key); ok && v != "" {
			out = append(out, hostOnly(v))
		}
		if v, ok := s.Get(key); ok && v != "" {
			out = append(out, hostOnly(v))
		}
	}
	sort.Strings(out)
	return dedupe(out)
}

// HostOnly drops a port and a CIDR suffix, both of which appear in
// wsrep_node_address — and in wsrep_cluster_address, where a peer is written
// however the person editing the config felt like writing it.
func HostOnly(v string) string { return hostOnly(v) }

// hostOnly drops a port and a CIDR suffix, both of which appear in
// wsrep_node_address in the wild.
func hostOnly(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.Index(v, "/"); i > 0 {
		v = v[:i]
	}
	if h, _, err := net.SplitHostPort(v); err == nil {
		return h
	}
	return v
}

func dedupe(in []string) []string {
	out := in[:0]
	var last string
	for i, v := range in {
		if v == "" || (i > 0 && v == last) {
			continue
		}
		out = append(out, v)
		last = v
	}
	return out
}

// Names is the node names of a snapshot set, in the order given.
func Names(snaps []Snapshot) []string {
	out := make([]string, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, s.Node)
	}
	return out
}
