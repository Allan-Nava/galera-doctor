# Checks

Each finding carries `check`, `target`, `status`, `message`, a `value`/`unit`
where there is a number, and a `hint` that says what it means operationally.
Worst first, always.

`ERROR` sorts above `BAD`: a node that could not be read invalidates every
cluster-wide statement made without it, and that has to be seen first.

## The cluster as a whole

| Check | Grades |
|---|---|
| `node/read` | the node answered at all. ERROR — every cluster finding below is conditional on it |
| `node/not-galera` | `wsrep_provider` unset: a standalone server, excluded from every comparison |
| `cluster/uuid` | nodes reporting different state UUIDs — one name, two clusters |
| `cluster/conf-id` | nodes disagreeing about the membership generation (usually a change caught mid-flight) |
| `cluster/primary` | a node outside the Primary component: it refuses writes and serves stale reads |
| `cluster/size` | membership size against `--expect-nodes`; nodes *disagreeing* about it beats the size itself |
| `cluster/versions`, `cluster/provider-version` | mixed builds — expected during a rolling upgrade, a liability afterwards |

## Per node

| Check | Grades |
|---|---|
| `node/ready` | `wsrep_ready` OFF: queries touching replicated tables are refused |
| `node/connected` | `wsrep_connected` OFF: not in the group communication at all |
| `node/wsrep-on` | `wsrep_on` OFF: writes here are not replicated and will conflict later |
| `node/state` | the local state comment. Synced is OK, Donor/Desynced and Joined are warnings, anything else is BAD |
| `node/desync` | `wsrep_desync` left ON after a backup or a schema change |
| `node/read-only` | `read_only` ON — a failover that never finished, unless it is deliberate |
| `queue/recv`, `queue/send` | instantaneous queue depths: the node about to flow-control |

## The ones no metric shows

### `systables/drift`

The column definitions of every table in the `mysql` schema, fingerprinted per
node and compared.

Galera replicates the application's DDL. It does not replicate the maintenance
that happens to the server's own tables, so two nodes can hold different
definitions of `mysql.column_stats` — or of whatever `mysql_upgrade` touched on
one node and not the others — indefinitely, while every `wsrep_*` counter reads
perfectly healthy. The symptom arrives much later as a query plan that differs
per node, or one node's error log filling up.

A node without `SELECT` on `information_schema` is reported as **not audited**
rather than silently left out of the comparison.

### `schema/drift`

The same fingerprint comparison over the **application** base tables, keyed
`schema.table`.

Galera *does* replicate this DDL, and that is what makes it a different
diagnosis rather than the same check pointed at another schema: a difference
here is a schema change that did not finish — a failed `ALTER`, one applied on
a single node by hand, or one that landed while a node was desynced. The
counters stay green because replication is not broken; it carried what it was
given.

The fix is per node too, but it is *re-apply the change*, not `mysql_upgrade`.

A node that missed a whole schema change drifts on every table at once, so past
five tables the finding becomes one line with the count — four hundred findings
would bury the rest of the report. Views are excluded: their columns are
derived from tables that are already being compared. A node without `SELECT` on
`information_schema` is **not audited** rather than dropped from the
comparison, and a cluster with no application tables at all is quiet — "there
are none" and "they could not be read" are different findings.

`--no-schema` skips this read and the primary key one with it: on an instance
with tens of thousands of tables they are the only part of the audit that costs
more than microseconds.

### `schema/no-pk`

Application tables without a primary key, as a union across nodes. Galera's
row-based certification applies these with a full-row scan on every node, and
`DELETE`s can apply in a different order — the documentation calls it
unsupported.

### `gcache/window`

Not the gcache size: the **time** it buys at the current write rate. 512 MB is
forty minutes or ninety seconds depending on the workload, and the number
decides whether a restarting node catches up incrementally or needs a full SST
— which also takes a donor out of service. Needs `--state`; see
[rates, not totals](rates.md).

## Against the proxy

With `--proxysql`, the proxy's view is compared with the cluster's — the
interesting state being the two disagreeing, which neither dashboard shows.

| Check | Grades |
|---|---|
| `proxysql/read` | the admin interface answered. ERROR; the cluster findings still stand |
| `proxysql/missing` | a cluster node in no serving hostgroup: capacity nothing reaches, and a failover target that cannot take over |
| `proxysql/disagreement` | ONLINE while the node reports Joined (BAD), or shunned while the node reports Synced (WARN) |
| `proxysql/hostgroup` | a hostgroup with servers configured and none ONLINE: every query routed there fails |
| `proxysql/mapping` | the writer/reader/offline mapping, reported and **not graded** |

The offline hostgroup — 999 in most deployments — is where ProxySQL's own Galera
monitor parks nodes. Its contents are never a finding: flagging them is a
permanent false positive, and "cleaning it up" fights the monitor.
