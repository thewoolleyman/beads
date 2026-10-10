# Error Handling in Beads

Last reviewed: 2026-10-10

Freshness source: `cmd/bd/*.go`, especially `cmd/bd/errors.go`, and
`internal/storage/dolt/conditional_metadata.go`.

## Return errors from commands

New Cobra commands should use `RunE` and return errors through the existing
helpers. Returning preserves deferred command metrics and main cleanup.
Do not call `os.Exit` from a command handler.

```go
RunE: func(cmd *cobra.Command, args []string) error {
    if err := performOperation(cmd.Context()); err != nil {
        return HandleErrorRespectJSON("operation failed: %v", err)
    }
    return nil
},
```

`HandleError` prints a human-readable error to stderr and returns an
`exitError` with code 1. `HandleErrorRespectJSON` uses JSON on stdout when
JSON output is enabled. Use the hint variants when there is a concrete
recovery action; a hint must not imply that a failed operation succeeded.
`SilentExit` returns the failure code after a caller has already reported
its diagnostic.

## Existing helper channels

| Helper | Human output | JSON output | Result |
| --- | --- | --- | --- |
| `HandleError` | stderr | human text on stderr | returned error |
| `HandleErrorRespectJSON` | stderr | stdout | returned error |
| `HandleErrorWithHint` | stderr | stderr | returned error |
| `HandleErrorWithHintRespectJSON` | stderr | stdout | returned error |
| `WarnError` | stderr | warning text on stderr | no failure return |

The JSON-aware helpers honor the optional envelope and schema version.
Their payload contains `error` and optional `hint`; it does not universally
include a `code`. The separate `outputJSONError` formatter has a coded shape.
See [JSON_SCHEMA.md](JSON_SCHEMA.md) before writing a consumer.

`FatalError` and its variants already exist for legacy proxied-server paths.
They call `os.Exit` and bypass deferred metrics and main cleanup; they are
not the pattern for new `RunE` handlers. The source documents that proxied
mode is currently rejected during initialization, leaving that telemetry gap
latent until the mode is implemented.

## Decide whether failure invalidates the result

A primary operation failure must propagate to the caller. Required metadata,
receipt, audit, or comment writes are part of the operation when correctness
depends on them. For example, a conditional metadata transition rolls back
if its native comment or receipt insertion fails. Metadata is not inherently
best-effort merely because it is stored alongside the main issue record.

Use a warning only for an ancillary action whose failure does not invalidate
the promised result, such as an optional diagnostic. Make partial success
explicit when the command contract permits it; do not print a success receipt
for an incomplete transaction.

At storage boundaries, return or wrap the underlying error with useful
operation context. Let the transaction owner perform rollback and preserve
both the failure and retry semantics. Avoid logging credentials or dumping
request metadata into errors.

## Cleanup and tests

Best-effort cleanup may ignore an error only when it cannot change the
reported operation result. Propagate flush, close, or finalization errors
when durable output depends on them. Deferred cleanup should still execute
on command failure, which is another reason to return rather than exit.

Tests should exercise the nonzero error result and the relevant stdout/stderr
channel. For JSON-aware paths, cover both legacy and envelope modes. For
transactional writes, inject a failure after the first write and assert that
no partial metadata, comments, or receipt survive. Do not test failure only
by matching a printed string while allowing a success exit status.
