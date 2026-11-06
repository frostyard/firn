// Guards .github/risk-paths.json against policies/agent-governance.json.
//
// frostyard/core docs/specs/risk-paths.md, section B: where a repository has
// a governance policy with protected boundaries, each `why` MUST name 1
// protected-boundary identifier (`protected_boundaries[].id` of the policy)
// instead of a free category. Repository baseline row B6 asks more: the list
// must cover the privileged helpers and the release and deploy paths, not a
// hand-picked subset. A hand-picked list left out the account writer
// (internal/sysconfig), the pipeline and the typed confirmation, so a pull
// request that changed them would get no `risk:high` label.
//
// So the list is held to the policy in 3 ways, and each fails loudly:
//   a. every `why` starts with `<id>:` for a boundary id of the policy;
//   b. every path of every protected boundary is a `glob` of the list
//      (exact string), unless no tracked file matches that path at all;
//   c. every tracked file under cmd/, internal/ and gui/ matches at least 1
//      glob of the list.
//
// A new protected boundary or a new install path means the policy and the
// list change in the same pull request. Read-only: it writes nothing.
import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { matchGlob, parseStrictJson } from "../scripts/lib/risk-paths.mjs";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const POLICY = "policies/agent-governance.json";
const LIST = ".github/risk-paths.json";
// The product trees whose every file must be a risk path: the installer runs
// as root and erases disks, so no helper in them is exempt.
const PRODUCT_TREES = ["cmd/", "internal/", "gui/"];

const policy = parseStrictJson(readFileSync(join(root, POLICY), "utf8"));
const list = parseStrictJson(readFileSync(join(root, LIST), "utf8"));
const tracked = execFileSync("git", ["-C", root, "ls-files"], {
  encoding: "utf8",
  stdio: ["ignore", "pipe", "pipe"],
})
  .split("\n")
  .filter((line) => line !== "");

const boundaries = policy.protected_boundaries ?? [];
const ids = boundaries.map((boundary) => boundary.id);
const globs = list.paths.map((entry) => entry.glob);

/** The 1 boundary id a `why` starts with, or null. */
function whyBoundary(why) {
  if (typeof why !== "string") return null;
  const match = /^([^:\s]+):\s+\S/.exec(why);
  return match ? match[1] : null;
}

test("each why names 1 protected boundary id of the policy", () => {
  assert.ok(ids.length > 0, `${POLICY} has no protected_boundaries`);
  const bad = [];
  for (const entry of list.paths) {
    const boundary = whyBoundary(entry.why);
    if (boundary === null || !ids.includes(boundary)) {
      bad.push(`${entry.glob}: why ${JSON.stringify(entry.why)} must start with 1 of ${ids.join(", ")} followed by ":"`);
    }
  }
  assert.deepEqual(bad, [], `FAIL risk-paths: ${bad.length} why value(s) name no protected boundary of ${POLICY}:\n${bad.join("\n")}`);
});

test("every policy boundary path is a glob of the risk-path list", (t) => {
  const checked = [];
  const skipped = [];
  const missing = [];
  for (const boundary of boundaries) {
    for (const path of boundary.paths ?? []) {
      const hasTrackedFile = tracked.some((file) => matchGlob(path, file));
      if (!hasTrackedFile) {
        skipped.push(`${boundary.id}: ${path} (no tracked file)`);
        continue;
      }
      checked.push(path);
      if (!globs.includes(path)) {
        missing.push(`${boundary.id}: ${path}`);
      }
    }
  }
  t.diagnostic(`${checked.length} boundary paths checked, ${skipped.length} skipped: ${skipped.join("; ")}`);
  assert.ok(checked.length > 0, `FAIL risk-paths: no boundary path of ${POLICY} has a tracked file`);
  assert.deepEqual(missing, [], `FAIL risk-paths: ${missing.length} path(s) of a protected boundary are not a glob of ${LIST}:\n${missing.join("\n")}`);
});

test("every tracked file under cmd/, internal/ and gui/ is a risk path", (t) => {
  const files = tracked.filter((file) => PRODUCT_TREES.some((tree) => file.startsWith(tree)));
  t.diagnostic(`${files.length} tracked product files checked against ${globs.length} globs`);
  assert.ok(files.length > 0, `FAIL risk-paths: no tracked file under ${PRODUCT_TREES.join(", ")}`);
  const unmatched = files.filter((file) => !globs.some((glob) => matchGlob(glob, file)));
  assert.deepEqual(unmatched, [], `FAIL risk-paths: ${unmatched.length} tracked file(s) under ${PRODUCT_TREES.join(", ")} match no glob of ${LIST}:\n${unmatched.join("\n")}`);
});
