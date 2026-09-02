// Package audit turns a set of node snapshots into findings.
//
// Two rules shape every check in here.
//
// A cluster statement is only as good as the nodes it was made from. A node
// that could not be read is an ERROR, not a skip, because "the cluster has 3
// members" said while one of five nodes was unreachable is a sentence with a
// hole in it.
//
// A total is not a rate. The wsrep counters only go up and reset on restart, so
// they are graded against the previous run or not graded at all — a threshold
// over a lifetime total goes red once and stays red, and a check that stays red
// is a check people stop reading.
package audit

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Allan-Nava/galera-doctor/internal/cluster"
	"github.com/Allan-Nava/galera-doctor/internal/finding"
	"github.com/Allan-Nava/galera-doctor/internal/state"
)

// Options are the thresholds an audit grades against.
type Options struct {
	Cluster string
	// ExpectNodes is the membership the cluster should report. 0 means "as many
	// nodes as were configured", which is the right default and wrong exactly
	// when someone probes a subset on purpose.
	ExpectNodes int
	// FlowWarn and FlowBad are the share of the interval spent flow-controlled.
	FlowWarn float64
	FlowBad  float64
	// RecvQueueWarn is the instantaneous received-queue depth above which a node
	// is falling behind.
	RecvQueueWarn float64
	// ISTWarn is the shortest gcache window worth having: below it, a node that
	// restarts is likely to need a full SST instead of an incremental transfer.
	ISTWarn time.Duration
	Now     time.Time
}

// DefaultOptions are the thresholds used when a caller passes none.
func DefaultOptions() Options {
	return Options{
		FlowWarn:      0.01,
		FlowBad:       0.10,
		RecvQueueWarn: 10,
		ISTWarn:       30 * time.Minute,
		Now:           time.Now(),
	}
}

// Report is one audit.
type Report struct {
	Cluster  string            `json:"cluster"`
	At       time.Time         `json:"at"`
	Nodes    []string          `json:"nodes"`
	Findings []finding.Finding `json:"findings"`
	// State is what to persist for the next run's rate calculations.
	State state.State `json:"-"`
}

// Worst is the highest severity in the report.
func (r Report) Worst() finding.Status { return finding.Worst(r.Findings) }

// Run audits a set of snapshots. prev may be nil: the counter checks then say
// they have no baseline instead of inventing one.
func Run(snaps []cluster.Snapshot, prev *state.State, opt Options) Report {
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	rep := Report{
		Cluster: opt.Cluster,
		At:      opt.Now,
		Nodes:   cluster.Names(snaps),
		State:   state.New(snaps, opt.Now),
	}
	if rep.Cluster == "" {
		rep.Cluster = "cluster"
	}

	add := func(fs ...finding.Finding) { rep.Findings = append(rep.Findings, fs...) }

	add(nodesRead(snaps)...)
	live := readable(snaps)
	if len(live) == 0 {
		add(finding.Finding{
			Check: "cluster/membership", Target: rep.Cluster, Status: finding.ERROR,
			Message: "no node could be read",
			Hint:    "nothing below this line was audited — fix access first",
		})
		finding.SortWorstFirst(rep.Findings)
		return rep
	}

	// A server without the wsrep provider is not a cluster member having a bad
	// day: it is a standalone server that somebody pointed this tool at. Every
	// cluster check would fire at once (size 0, status Disconnected, not ready,
	// wsrep off) and describe a catastrophe that is not happening — so it gets
	// one finding, and it is excluded from every comparison below.
	var foreign []cluster.Snapshot
	live, foreign = splitGalera(live)
	add(notGalera(foreign)...)
	if len(live) == 0 {
		add(finding.Finding{
			Check: "cluster/membership", Target: rep.Cluster, Status: finding.ERROR,
			Message: "no node in this list is running Galera",
			Hint:    "nothing was audited as a cluster — check the DSNs, or use a tool for standalone servers",
		})
		finding.SortWorstFirst(rep.Findings)
		return rep
	}

	add(clusterIdentity(rep.Cluster, live)...)
	add(membership(rep.Cluster, live, len(snaps), opt)...)
	add(nodeState(live)...)
	add(flowControl(live, prev, rep.State, opt)...)
	add(certification(live, prev, rep.State)...)
	add(queues(live, opt)...)
	add(sysTableDrift(rep.Cluster, live)...)
	add(schemaDrift(rep.Cluster, live)...)
	add(sstReadiness(rep.Cluster, live)...)
	add(quorumSettings(rep.Cluster, live)...)
	add(syncWait(rep.Cluster, live)...)
	add(autoIncrement(rep.Cluster, live)...)
	add(gcacheRecover(live)...)
	add(osuMethod(live)...)
	add(storageEngines(live)...)
	add(primaryKeys(live)...)
	add(versions(rep.Cluster, live)...)
	add(gcache(live, prev, rep.State, opt)...)

	finding.SortWorstFirst(rep.Findings)
	return rep
}

// splitGalera separates cluster members from standalone servers.
//
// wsrep_provider is the reliable signal: it is a path to the library and reads
// "none" on a server with replication compiled in but not configured.
// wsrep_on, by contrast, is OFF on a real cluster member that somebody has
// temporarily taken out of replication — a serious finding that must not be
// mistaken for "this is not a cluster".
func splitGalera(live []cluster.Snapshot) (galera, foreign []cluster.Snapshot) {
	for _, s := range live {
		provider, ok := s.Var("wsrep_provider")
		switch {
		case ok && provider != "" && !strings.EqualFold(provider, "none"):
			galera = append(galera, s)
		case ok:
			foreign = append(foreign, s)
		default:
			// No such variable at all: a MySQL build without wsrep.
			foreign = append(foreign, s)
		}
	}
	return galera, foreign
}

