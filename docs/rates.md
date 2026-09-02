# Rates, not totals

The `wsrep_*` counters only ever go up, and they reset when the server
restarts. That makes them useless as thresholds:

- `wsrep_flow_control_paused` is the fraction of the time **since the last
  status reset** that the node spent flow-controlling. A cluster that struggled
  for ten minutes last March reports the same number today as it did the day
  after.
- `wsrep_local_cert_failures` is a lifetime count. Divided by nothing, it is a
  number that only grows.

A check built on either goes red once and stays red, and a check that stays red
is one people learn to silence.

## What galera-doctor does instead

`--state FILE` writes the counters after every run. The next run diffs them and
grades the **interval**:

```console
  WARN  flow/paused   sg-01   flow-controlled 2.10% of the last 10m0s
        ↳ this node is intermittently the slowest in the cluster; look at its disk and its replication threads
```

Without a baseline, the same check reports the lifetime figure and refuses to
judge it:

```console
  OK    flow/paused   sg-01   0.9% of the time since the last status reset (not graded: no baseline)
        ↳ run again with --state to grade the interval between runs instead of the lifetime total
```

The same applies to `repl/cert-failures` (conflicts as a share of the writesets
in the interval) and to `gcache/window` (the gcache size divided by the write
rate in the interval).

## When there is no baseline

All of these mean *no baseline*, and none of them produce a number:

- no state file yet — the first run of anything;
- the node was not in the previous run;
- the counter went **backwards**: the server restarted, or somebody ran
  `FLUSH STATUS`. Reading the difference would invent an incident, and after a
  wraparound, a spectacular one;
- the node's uptime is shorter than it was last time — a restart, caught even
  when the counter has already climbed past its old value;
- the interval is zero or negative, so there is nothing to divide by;
- the state file is from another format version, or unreadable. The audit still
  runs; the rates do not.

The state file is a cache, never a source of truth. Delete it and the next run
simply has nothing to compare against — it says so.
