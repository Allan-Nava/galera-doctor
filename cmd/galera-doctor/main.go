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
		{"proxysql/*", "the proxy's view against the cluster's (needs --proxysql)"},
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
	opt.Now = time.Now()

	var reps []audit.Report
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
		rep := audit.Run(snaps, prev, o)
		dsn := c.ProxySQLDSN
		if *proxyDSN != "" {
			dsn = *proxyDSN
		}
		if dsn != "" {
			rep.Findings = append(rep.Findings, proxysql.Audit(proxysql.Collect(ctx, dsn, *timeout), snaps)...)
			finding.SortWorstFirst(rep.Findings)
		}
		for node, ns := range rep.State.Nodes {
			merged.Nodes[name+"/"+node] = ns
		}
		reps = append(reps, rep)
	}

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

	// The state file is written last and never blocks the output: a failure to
	// persist a baseline costs the next run its rates, nothing more.
	if err := state.Save(*statePath, merged); err != nil {
		fmt.Fprintln(os.Stderr, "galera-doctor: could not write the state file:", err)
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