// notGalera reports a standalone server, once.
func notGalera(foreign []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	for _, s := range foreign {
		version, _ := s.Var("version")
		msg := "not a Galera node: wsrep_provider is not configured"
		if version != "" {
			msg += " (server " + version + ")"
		}
		out = append(out, finding.Finding{
			Check: "node/not-galera", Target: s.Node, Status: finding.ERROR,
			Message: msg,
			Hint:    "excluded from every cluster comparison — grading it would report an outage that is not happening",
		})
	}
	return out
}

func readable(snaps []cluster.Snapshot) []cluster.Snapshot {
	var out []cluster.Snapshot
	for _, s := range snaps {
		if s.OK() {
			out = append(out, s)
		}
	}
	return out
}

// nodesRead reports the nodes that were not read. It comes first because every
// other cluster-wide statement is conditional on it.
func nodesRead(snaps []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	for _, s := range snaps {
		if s.OK() {
			continue
		}
		out = append(out, finding.Finding{
			Check: "node/read", Target: s.Node, Status: finding.ERROR,
			Message: "not read: " + s.Err,
			Hint:    "every cluster-wide finding below was made without this node",
		})
	}
	return out
}

// clusterIdentity is the split-brain check: two nodes that report different
// cluster state UUIDs are two clusters wearing the same name.
func clusterIdentity(name string, live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding

	uuids := groupBy(live, func(s cluster.Snapshot) string {
		v, _ := s.Get("wsrep_cluster_state_uuid")
		return v
	})
	if len(uuids) > 1 {
		out = append(out, finding.Finding{
			Check: "cluster/uuid", Target: name, Status: finding.BAD,
			Message: "nodes report different cluster state UUIDs: " + describeGroups(uuids),
			Value:   finding.Num(float64(len(uuids))), Unit: "clusters",
			Hint: "this is a partition, not a lag: the groups have diverged and one side has to be reinitialised from the other",
		})
	}

	confs := groupBy(live, func(s cluster.Snapshot) string {
		v, _ := s.Get("wsrep_cluster_conf_id")
		return v
	})
	if len(uuids) == 1 && len(confs) > 1 {
		out = append(out, finding.Finding{
			Check: "cluster/conf-id", Target: name, Status: finding.WARN,
			Message: "nodes report different membership configuration ids: " + describeGroups(confs),
			Hint:    "usually a membership change caught mid-flight — re-run; if it persists, a node is not receiving membership updates",
		})
	}

	for _, s := range live {
		status, ok := s.Get("wsrep_cluster_status")
		if !ok {
			continue
		}
		if !strings.EqualFold(status, "Primary") {
			out = append(out, finding.Finding{
				Check: "cluster/primary", Target: s.Node, Status: finding.BAD,
				Message: "cluster status is " + status + ", not Primary",
				Hint:    "a non-Primary node refuses writes and reads stale data: it has lost quorum or was partitioned off",
			})
		}
	}
	return out
}

// membership compares what each node believes about the size of the cluster
// with what was asked of it.
func membership(name string, live []cluster.Snapshot, configured int, opt Options) []finding.Finding {
	var out []finding.Finding
	expect := opt.ExpectNodes
	if expect == 0 {
		expect = configured
	}

	sizes := groupBy(live, func(s cluster.Snapshot) string {
		v, _ := s.Get("wsrep_cluster_size")
		return v
	})
	if len(sizes) > 1 {
		out = append(out, finding.Finding{
			Check: "cluster/size", Target: name, Status: finding.BAD,
			Message: "nodes disagree about the cluster size: " + describeGroups(sizes),
			Hint:    "the nodes are not seeing the same membership — check connectivity between the groups above",
		})
		return out
	}
	for _, s := range live {
		size, ok := s.Float("wsrep_cluster_size")
		if !ok {
			continue
		}
		st := finding.OK
		hint := ""
		if int(size) != expect {
			st = finding.BAD
			hint = fmt.Sprintf("expected %d members; a member that left is not replicating and, below quorum, the rest stop accepting writes", expect)
		}
		out = append(out, finding.Finding{
			Check: "cluster/size", Target: name, Status: st,
			Message: fmt.Sprintf("%d member(s), expected %d", int(size), expect),
			Value:   finding.Num(size), Unit: "nodes", Hint: hint,
		})
		break // every node agreed; one statement is enough
	}
	return out
}

// nodeState grades what each node says about itself.
func nodeState(live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	for _, s := range live {
		if ready, ok := s.Bool("wsrep_ready"); ok && !ready {
			out = append(out, finding.Finding{
				Check: "node/ready", Target: s.Node, Status: finding.BAD,
				Message: "wsrep_ready is OFF",
				Hint:    "the node is refusing queries that touch replicated tables",
			})
		}
		if conn, ok := s.Bool("wsrep_connected"); ok && !conn {
			out = append(out, finding.Finding{
				Check: "node/connected", Target: s.Node, Status: finding.BAD,
				Message: "wsrep_connected is OFF",
				Hint:    "the node is not part of the group communication at all",
			})
		}
		if on, ok := s.Bool("wsrep_on"); ok && !on {
			out = append(out, finding.Finding{
				Check: "node/wsrep-on", Target: s.Node, Status: finding.BAD,
				Message: "wsrep_on is OFF: writes on this node are not replicated",
				Hint:    "anything written here stays here, and will conflict when the setting is put back",
			})
		}
		if desync, ok := s.Bool("wsrep_desync"); ok && desync {
			out = append(out, finding.Finding{
				Check: "node/desync", Target: s.Node, Status: finding.WARN,
				Message: "wsrep_desync is ON",
				Hint:    "deliberate during a backup or a schema change; left on, the node drifts without flow-controlling the cluster",
			})
		}
		if comment, ok := s.Get("wsrep_local_state_comment"); ok {
			st, hint := stateComment(comment)
			out = append(out, finding.Finding{
				Check: "node/state", Target: s.Node, Status: st,
				Message: "local state is " + comment, Hint: hint,
			})
		}
		if ro, ok := s.Bool("read_only"); ok && ro {
			out = append(out, finding.Finding{
				Check: "node/read-only", Target: s.Node, Status: finding.WARN,
				Message: "read_only is ON",
				Hint:    "expected on a node kept out of the write path; unexpected, it is a failover that never finished",
			})
		}
	}
	return out
}

