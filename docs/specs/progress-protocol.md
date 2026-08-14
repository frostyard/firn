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
| `step_progress` | `index`, `fraction` (0.0–1.0) and/or `bytes`, `total_bytes` | Optional fine-grained progress within a step (e.g. image streaming). |
| `info` | `message` | Human-readable narration; no machine meaning. |
| `warning` | `code` (string, stable), `message` | Non-fatal degradation (e.g. `no_tpm`, `flatpak_unreachable`). |
| `summary` | `items` (array of `{code, detail}`) | Once, before `done`: everything the user must know (unreachable flatpaks, skipped enrollments). |
| `recovery_key` | `key` | Deliberate secret disclosure of a generated recovery key. The only event ever containing a secret; in `--json-progress` mode the literal key is present on stdout. |
| `done` | `ok` (bool, `true`), `boot_entry` (string, optional) | Successful completion; final event. |
| `error` | `step` (name or `null`), `code`, `message` | Fatal failure; final event. Exit status is non-zero. |

```json
{"event":"start","protocol":1,"firn":"0.1.0","steps":[{"name":"preflight","weight":2},{"name":"partition","weight":5}]}
{"event":"step_start","index":0,"name":"preflight"}
{"event":"warning","code":"no_tpm","message":"no TPM device; skipping enrollment"}
{"event":"done","ok":true,"boot_entry":"0003"}
```

## Rules

1. Exactly one `start` event, first; exactly one terminal event (`done`
   or `error`), last. A stream without a terminal event means firn died:
   consumers MUST treat it as failure.
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
| TUI progress view | Renders the same event structs pre-serialization. |
| E2E test assertions | Tests assert on `code` values and terminal events. |

## References

- Rationale: [ADR-0007](../adr/0007-tui-only-frontend-single-binary.md)
- Context: [design/architecture.md](../design/architecture.md)
