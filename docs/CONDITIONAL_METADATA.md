# Conditional metadata and comments (v1.2.2 compatibility branch)

`bd update ID --conditional-metadata-file request.json --json` applies a
complete metadata replacement and an ordered list of structured comments to
one regular issue in one SQL transaction. It is available only with the
direct SQL-server backend. Embedded, proxied, ephemeral, read-only template,
routed, abbreviated
and multi-ID operations are unsupported and fail without applying this request.
A same-ID wisp also refuses before mutation.
All ordinary `update` behavior remains unchanged.

```json
{
  "operation_id": "caller-stable-operation-identifier",
  "expected_metadata": {"existing": "value"},
  "metadata": {"existing": "value", "new": "value"},
  "comments": ["The comment to append with this transition."]
}
```

Use `-` for stdin. This flag cannot be combined with other update mutation
flags. `--actor`, `--json` and the ordinary connection/read-only controls still
apply. Copy the **entire** prior metadata object from `bd show --json`; this
command deliberately replaces metadata rather than using ordinary update's
top-level merge. Preserve every foreign and nested field that must survive.

The backend sets `_bd_conditional_revision` to a fresh UUID on every new
transition, including comment-only requests. Preserve the observed revision in
`expected_metadata`; the desired object may omit it or preserve its old value,
but cannot choose its successor. Two different operations from the same prior
state cannot both commit. The server's session conflict-commit and forced-commit
settings must both be disabled; the command refuses otherwise.

Retain the exact operation ID, original prior metadata, desired metadata,
ordered comments and actor across retries. A stored receipt is checked before
the stale-state comparison. An identical retry returns the frozen original
result with `replayed: true`, even after later successful operations, without
restoring old metadata or appending comments again. Reusing the ID with
different contents or actor fails. A native transaction conflict and an
indeterminate connection loss at commit are errors, not success; retry the
identical request to distinguish a committed receipt from a stale refusal.
Do not refresh the prior metadata and reuse the old operation ID.

The receipt is a versioned `updated` event in the existing events table; its
UUID is domain-separated from the issue and caller's operation ID. It stores a
SHA-256 request/actor fingerprint and the original result, including native
comment IDs and timestamps normalized to SQL's second precision. Metadata,
structured comments and the receipt commit or roll back together. The operation
issues no `DOLT_COMMIT` and preserves the normal server transaction policy.

The ordinary update hook runs best-effort after a newly committed transition.
Receipt replay does not repeat it. A crash between commit and hook execution
can miss that external hook; this API does not promise exactly-once external
effects. The atomic guarantee covers the ledger transition.

Inputs must be UTF-8 JSON objects, with no duplicate members, unknown request
fields or trailing values. Limits are 32 KiB per encoded request, 32 comments,
32 nesting levels and 10,000 values. The encoded receipt is bounded to 60,000
bytes before commit. Number spelling participates in identity/comparison;
numbers are never rounded through float64. Key order and whitespace do not
matter. An absent/SQL-NULL initial metadata value is observed as `{}`.
Unsupported legacy nonobject metadata refuses unchanged.
If the backend would round a JSON number or change its defined spelling, the
operation rolls back rather than return a value that was not persisted.

The guarantee applies to retained regular issues and their retained receipts.
Deletion/recreation, arbitrary restore/import, demotion, or native metadata
writes can invalidate the history. All writers of the guarded authority must
use this operation; a caller cannot treat an unrelated comment-only write as
part of the guarded transition. No migration, lock service, queue, new table,
or orchestration policy is introduced.

Design comparison: upstream PRs
[6030](https://github.com/gastownhall/beads/pull/6030) (jamelt),
[4682](https://github.com/gastownhall/beads/pull/4682) (julianknutsen), and
[6904](https://github.com/gastownhall/beads/pull/6904) (Mortsik) supplied relevant
revision-guard and stale-write testing precedents. Their modern storage/schema
dependencies are not imported into the schema-v53 compatibility line. This
change additionally requires atomic structured comments and durable replay;
it neither replaces nor closes those contributor proposals.
