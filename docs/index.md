# galera-doctor

**A read-only audit of a Galera cluster that reports the states its own metrics
cannot show you.**

- [Install](install.md)
- [Usage](usage.md) — flags, config, output, exit status
- [Checks](checks.md) — every check, what it grades and why
- [Rates, not totals](rates.md) — the state file, and why a counter is never a verdict
- [Permissions and safety](safety.md) — the grants it needs, the writes it cannot do

## The one-minute version

```console
$ galera-doctor audit --config clusters.json --state /var/lib/galera-doctor/compress.json
BAD   compress  3 node(s)
  BAD   systables/drift   mysql.column_stats  definition differs across nodes: aaaaaaaaaaaa… (ov-03, sg-01) vs cccccccccccc… (cl-02)
        ↳ Galera does not replicate this: fix it per node — no wsrep_* metric will ever show it
  WARN  flow/paused       sg-01               flow-controlled 2.10% of the last 10m0s
  OK    cluster/size      compress            3 member(s), expected 3
```

Three nodes, one cluster, every replication counter green — and one of them has
been holding a different definition of a system table since somebody ran
`mysql_upgrade` on two of the three.
