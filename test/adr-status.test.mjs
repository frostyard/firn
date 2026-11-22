// Guards the ADR statuses against the ADR index in docs/README.md (#168).
//
// AGENTS.md: docs/adr/ is immutable once Accepted, and a reversal is a new ADR
// that marks the old one Superseded. A status that no longer matches the code
// misleads a reader: the A/B ADRs 0008, 0009 and 0013 still said Accepted
// after the A/B path was removed (#101, ADR-0016), and the shipped ADR-0020
// still said Proposed.
//
// So each ADR and its index entry are held together in 3 ways:
//   a. every ADR has 1 `- **Status:**` line that starts with Proposed,
//      Accepted or `Superseded by [NNNN](NNNN-....md)`, and that file exists;
//   b. every ADR has exactly 1 index entry in docs/README.md;
//   c. a status marker in the entry's link text, `(Proposed)`, `(Accepted)`
//      or `(Superseded by NNNN)`, equals the ADR's status, and a Proposed or
//      Superseded ADR must carry its marker (an Accepted ADR may omit it).
//
// It does not judge whether a decision shipped; it keeps the 2 records in
// step, so a status change has to update both. Read-only: it writes nothing.
import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const adrDir = join(root, "docs", "adr");
const ADR_FILE = /^(\d{4})-.+\.md$/;
const STATUS_LINE = /^- \*\*Status:\*\* (.*)$/;
const SUPERSEDED = /^Superseded by \[(\d{4})\]\(((\d{4})-[^)]+\.md)\)/;
const MARKER = /\((Proposed|Accepted|Superseded by (\d{4}))\)/;

// statusOf returns {kind, by, target} for an ADR text, or a problem string.
export function statusOf(text) {
  const lines = text.split("\n").filter((line) => STATUS_LINE.test(line));
  if (lines.length !== 1) return `has ${lines.length} Status lines, want 1`;
  const value = lines[0].match(STATUS_LINE)[1];
  if (/^Proposed\b/.test(value)) return { kind: "Proposed" };
  if (/^Accepted\b/.test(value)) return { kind: "Accepted" };
  const superseded = value.match(SUPERSEDED);
  if (superseded && superseded[1] === superseded[3]) {
    return { kind: "Superseded", by: superseded[1], target: superseded[2] };
  }
  return `Status "${value}" is not Proposed, Accepted or Superseded by [NNNN](NNNN-....md)`;
}

// problems checks every ADR of adrs ({file: text}) against the index text.
export function problems(adrs, index, exists = () => true) {
  const found = [];
  for (const [file, text] of Object.entries(adrs)) {
    const status = statusOf(text);
    if (typeof status === "string") {
      found.push(`${file}: ${status}`);
      continue;
    }
    if (status.target && !exists(status.target)) {
      found.push(`${file}: superseded by ${status.target}, which does not exist`);
    }
    const entries = index
      .split("\n")
      .map((line) => line.match(/^- \[([^\]]*)\]\(adr\/([^)]+)\)/))
      .filter((match) => match && match[2] === file);
    if (entries.length !== 1) {
      found.push(`${file}: ${entries.length} index entries in docs/README.md, want 1`);
      continue;
    }
    const marker = entries[0][1].match(MARKER);
    const want = status.kind === "Superseded" ? `Superseded by ${status.by}` : status.kind;
    if (marker && marker[1] !== want) {
      found.push(`${file}: index marker (${marker[1]}) but Status is ${want}`);
    } else if (!marker && status.kind !== "Accepted") {
      found.push(`${file}: Status is ${want}, so its index entry needs the marker (${want})`);
    }
  }
  return found;
}

test("every ADR status matches its docs/README.md index entry", () => {
  const adrs = {};
  for (const file of readdirSync(adrDir).filter((name) => ADR_FILE.test(name)).sort()) {
    adrs[file] = readFileSync(join(adrDir, file), "utf8");
  }
  assert.ok(Object.keys(adrs).length > 20, "found too few ADRs; is docs/adr/ readable?");
  const index = readFileSync(join(root, "docs", "README.md"), "utf8");
  assert.deepEqual(problems(adrs, index, (target) => existsSync(join(adrDir, target))), []);
});

test("self-test: drift between an ADR and its index entry fails", () => {
  const accepted = "# 0001 — A\n\n- **Status:** Accepted\n";
  const proposed = "# 0002 — B\n\n- **Status:** Proposed\n";
  const superseded = "# 0003 — C\n\n- **Status:** Superseded by [0004](0004-d.md): gone\n";
  const entry = (file, title) => `- [${title}](adr/${file})`;
  const good = {
    adrs: { "0001-a.md": accepted, "0002-b.md": proposed, "0003-c.md": superseded },
    index: [
      entry("0001-a.md", "0001 — A"),
      entry("0002-b.md", "0002 — B (Proposed)"),
      entry("0003-c.md", "0003 — C (Superseded by 0004)"),
    ].join("\n"),
  };
  assert.deepEqual(problems(good.adrs, good.index), []);
  assert.deepEqual(problems({ "0001-a.md": accepted }, entry("0001-a.md", "0001 — A (Accepted)")), []);

  const cases = [
    ["Accepted ADR, Proposed marker", { "0001-a.md": accepted }, entry("0001-a.md", "0001 — A (Proposed)"), /index marker \(Proposed\) but Status is Accepted/],
    ["Proposed ADR, no marker", { "0002-b.md": proposed }, entry("0002-b.md", "0002 — B"), /needs the marker \(Proposed\)/],
    ["Proposed ADR, Accepted marker", { "0002-b.md": proposed }, entry("0002-b.md", "0002 — B (Accepted)"), /index marker \(Accepted\) but Status is Proposed/],
    ["Superseded ADR, no marker", { "0003-c.md": superseded }, entry("0003-c.md", "0003 — C"), /needs the marker \(Superseded by 0004\)/],
    ["Superseded ADR, wrong successor", { "0003-c.md": superseded }, entry("0003-c.md", "0003 — C (Superseded by 0005)"), /index marker \(Superseded by 0005\)/],
    ["no index entry", { "0001-a.md": accepted }, "", /0 index entries/],
    ["2 index entries", { "0001-a.md": accepted }, `${entry("0001-a.md", "A")}\n${entry("0001-a.md", "A")}`, /2 index entries/],
    ["no Status line", { "0001-a.md": "# 0001 — A\n" }, entry("0001-a.md", "A"), /0 Status lines/],
    ["unknown Status", { "0001-a.md": "- **Status:** Draft\n" }, entry("0001-a.md", "A"), /is not Proposed, Accepted or Superseded/],
    ["Superseded without a link", { "0003-c.md": "- **Status:** Superseded by 0004\n" }, entry("0003-c.md", "C (Superseded by 0004)"), /is not Proposed, Accepted or Superseded/],
    ["Superseded link to another number", { "0003-c.md": "- **Status:** Superseded by [0004](0005-e.md)\n" }, entry("0003-c.md", "C (Superseded by 0004)"), /is not Proposed, Accepted or Superseded/],
  ];
  for (const [name, adrs, index, want] of cases) {
    const found = problems(adrs, index);
    assert.equal(found.length, 1, `${name}: ${JSON.stringify(found)}`);
    assert.match(found[0], want, name);
  }
  const missing = problems({ "0003-c.md": superseded }, entry("0003-c.md", "C (Superseded by 0004)"), () => false);
  assert.deepEqual(missing, ["0003-c.md: superseded by 0004-d.md, which does not exist"]);
});
