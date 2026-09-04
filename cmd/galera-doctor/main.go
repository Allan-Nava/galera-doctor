// Command galera-doctor audits a MariaDB/MySQL Galera cluster read-only, and
// reports the states the cluster's own metrics cannot show you: two nodes that
// have quietly become two clusters, a system table whose definition drifted on
// one node, a proxy sending traffic to a node that is still joining, a gcache
// too small to survive a restart.
//
// It only ever issues SHOW and SELECT — enforced in code, not promised in a
// README — so it is safe to run against a cluster that is already having a bad
// day.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Allan-Nava/galera-doctor/internal/audit"
	"github.com/Allan-Nava/galera-doctor/internal/cluster"
	"github.com/Allan-Nava/galera-doctor/internal/config"
	"github.com/Allan-Nava/galera-doctor/internal/finding"
	"github.com/Allan-Nava/galera-doctor/internal/output"
	"github.com/Allan-Nava/galera-doctor/internal/proxysql"
	"github.com/Allan-Nava/galera-doctor/internal/state"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "audit":
		os.Exit(cmdAudit(os.Args[2:]))
	case "checks":
		os.Exit(cmdChecks())
	case "version", "--version", "-v":
		fmt.Println("galera-doctor", version)
	case "help", "-h", "--help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "galera-doctor: unknown command %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprint(w, `galera-doctor — a read-only audit of a Galera cluster

usage:
  galera-doctor audit [flags]
  galera-doctor checks
  galera-doctor version

nodes:
  --config FILE            cluster definitions (JSON; DSNs may use ${ENV_VAR})
  --cluster NAME           audit one cluster from the config (default: all)
  --node name=DSN          a node, repeatable — an alternative to --config
  --proxysql DSN           ProxySQL admin interface to compare against

flags:
  --state FILE             remember counters between runs so rates can be graded
  --expect-nodes N         membership the cluster should report (default: nodes given)
  --timeout D              per-node timeout (default 10s)
  --no-systables           skip the system table drift comparison
  --no-schema              skip the application schema reads (drift, primary keys)
  --flow-warn F            flow-control share of the interval to WARN at (default 0.01)
  --flow-bad F             ... and to call BAD (default 0.10)
  --ist-warn D             gcache window below which a restart means a full SST (default 30m)
  --clock-warn D           spread between node clocks to WARN at (default 2s)
  --clock-bad D            ... and to call BAD (default 30s)
  --latency-floor D        replication latency below which a difference inside
                           a segment is noise (default 2ms)
  --watch D                re-audit every D and print only the transitions
  --json                   full report
  --findings               flat findings array
  --min-severity S         hide findings below S (OK|WARN|BAD|ERROR)
  --exit-on S              exit 1 when a finding reaches S (default: never)

exit status:
  0  the audit ran (findings are output, not an error)
  1  --exit-on threshold reached
  2  usage error, or no node could be resolved

galera-doctor issues only SHOW and SELECT statements. It changes nothing, and
it never prints a DSN.
`)
}

