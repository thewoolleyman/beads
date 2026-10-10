# OpenTelemetry Data Model

Last reviewed: 2026-10-10

Freshness source: `internal/telemetry/`, `internal/storage/dolt/store.go`,
`internal/compact/haiku.go`, `cmd/bd/find_duplicates.go`, and hook execution
code under `internal/hooks/`.

Selected instrumentation inventory for the v1.2.2 compatibility source.
Telemetry is opt-in through `BD_OTEL_METRICS_URL` or `BD_OTEL_STDOUT=true`.
Metrics export to the explicitly configured URL and/or stdout; there is no
default VictoriaMetrics destination. Traces currently export only to stdout.
`BD_OTEL_LOGS_URL` is reserved, not an implemented logs exporter. Not every
span has a corresponding metric.

The root command span carries `bd.command`, `bd.version`, and `bd.args`;
`bd.actor` is added after actor resolution. Parent context does not copy these
attributes onto every child span.

---

## Metric Naming Convention

OTel SDK names use **dot notation** internally. Prometheus-compatible backends (VictoriaMetrics, Prometheus) export these as **underscore-separated** names, appending type suffixes:

| Code name (SDK) | Exported name (Prometheus/VM) |
|-----------------|-------------------------------|
| `bd.storage.operations` | `bd_storage_operations_total` |
| `bd.storage.operation.duration` | `bd_storage_operation_duration_ms` |
| `bd.storage.errors` | `bd_storage_errors_total` |
| `bd.issue.count` | `bd_issue_count` |
| `bd.db.retry_count` | `bd_db_retry_count_total` |
| `bd.db.lock_wait_ms` | `bd_db_lock_wait_ms` |
| `bd.db.circuit_trips` | `bd_db_circuit_trips_total` |
| `bd.db.circuit_rejected` | `bd_db_circuit_rejected_total` |
| `bd.ai.input_tokens` | `bd_ai_input_tokens_total` |
| `bd.ai.output_tokens` | `bd_ai_output_tokens_total` |
| `bd.ai.request.duration` | `bd_ai_request_duration_ms` |

---

## Event Index

| Event | Category | Status |
|-------|----------|--------|
| `bd.command` | CLI | ✅ Implemented |
| `storage.*` | Storage | Decorator implemented; not wired into CLI storage |
| `dolt.*` | Dolt Backend | ✅ Implemented |
| `doltserver.*` | Server Lifecycle | 🔲 Roadmap (Tier 1) |
| `hook.exec` | Hooks | ✅ Implemented (span only) |
| `anthropic.messages.new` | AI | ✅ Implemented |

---

## 1. Identity Hierarchy

### 1.1 Instance

Resources use the OTel host and process detectors plus service attributes.
There is no synthesized host-plus-working-directory identity or `WithOS`
resource configured by this initialization path.

| Attribute | Type | Description |
|---|---|---|
| `host.name` | string | Host detector name |
| `process.pid` | integer | Process detector identity |
| `service.name` | string | Configured Beads service identity |

### 1.2 Command

Each `bd` command execution generates a span with full context.

| Attribute | Type | Source |
|---|---|---|
| `bd.command` | string | Subcommand name (`create`, `list`, `show`, etc.) |
| `bd.version` | string | Current bd version string |
| `bd.args` | string | Full argument list |
| `bd.actor` | string | Actor identity — set after actor resolution (may lag span start) |

---

## 2. CLI Command Events

### `bd.command.<name>`

Emitted once per `bd` subcommand execution. Anchors all subsequent events for that command. The span name is `bd.command.` + command name (e.g. `bd.command.create`).

| Attribute | Type | Description |
|---|---|---|
| `bd.command` | string | Subcommand name |
| `bd.version` | string | bd version |
| `bd.args` | string | Full arguments passed to command |
| `bd.actor` | string | Actor identity (set after actor resolution) |

---

## 3. Storage Events

These events belong to `InstrumentedStorage`, constructed by `WrapStorage`.
This source tree defines the wrapper but does not call it from the CLI.
Consequently these definitions do not establish emission on current CLI
storage operations. A caller must explicitly install the decorator.

### `storage.CreateIssue`

