// Guards the `vuln` target of the Makefile (#169): it must scan both Go
// modules and fail closed.
//
// `govulncheck ./...` at the root does not enter the nested module gui/
// (its own gui/go.mod), so `make vuln` runs it at the root and then in gui/.
// A target that dropped the gui/ scan, or that ran it but lost its exit
// status, would pass the required Security Scan check with gui/ unscanned
// (frostyard/firn#173, cross-review finding 1). Each case runs the real target
// offline against a fake `govulncheck` on PATH that records where it ran.
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { chmodSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const gui = join(root, "gui");

// The make binary, resolved once: the missing-tool case runs with a PATH that
// holds no tools at all.
const make = spawnSync("sh", ["-c", "command -v make"], { encoding: "utf8" }).stdout.trim();
assert.ok(make, "make must be on PATH");

// Runs `make vuln` with only dir on PATH. With a fake, the fake appends
// "<working directory> <args>" to calls.log and exits with the status named
// for its working directory (failIn: "root" or "gui"), else 0.
function runVuln({ fake = true, failIn = "" } = {}) {
  const dir = mkdtempSync(join(tmpdir(), "firn-make-vuln-"));
  const log = join(dir, "calls.log");
  try {
    if (fake) {
      const failDir = failIn === "gui" ? gui : failIn === "root" ? root : "";
      writeFileSync(
        join(dir, "govulncheck"),
        [
          "#!/bin/sh",
          `printf '%s %s\\n' "$(pwd -P)" "$*" >> '${log}'`,
          `if [ "$(pwd -P)" = '${failDir}' ]; then echo 'fake: vulnerability found' >&2; exit 3; fi`,
          "echo 'No vulnerabilities found.'",
          "exit 0",
          "",
        ].join("\n"),
      );
      chmodSync(join(dir, "govulncheck"), 0o755);
    }
    const result = spawnSync(make, ["--no-print-directory", "-C", root, "vuln"], {
      encoding: "utf8",
      env: { ...process.env, PATH: dir },
    });
    const calls = existsSync(log) ? readFileSync(log, "utf8").trim().split("\n") : [];
    return { ...result, calls };
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

test("make vuln scans the root module, then gui/", () => {
  const result = runVuln();
  assert.equal(result.status, 0, result.stdout + result.stderr);
  assert.deepEqual(result.calls, [`${root} ./...`, `${gui} ./...`]);
});

test("make vuln fails when the gui/ scan fails", () => {
  const result = runVuln({ failIn: "gui" });
  assert.notEqual(result.status, 0, "a failing gui/ scan must fail the target");
  assert.deepEqual(result.calls, [`${root} ./...`, `${gui} ./...`]);
});

test("make vuln fails when the root scan fails, before gui/", () => {
  const result = runVuln({ failIn: "root" });
  assert.notEqual(result.status, 0, "a failing root scan must fail the target");
  assert.deepEqual(result.calls, [`${root} ./...`]);
});

test("make vuln fails with the mise install hint when govulncheck is absent", () => {
  const result = runVuln({ fake: false });
  assert.notEqual(result.status, 0, "a missing govulncheck must fail the target");
  assert.match(result.stdout, /govulncheck not installed; provision every pinned tool with:\nmise install\n/);
  assert.deepEqual(result.calls, []);
});

test("the offline gates do not run make vuln", () => {
  // govulncheck needs the network; verify, check and ci are offline gates.
  const makefile = readFileSync(join(root, "Makefile"), "utf8");
  for (const target of ["verify", "check", "ci"]) {
    const rule = new RegExp(`^${target}:([^\\n]*)\\n((?:\\t[^\\n]*\\n|\\n)*)`, "m").exec(makefile);
    assert.ok(rule, `Makefile has no ${target} target`);
    assert.doesNotMatch(rule[0], /\bvuln\b|govulncheck/, `${target} must not run the vulnerability scan`);
  }
});
