# Permissions and safety

## The grants

```sql
CREATE USER 'audit'@'10.%' IDENTIFIED BY '…';
GRANT USAGE, PROCESS, SELECT, REPLICATION CLIENT ON *.* TO 'audit'@'10.%';
```

- `USAGE` and `PROCESS` cover `SHOW GLOBAL STATUS` and `SHOW GLOBAL VARIABLES`.
- `REPLICATION CLIENT` covers `SHOW REPLICA STATUS` and `SHOW REPLICAS`, which
  is how [`repl/async-in` and `repl/async-out`](checks.md#repl-async-in-repl-async-out)
  see a write path the cluster is not part of. Without it those reads are
  refused and [`audit/coverage`](checks.md#auditcoverage) names the node whose
  replication status could not be read — the audit still runs, it just says
  what it could not look at.
- `SELECT` is what `information_schema` needs for the system table drift
  comparison and the primary key check.

Without the `information_schema` access, the drift check reports each node as
**not audited** instead of quietly comparing fewer nodes than it claims. Run
with `--no-systables --no-schema` if you would rather not grant it.

For `--proxysql`, a read-only admin user on the admin interface (default port
6032) is enough: the tool reads `runtime_mysql_servers` and
`runtime_mysql_galera_hostgroups`.

## Why it cannot write

The promise is mechanical, not editorial:

1. Every database access in the tool goes through one function, `cluster.Query`,
   which matches the statement against `^\s*(SHOW|SELECT)\s` and refuses
   anything else.
2. A test hands it `UPDATE`, `DELETE`, `SET GLOBAL`, `FLUSH`, `DROP` and
   `INSERT` and requires all of them to be rejected.
3. CI greps the source for a writing statement and for `db.Exec`, and fails the
   build on either.

This matters because the tool is most useful during an incident, which is
exactly when nobody has the patience to audit what a binary might do to a
cluster.

## Cost on the server

- Two `SHOW GLOBAL` reads per node: microseconds.
- One `information_schema.COLUMNS` read restricted to the `mysql` schema: tens
  of milliseconds on a normal instance.
- One `information_schema.TABLES`/`TABLE_CONSTRAINTS` join: this is the only
  query that can be slow on an instance with tens of thousands of tables. Turn
  it off with `--no-schema`.

Nodes are read concurrently, one connection each, closed at the end of the run.

## Secrets

A DSN — and therefore a password — is never printed. Findings identify nodes by
their configured name, and driver errors are redacted before they are shown,
because an error message ends up pasted into a ticket. Configuration files can
keep passwords out entirely with `${ENV_VAR}` references.