func stateComment(comment string) (finding.Status, string) {
	switch strings.ToLower(strings.TrimSpace(comment)) {
	case "synced":
		return finding.OK, ""
	case "donor/desynced", "donor":
		return finding.WARN, "the node is feeding a state transfer: expected while another node joins, a problem if it never ends"
	case "joined", "joiner":
		return finding.WARN, "the node is catching up and is not yet serving replicated reads consistently"
	case "initialized", "initializing":
		return finding.BAD, "the node is not part of the cluster: it has not completed its state transfer"
	default:
		return finding.BAD, "unexpected local state — the node is neither synced nor knowingly transferring"
	}
}

// flowControl grades the share of the interval the node spent flow-controlled,
// which is the honest form of a counter that only ever goes up.
func flowControl(live []cluster.Snapshot, prev *state.State, now state.State, opt Options) []finding.Finding {
	var out []finding.Finding
	for _, s := range live {
		ns := now.Nodes[s.Node]
		d, ok := prev.Since(s.Node, "wsrep_flow_control_paused_ns", ns)
		if !ok {
			// No baseline: report the lifetime figure as context and refuse to
			// grade it. A total that covers an incident from March would
			// otherwise be red forever.
			if v, has := s.Float("wsrep_flow_control_paused"); has {
				out = append(out, finding.Finding{
					Check: "flow/paused", Target: s.Node, Status: finding.OK,
					Message: fmt.Sprintf("%.1f%% of the time since the last status reset (not graded: no baseline)", v*100),
					Value:   finding.Num(v), Unit: "fraction",
					Hint: "run again with --state to grade the interval between runs instead of the lifetime total",
				})
			}
			continue
		}
		frac := d.Fraction()
		st := finding.OK
		hint := ""
		switch {
		case frac >= opt.FlowBad:
			st, hint = finding.BAD, "the cluster is being held back by this node: writers everywhere are waiting on it"
		case frac >= opt.FlowWarn:
			st, hint = finding.WARN, "this node is intermittently the slowest in the cluster; look at its disk and its replication threads"
		}
		out = append(out, finding.Finding{
			Check: "flow/paused", Target: s.Node, Status: st,
			Message: fmt.Sprintf("flow-controlled %.2f%% of the last %s", frac*100, d.Elapsed.Round(time.Second)),
			Value:   finding.Num(frac), Unit: "fraction", Hint: hint,
		})
	}
	return out
}

// certification grades conflicts, again over the interval.
func certification(live []cluster.Snapshot, prev *state.State, now state.State) []finding.Finding {
	var out []finding.Finding
	for _, s := range live {
		ns := now.Nodes[s.Node]
		fails, ok1 := prev.Since(s.Node, "wsrep_local_cert_failures", ns)
		repl, ok2 := prev.Since(s.Node, "wsrep_replicated", ns)
		if !ok1 || !ok2 {
			continue
		}
		if repl.Value == 0 {
			continue
		}
		ratio := fails.Value / repl.Value
		st := finding.OK
		hint := ""
		switch {
		case ratio >= 0.05:
			st, hint = finding.BAD, "transactions are being rolled back after committing locally: the same rows are being written on more than one node"
		case ratio >= 0.005:
			st, hint = finding.WARN, "some write conflicts across nodes — usually a workload that should be pinned to one writer"
		}
		out = append(out, finding.Finding{
			Check: "repl/cert-failures", Target: s.Node, Status: st,
			Message: fmt.Sprintf("%.0f certification failure(s) out of %.0f writesets in the last %s (%.3f%%)",
				fails.Value, repl.Value, fails.Elapsed.Round(time.Second), ratio*100),
			Value: finding.Num(ratio), Unit: "fraction", Hint: hint,
		})
	}
	return out
}

// queues reads the instantaneous depths, which are gauges and can be graded
// as they are.
func queues(live []cluster.Snapshot, opt Options) []finding.Finding {
	var out []finding.Finding
	for _, s := range live {
		if q, ok := s.Float("wsrep_local_recv_queue"); ok && q >= opt.RecvQueueWarn {
			out = append(out, finding.Finding{
				Check: "queue/recv", Target: s.Node, Status: finding.WARN,
				Message: fmt.Sprintf("%.0f writesets waiting to be applied", q),
				Value:   finding.Num(q), Unit: "writesets",
				Hint: "this node is applying more slowly than the cluster writes; it is the next one to flow-control",
			})
		}
		if q, ok := s.Float("wsrep_local_send_queue"); ok && q >= opt.RecvQueueWarn {
			out = append(out, finding.Finding{
				Check: "queue/send", Target: s.Node, Status: finding.WARN,
				Message: fmt.Sprintf("%.0f writesets waiting to be sent", q),
				Value:   finding.Num(q), Unit: "writesets",
				Hint: "the node cannot push to the group fast enough — look at the network before the disk",
			})
		}
	}
	return out
}

