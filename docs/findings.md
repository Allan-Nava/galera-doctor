# The findings contract

`--findings` emits a flat JSON array of findings. It is the interface another
tool consumes — [checkfleet](https://github.com/Allan-Nava/checkfleet) emits the
same findings under a `galera` module — so this page describes it as a promise
rather than as a description of what the code happens to do today.

```console
$ galera-doctor audit --config clusters.json --findings --min-severity WARN
```

```json
[
  {
    "check": "flow/paused",
    "target": "sg-01",
    "status": "WARN",
    "message": "flow-controlled 2.10% of the last 10m0s",
    "value": 42.5,
    "unit": "percent",
    "hint": "this node is intermittently the slowest in the cluster"
  }
]
```

## The fields

| Field | Type | Always present | What it is |
|---|---|---|---|
| `check` | string | yes | the analysis that produced it — `flow/paused`, `systables/drift`, … The list is `galera-doctor checks` |
| `target` | string | yes | what was looked at: a node name, a table, or the cluster name |
| `status` | string | yes | `OK`, `WARN`, `BAD` or `ERROR`, in that severity order |
| `message` | string | yes | the measurement, in a sentence. **Prose: never parse it** |
| `value` | number | no | the number behind the finding, when there is one |
| `unit` | string | no | what `value` counts: `seconds`, `bytes`, `tables`, `nodes`, … |
| `hint` | string | no | what it means for whoever is on call |

## The promises

- **The array is an array.** A run with nothing to report emits `[]`, never
  `null`. A consumer iterating it does not have to special-case a healthy
  cluster.
- **Worst first.** `ERROR`, then `BAD`, `WARN`, `OK`. Taking the first element
  takes the most severe finding, and that is intended rather than an accident of
  ordering.
- **`ERROR` sorts above `BAD`** because nothing below it can be concluded: a node
  that was not read is not a node that is healthy.
- **A number lives in `value`, never only in `message`.** The message is written
  for a person and its wording changes; the pair `value`/`unit` is what a machine
  reads.
- **A DSN never appears** in any field. Nodes are identified by name and driver
  errors are redacted, because findings end up in tickets.
- **Exit status is not severity.** The process exits `0` whenever the audit ran,
  whatever it found; `1` only with `--exit-on`, and `2` for a usage error. See
  [exit status](usage.md#exit-status).

## Changing it

The field names above are frozen by a test that compares the whole document
(`TestTheFindingsContractIsFrozen`). When that test fails the question is not
how to make it pass: it is whether this is a breaking change to a published
interface, and therefore whether it belongs in a major release.

Adding an **optional** field is not a breaking change and does not need one.
