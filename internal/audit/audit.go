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