// sysTableDrift is the check no wsrep counter can make.
//
// Galera replicates the application's DDL, not the maintenance that happens to
// the server's own tables. So two nodes can hold different definitions of
// mysql.column_stats — or of the tables mysql_upgrade touched on one node and
// not the others — for months, while every replication metric stays green. The
// symptom, when it finally arrives, is a query plan that differs per node or a
// flood of errors in one node's log, which nobody connects to a schema nobody
// looked at.
func sysTableDrift(name string, live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	var audited []cluster.Snapshot
	for _, s := range live {
		if len(s.SysTables) == 0 {
			out = append(out, finding.Finding{
				Check: "systables/drift", Target: s.Node, Status: finding.WARN,
				Message: "system table definitions were not read",
				Hint:    "the audit user needs SELECT on information_schema.COLUMNS; without it this node is excluded from the drift comparison",
			})
			continue
		}
		audited = append(audited, s)
	}
	if len(audited) < 2 {
		return out
	}

	tables := map[string]bool{}
	for _, s := range audited {
		for t := range s.SysTables {
			tables[t] = true
		}
	}
	names := make([]string, 0, len(tables))
	for t := range tables {
		names = append(names, t)
	}
	sort.Strings(names)

	var drifted []string
	for _, table := range names {
		groups := map[string][]string{}
		for _, s := range audited {
			fp, ok := s.SysTables[table]
			if !ok {
				fp = "absent"
			}
			groups[fp] = append(groups[fp], s.Node)
		}
		if len(groups) == 1 {
			continue
		}
		drifted = append(drifted, table)
		out = append(out, finding.Finding{
			Check: "systables/drift", Target: "mysql." + table, Status: finding.BAD,
			Message: "definition differs across nodes: " + describeGroups(groups),
			Hint:    "Galera does not replicate this: fix it per node (mysql_upgrade, or align the definition by hand) — no wsrep_* metric will ever show it",
		})
	}
	if len(drifted) == 0 {
		out = append(out, finding.Finding{
			Check: "systables/drift", Target: name, Status: finding.OK,
			Message: fmt.Sprintf("%d system table(s) identical across %d node(s)", len(names), len(audited)),
			Value:   finding.Num(float64(len(names))), Unit: "tables",
		})
	}
	return out
}

// maxDriftedListed bounds the per-table findings. A node that missed a whole
// schema drifts on every table in it, and four hundred findings bury the rest
// of the report — at which point the number is the finding.
const maxDriftedListed = 5

// schemaDrift compares the application schemas across nodes (GD-13).
//
// Galera *does* replicate application DDL, which is what makes this a
// different diagnosis from sysTableDrift rather than the same check pointed at
// another schema: a difference here means a change that failed, was applied on
// one node by hand, or landed while a node was desynced — and the fix is to
// re-apply it, not to run mysql_upgrade. Both are equally invisible to every
// wsrep_* counter, because replication is not broken. It already carried
// whatever it was given.
func schemaDrift(name string, live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	var audited []cluster.Snapshot
	for _, s := range live {
		// nil is "not read"; an empty map is "read, and there are none".
		if s.AppTables == nil {
			out = append(out, finding.Finding{
				Check: "schema/drift", Target: s.Node, Status: finding.WARN,
				Message: "application schema definitions were not read",
				Hint:    "the audit user needs SELECT on information_schema.COLUMNS; without it this node is excluded from the drift comparison",
			})
			continue
		}
		audited = append(audited, s)
	}
	if len(audited) < 2 {
		return out
	}

	tables := map[string]bool{}
	for _, s := range audited {
		for t := range s.AppTables {
			tables[t] = true
		}
	}
	names := make([]string, 0, len(tables))
	for t := range tables {
		names = append(names, t)
	}
	sort.Strings(names)

	type drift struct {
		table  string
		groups map[string][]string
	}
	var drifted []drift
	for _, table := range names {
		groups := map[string][]string{}
		for _, s := range audited {
			fp, ok := s.AppTables[table]
			if !ok {
				fp = "absent"
			}
			groups[fp] = append(groups[fp], s.Node)
		}
		if len(groups) > 1 {
			drifted = append(drifted, drift{table: table, groups: groups})
		}
	}

	const hint = "application DDL is replicated, so this is a change that did not finish: compare the definitions and re-apply it on the nodes that are behind (a failed ALTER, a change applied by hand, or one that landed while a node was desynced)"

	switch {
	case len(drifted) == 0:
		out = append(out, finding.Finding{
			Check: "schema/drift", Target: name, Status: finding.OK,
			Message: fmt.Sprintf("%d application table(s) identical across %d node(s)", len(names), len(audited)),
			Value:   finding.Num(float64(len(names))), Unit: "tables",
		})
	case len(drifted) <= maxDriftedListed:
		for _, d := range drifted {
			out = append(out, finding.Finding{
				Check: "schema/drift", Target: d.table, Status: finding.BAD,
				Message: "definition differs across nodes: " + describeGroups(d.groups),
				Hint:    hint,
			})
		}
	default:
		listed := make([]string, 0, maxDriftedListed)
		for _, d := range drifted[:maxDriftedListed] {
			listed = append(listed, d.table)
		}
		out = append(out, finding.Finding{
			Check: "schema/drift", Target: name, Status: finding.BAD,
			Message: fmt.Sprintf("%d application table(s) differ across nodes: %s and %d more",
				len(drifted), strings.Join(listed, ", "), len(drifted)-maxDriftedListed),
			Value: finding.Num(float64(len(drifted))), Unit: "tables",
			Hint: "this many tables at once is a node that missed a whole schema change, not one bad ALTER: " + hint,
		})
	}
	return out
}

// backupSSTMethods are the state transfer methods that log in to the donor.
// rsync and its variants copy files and authenticate at the filesystem level,
// so an empty wsrep_sst_auth says nothing about them.
var backupSSTMethods = map[string]bool{
	"mariabackup":   true,
	"xtrabackup":    true,
	"xtrabackup-v2": true,
	"mysqldump":     true,
}