Emitted when an issue is created.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"CreateIssue"` |
| `bd.issue.type` | string | Issue type (`task`, `epic`, `merge-request`, etc.) |
| `bd.actor` | string | Actor creating the issue |

### `storage.UpdateIssue`

Emitted when an issue is updated.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"UpdateIssue"` |
| `bd.issue.id` | string | Issue ID being updated |
| `bd.update.count` | int | Number of fields being updated |
| `bd.actor` | string | Actor updating the issue |

### `storage.GetIssue`

Emitted when an issue is retrieved.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"GetIssue"` |
| `bd.issue.id` | string | Issue ID being retrieved |

### `storage.SearchIssues`

Emitted when searching for issues.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"SearchIssues"` |
| `bd.query` | string | Search query string |
| `bd.result.count` | int | Number of results returned |

### `storage.GetReadyWork`

Emitted when querying for ready work.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"GetReadyWork"` |
| `bd.result.count` | int | Number of ready issues returned |

### `storage.GetBlockedIssues`

Emitted when querying for blocked issues.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"GetBlockedIssues"` |
| `bd.result.count` | int | Number of blocked issues returned |

### `storage.RunInTransaction`

Emitted when executing a transaction.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"RunInTransaction"` |
| `db.commit_msg` | string | Commit message |

---

## 4. Dolt Backend Events

### `dolt.query`

Emitted for each SQL read query via `queryContext()`.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"query"` |
| `db.statement` | string | SQL statement (truncated to 300 chars) |
| `db.system` | string | `"dolt"` |
| `db.readonly` | bool | Whether store is read-only |

### `dolt.exec`

