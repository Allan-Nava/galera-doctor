package cluster

// GD-76 — the pure helpers nobody tested.
//
// Every function here is arithmetic or string handling with no server behind
// it, and every one of them was at 0% coverage: reachable in production, and
// in the test suite only through the integration test behind GD_TEST_DSN,
// which does not run on a laptop and cannot plant the inputs that matter
// anyway. HostOnly is the one that stings — it is the documented ProxySQL
// matching trap, and a node reported as missing from a proxy it is plainly in
// is the bug this repository has a whole rule about.

import (
	"testing"
	"time"
)

func TestHostOnlyDropsPortAndCIDR(t *testing.T) {
	// The four spellings wsrep_node_address and wsrep_cluster_address are
	// written in, in the wild. A proxy's server list is written by a human, so
	// all four have to reduce to the same thing.
	for _, tc := range []struct{ in, want string }{
		{"10.11.1.5", "10.11.1.5"},
		{"10.11.1.5:4567", "10.11.1.5"},
		{"10.11.1.5/24", "10.11.1.5"},
		{"10.11.1.5/24:4567", "10.11.1.5"},
		{"  10.11.1.5:4567  ", "10.11.1.5"},
		{"db-01.internal:3306", "db-01.internal"},
		{"db-01.internal", "db-01.internal"},
		{"", ""},
	} {
		if got := HostOnly(tc.in); got != tc.want {
			t.Errorf("HostOnly(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHostOnlyKeepsAnIPv6AddressIntact(t *testing.T) {
	// net.SplitHostPort is what strips the port, and it is also what keeps a
	// bracketed IPv6 address from being cut at its first colon. A plain IPv6
	// address has no port to remove and must survive whole — truncating it to
	// "2001" would match every node in the same prefix.
	if got := HostOnly("[2001:db8::5]:4567"); got != "2001:db8::5" {
		t.Errorf("a bracketed IPv6 address with a port: got %q", got)
	}
	if got := HostOnly("2001:db8::5"); got != "2001:db8::5" {
		t.Errorf("a bare IPv6 address must survive whole: got %q", got)
	}
}

func TestReplLatencyRefusesAnUnmeasuredProvider(t *testing.T) {
	// 0/0/0/0/0 is what a provider reports before it has measured anything.
	// Read as a latency it is zero, which would make the least-known cluster
	// in the fleet look like the fastest.
	s := Snapshot{Status: map[string]string{"wsrep_evs_repl_latency": "0/0/0/0/0"}}
	if _, _, ok := s.ReplLatency(); ok {
		t.Fatal("zero samples is no measurement, not a measurement of zero")
	}
}

func TestReplLatencyParsesTheProvidersFiveFields(t *testing.T) {
	// min/avg/max/stddev/samples, in seconds.
	s := Snapshot{Status: map[string]string{
		"wsrep_evs_repl_latency": "0.000313/0.001255/0.004024/0.000682/42",
	}}
	avg, max, ok := s.ReplLatency()
	if !ok {
		t.Fatal("a measured provider has a latency")
	}
	if avg != time.Duration(0.001255*float64(time.Second)) {
		t.Errorf("avg = %v, want the second field", avg)
	}
	if max != time.Duration(0.004024*float64(time.Second)) {
		t.Errorf("max = %v, want the third field", max)
	}
}

func TestReplLatencyIsNotGradedWhenItCannotBeRead(t *testing.T) {
	for name, status := range map[string]map[string]string{
		"missing":     {},
		"empty":       {"wsrep_evs_repl_latency": ""},
		"too few":     {"wsrep_evs_repl_latency": "0.1/0.2/0.3"},
		"not numbers": {"wsrep_evs_repl_latency": "a/b/c/d/e"},
		"negative":    {"wsrep_evs_repl_latency": "0/-1/0.3/0/9"},
	} {
		s := Snapshot{Status: status}
		if _, _, ok := s.ReplLatency(); ok {
			t.Errorf("%s: reported a latency it could not read", name)
		}
	}
}

func TestSegmentIsEmptyWhenTheProviderDoesNotReportOne(t *testing.T) {
	if got := (Snapshot{}).Segment(); got != "" {
		t.Errorf("no provider options means no segment, got %q", got)
	}
	s := Snapshot{Vars: map[string]string{
		"wsrep_provider_options": "gcache.size = 512M; gmcast.segment = 2; gcs.fc_limit = 16",
	}}
	if got := s.Segment(); got != "2" {
		t.Errorf("Segment() = %q, want 2", got)
	}
}

func TestColumnRowIsStableAndSeparated(t *testing.T) {
	// The canonical form that goes into a fingerprint. Two different columns
	// must not render to the same row, or drift between them is invisible.
	a := ColumnRow("Host", 1, "char(60)", "NO", "PRI", "")
	b := ColumnRow("Host", 1, "char(60)", "NO", "PRI", "")
	if a != b {
		t.Fatal("the same column has to render the same way twice")
	}
	if c := ColumnRow("Host", 2, "char(60)", "NO", "PRI", ""); c == a {
		t.Fatal("the ordinal is part of the definition")
	}
	if c := ColumnRow("Host", 1, "char(60)", "YES", "PRI", ""); c == a {
		t.Fatal("nullability is part of the definition")
	}
}

func TestNamesKeepsTheOrderGiven(t *testing.T) {
	got := Names([]Snapshot{{Node: "cl-03"}, {Node: "cl-01"}, {Node: "cl-02"}})
	want := []string{"cl-03", "cl-01", "cl-02"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v — the order is the caller's, not sorted", got, want)
		}
	}
	if n := Names(nil); len(n) != 0 {
		t.Fatalf("no snapshots is no names, got %v", n)
	}
}

func TestOKIsWhetherTheNodeWasRead(t *testing.T) {
	if !(Snapshot{Node: "cl-01"}).OK() {
		t.Error("a snapshot with no error was read")
	}
	if (Snapshot{Node: "cl-01", Err: "dial tcp: timeout"}).OK() {
		t.Error("a snapshot carrying an error was not read")
	}
}

func TestAReplicaLinkRunsOnlyWhenBothThreadsDo(t *testing.T) {
	// One thread up is the interesting case: the SQL thread stopped on an
	// error while the IO thread keeps pulling is a link that looks alive in a
	// connection count and is not replicating.
	for _, tc := range []struct {
		io, sql, want bool
	}{
		{true, true, true},
		{true, false, false},
		{false, true, false},
		{false, false, false},
	} {
		l := ReplicaLink{IORunning: tc.io, SQLRunning: tc.sql}
		if l.Running() != tc.want {
			t.Errorf("IO=%v SQL=%v: Running() = %v, want %v", tc.io, tc.sql, l.Running(), tc.want)
		}
	}
}