// sstReadiness reports what the next rejoin will cost (GD-25).
//
// Nothing here is wrong with the cluster today. A node whose SST method its
// peers cannot serve, or whose donor list names a server that was
// decommissioned in March, is Synced and green right up to the moment it
// restarts — at which point it either cannot rejoin or takes an unexpected
// donor out of service. No wsrep_* counter has an opinion about a setting that
// has not been exercised yet.
func sstReadiness(name string, live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding

	// The method, compared across the nodes that report it. A build that does
	// not report it is not a build that agrees with everybody else.
	var reporting []cluster.Snapshot
	for _, s := range live {
		if v, ok := s.Var("wsrep_sst_method"); ok && strings.TrimSpace(v) != "" {
			reporting = append(reporting, s)
		}
	}
	if len(reporting) > 0 {
		groups := groupBy(reporting, func(s cluster.Snapshot) string {
			v, _ := s.Var("wsrep_sst_method")
			return strings.ToLower(strings.TrimSpace(v))
		})
		if len(groups) > 1 {
			out = append(out, finding.Finding{
				Check: "sst/method", Target: name, Status: finding.WARN,
				Message: "nodes disagree about the state transfer method: " + describeGroups(groups),
				Hint:    "the joiner asks and the donor serves: a donor without the joiner's method installed cannot answer, so the node that restarts is the one that finds out",
			})
		} else {
			method, _ := reporting[0].Var("wsrep_sst_method")
			out = append(out, finding.Finding{
				Check: "sst/method", Target: name, Status: finding.OK,
				Message: fmt.Sprintf("all %d node(s) use %s for state transfer",
					len(reporting), strings.ToLower(strings.TrimSpace(method))),
			})
		}
	}

	// Every spelling this cluster answers to, so a donor named by address is
	// not reported as missing.
	known := map[string]bool{}
	for _, s := range live {
		for _, a := range s.Addresses() {
			known[strings.ToLower(a)] = true
		}
	}

	for _, s := range live {
		list, ok := s.Var("wsrep_sst_donor")
		if !ok || strings.TrimSpace(list) == "" {
			continue
		}
		// A trailing empty element — "node," — is Galera's way of saying "and
		// otherwise anybody". Without it the list is the only answer allowed.
		parts := strings.Split(list, ",")
		fallback := strings.TrimSpace(parts[len(parts)-1]) == ""
		var unknown []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" || known[strings.ToLower(p)] {
				continue
			}
			unknown = append(unknown, p)
		}
		if len(unknown) == 0 {
			continue
		}
		status, hint := finding.BAD, "with no trailing comma this list is the only donor allowed: the node will refuse to start when that donor is unavailable — fix the name, or end the list with a comma to allow any donor"
		if fallback {
			status, hint = finding.WARN, "the trailing comma means it will fall back to any available donor, so the node still starts — but the list no longer says what somebody meant it to say"
		}
		out = append(out, finding.Finding{
			Check: "sst/donor", Target: s.Node, Status: status,
			Message: fmt.Sprintf("SST donor list names %s, which is not in this cluster (wsrep_sst_donor=%s)",
				strings.Join(unknown, ", "), list),
			Hint: hint,
		})
	}

	// A backup-based transfer logs in to the donor. An empty wsrep_sst_auth is
	// not proof the credentials are missing — they can live in the [sst]
	// section of the config, where the server never sees them — so the finding
	// says that rather than pretending to be sure.
	for _, s := range live {
		method, ok := s.Var("wsrep_sst_method")
		if !ok || !backupSSTMethods[strings.ToLower(strings.TrimSpace(method))] {
			continue
		}
		auth, ok := s.Var("wsrep_sst_auth")
		if !ok || strings.TrimSpace(auth) != "" {
			continue
		}
		out = append(out, finding.Finding{
			Check: "sst/auth", Target: s.Node, Status: finding.WARN,
			Message: fmt.Sprintf("wsrep_sst_auth is empty and the method is %s, which authenticates against the donor",
				strings.ToLower(strings.TrimSpace(method))),
			Hint: "credentials may be in the [sst] section of the config instead, which the server cannot see — confirm it there, because the alternative is an SST that fails at 03:00",
		})
	}
	return out
}