func cmdChecks() int {
	rows := [][2]string{
		{"node/read", "the node answered at all — every cluster finding is conditional on this"},
		{"cluster/uuid", "nodes reporting different state UUIDs: one name, two clusters"},
		{"cluster/conf-id", "nodes disagreeing about the membership generation"},
		{"cluster/primary", "a node that is not in the Primary component"},
		{"cluster/size", "membership size, and nodes disagreeing about it"},
		{"node/ready, node/connected, node/wsrep-on", "the node is replicating at all"},
		{"node/state", "Synced, Donor/Desynced, Joined, or something worse"},
		{"node/desync, node/read-only", "deliberate exclusions that were never undone"},
		{"flow/paused", "share of the interval spent flow-controlling (needs --state)"},
		{"repl/cert-failures", "write conflicts as a share of writesets (needs --state)"},
		{"queue/recv, queue/send", "instantaneous queue depths"},
		{"systables/drift", "definitions of the mysql.* tables differing between nodes"},
		{"schema/drift", "application table definitions differing between nodes: a schema change that did not finish"},
		{"schema/no-pk", "tables Galera cannot certify reliably"},
		{"schema/engine", "application tables on an engine Galera does not replicate"},
		{"sst/method, sst/donor, sst/auth", "whether the next node to restart can rejoin"},
		{"quorum/ignore-sb, quorum/bootstrap", "settings that decide the next partition"},
		{"quorum/weight", "the quorum arithmetic, when it is not one vote per node"},
		{"repl/sync-wait", "nodes disagreeing about causal reads"},
		{"repl/auto-increment", "ids that collide once a second node takes writes"},
		{"cluster/versions", "mixed server or wsrep provider versions"},
		{"gcache/window", "how much time the gcache buys before a restart needs a full SST (needs --state)"},
		{"gcache/recover", "a clean restart that discards the write-set cache anyway"},
		{"repl/osu-method", "a node on RSU: DDL applied here and not replicated"},
		{"repl/ws-limits", "appliers that will refuse a write-set the cluster certified"},
		{"node/clock", "the spread between the nodes' own clocks"},
		{"node/durability", "a cluster whose durability is one node's, not its average"},
		{"cluster/segments", "the segment map, and the one shape that cannot be deliberate"},
		{"cluster/latency", "slow, or simply far away: the cluster's own round-trip measurement"},
		{"cluster/peers", "a peer list that describes a cluster which no longer exists"},
		{"flow/settings", "one node's flow-control limit pacing every writer"},
		{"repl/appliers", "a node that applies with fewer threads than its peers"},
		{"sst/size", "what a rejoin copies, and how long a donor is out of service"},
		{"audit/coverage", "what this run could not audit, in one line"},
		{"repl/async-in, repl/async-out", "async replication into or out of a cluster member"},
		{"repl/server-id", "nodes sharing a server_id"},
		{"repl/gtid-domain, repl/gtid-strict", "what a failover does to a downstream replica"},
		{"repl/triggers", "triggers that run on the appliers of one node only"},
		{"repl/writers", "who is actually writing, over the interval (needs --state)"},
		{"node/binlog, node/binlog-format", "what the cluster cannot be used for: backups, downstream replicas"},
		{"node/binlog-updates", "a node no downstream replica can be moved to"},
		{"node/restarted", "a node that came back between two runs (needs --state)"},
		{"cluster/membership-view", "the group's own member list against the nodes audited"},
		{"audit/changes", "what appeared, cleared or got worse since the last run (needs --state)"},
		{"proxysql/*", "the proxy's view against the cluster's (needs --proxysql)"},
		{"proxysql/monitor", "a proxy whose Galera monitor stopped: the hostgroups are a photograph"},
	}
	for _, r := range rows {
		fmt.Printf("%-42s %s\n", r[0], r[1])
	}
	fmt.Println("\nChecks marked (needs --state) are graded over the interval between two")
	fmt.Println("runs. Without a baseline they report the lifetime total and say so: a")
	fmt.Println("threshold over a counter that only goes up goes red once and stays red.")
	return 0
}

// nodeFlag collects repeated --node name=DSN arguments.
type nodeFlag []cluster.Node

func (n *nodeFlag) String() string { return "" }

func (n *nodeFlag) Set(v string) error {
	name, dsn, ok := strings.Cut(v, "=")
	if !ok || name == "" || dsn == "" {
		return fmt.Errorf("want name=DSN, got %q", v)
	}
	*n = append(*n, cluster.Node{Name: name, DSN: dsn})
	return nil
}

