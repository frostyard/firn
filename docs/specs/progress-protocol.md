# Spec: Firn progress protocol (version 1)

This contract governs the machine-readable event stream firn emits with
`--json-progress` — the only supported interface for external consumers
(automation, tests, any future GUI), per
[ADR-0007](../adr/0007-tui-only-frontend-single-binary.md). The in-process
TUI consumes the same event model over a Go channel; this spec pins its
serialized form. It replaces fisherman's event stream and
`snosi-install`'s proto-1.

## Interface

Framing: newline-delimited JSON (NDJSON) on **stdout**; one complete JSON
object per line; nothing else is written to stdout while the flag is
active (human/log output goes to stderr). Every event carries `event`
(string type tag).

| Event | Fields | Meaning |
| --- | --- | --- |
| `start` | `protocol` (int, `1`), `firn` (version string), `steps` (array of `{name, weight}`) | First event, exactly once. `steps` is the fully assembled pipeline, in order. |
| `step_start` | `index` (int, 0-based into `steps`), `name` | A step began. Strictly increasing `index`. |
| `step_progress` | `index`, `fraction` (0.0–1.0), `bytes`, `total_bytes` | Optional fine-grained progress within a step (e.g. image streaming). All fields are present: `total_bytes > 0` selects byte mode; otherwise `fraction` is authoritative, including `0.0`. Unused numeric fields are zero. |
| `info` | `message` | Human-readable narration; no machine meaning. |
| `warning` | `code` (string, stable), `message` | Non-fatal degradation (e.g. `no_tpm`, `flatpak_unreachable`). |
| `summary` | `items` (array of `{code, detail}`) | Once when non-empty, after cleanup warnings and immediately before either terminal event: everything the user must know (unreachable flatpaks, skipped enrollments), on success or failure. |
| `recovery_key` | `key` | Deliberate secret disclosure of a generated recovery key. The only event ever containing a secret; in `--json-progress` mode the literal key is present on stdout. |
| `done` | `ok` (bool, `true`) | Successful completion; final event. |
| `error` | `step` (string; empty for a run-level failure), `code`, `message` | Fatal failure; final event. Exit status is non-zero. |

An early version-1 draft listed an optional `done.boot_entry`, but no producer
ever emitted it and neither bundled consumer read it. It is not part of the
version-1 wire contract.

```json
{"event":"start","protocol":1,"firn":"0.1.0","steps":[{"name":"preflight","weight":2},{"name":"partition","weight":5}]}
{"event":"step_start","index":0,"name":"preflight"}
{"event":"warning","code":"no_tpm","message":"no TPM device; skipping enrollment"}
{"event":"done","ok":true}
```

## Rules

1. Exactly one `start` event, first; exactly one terminal event (`done`
   or `error`), last. A stream without a terminal event means firn died:
   consumers MUST treat it as failure. Cleanup warnings precede a non-empty
   `summary`, and that summary immediately precedes the terminal event on both
   success and failure. Firn's emitters reject any event after a terminal
   event; closing an in-process producer without one reports a truncated
   stream.
2. Consumers MUST ignore unknown event types and unknown fields;
   producers MAY add both within protocol 1. Removing or re-typing an
   existing field requires incrementing `protocol`.
3. `warning`/`summary`/`error` `code` values are stable identifiers:
   documented in this spec's companion table as they are added, never
   renamed within a protocol version.
4. No event except `recovery_key` may contain secret material; recipes,
   passphrases, password hashes, and key file contents MUST NOT appear
   in any `message` or `detail`.
5. Every line is valid standalone JSON under 64 KiB; consumers may
   process the stream with a line reader and no buffering lookahead.
6. Event order is the source of truth for progress; wall-clock pacing
   carries no meaning.

### Stable codes

| Code | Event surfaces | Meaning |
| --- | --- | --- |
| `cleanup_failed` | `warning` | A registered pipeline cleanup failed; the terminal event still follows and the run is unsuccessful. |
| `flatpak_unreachable` | `warning`, `summary` | A requested Flatpak could not be reached and was not installed. |
| `group_missing` | `warning`, `summary` | A requested supplementary user group was absent from the installed image. |
| `image_verification_failed` | `error` | Bootc image digest resolution or cosign verification failed in `preflight-image`; no destructive step has run. |
| `no_core_set` | `warning`, `summary` | Core Flatpaks were requested but the selected image publishes no core set. |
| `no_tpm` | `warning` | No TPM was detected; only choices that do not require it remain valid. |
| `step_failed` | `error` | A pipeline step failed without a more specific stable code. |
| `store_cleanup_failed` | `warning` | Cleanup of the redirected bootc container-image store failed. |
| `store_umount_failed` | `warning` | Unmounting the redirected bootc container-image store failed. |
| `stream_truncated` | consumer-synthesized `error` | The producer stream ended without `done` or `error`; consumers render this as failure, never user cancellation. |

## Recovery-key disclosure

Recovery keys deliberately have different presentation boundaries for the
two frontends:

- **Interactive:** the `recovery_key` event remains in-process. The TUI shows
  the literal key on a blocking acknowledgement screen, clears it from the
  returned result after acknowledgement, and MUST NOT repeat it to stdout,
  stderr, console scrollback, or ordinary logs after the TUI exits.
- **Headless with `--json-progress`:** the literal key is the `key` field of
  the `recovery_key` NDJSON event on stdout. Consumers MUST treat the stream
  as secret-bearing and keep it out of ordinary logs.
- **Headless human output:** without `--json-progress`, the human renderer
  prints the literal key to stderr. This is deliberate because there is no
  interactive acknowledgement screen; callers are responsible for securing
  stderr. For A/B recipes, `security.recovery_key_out` may additionally write
  the key to the explicitly requested 0600 file described by the
  [recipe schema](recipe-schema.md#security).

These are disclosure surfaces, not narration: no `info`, `warning`,
`summary`, `error`, recipe diagnostic, or post-TUI reproduction message may
contain the key.

## Derived artifacts

| Artifact | Derivation |
| --- | --- |
| TUI progress view | Renders the same event structs pre-serialization. Warning and error stable codes remain visible alongside their messages; `flatpak_unreachable` is expanded into an intelligible sentence without dropping the code. A closed channel without a terminal event becomes `stream_truncated`. |
| Headless human renderer | Renders every event kind to stderr, including fraction- and byte-based `step_progress`; the NDJSON stdout contract is unchanged. |
| E2E test assertions | Tests assert on `code` values and terminal events. |

## References

- Rationale: [ADR-0007](../adr/0007-tui-only-frontend-single-binary.md)
- Context: [design/architecture.md](../design/architecture.md)
