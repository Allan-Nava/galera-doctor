# Usage

```
galera-doctor audit [flags]
galera-doctor checks
galera-doctor version
```

## Where the nodes come from

Either a config file:

```json
{
  "clusters": {
    "compress": {
      "nodes": [
        {"name": "sg-01", "dsn": "audit:${GALERA_DOCTOR_PASS}@tcp(10.11.1.5:3306)/"},
        {"name": "cl-02", "dsn": "audit:${GALERA_DOCTOR_PASS}@tcp(10.21.1.5:3306)/"},
        {"name": "ov-03", "dsn": "audit:${GALERA_DOCTOR_PASS}@tcp(10.35.1.5:3306)/"}
      ],
      "proxysql_dsn": "admin:${PROXYSQL_ADMIN_PASS}@tcp(10.11.1.9:6032)/",
      "expect_nodes": 3
    }
  }
}
```

...or the command line, with `--node` repeated:

```sh
galera-doctor audit \
  --node "sg-01=audit:$PASS@tcp(10.11.1.5:3306)/" \
  --node "cl-02=audit:$PASS@tcp(10.21.1.5:3306)/"
```

`${ENV_VAR}` is expanded in every DSN. An **unset** variable is an error, not an
empty string: a DSN with an empty password fails with "access denied", which is
a bad way to learn that a variable is missing. Unknown keys in the file are
rejected too — a typo in `"noeds"` would otherwise produce a cluster with no
nodes.

A DSN is never printed. Findings identify nodes by name, and a driver error that
quotes a DSN is redacted first.

## Flags

| Flag | Default | What it does |
|---|---|---|
| `--config FILE` | — | cluster definitions (JSON) |
| `--cluster NAME` | all | audit one cluster from the config |
| `--node name=DSN` | — | a node, repeatable |
| `--proxysql DSN` | — | ProxySQL admin interface to compare against |
| `--state FILE` | — | remember counters between runs so rates can be graded |
| `--expect-nodes N` | nodes given | membership the cluster should report |
| `--timeout D` | `10s` | per-node timeout — one dead node does not decide the run's length |
| `--no-systables` | off | skip the system table drift comparison |
| `--no-schema` | off | skip the primary key check |
| `--flow-warn F` / `--flow-bad F` | `0.01` / `0.10` | flow-control share of the interval |
| `--ist-warn D` | `30m` | gcache window below which a restart means a full SST |
| `--json` / `--findings` | — | full report / flat findings array |
| `--min-severity S` | — | hide findings below `S` |
| `--exit-on S` | never | exit 1 when a finding reaches `S` |
| `--watch D` | off | re-audit every `D` and print only the transitions |

## Exit status

| Code | Meaning |
|---|---|
| `0` | the audit ran — findings are output, not an error |
| `1` | `--exit-on` threshold reached |
| `2` | usage error, or no node could be resolved |

## While you are repairing it

```console
$ galera-doctor audit --config clusters.json --watch 10s
```

The first report in full, so you know where you are starting from, and after
that **only what moved**:

```console
16:34:48  OK    cluster/uuid   compress/compress  all nodes report one cluster  [BAD → OK]
16:35:18  WARN  node/state     compress/ov-03     local state is Donor/Desynced  [OK → WARN]
16:35:48  ERROR                compress/node/read@ov-03 no longer reported  [ERROR → gone]
```

A tick that changed nothing prints nothing — reprinting twenty `OK` lines every
ten seconds buries the one that matters. A finding that **disappeared** is
reported too, because during a repair that is usually the line you are waiting
for; an `OK` that stopped being reported is not, since the check simply did not
run this time.

`--watch` refuses an interval under a second (a busy loop against a cluster
that is already having a bad day) and refuses `--json` and `--findings`: those
emit one document per run, and a stream of documents is not a document.

With `--state`, each tick writes the file and reads back what it wrote, so the
rate checks grade the watch interval rather than the whole session.

It is not a daemon and not a monitoring system: it runs in the foreground,
holds its baseline in memory, and stops when you do.

## In a scheduled job

```sh
galera-doctor audit --config /etc/galera-doctor/clusters.json \
  --state /var/lib/galera-doctor/state.json \
  --findings --min-severity WARN --exit-on BAD > findings.json
```

The state file is written **after** the output, so a failure to persist a
baseline costs the next run its rates and nothing else.