func cmdAudit(args []string) int {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var nodes nodeFlag
	fs.Var(&nodes, "node", "a node as name=DSN, repeatable")
	var (
		cfgPath     = fs.String("config", "", "cluster definitions (JSON)")
		clusterName = fs.String("cluster", "", "cluster to audit (default: all in the config)")
		proxyDSN    = fs.String("proxysql", "", "ProxySQL admin DSN")
		statePath   = fs.String("state", "", "state file for rate calculations")
		expect      = fs.Int("expect-nodes", 0, "membership the cluster should report")
		timeout     = fs.Duration("timeout", 10*time.Second, "per-node timeout")
		noSysTables = fs.Bool("no-systables", false, "skip the system table drift comparison")
		noSchema    = fs.Bool("no-schema", false, "skip the application schema reads (drift, primary keys)")
		flowWarn    = fs.Float64("flow-warn", 0.01, "flow-control share to WARN at")
		flowBad     = fs.Float64("flow-bad", 0.10, "flow-control share to call BAD")
		istWarn     = fs.Duration("ist-warn", 30*time.Minute, "gcache window below which a restart means a full SST")
		clockWarn   = fs.Duration("clock-warn", 2*time.Second, "spread between node clocks to WARN at")
		latFloor    = fs.Duration("latency-floor", 2*time.Millisecond, "replication latency below which a difference inside a segment is noise")
		clockBad    = fs.Duration("clock-bad", 30*time.Second, "spread between node clocks to call BAD")
		watchEvery  = fs.Duration("watch", 0, "re-audit on this interval and print only the transitions")
		asJSON      = fs.Bool("json", false, "full JSON report")
		asFindings  = fs.Bool("findings", false, "flat findings array")
		minSev      = fs.String("min-severity", "", "hide findings below this status")
		exitOn      = fs.String("exit-on", "", "exit 1 when a finding reaches this status")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	for _, s := range []string{*minSev, *exitOn} {
		if err := validStatus(s); err != nil {
			fmt.Fprintln(os.Stderr, "galera-doctor:", err)
			return 2
		}
	}
	// `--watch 0` and no --watch at all are the same value, and they are not
	// the same instruction: one is a mistake worth refusing, the other is the
	// default. fs.Visit is what tells them apart.
	watching := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "watch" {
			watching = true
		}
	})
	if watching {
		if err := validWatch(*watchEvery, *asJSON, *asFindings); err != nil {
			fmt.Fprintln(os.Stderr, "galera-doctor:", err)
			return 2
		}
	}

	targets := map[string]config.Cluster{}
	if *cfgPath != "" {
		f, err := config.Load(*cfgPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "galera-doctor:", err)
			return 2
		}
		for _, name := range f.Names() {
			if *clusterName != "" && name != *clusterName {
				continue
			}
			targets[name] = f.Clusters[name]
		}
		if len(targets) == 0 {
			fmt.Fprintf(os.Stderr, "galera-doctor: no cluster called %q in %s\n", *clusterName, *cfgPath)
			return 2
		}
	}
	if len(nodes) > 0 {
		name := *clusterName
		if name == "" {
			name = "cluster"
		}
		targets[name] = config.Cluster{Nodes: nodes, ProxySQLDSN: *proxyDSN, ExpectNodes: *expect}
	}
	if len(targets) == 0 {
		fmt.Fprintln(os.Stderr, "galera-doctor: no node to audit — give --config or --node name=DSN")
		return 2
	}

	prev, err := state.Load(*statePath)
	if err != nil {
		// A corrupt cache is not a reason to refuse to audit: say so and grade
		// nothing that needs a baseline.
		fmt.Fprintln(os.Stderr, "galera-doctor: ignoring unreadable state file:", err)
		prev = nil
	}

	collector := cluster.Collector{Timeout: *timeout, SkipSysTables: *noSysTables, SkipSchema: *noSchema}
	ctx := context.Background()
	opt := audit.DefaultOptions()
	opt.FlowWarn, opt.FlowBad, opt.ISTWarn = *flowWarn, *flowBad, *istWarn
	opt.ClockWarn, opt.ClockBad = *clockWarn, *clockBad
	opt.LatencyFloor = *latFloor
	opt.Now = time.Now()

	// One pass over every target: collect, audit, persist. Extracted so watch
	// mode can call it on a tick without a second copy of any of it.
	runOnce := func() []audit.Report {
		var reps []audit.Report
		opt.Now = time.Now()
		merged := state.State{Version: state.Version, At: opt.Now, Nodes: map[string]state.NodeState{}}
		for _, name := range sortedKeys(targets) {
			c := targets[name]
			snaps := collector.CollectAll(ctx, c.Nodes)
			o := opt
			o.Cluster = name
			o.ExpectNodes = c.ExpectNodes
			if *expect != 0 {
				o.ExpectNodes = *expect
			}
			// The state file namespaces everything by cluster; the audit asks
			// about bare node names, so it gets this cluster's view of it.
			rep := audit.Run(snaps, prev.Scope(name), o)
			dsn := c.ProxySQLDSN
			if *proxyDSN != "" {
				dsn = *proxyDSN
			}
			if dsn != "" {
				rep.Findings = append(rep.Findings, proxysql.Audit(proxysql.Collect(ctx, dsn, *timeout), snaps)...)
				finding.SortWorstFirst(rep.Findings)
			}
			merged.Merge(name, rep.State)
			reps = append(reps, rep)
		}

		// The state file is written here and never blocks the output: a failure to
		// persist a baseline costs the next run its rates, nothing more. In watch
		// mode the baseline is also held in memory, so a missing file costs
		// nothing at all.
		if err := state.Save(*statePath, merged); err != nil {
			fmt.Fprintln(os.Stderr, "galera-doctor: could not write the state file:", err)
		}
		// Each pass reads the state it just wrote, so a watch loop grades rates
		// over its own interval rather than over the whole session.
		prev = &merged
		return reps
	}

	// A loop, in the foreground, printing only what moved: the window this is
	// for is the one in which somebody is repairing a cluster. Still not a
	// daemon and still not a monitoring system — it stops when the person
	// watching it stops.
	if watching {
		ticker := time.NewTicker(*watchEvery)
		defer ticker.Stop()
		ticks := make(chan time.Time)
		go func() {
			// The first tick is now: nobody wants to wait an interval to see
			// where the cluster is starting from.
			ticks <- time.Now()
			for t := range ticker.C {
				ticks <- t
			}
		}()
		return watch(os.Stdout, ticks, runOnce)
	}

	reps := runOnce()

	switch {
	case *asFindings:
		err = output.Findings(os.Stdout, reps, finding.Status(*minSev))
	case *asJSON:
		err = output.JSON(os.Stdout, reps)
	default:
		if err = output.Text(os.Stdout, reps, finding.Status(*minSev)); err == nil {
			err = output.Summary(os.Stdout, reps)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "galera-doctor:", err)
		return 2
	}

	if *exitOn != "" {
		for _, r := range reps {
			if finding.AtLeast(r.Worst(), finding.Status(*exitOn)) {
				return 1
			}
		}
	}
	return 0
}

