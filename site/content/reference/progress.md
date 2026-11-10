---
description: Consume protocol-1 NDJSON safely, recognize completion and protect recovery-key disclosure.
---

# Progress events

`firn install --json-progress` exposes the supported machine-readable progress interface. It sends newline-delimited JSON to stdout, one complete object per line. Human diagnostics go to stderr.

This page summarizes the stream. The [full protocol specification](https://github.com/frostyard/firn/blob/main/docs/specs/progress-protocol.md) defines every field and stable code.

## Event sequence

| Event | Meaning |
| --- | --- |
| `start` | First event, once; `protocol = 1`, firn version and ordered step names with weights. |
| `step_start` | A step begins; index is 0-based and increases. |
| `step_progress` | Optional movement within a step: fraction or bytes. |
| `info` | Human narration; do not parse it for machine meaning. |
| `warning` | Non-fatal degradation with a stable `code`. |
| `recovery_key` | Deliberate disclosure of a generated recovery key. |
| `summary` | Non-empty accumulated items, after cleanup warnings and immediately before the terminal event. |
| `done` | Successful terminal event, with `ok = true`. |
| `error` | Failed terminal event, with step, stable code and message; process exits non-zero. |

A protocol consumer must:

1. Check the `start` protocol version.
2. Ignore unknown event types and fields within protocol 1.
3. Use event order, not timing, as the source of truth.
4. Require exactly 1 terminal event, `done` or `error`, at the end.
5. Treat a stream ending without a terminal event as failure, not cancellation or success.

Argument and recipe-validation errors can occur before the stream starts. Check the process exit status and stderr too.

## Measure progress

`start.steps` provides the assembled steps and weights for overall progress. `step_progress` includes `index`, `fraction`, `bytes` and `total_bytes` on every event. When `total_bytes > 0`, use byte progress; otherwise use `fraction`, including 0.0. Unused numeric fields are zero.

No wall-clock pacing is promised. A step without fine-grained updates can still be running.

## Recovery-key handling

!!! danger "The stream can contain a secret"

    A `recovery_key` event contains the literal key in its `key` field on stdout. Keep the stream out of ordinary logs, CI output and public issue attachments. Protect any captured file and terminal scrollback.

Without `--json-progress`, headless output prints the key to stderr instead. Neither stream is automatically safe to archive.

Interactive frontends receive events in-process. They show the key once on a blocking acknowledgement, clear it afterwards, and do not repeat it into logs when they exit. The GUI does not offer clipboard copying.

## Stable codes and diagnosis

Use `warning.code`, `summary.items[].code` and `error.code` for automation, not message text. Examples include `install_in_progress`, `image_verification_failed`, `registry_unreachable`, `flatpak_unreachable` and `cleanup_failed`.

A summary can appear on either success or failure. Do not discard it because the terminal event was an error. Both interactive frontends preserve stable codes beside human-readable messages.

See [troubleshooting](troubleshooting.md) for remedies and the [complete stable-code table](https://github.com/frostyard/firn/blob/main/docs/specs/progress-protocol.md#stable-codes) for the contract.
