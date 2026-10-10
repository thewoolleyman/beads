# JSON Output Schema Contract

Last reviewed: 2026-10-10

Freshness source: `cmd/bd/output.go`, `cmd/bd/errors.go`, and
`cmd/bd/protocol/json_contract_test.go`.

Commands using the shared `outputJSON` and JSON error helpers support an
optional envelope through `BD_JSON_ENVELOPE=1`. Raw JSON and JSONL paths,
including export, are separate contracts. A future default change is a plan,
not behavior implemented by this v1.2.2 compatibility source.

## Migration Guide

### Opt in to the envelope format

```bash
export BD_JSON_ENVELOPE=1
```

### Envelope format (BD_JSON_ENVELOPE=1)

The shared formatter wraps output as:

```json
{"schema_version": 1, "data": <original-payload>}
```

The original payload is untouched inside `.data` — no type corruption,
no field injection. Works identically for objects, arrays, and maps.

### Updating consumers

```bash
# Before (legacy):
bd list --json | jq '.[0].id'
bd show beads-abc --json | jq '.[0].title'

# After (envelope):
bd list --json | jq '.data[0].id'
bd show beads-abc --json | jq '.data[0].title'

# Version check:
bd show beads-abc --json | jq '.schema_version'
```

### Timeline

- **Current release**: Legacy format is default. Set `BD_JSON_ENVELOPE=1` to opt in.
  The shared formatter emits a deprecation notice once per process only when
  stderr is a terminal and envelope mode is disabled.
- **Future proposal**: make envelopes the default. Consumers must inspect the
  actual release contract before relying on a changed default or escape hatch.

## Schema Version

Current version: **1**

The `schema_version` field is an integer that increments when:
- Required fields are added, or existing fields are renamed or removed
- Output structure changes (e.g., nesting depth)
- Field types change (e.g., string to integer)

Additive changes (new optional fields) do NOT bump the version.

## Output Formats

### Envelope mode (BD_JSON_ENVELOPE=1)

Commands using the shared formatter emit a uniform envelope:

```json
{
  "schema_version": 1,
  "data": {
    "id": "beads-abc",
    "title": "Example issue",
    "status": "open"
  }
}
```

Arrays are wrapped the same way:

```json
{
  "schema_version": 1,
  "data": [
    {"id": "beads-abc", "title": "First"},
    {"id": "beads-def", "title": "Second"}
  ]
}
```

### Legacy mode (current default)

### Object results (for example, create and conditional metadata updates)

Commands that return a single issue or result emit a JSON object with
`schema_version` as a top-level field alongside the data:

```json
{
  "schema_version": 1,
  "id": "beads-abc",
  "title": "Example issue",
  "status": "open",
  "priority": 1,
  "issue_type": "task",
  "created_at": "2026-04-20T12:00:00Z"
}
```

### List commands (list, ready, search, stale, etc.)

Commands that return multiple items emit a raw JSON array:

```json
[
  {"id": "beads-abc", "title": "First", ...},
  {"id": "beads-def", "title": "Second", ...}
]
```

### Error output

Check both the exit status and the command's documented output channel.
`HandleErrorRespectJSON` and `HandleErrorWithHintRespectJSON` write JSON to
stdout; `HandleErrorWithHint` writes it to stderr. These helpers include
`error`, optional `hint`, and the schema version (outside `.data` in envelope
mode). The separate `outputJSONError` helper also supports `code`; callers
must not assume every error has that field or uses the same channel.

## Field Contracts by Command

### bd list --json

Required fields per item:
- `id` (string): Issue ID (e.g., "beads-abc")
- `title` (string): Issue title
- `status` (string): open, in_progress, closed, deferred
- `priority` (number): 0-4
- `issue_type` (string): bug, feature, task, epic, chore
- `created_at` (string): RFC3339 timestamp

Optional fields:
- `description`, `owner`, `updated_at`, `closed_at`
- `labels` (string[]): Attached labels
- `dependencies` (object[]): Dependency records
- `dependency_count`, `dependent_count`, `comment_count` (number)
- `parent` (string|null): Parent issue ID

### bd ready --json

Same schema as `bd list --json`. Items are filtered to unblocked issues only.
Each item includes `dependency_count`, `dependent_count`, `comment_count`,
and optional `parent` fields.

### bd blocked --json

Returns issues that are blocked by unresolved dependencies.
Each item includes all standard issue fields plus:
- `blocked_by_count` (number): Number of blocking dependencies
- `blocked_by` (string[]): IDs of blocking issues

### bd show --json

Returns an array, including when one ID is requested. In envelope mode the
array is under `.data`. Each entry has standard issue fields and may include
`description`, `acceptance_criteria`, and `dependencies`. Zero-valued optional
fields can be omitted. Default output carries `comment_count`; request
`--include-comments` for the full comment thread. A missing `comments` field
in default output does not establish that the issue has no comments.

### `import --json`

Returns a summary object when `--json` is active:
- `source` (string): File path or "stdin"
- `created` (number): Issues created
- `skipped` (number): Issues skipped (dedup)
- `dedup_skipped` (number): Issues skipped by `--dedup` title match
- `memories` (number): Memory records imported
- `ids` (string[]): IDs of created issues
- `dry_run` (boolean): Whether `--dry-run` was active

### bd export --json

Outputs JSONL (one JSON object per line), not wrapped in an envelope.
Each line is a self-contained issue or memory record with `record_type`.
This direct JSONL writer does not inject `schema_version` per line.

## Consumer Guidelines

1. **Check `schema_version`** on object output. If the version is
   higher than expected, log a warning but attempt to parse anyway
   (additive changes are backward-compatible).

2. **For collection results**, parse the raw array in legacy mode or the
   `.data` array in envelope mode. `show` and `close` also return collections.

3. **Ignore unknown fields**. New fields may be added without bumping
   the schema version.

4. **Use `--json` flag**, not `--format json`. The `--json` flag is
   the stable contract; `--format` is for human-readable variants.
