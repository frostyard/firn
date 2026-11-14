// Guards scripts/third-party-notices.mjs, the generator of THIRD_PARTY_NOTICES
// and gui/THIRD_PARTY_NOTICES (ADR-0023, frostyard/firn#151). The packages
// and archives ship these files to carry the license and notice texts of
// every Go module compiled into firn and firn-gui, so the generator must find
// every license file, never take a file from outside a module, fail on a
// package it cannot attribute, and give the same bytes for the same inputs.
// These tests use temporary directories and never run the real `go`: the
// command-level test puts a stub `go` first on PATH.
import { test } from "node:test";
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import {
  collectLicenseFiles,
  findLicenseFiles,
  isLicenseFileName,
  main,
  normalizeText,
  parseGoList,
  renderNotices,
  sectionCount,
  staleMessage,
} from "../scripts/third-party-notices.mjs";

function tempTree(t, files) {
  const dir = mkdtempSync(join(tmpdir(), "firn-notices-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  for (const [path, content] of Object.entries(files)) {
    mkdirSync(join(dir, path, ".."), { recursive: true });
    writeFileSync(join(dir, path), content);
  }
  return dir;
}

test("the name matcher takes license and notice files", () => {
  for (const name of [
    "LICENSE",
    "license",
    "LICENSE.md",
    "LICENSE.txt",
    "LICENSE-APACHE",
    "LICENSE_MIT",
    "LICENCE",
    "COPYING",
    "COPYING.LESSER",
    "COPYRIGHT",
    "NOTICE",
    "NOTICE.md",
    "OFL.txt",
  ]) {
    assert.equal(isLicenseFileName(name), true, `${name} must match`);
  }
});

test("the name matcher refuses source files and other names", () => {
  for (const name of [
    "licenses.go",
    "license.go",
    "license_test.go",
    "notice_test.go",
    "LICENSE.json",
    "LICENSES",
    "NOTICEBOARD",
    "OFLfont.ttf",
    "README.md",
    "mylicense",
    "InterVariable.ttf",
  ]) {
    assert.equal(isLicenseFileName(name), false, `${name} must not match`);
  }
});

test("the walk finds a license at the module root and a nested one in a sub-package", (t) => {
  const mod = tempTree(t, {
    "LICENSE": "root license",
    "NOTICE": "root notice",
    "README.md": "readme",
    "sub/pkg/LICENSE": "nested license",
    "sub/pkg/code.go": "package pkg",
    "sub/license.go": "package sub",
    "other/COPYING": "not on the walk",
  });
  assert.deepEqual(findLicenseFiles(mod, join(mod, "sub", "pkg")), ["LICENSE", "NOTICE", "sub/pkg/LICENSE"]);
  assert.deepEqual(findLicenseFiles(mod, mod), ["LICENSE", "NOTICE"]);
});

test("the walk never goes above the module root", (t) => {
  const tree = tempTree(t, {
    "LICENSE": "outside the module",
    "mod/pkg/a.go": "package pkg",
  });
  assert.deepEqual(findLicenseFiles(join(tree, "mod"), join(tree, "mod", "pkg")), []);
  assert.throws(() => findLicenseFiles(join(tree, "mod"), tree), /is not inside the module root/);
});

test("the walk skips a directory named like a license file", (t) => {
  const mod = tempTree(t, { "LICENSE.d/x": "x", "COPYING": "copying" });
  assert.deepEqual(findLicenseFiles(mod, mod), ["COPYING"]);
});

test("collectLicenseFiles keys files by module path, skips stdlib, main and own modules, and follows embeds", (t) => {
  const tree = tempTree(t, {
    "ui/LICENSE": "MIT",
    "ui/fonts/OFL.txt": "OFL",
    "ui/fonts/Inter.ttf": "font",
    "ui/widgets/w.go": "package widgets",
    "firn/LICENSE": "GPL",
    "main/LICENSE": "main",
  });
  const packages = [
    { importPath: "fmt", dir: "/goroot/src/fmt", modulePath: "", moduleDir: "", main: false, embedFiles: [] },
    { importPath: "example.com/main", dir: join(tree, "main"), modulePath: "example.com/main", moduleDir: join(tree, "main"), main: true, embedFiles: [] },
    { importPath: "github.com/frostyard/firn/x", dir: join(tree, "firn"), modulePath: "github.com/frostyard/firn", moduleDir: join(tree, "firn"), main: false, embedFiles: [] },
    { importPath: "example.com/ui", dir: join(tree, "ui"), modulePath: "example.com/ui", moduleDir: join(tree, "ui"), main: false, embedFiles: ["fonts/Inter.ttf"] },
    { importPath: "example.com/ui/widgets", dir: join(tree, "ui", "widgets"), modulePath: "example.com/ui", moduleDir: join(tree, "ui"), main: false, embedFiles: [] },
  ];
  const files = collectLicenseFiles(packages);
  assert.deepEqual([...files.keys()].sort(), ["example.com/ui/LICENSE", "example.com/ui/fonts/OFL.txt"]);
  assert.equal(files.get("example.com/ui/fonts/OFL.txt"), join(tree, "ui", "fonts", "OFL.txt"));
});

test("a package with no license file on its walk is an error that names it and its module", (t) => {
  const tree = tempTree(t, { "bare/pkg/p.go": "package pkg" });
  const packages = [
    { importPath: "example.com/bare/pkg", dir: join(tree, "bare", "pkg"), modulePath: "example.com/bare", moduleDir: join(tree, "bare"), main: false, embedFiles: [] },
  ];
  assert.throws(() => collectLicenseFiles(packages), /package example\.com\/bare\/pkg \(module example\.com\/bare\)/);
});

test("parseGoList reads module and embed fields, and empty module fields for the standard library", () => {
  const output = [
    "fmt\t/goroot/src/fmt\t\t\t\t",
    "example.com/ui\t/mod/ui\texample.com/ui\t/mod/ui\tfalse\tfonts/a.ttf\ticons/b.svg",
    "example.com/main\t/src\texample.com/main\t/src\ttrue\t",
    "",
  ].join("\n");
  assert.deepEqual(parseGoList(output), [
    { importPath: "fmt", dir: "/goroot/src/fmt", modulePath: "", moduleDir: "", main: false, embedFiles: [] },
    { importPath: "example.com/ui", dir: "/mod/ui", modulePath: "example.com/ui", moduleDir: "/mod/ui", main: false, embedFiles: ["fonts/a.ttf", "icons/b.svg"] },
    { importPath: "example.com/main", dir: "/src", modulePath: "example.com/main", moduleDir: "/src", main: true, embedFiles: [] },
  ]);
});

test("normalizeText turns CRLF into LF and ends with exactly 1 newline", () => {
  assert.equal(normalizeText("a\r\nb\r\n\r\n  \n"), "a\nb\n");
  assert.equal(normalizeText("a"), "a\n");
});

test("renderNotices sorts the sections by key and is deterministic", () => {
  const rule = "=".repeat(78);
  const one = renderNotices("Header\n", new Map([["b.example/LICENSE", "B\r\n"], ["a.example/LICENSE", "A"]]));
  const two = renderNotices("Header", new Map([["a.example/LICENSE", "A\n\n"], ["b.example/LICENSE", "B"]]));
  assert.equal(one, two);
  assert.equal(one, `Header\n\n${rule}\na.example/LICENSE\n${rule}\n\nA\n\n${rule}\nb.example/LICENSE\n${rule}\n\nB\n`);
  assert.equal(sectionCount(one), 2);
  assert.equal(sectionCount(renderNotices("Header", new Map())), 0);
});

test("staleMessage passes a current file and names a stale or missing one", (t) => {
  const dir = tempTree(t, { "NOTICES": "current\n" });
  assert.equal(staleMessage(join(dir, "NOTICES"), "NOTICES", "current\n"), null);
  assert.equal(staleMessage(join(dir, "NOTICES"), "NOTICES", "changed\n"), "NOTICES is stale or missing: run make notices");
  assert.equal(staleMessage(join(dir, "MISSING"), "MISSING", "x\n"), "MISSING is stale or missing: run make notices");
});

// The gate is the exit status, not the message: `make verify` and `make
// gui-check` pass when `--check` returns 0, so a check that reports a stale
// file and still returns 0 lets the stale file through (frostyard/firn#158).
// This runs main against a temporary repository with a stub `go` on PATH.
test("main writes the notices file, and --check returns 0 when current and 1 when stale or missing", (t) => {
  const tree = tempTree(t, {
    "repo/go.mod": "module example.com/repo\n",
    "mod/LICENSE": "fake module license",
    "mod/pkg/p.go": "package pkg",
    "goroot/LICENSE": "fake Go license",
  });
  const repo = join(tree, "repo");
  const bin = join(tree, "bin");
  mkdirSync(bin);
  const line = [`example.com/mod/pkg`, join(tree, "mod", "pkg"), "example.com/mod", join(tree, "mod"), "false", ""].join("\t");
  const stub = [
    "#!/bin/sh",
    'if [ "$1" = "list" ]; then',
    `  printf '%s\\n' '${line}'`,
    "  exit 0",
    "fi",
    'if [ "$1" = "env" ] && [ "$2" = "GOROOT" ]; then',
    `  printf '%s\\n' '${join(tree, "goroot")}'`,
    "  exit 0",
    "fi",
    'echo "stub go: unexpected arguments: $*" >&2',
    "exit 2",
    "",
  ].join("\n");
  writeFileSync(join(bin, "go"), stub);
  chmodSync(join(bin, "go"), 0o755);
  const savedPath = process.env.PATH;
  process.env.PATH = `${bin}:${savedPath}`;
  t.after(() => {
    process.env.PATH = savedPath;
  });

  const notices = join(repo, "THIRD_PARTY_NOTICES");
  assert.equal(main(["firn"], repo), 0, "writing the notices file must return 0");
  const written = readFileSync(notices, "utf8");
  assert.match(written, /example\.com\/mod\/LICENSE\n=+\n\nfake module license\n/);
  assert.match(written, /Go standard library\/LICENSE\n=+\n\nfake Go license\n/);

  assert.equal(main(["--check", "firn"], repo), 0, "--check must return 0 on a current file");

  const stale = Buffer.from(written);
  stale[0] = stale[0] === 0x54 ? 0x74 : 0x54;
  writeFileSync(notices, stale);
  assert.equal(main(["--check", "firn"], repo), 1, "--check must return 1 on a stale file");
  assert.deepEqual(readFileSync(notices), stale, "--check must not rewrite a stale file");

  rmSync(notices);
  assert.equal(main(["--check", "firn"], repo), 1, "--check must return 1 on a missing file");
  assert.equal(existsSync(notices), false, "--check must not create a missing file");
});
