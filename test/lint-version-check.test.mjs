// Guards the `lint-version-check` target of the Makefile: it must fail closed.
//
// frostyard/core ADR-0043: golangci-lint must be built with a Go minor no
// older than go.mod's, or its embedded gofmt and type checker disagree with
// the toolchain. The gate read `built with goX.Y` from `golangci-lint
// version` and compared it only when the text was there, so a build that
// printed no such text, or a `version` command that failed, passed the gate
// with no check at all (frostyard/firn#145, cross-review finding 1). Each
// case below runs the real target against a fake `golangci-lint` on PATH: a
// gate that cannot read the build version must stop, not pass.
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const mise = readFileSync(join(root, "mise.toml"), "utf8");
const pin = /^golangci-lint = "(.*)"/m.exec(mise)?.[1];
assert.ok(pin, "mise.toml must pin golangci-lint");

// The fake golangci-lint answers `version --short` with `short`, and `version`
// with `full`, or with exit status `fullStatus`.
function fakeLinter({ short = pin, full = "", fullStatus = 0 }) {
  const script = [
    "#!/bin/sh",
    'if [ "$*" = "version --short" ]; then',
    `  printf '%s\\n' '${short}'`,
    "  exit 0",
    "fi",
    'if [ "$*" = "version" ]; then',
    full
      ? `  printf '%s\\n' '${full}'`
      : "  exit " + String(fullStatus),
    full ? "  exit 0" : "",
    "fi",
    "exit 0",
    "",
  ].join("\n");
  return script;
}

// Runs the target with the fake linter first on PATH, and returns its result.
function runGate(options, makeArgs = []) {
  const dir = mkdtempSync(join(tmpdir(), "firn-lint-version-"));
  try {
    const linter = join(dir, "golangci-lint");
    writeFileSync(linter, fakeLinter(options));
    chmodSync(linter, 0o755);
    return spawnSync("make", ["--no-print-directory", "-C", root, "lint-version-check", ...makeArgs], {
      encoding: "utf8",
      env: { ...process.env, PATH: `${dir}:${process.env.PATH}` },
    });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

const output = (result) => `${result.stdout ?? ""}${result.stderr ?? ""}`;

test("the pinned linter built with an older patch release passes", () => {
  const result = runGate({ full: `golangci-lint has version ${pin} built with go1.27.0 from x on y` });
  assert.equal(result.status, 0, output(result));
});

test("a go.mod Go minor newer than the linter's build fails", () => {
  const result = runGate({ full: `golangci-lint has version ${pin} built with go1.27.0 from x on y` }, ["GO_VERSION=1.28.0"]);
  assert.notEqual(result.status, 0, "the gate must fail when golangci-lint is older than go.mod's Go minor");
  assert.match(output(result), /older than/);
});

test("a version line with no build version fails closed", () => {
  const result = runGate({ full: `golangci-lint has version ${pin}` });
  assert.notEqual(result.status, 0, "the gate must fail when it cannot read the build version");
  assert.match(output(result), /reports no Go build version/);
});

test("a failing version command fails closed", () => {
  const result = runGate({ fullStatus: 3 });
  assert.notEqual(result.status, 0, "the gate must fail when the version command fails");
  assert.match(output(result), /golangci-lint version failed/);
});

test("a linter other than the mise.toml pin fails", () => {
  const result = runGate({ short: "0.0.1", full: `golangci-lint has version 0.0.1 built with go1.27.0 from x on y` });
  assert.notEqual(result.status, 0, "the gate must fail when the installed linter is not the pin");
  assert.match(output(result), /expected golangci-lint/);
});

test("sort -V orders 1.9 below 1.27", () => {
  const result = runGate({ full: `golangci-lint has version ${pin} built with go1.27.0 from x on y` }, ["GO_VERSION=1.9.0"]);
  assert.equal(result.status, 0, output(result));
});