// watch re-audits on every tick and prints the first report in full, then
// only the transitions (GD-18).
//
// It is driven by a channel rather than by a ticker so it can be tested: a
// loop that only stops on a signal cannot be, and an untested loop is how
// "it printed nothing" becomes "it printed nothing because it never ran". It
// returns when the channel closes.
func watch(w io.Writer, ticks <-chan time.Time, run func() []audit.Report) int {
	prev := map[string]string{}
	baseline := true
	for t := range ticks {
		reps := run()
		if baseline {
			// Where we are starting from, in full: the transitions afterwards
			// only mean something against it.
			if err := output.Text(w, reps, ""); err != nil {
				fmt.Fprintln(os.Stderr, "galera-doctor:", err)
				return 2
			}
			baseline = false
		} else if _, err := output.Changed(w, reps, prev, t); err != nil {
			fmt.Fprintln(os.Stderr, "galera-doctor:", err)
			return 2
		}
		prev = output.Statuses(reps)
	}
	return 0
}

// validWatch is the interval and the renderers it can be combined with. It is
// only called when --watch was actually given, so a zero here was typed.
func validWatch(every time.Duration, asJSON, asFindings bool) error {
	// Shorter than one audit takes is a busy loop against a cluster that is
	// already having a bad day.
	if every < time.Second {
		return fmt.Errorf("--watch %s is too short: give it at least a second", every)
	}
	if asJSON || asFindings {
		return fmt.Errorf("--watch prints transitions for a person to read; --json and --findings emit one document per run, and a stream of them is not a document")
	}
	return nil
}

func sortedKeys(m map[string]config.Cluster) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func validStatus(s string) error {
	switch finding.Status(s) {
	case "", finding.OK, finding.WARN, finding.BAD, finding.ERROR:
		return nil
	}
	return fmt.Errorf("bad status %q: want OK, WARN, BAD or ERROR", s)
}