// quorumSettings reports a split brain that is already configured (GD-26).
//
// The cluster is Primary, every counter is green, and pc.* has already decided
// what happens at the next partition: a node left with pc.ignore_sb keeps
// taking writes on the wrong side of it, and the weights decide which side
// that is. None of this is exercised until the network moves, which is why no
// metric reports it.
func quorumSettings(name string, live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding

	for _, s := range live {
		if v, ok := s.ProviderOption("pc.ignore_sb"); ok && truthy(v) {
			out = append(out, finding.Finding{
				Check: "quorum/ignore-sb", Target: s.Node, Status: finding.BAD,
				Message: "pc.ignore_sb is on: this node keeps accepting writes in a non-Primary component",
				Hint:    "at the next partition both sides stay writable and diverge — this is normally left on after somebody recovered a cluster by hand, and it has to be turned off again",
			})
		}
		if v, ok := s.ProviderOption("pc.bootstrap"); ok && truthy(v) {
			out = append(out, finding.Finding{
				Check: "quorum/bootstrap", Target: s.Node, Status: finding.WARN,
				Message: "pc.bootstrap is still set on this node",
				Hint:    "the bootstrap trigger is a one-shot: left in the configuration it makes this node form its own Primary component the next time it starts alone",
			})
		}
	}

	// The weights, as arithmetic rather than as an opinion: unequal weights are
	// legal and sometimes deliberate, and the useful statement is what the sum
	// makes possible.
	weights := map[string]float64{}
	total := 0.0
	for _, s := range live {
		v, ok := s.ProviderOption("pc.weight")
		if !ok {
			continue
		}
		w, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			continue
		}
		weights[s.Node] = w
		total += w
		if w == 0 {
			out = append(out, finding.Finding{
				Check: "quorum/weight", Target: s.Node, Status: finding.BAD,
				Message: "pc.weight is 0: this node never counts towards quorum",
				Value:   finding.Num(0), Unit: "weight",
				Hint: "the cluster has one fewer vote than it has nodes — losing one of the others is losing quorum, and the node count says nothing about it",
			})
		}
	}
	if len(weights) > 1 {
		equal := true
		var first float64
		i := 0
		for _, w := range weights {
			if i == 0 {
				first = w
			} else if w != first {
				equal = false
			}
			i++
		}
		switch {
		case equal && first > 0:
			out = append(out, finding.Finding{
				Check: "quorum/weight", Target: name, Status: finding.OK,
				Message: fmt.Sprintf("all %d node(s) carry equal quorum weight (%s)", len(weights), trimFloat(first)),
				Value:   finding.Num(total), Unit: "weight",
			})
		case !equal:
			nodes := make([]string, 0, len(weights))
			for n := range weights {
				nodes = append(nodes, n)
			}
			sort.Strings(nodes)
			parts := make([]string, 0, len(nodes))
			var majority []string
			for _, n := range nodes {
				parts = append(parts, fmt.Sprintf("%s=%s", n, trimFloat(weights[n])))
				if weights[n]*2 > total {
					majority = append(majority, n)
				}
			}
			msg := fmt.Sprintf("quorum weights are not equal: %s of %s total",
				strings.Join(parts, ", "), trimFloat(total))
			hint := "the node count is not the vote count: check that this is the arithmetic somebody meant, because it decides which side of a partition survives"
			if len(majority) > 0 {
				msg += fmt.Sprintf(" — %s alone holds a majority", strings.Join(majority, ", "))
				hint = "a single node holding a majority is a cluster that survives losing everything else, and dies when it loses that one: deliberate for a two-node-plus-arbitrator setup, an accident otherwise"
			}
			out = append(out, finding.Finding{
				Check: "quorum/weight", Target: name, Status: finding.WARN,
				Message: msg, Value: finding.Num(total), Unit: "weight",
				Hint: hint,
			})
		}
	}
	return out
}

// syncWait reports nodes that disagree about causal reads (GD-27).
//
// With wsrep_sync_wait on, a read waits for the writes that were committed
// before it; with it off, the same query can return a row that is not there
// yet. When the nodes disagree, the answer depends on which node the proxy
// picked — and every node is behaving exactly as configured, so nothing
// reports a problem. What the cluster wants is not this tool's business; the
// nodes not agreeing is.
func syncWait(name string, live []cluster.Snapshot) []finding.Finding {
	var reporting []cluster.Snapshot
	for _, s := range live {
		if v, ok := s.Var("wsrep_sync_wait"); ok && strings.TrimSpace(v) != "" {
			reporting = append(reporting, s)
		}
	}
	if len(reporting) < 2 {
		return nil
	}
	groups := groupBy(reporting, func(s cluster.Snapshot) string {
		v, _ := s.Var("wsrep_sync_wait")
		return strings.TrimSpace(v)
	})
	if len(groups) == 1 {
		return nil
	}
	return []finding.Finding{{
		Check: "repl/sync-wait", Target: name, Status: finding.WARN,
		Message: "nodes disagree about wsrep_sync_wait: " + describeGroups(groups),
		Hint:    "a read is causal or not depending on which node the proxy picked, which reaches the application as \"sometimes the row is not there yet\" and reaches no dashboard at all",
	}}
}

// autoIncrement reports ids that will collide (GD-28).
//
// Galera keeps auto_increment_increment and auto_increment_offset in step with
// the membership itself — unless wsrep_auto_increment_control was turned off,
// at which point the values are whatever somebody typed. Two nodes sharing an
// offset generate the same ids as soon as both take writes, and the damage
// lands in application data rather than in a replication counter.
func autoIncrement(name string, live []cluster.Snapshot) []finding.Finding {
	var uncontrolled []cluster.Snapshot
	controlled := 0
	for _, s := range live {
		on, ok := s.Bool("wsrep_auto_increment_control")
		if !ok {
			continue
		}
		if on {
			controlled++
			continue
		}
		uncontrolled = append(uncontrolled, s)
	}
	if len(uncontrolled) == 0 {
		if controlled == 0 {
			return nil
		}
		return []finding.Finding{{
			Check: "repl/auto-increment", Target: name, Status: finding.OK,
			Message: fmt.Sprintf("Galera manages the auto-increment step and offset on all %d node(s)", controlled),
		}}
	}

	offsets := map[string][]string{}
	step := map[string]bool{}
	var smallest float64
	first := true
	for _, s := range uncontrolled {
		off, ok := s.Float("auto_increment_offset")
		if !ok {
			if v, k := s.Var("auto_increment_offset"); k {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					off, ok = f, true
				}
			}
		}
		if ok {
			offsets[trimFloat(off)] = append(offsets[trimFloat(off)], s.Node)
		}
		if v, k := s.Var("auto_increment_increment"); k {
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				step[trimFloat(f)] = true
				if first || f < smallest {
					smallest, first = f, false
				}
			}
		}
	}

	var shared []string
	for off, nodes := range offsets {
		if len(nodes) > 1 {
			sort.Strings(nodes)
			shared = append(shared, fmt.Sprintf("%s (%s)", off, strings.Join(nodes, ", ")))
		}
	}
	sort.Strings(shared)

	const hint = "wsrep_auto_increment_control is ON by default and keeps the step and the offsets in line with the membership; with it OFF the values are whatever was typed, and the ids collide the moment a second node takes a write"
	if len(shared) > 0 {
		return []finding.Finding{{
			Check: "repl/auto-increment", Target: name, Status: finding.BAD,
			Message: fmt.Sprintf("wsrep_auto_increment_control is off and nodes share an auto_increment_offset: %s",
				strings.Join(shared, "; ")),
			Hint: hint,
		}}
	}
	if !first && smallest < float64(len(live)) {
		return []finding.Finding{{
			Check: "repl/auto-increment", Target: name, Status: finding.WARN,
			Message: fmt.Sprintf("wsrep_auto_increment_control is off and the step is %s for %d node(s): the offsets run out",
				trimFloat(smallest), len(live)),
			Value: finding.Num(smallest), Unit: "step",
			Hint: hint,
		}}
	}
	return []finding.Finding{{
		Check: "repl/auto-increment", Target: name, Status: finding.OK,
		Message: fmt.Sprintf("auto-increment is set by hand on %d node(s), with distinct offsets and a step of %s",
			len(uncontrolled), trimFloat(smallest)),
	}}
}

