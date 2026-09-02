package proxysql

import (
	"strings"
	"testing"

	"github.com/Allan-Nava/galera-doctor/internal/cluster"
	"github.com/Allan-Nava/galera-doctor/internal/finding"
)

func node(name, addr, stateComment string) cluster.Snapshot {
	return cluster.Snapshot{
		Node:   name,
		Status: map[string]string{"wsrep_local_state_comment": stateComment},
		Vars:   map[string]string{"wsrep_node_address": addr},
	}
}

func snapshot(servers ...Server) Snapshot {
	return Snapshot{
		Servers:    servers,
		Hostgroups: []HostgroupSet{{Writer: 10, BackupWriter: 20, Reader: 30, Offline: 999, Active: 1}},
	}
}

func has(fs []finding.Finding, check string) *finding.Finding {
	for i := range fs {
		if fs[i].Check == check {
			return &fs[i]
		}
	}
	return nil
}

// The rule that keeps this check usable: ProxySQL's Galera monitor owns the
// offline hostgroup. Grading its contents is a permanent false positive, and
// "cleaning it up" fights the monitor.
func TestOfflineHostgroupIsNeverAFinding(t *testing.T) {
	snap := snapshot(
		Server{Hostgroup: 10, Hostname: "10.11.1.5", Port: 3306, Status: "ONLINE"},
		Server{Hostgroup: 999, Hostname: "10.11.1.6", Port: 3306, Status: "ONLINE"},
	)
	fs := Audit(snap, []cluster.Snapshot{node("sg-01", "10.11.1.5", "Synced")})
	for _, f := range fs {
		if strings.Contains(f.Message, "999") && f.Status != finding.OK {
			t.Fatalf("the offline hostgroup produced a verdict: %+v", f)
		}
	}
	if m := has(fs, "proxysql/mapping"); m == nil || !strings.Contains(m.Hint, "monitor") {
		t.Fatalf("the mapping must be reported as monitor-managed: %+v", m)
	}
}

// A node in the offline hostgroup only is a node no traffic reaches.
func TestNodeMissingFromEveryServingHostgroup(t *testing.T) {
	snap := snapshot(Server{Hostgroup: 999, Hostname: "10.11.1.9", Status: "ONLINE"})
	fs := Audit(snap, []cluster.Snapshot{node("ov-03", "10.11.1.9", "Synced")})
	f := has(fs, "proxysql/missing")
	if f == nil || f.Status != finding.BAD {
		t.Fatalf("got %+v", f)
	}
}

func TestOnlineButNotSyncedIsBad(t *testing.T) {
	snap := snapshot(Server{Hostgroup: 10, Hostname: "10.11.1.5", Status: "ONLINE"})
	fs := Audit(snap, []cluster.Snapshot{node("sg-01", "10.11.1.5", "Joined")})
	f := has(fs, "proxysql/disagreement")
	if f == nil || f.Status != finding.BAD || !strings.Contains(f.Message, "Joined") {
		t.Fatalf("got %+v", f)
	}
}

func TestShunnedButSyncedIsAWarning(t *testing.T) {
	snap := snapshot(Server{Hostgroup: 10, Hostname: "10.11.1.5", Status: "SHUNNED"})
	fs := Audit(snap, []cluster.Snapshot{node("sg-01", "10.11.1.5", "Synced")})
	f := has(fs, "proxysql/disagreement")
	if f == nil || f.Status != finding.WARN {
		t.Fatalf("got %+v", f)
	}
}

func TestAHostgroupWithNothingOnlineIsAnOutage(t *testing.T) {
	snap := snapshot(
		Server{Hostgroup: 10, Hostname: "10.11.1.5", Status: "SHUNNED"},
		Server{Hostgroup: 10, Hostname: "10.11.1.6", Status: "OFFLINE_HARD"},
	)
	fs := Audit(snap, []cluster.Snapshot{node("sg-01", "10.11.1.5", "Synced"), node("cl-02", "10.11.1.6", "Synced")})
	f := has(fs, "proxysql/hostgroup")
	if f == nil || f.Status != finding.BAD {
		t.Fatalf("got %+v", f)
	}
}

func TestMatchingUsesEverySpellingOfTheNode(t *testing.T) {
	// The proxy lists the node by address; the cluster calls it sg-01.
	snap := snapshot(Server{Hostgroup: 10, Hostname: "10.11.1.5", Status: "ONLINE"})
	fs := Audit(snap, []cluster.Snapshot{node("sg-01", "10.11.1.5", "Synced")})
	if f := has(fs, "proxysql/missing"); f != nil {
		t.Fatalf("the node was found under its address and must not be reported missing: %+v", f)
	}
}

func TestAnUnreadableProxyIsAnErrorNotSilence(t *testing.T) {
	fs := Audit(Snapshot{Err: "admin interface not reachable"}, nil)
	f := has(fs, "proxysql/read")
	if f == nil || f.Status != finding.ERROR {
		t.Fatalf("got %+v", f)
	}
	if !strings.Contains(f.Hint, "cluster findings still stand") {
		t.Fatalf("the hint must scope what was lost: %q", f.Hint)
	}
}
