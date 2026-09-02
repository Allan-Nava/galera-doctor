<p align="center">
  <img src="assets/mark.svg" alt="" width="96" height="96">
</p>

# INTENT — galera-doctor

This is the charter: what the tool is for, what it will never become, and the
properties that are not allowed to erode. [README.md](README.md) says how to use
it, [AGENTS.md](AGENTS.md) says how to work in the repo. When a change is
plausible but does not fit this file, this file wins.

## The one sentence

> **Say what is wrong with this cluster that its own metrics do not say.**

Every feature, flag, check and line of output is measured against that
sentence. If a `wsrep_*` counter or a MySQL dashboard already shows it, it does
not belong here.

## Why it exists

`wsrep_cluster_size` being right is not the same as the cluster being right. A
Galera cluster has a set of states that are invisible to the instruments the
cluster ships with:

- **A system table's definition differs between nodes.** Galera does not
  replicate maintenance on the server's own `mysql.*` tables the way it
  replicates application DDL, so two nodes can disagree about
  `mysql.column_stats` for months with every replication counter green.
- **One name, two clusters.** Each half reports a consistent size and a Primary
  status. The divergence only exists in the comparison of every node's
  `wsrep_cluster_state_uuid` *to each other* — which no single node performs.
- **The proxy and the cluster disagree.** ProxySQL says ONLINE, the node says
  Joined. Both dashboards are green and traffic is going somewhere unsynced.
- **The gcache is too small for the write rate.** 512 MB is forty minutes or
  ninety seconds, and nobody finds out until a restart needs a full SST and
  takes a donor down with it.
- **Flow control that already happened.** `wsrep_flow_control_paused` is a
  fraction since the last status reset, so an incident from March reads the
  same today.

The common shape: the state is only visible *between* nodes, or only visible
*over time*. A per-node instantaneous gauge cannot express either.

## Non-goals

These are refusals, not missing features.

- **Not a monitoring system.** No daemon, no scraper, no time series. One state
  file, used for rates. It runs from cron, from CI, or during an incident.
- **Not a generic MySQL health check.** Connections, buffer pool, slow queries,
  replica lag: real problems, wrong tool. They belong in
  [checkfleet](https://github.com/Allan-Nava/checkfleet), which has a `mysql`
  module.
- **Not a repair tool.** Bootstrapping a cluster, dropping a peer, resyncing a
  node and desyncing a donor are decisions with consequences. This tool reports
  and a human decides; none of it is one flag away.
- **Not a config linter.** `wsrep_provider_options` opinions age badly and are
  workload-specific. A setting is only a finding when it is measurably wrong
  *for this cluster's observed rate* — which is why the gcache is reported as a
  window in seconds, not as a size in megabytes.
- **Not an alerting policy.** Severities are the tool's judgement about a
  measurement; the decision to page somebody is `--exit-on` and the caller's.

## Properties that must not erode

1. **Read-only, mechanically.** Every query goes through one function that
    refuses anything but `SHOW` and `SELECT`, and CI greps the source for
    writing statements and for `Exec`. This tool is pointed at clusters that are
    already in trouble: "it does not write" has to be a property of the code,
    not a claim in a README.
2. **A total is never a verdict.** The `wsrep_*` counters only go up and reset
    on restart. Grade them over the interval between runs, or say plainly that
    they were not graded. A check that goes red once and stays red is a check
    people stop reading.
3. **No baseline is a state, not a zero.** A missing state file, a counter that
    went backwards, an uptime that shrank, a state file from another format
    version: all of them mean *no baseline*. Never a negative rate, never a
    spectacular one.
4. **A missing value is not zero.** A build without a metric is not a healthy
    one, so a missing or unparseable value is reported as absent.
5. **Say what was not audited.** A node that could not be read is an `ERROR`,
    because every cluster-wide statement below it was made without that node. A
    node missing the grants is *not audited*, never silently dropped from the
    comparison.
6. **Do not grade what the tool does not own.** ProxySQL's offline hostgroup is
    moved by ProxySQL's own Galera monitor. Its contents are never a finding,
    and "cleaning it up" would fight the monitor.
7. **One cause, one finding.** A standalone server is *not a cluster member*,
    said once, and excluded from every comparison — not five simultaneous
    catastrophes.
8. **A DSN never reaches the output.** Nodes are identified by name and driver
    errors are redacted, because error messages end up in tickets.
9. **The finding says what to do.** The message carries the measurement and the
    hint says what it means for whoever is on call. `wsrep_local_recv_queue_avg`
    tells them nothing at 03:00.
10. **Exit 0 whenever the audit ran.** Findings are output, not failure. Only
    `--exit-on` produces 1; a usage error or an unusable config exits 2.

## How a change earns its place

A new check has to answer three questions before it is written:

- **Which invisible state does it expose?** Name the state, and name the metric
  or dashboard that fails to show it.
- **Is it a gauge or a counter?** A gauge can be graded as it stands. A counter
  needs the state file and an honest ungraded fallback. There is no third
  option.
- **What does the operator do with it?** If the answer is "look at it", the
  finding is not finished.

Then: backlog item first (`GD-n`), failing fixture test second, code third. The
interesting states — a split brain, a drifted system table, a proxy that
disagrees — cannot be conjured on demand against a real server, so the fixtures
are the specification.