// truthy reads the spellings Galera uses inside wsrep_provider_options, which
// are not the ON/OFF of a server variable.
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}

// trimFloat prints a weight or an offset the way somebody typed it: 3, not
// 3.000000.
func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// storageEngines reports application tables Galera does not replicate (GD-29).
//
// Galera replicates InnoDB. A write to a MyISAM or Aria table succeeds, is
// certified by nothing, travels nowhere, and leaves the row on exactly one
// node — with every replication counter green, because from replication's
// point of view nothing happened. MariaDB can be told to replicate MyISAM and
// Aria (wsrep_mode, or the older wsrep_replicate_myisam), which is
// experimental and, more to the point here, per node: nodes disagreeing about
// it is worse than none of them doing it, because then the same write lands on
// some of them.
func storageEngines(live []cluster.Snapshot) []finding.Finding {
	seen := map[string]bool{}
	for _, s := range live {
		for _, t := range s.TablesNonInnoDB {
			seen[t] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	tables := make([]string, 0, len(seen))
	for t := range seen {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	shown := tables
	suffix := ""
	if len(shown) > 5 {
		shown, suffix = shown[:5], fmt.Sprintf(" (+%d more)", len(tables)-5)
	}

	// How each node answers "do you replicate these at all".
	groups := groupBy(live, func(s cluster.Snapshot) string {
		mode, hasMode := s.Var("wsrep_mode")
		if hasMode && (containsFold(mode, "REPLICATE_MYISAM") || containsFold(mode, "REPLICATE_ARIA")) {
			return "replicated"
		}
		if on, ok := s.Bool("wsrep_replicate_myisam"); ok && on {
			return "replicated"
		}
		if !hasMode {
			if _, ok := s.Var("wsrep_replicate_myisam"); !ok {
				return "unknown"
			}
		}
		return "not replicated"
	})
	delete(groups, "unknown")

	msg := fmt.Sprintf("%d application table(s) on an engine Galera does not replicate: %s%s",
		len(tables), strings.Join(shown, ", "), suffix)
	if len(groups) > 1 {
		return []finding.Finding{{
			Check: "schema/engine", Target: "schema", Status: finding.BAD,
			Message: msg + " — and the nodes disagree about replicating them: " + describeGroups(groups),
			Value:   finding.Num(float64(len(tables))), Unit: "tables",
			Hint: "the same write lands on some nodes and not others, which is divergence arriving one statement at a time: align wsrep_mode across the nodes, then move these tables to InnoDB",
		}}
	}
	hint := "these writes are not replicated: they succeed, no counter records them, and the rows exist on the node that took them — move the tables to InnoDB, or accept that they are per-node data"
	if _, replicated := groups["replicated"]; replicated {
		hint = "MyISAM and Aria replication is enabled here, which is experimental and certifies nothing: a conflicting write is not detected, it is applied — move the tables to InnoDB"
	}
	return []finding.Finding{{
		Check: "schema/engine", Target: "schema", Status: finding.WARN,
		Message: msg,
		Value:   finding.Num(float64(len(tables))), Unit: "tables",
		Hint: hint,
	}}
}

// containsFold is strings.Contains, case-insensitively.
func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToUpper(haystack), strings.ToUpper(needle))
}

// gcacheRecover reports a restart that throws the write-set cache away (GD-33).
//
// gcache/window measures how much time the cache buys before a restarting node
// needs a full state transfer. With gcache.recover off, a clean restart
// discards the cache along with the process: the window the other check
// reports is a buffer this setting quietly throws away, and the node comes back
// needing an SST — which takes a donor out of service with it. Each node's
// restart is its own, so this is one finding per node rather than one about the
// cluster.
func gcacheRecover(live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	for _, s := range live {
		v, ok := s.ProviderOption("gcache.recover")
		// The option arrived in Galera 3.19: a provider that does not report
		// it is not a provider with it off.
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		if truthy(v) {
			continue
		}
		out = append(out, finding.Finding{
			Check: "gcache/recover", Target: s.Node, Status: finding.WARN,
			Message: fmt.Sprintf("gcache.recover is %s: a clean restart discards this node's write-set cache",
				strings.TrimSpace(v)),
			Hint: "with it on the node rejoins by IST from its own cache after a restart; with it off even a two-minute maintenance window costs a full SST, and an SST takes a donor out of service too",
		})
	}
	return out
}

// osuMethod reports the DDL method that explains schema/drift (GD-34).
//
// TOI replicates a schema change to every node; NBO does the same without
// holding the cluster-wide lock. RSU does not replicate it at all — it applies
// the change on the node it was run on and leaves the others alone, which is
// precisely how the application schema drift that schema/drift reports comes to
// exist. Reporting the cause next to the symptom is the difference between a
// finding and a diagnosis.
func osuMethod(live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	for _, s := range live {
		v, ok := s.Var("wsrep_osu_method")
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(v), "RSU") {
			continue
		}
		out = append(out, finding.Finding{
			Check: "repl/osu-method", Target: s.Node, Status: finding.WARN,
			Message: "wsrep_OSU_method is RSU: DDL run on this node is applied here and not replicated",
			Hint:    "this is where a schema/drift finding comes from — RSU is a per-node operation to be turned on for one change and off again, not a default; TOI and NBO both replicate",
		})
	}
	return out
}