Emitted for each SQL write statement via `execContext()`.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"exec"` |
| `db.statement` | string | SQL statement (truncated to 300 chars) |
| `db.system` | string | `"dolt"` |

### `dolt.query_row`

Emitted for single-row queries via `queryRowContext()`.

| Attribute | Type | Description |
|---|---|---|
| `db.operation` | string | `"query_row"` |
| `db.statement` | string | SQL statement (truncated to 300 chars) |
| `db.system` | string | `"dolt"` |

### `dolt.commit`

Emitted for DOLT_COMMIT operations.

| Attribute | Type | Description |
|---|---|---|
| `commit_msg` | string | Commit message |

### `dolt.push`

Emitted for DOLT_PUSH operations.

| Attribute | Type | Description |
|---|---|---|
| `dolt.branch` | string | Branch being pushed |

### `dolt.force_push`

The force-push path has its own span, distinct from ordinary `dolt.push`.

### `dolt.pull`

Emitted for DOLT_PULL operations.

| Attribute | Type | Description |
|---|---|---|
| `dolt.branch` | string | Branch being pulled |

### `dolt.merge`

Emitted for DOLT_MERGE operations.

| Attribute | Type | Description |
|---|---|---|
| `dolt.merge_branch` | string | Branch being merged |

### `dolt.branch`

Emitted for DOLT_BRANCH operations.

| Attribute | Type | Description |
|---|---|---|
| `dolt.branch` | string | Branch name |

### `dolt.checkout`

Emitted for DOLT_CHECKOUT operations.

| Attribute | Type | Description |
|---|---|---|
| `dolt.branch` | string | Branch being checked out |

---

## 5. Dolt Server Events (Roadmap — not yet implemented)

`internal/doltserver/` has no OTel imports. The events below are planned for Tier 1.

### `doltserver.start` *(planned)*

| Attribute | Type | Description |
|---|---|---|
| `port` | int | Port server is listening on |
| `data_dir` | string | Path to Dolt data directory |
| `pid` | int | Process ID of server |

### `doltserver.stop` *(planned)*

| Attribute | Type | Description |
|---|---|---|
| `pid` | int | Process ID of stopped server |
| `reason` | string | Stop reason (`graceful`, `forced`, `idle_timeout`, `crash`) |

---

## 6. Hooks Events

### `hook.exec`

Emitted for hook execution. **Span only** — no metric counters or histograms exist for hooks. Duration aggregation is a Tier 3 roadmap item.

| Attribute | Type | Description |
|---|---|---|
| `hook.event` | string | Event type (`create`, `update`, `close`, `delete`, etc.) |
| `hook.path` | string | Absolute path to hook script |
| `bd.issue_id` | string | ID of triggering issue |

Stdout/stderr are added as span **events** (not attributes):
- `hook.stdout` event: `output` (string, truncated), `bytes` (int, original size)
- `hook.stderr` event: `output` (string, truncated), `bytes` (int, original size)

---

## 7. AI Events

Emitted by the compaction engine (`bd compact`) via `internal/compact/haiku.go`, and by duplicate detection (`bd find-duplicates --method ai`) via `cmd/bd/find_duplicates.go`. Both use the Anthropic SDK directly via `ANTHROPIC_API_KEY`.

> **Note**: Only `compact/haiku.go` records to the `bd.ai.*` OTel metric instruments. `find_duplicates.go` records token counts as span attributes only.

### `anthropic.messages.new`

One span per Anthropic API call. The `bd.ai.operation` attribute distinguishes the two callers.

| Attribute | Type | Description |
|---|---|---|
| `bd.ai.model` | string | Model used (e.g. `"claude-haiku-4-5"`) |
| `bd.ai.operation` | string | `"compact"` or `"find_duplicates"` |
| `bd.ai.input_tokens` | int | Input tokens consumed |
| `bd.ai.output_tokens` | int | Output tokens generated |
| `bd.ai.attempts` | int | Compaction only: attempts including retries |
| `bd.ai.batch_size` | int | Candidate pairs evaluated (`find_duplicates` only) |

**Compaction retry policy**: initial attempt plus up to three retries with
exponential backoff for retryable errors. Duplicate detection does not record
`bd.ai.attempts` or use this custom retry loop.

---

## 8. Selected Metrics Reference

| Metric (code name) | Type | Labels | Status |
|--------|------|--------|--------|
| `bd.storage.operations` | Counter | `db.operation` + method attributes | Decorator only |
| `bd.storage.operation.duration` | Histogram (ms) | Method attributes, without `db.operation` | Decorator only |
| `bd.storage.errors` | Counter | Method attributes, without `db.operation` | Decorator only |
| `bd.issue.count` | Gauge | `status` | Decorator only |
| `bd.db.retry_count` | Counter | — | ✅ Implemented |
| `bd.db.lock_wait_ms` | Histogram | — | 🔲 Registered, not recorded |
| `bd.db.circuit_trips` | Counter | — | ✅ Implemented |
| `bd.db.circuit_rejected` | Counter | — | ✅ Implemented |
| `bd.ai.input_tokens` | Counter | `bd.ai.model` | ✅ Implemented (compact only) |
| `bd.ai.output_tokens` | Counter | `bd.ai.model` | ✅ Implemented (compact only) |
| `bd.ai.request.duration` | Histogram (ms) | `bd.ai.model` | ✅ Implemented (compact only) |

---

## 9. Recommended Indexed Attributes

```
host.name, process.pid, service.name, bd.command, bd.version, bd.actor, db.operation, db.statement,
bd.issue.id, bd.issue.type, hook.event, hook.path, bd.ai.model, bd.ai.operation
```

---

## 10. Configuration and Backend

Environment variables, backend compatibility, Dolt system tables, and roadmap are documented in [otel-architecture.md](otel-architecture.md) to avoid duplication.

Active exporter variables: `BD_OTEL_METRICS_URL`, `BD_OTEL_STDOUT`.
`BD_OTEL_LOGS_URL` is reserved.

---

## Appendix: Current Source Review

The review date covers source inspection, not evidence that exporters were run.
Use symbols rather than old line numbers from historical `main@371df32b`:

| Surface | Source |
| --- | --- |
| Resource and exporter configuration | `internal/telemetry/telemetry.go` initialization |
| Storage decorator and metric labels | `internal/telemetry/storage.go`, `WrapStorage` and operation recording |
| Dolt spans and instrument registration | `internal/storage/dolt/store.go` |
| Serialization/write retries | `internal/storage/dolt/transaction.go` |
| AI compaction retry and metrics | `internal/compact/haiku.go` |
| Duplicate detection spans | `cmd/bd/find_duplicates.go` |
| Hook spans and output events | `internal/hooks/` |

The selected table above is not exhaustive. Dolt also registers serialization
errors, write retries, connection acquisition, and pool wait/open/in-use/idle/
maximum instruments; inspect `store.go` and `transaction.go` for their exact
names and recording sites. Server-lifecycle events remain labelled roadmap.