// primaryKeys reports application tables Galera cannot certify reliably.
func primaryKeys(live []cluster.Snapshot) []finding.Finding {
	seen := map[string]bool{}
	for _, s := range live {
		for _, t := range s.TablesNoPK {
			seen[t] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	tables := make([]string, 0, len(seen))
	for t := range seen {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	shown := tables
	suffix := ""
	if len(shown) > 5 {
		shown, suffix = shown[:5], fmt.Sprintf(" (+%d more)", len(tables)-5)
	}
	return []finding.Finding{{
		Check: "schema/no-pk", Target: "schema", Status: finding.WARN,
		Message: fmt.Sprintf("%d table(s) without a primary key: %s%s", len(tables), strings.Join(shown, ", "), suffix),
		Value:   finding.Num(float64(len(tables))), Unit: "tables",
		Hint: "row-based replication applies these by full-row scan on every node, and DELETEs can apply in a different order — Galera's own documentation calls this unsupported",
	}}
}

// versions reports a cluster running more than one build.
func versions(name string, live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	server := groupBy(live, func(s cluster.Snapshot) string {
		v, _ := s.Var("version")
		return v
	})
	if len(server) > 1 {
		out = append(out, finding.Finding{
			Check: "cluster/versions", Target: name, Status: finding.WARN,
			Message: "mixed server versions: " + describeGroups(server),
			Value:   finding.Num(float64(len(server))), Unit: "versions",
			Hint: "expected during a rolling upgrade and a liability after it: keep the window short",
		})
	}
	provider := groupBy(live, func(s cluster.Snapshot) string {
		v, _ := s.Get("wsrep_provider_version")
		return v
	})
	if len(provider) > 1 {
		out = append(out, finding.Finding{
			Check: "cluster/provider-version", Target: name, Status: finding.WARN,
			Message: "mixed wsrep provider versions: " + describeGroups(provider),
			Value:   finding.Num(float64(len(provider))), Unit: "versions",
			Hint: "the group communication protocol is negotiated down to the oldest member",
		})
	}
	return out
}

// gcache estimates how far back a restarting node can catch up incrementally.
//
// The number that matters is not the gcache size but the *time* it buys at the
// current write rate, and that needs two runs. Without a baseline the size is
// reported without a verdict, because "1 GB" is either an hour or ninety
// seconds depending on a workload the tool has not measured yet.
func gcache(live []cluster.Snapshot, prev *state.State, now state.State, opt Options) []finding.Finding {
	var out []finding.Finding
	for _, s := range live {
		raw, ok := s.ProviderOption("gcache.size")
		if !ok {
			continue
		}
		size, ok := cluster.Bytes(raw)
		if !ok || size <= 0 {
			continue
		}
		d, haveRate := prev.Since(s.Node, "wsrep_replicated_bytes", now.Nodes[s.Node])
		if !haveRate || d.PerSecond() <= 0 {
			out = append(out, finding.Finding{
				Check: "gcache/window", Target: s.Node, Status: finding.OK,
				Message: fmt.Sprintf("gcache is %s (not graded: no write-rate baseline)", raw),
				Value:   finding.Num(float64(size)), Unit: "bytes",
				Hint: "run again with --state: the useful number is how many minutes of writes it holds, not how many bytes",
			})
			continue
		}
		window := time.Duration(float64(size)/d.PerSecond()) * time.Second
		st := finding.OK
		hint := ""
		if window < opt.ISTWarn {
			st = finding.WARN
			hint = "a node restarting after longer than this needs a full SST: hours of transfer, and a donor taken out of service to provide it"
		}
		out = append(out, finding.Finding{
			Check: "gcache/window", Target: s.Node, Status: st,
			Message: fmt.Sprintf("gcache %s holds about %s of writes at the current rate (%.1f MB/s)",
				raw, window.Round(time.Minute), d.PerSecond()/(1<<20)),
			Value: finding.Num(window.Seconds()), Unit: "seconds", Hint: hint,
		})
	}
	return out
}

// groupBy buckets nodes by a value. A missing value is its own bucket, spelled
// so a report does not show an empty string.
func groupBy(live []cluster.Snapshot, key func(cluster.Snapshot) string) map[string][]string {
	out := map[string][]string{}
	for _, s := range live {
		v := key(s)
		if v == "" {
			v = "unknown"
		}
		out[v] = append(out[v], s.Node)
	}
	return out
}

// describeGroups renders "value (node, node) vs value (node)" deterministically.
func describeGroups(groups map[string][]string) string {
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		nodes := append([]string(nil), groups[k]...)
		sort.Strings(nodes)
		parts = append(parts, fmt.Sprintf("%s (%s)", short(k), strings.Join(nodes, ", ")))
	}
	return strings.Join(parts, " vs ")
}

// short keeps a UUID or a fingerprint readable in one line of terminal.
func short(v string) string {
	if len(v) > 12 {
		return v[:12] + "…"
	}
	return v
}
