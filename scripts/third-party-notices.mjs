#!/usr/bin/env node
// Generates the third-party notices file of each firn binary: the license
// and notice texts of every Go module compiled into it, plus the Go standard
// library license (ADR-0023, frostyard/firn#151). GoReleaser ships the
// files in the archives and in the deb, rpm and apk packages, so they are
// committed and reviewed, and `make verify` and `make gui-check` run
// `--check` to fail when a dependency change leaves one stale.
//
// Usage: node scripts/third-party-notices.mjs [--check] <target>...
//   targets: firn (./cmd/firn-cli, THIRD_PARTY_NOTICES)
//            firn-gui (gui/ ./cmd/firn-gui, gui/THIRD_PARTY_NOTICES)
//
// For linux/amd64 and linux/arm64 (CGO_ENABLED=0, as .goreleaser.yaml
// builds), `go list -deps` names every package the binary links. For each
// package outside the standard library and outside firn's own modules, the
// script walks from the package directory, and from the directory of each
// file the package embeds, up to the module root, and takes every license
// or notice file it finds there. A package with no license file on its walk
// is an error. The output has no module versions, so a version bump alone
// does not make it stale; a changed license text does.
//
// Node standard library only. The name must not start with `check-`:
// scripts/check-*.mjs are copies of another repository.
import { execFileSync } from "node:child_process";
import { lstatSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");

export const TARGETS = {
  firn: {
    packageName: "frostyard-firn",
    moduleDir: ".",
    pkg: "./cmd/firn-cli",
    output: "THIRD_PARTY_NOTICES",
  },
  "firn-gui": {
    packageName: "frostyard-firn-gui",
    moduleDir: "gui",
    pkg: "./cmd/firn-gui",
    output: "gui/THIRD_PARTY_NOTICES",
  },
};

export const GOARCHES = ["amd64", "arm64"];

// firn's own module: gui/ requires it through a replace of ../, and firn's
// LICENSE and NOTICE already cover it.
export const OWN_MODULES = new Set(["github.com/frostyard/firn"]);

export const STDLIB_KEY = "Go standard library/LICENSE";

const RULE = "=".repeat(78);

// A license or notice file: LICENSE, LICENCE, COPYING, COPYRIGHT, NOTICE or
// OFL (any case), alone or followed by ".", "-" or "_" and more
// (LICENSE.md, LICENSE-APACHE, COPYING.LESSER, OFL.txt). A name that only
// starts with such a word (LICENSES, NOTICEBOARD) does not match, nor does a
// source or data file (license.go, notice_test.go, license.json).
const NAME_PATTERN = /^(LICENSE|LICENCE|COPYING|COPYRIGHT|NOTICE|OFL)([._-].*)?$/;
const SOURCE_EXTENSIONS = new Set([
  "C", "CC", "CPP", "CSS", "GO", "H", "HTML", "JS", "JSON", "MJS", "MOD",
  "PL", "PY", "RB", "RS", "S", "SH", "SUM", "TMPL", "TOML", "TS", "YAML", "YML",
]);

export function isLicenseFileName(name) {
  const upper = name.toUpperCase();
  if (!NAME_PATTERN.test(upper)) {
    return false;
  }
  const dot = upper.lastIndexOf(".");
  return dot === -1 || !SOURCE_EXTENSIONS.has(upper.slice(dot + 1));
}

function isInside(base, path) {
  const rel = relative(base, path);
  return rel === "" || (!rel.startsWith(`..${sep}`) && rel !== ".." && !isAbsolute(rel));
}

// The license files on the walk from startDir up to moduleRoot, both
// included, as paths relative to moduleRoot with "/" separators, sorted. The
// walk never leaves moduleRoot; a startDir outside it is an error.
export function findLicenseFiles(moduleRoot, startDir) {
  const top = resolve(moduleRoot);
  let dir = resolve(startDir);
  if (!isInside(top, dir)) {
    throw new Error(`${startDir} is not inside the module root ${moduleRoot}`);
  }
  const found = [];
  for (;;) {
    for (const name of readdirSync(dir)) {
      if (isLicenseFileName(name) && lstatSync(join(dir, name)).isFile()) {
        found.push(relative(top, join(dir, name)).split(sep).join("/"));
      }
    }
    if (dir === top) {
      break;
    }
    dir = dirname(dir);
  }
  return found.sort();
}

// The -f template of `go list`: 1 line per package, fields separated by tabs:
// import path, package dir, module path, module dir, module main, then the
// embedded files. A standard library package has empty module fields.
export const GO_LIST_TEMPLATE = [
  "{{.ImportPath}}",
  "{{.Dir}}",
  "{{with .Module}}{{.Path}}{{end}}",
  "{{with .Module}}{{if .Replace}}{{.Replace.Dir}}{{else}}{{.Dir}}{{end}}{{end}}",
  "{{with .Module}}{{.Main}}{{end}}",
  '{{join .EmbedFiles "\\t"}}',
].join("\t");

export function parseGoList(output) {
  return output
    .split("\n")
    .filter((line) => line.trim() !== "")
    .map((line) => {
      const [importPath, dir, modulePath, moduleDir, main, ...embedFiles] = line.split("\t");
      return {
        importPath,
        dir,
        modulePath,
        moduleDir,
        main: main === "true",
        embedFiles: embedFiles.filter((file) => file !== ""),
      };
    });
}

// The license files of the packages, as a Map from key
// (`<module path>/<path in the module>`) to the absolute file path. It skips
// standard library packages, the main module and OWN_MODULES. A package
// whose walk from its own directory finds no license file is an error; the
// walks from its embedded files' directories add to it.
export function collectLicenseFiles(packages, ownModules = OWN_MODULES) {
  const files = new Map();
  for (const pkg of packages) {
    if (!pkg.modulePath || pkg.main || ownModules.has(pkg.modulePath)) {
      continue;
    }
    if (!pkg.moduleDir) {
      throw new Error(`package ${pkg.importPath} (module ${pkg.modulePath}) has no module directory: run go mod download`);
    }
    const own = findLicenseFiles(pkg.moduleDir, pkg.dir);
    if (own.length === 0) {
      throw new Error(
        `no license file found for package ${pkg.importPath} (module ${pkg.modulePath}) between ${pkg.dir} and its module root`,
      );
    }
    const embedded = pkg.embedFiles.flatMap((file) => findLicenseFiles(pkg.moduleDir, dirname(join(pkg.dir, file))));
    for (const rel of [...own, ...embedded]) {
      files.set(`${pkg.modulePath}/${rel}`, join(pkg.moduleDir, ...rel.split("/")));
    }
  }
  return files;
}

export function normalizeText(text) {
  return `${text.replace(/\r\n?/g, "\n").trimEnd()}\n`;
}

export function header(targetName, target) {
  return [
    `Third-party notices for ${targetName} (package ${target.packageName})`,
    "",
    "Generated by scripts/third-party-notices.mjs from the Go packages that",
    `${target.pkg} links for linux/amd64 and linux/arm64. Do not edit this file:`,
    'run "make notices" after a Go dependency change. firn\'s own license and',
    "attribution are in LICENSE and NOTICE.",
    "",
  ].join("\n");
}

// The notices file: the header, then 1 section per key, sorted by key. The
// texts are normalized, so the same inputs always give the same bytes.
export function renderNotices(headerText, sections) {
  const keys = [...sections.keys()].sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
  const parts = [normalizeText(headerText)];
  for (const key of keys) {
    parts.push(`\n${RULE}\n${key}\n${RULE}\n\n${normalizeText(sections.get(key))}`);
  }
  return parts.join("");
}

// null when the file at path holds exactly content, else the stale message.
export function staleMessage(path, displayPath, content) {
  let current;
  try {
    current = readFileSync(path, "utf8");
  } catch {
    current = null;
  }
  return current === content ? null : `${displayPath} is stale or missing: run make notices`;
}

function goEnv(goarch) {
  return { ...process.env, GOOS: "linux", GOARCH: goarch, CGO_ENABLED: "0" };
}

function runGo(moduleDir, args, env) {
  return execFileSync("go", args, {
    cwd: moduleDir,
    env,
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
    stdio: ["ignore", "pipe", "inherit"],
  });
}

export function generate(targetName, repoRoot = root) {
  const target = TARGETS[targetName];
  if (!target) {
    throw new Error(`unknown target ${targetName}: want one of ${Object.keys(TARGETS).join(", ")}`);
  }
  const moduleDir = resolve(repoRoot, target.moduleDir);
  const packages = GOARCHES.flatMap((goarch) =>
    parseGoList(runGo(moduleDir, ["list", "-deps", "-f", GO_LIST_TEMPLATE, target.pkg], goEnv(goarch))),
  );
  const sections = new Map();
  for (const [key, file] of collectLicenseFiles(packages)) {
    sections.set(key, readFileSync(file, "utf8"));
  }
  const goroot = runGo(moduleDir, ["env", "GOROOT"], process.env).trim();
  sections.set(STDLIB_KEY, readFileSync(join(goroot, "LICENSE"), "utf8"));
  return renderNotices(header(targetName, target), sections);
}

// The number of sections (license and notice files) of a notices file.
export function sectionCount(content) {
  return content.split("\n").filter((line) => line === RULE).length / 2;
}

export function main(argv, repoRoot = root) {
  const check = argv.includes("--check");
  const names = argv.filter((arg) => arg !== "--check");
  if (names.length === 0) {
    console.error(`usage: node scripts/third-party-notices.mjs [--check] <${Object.keys(TARGETS).join("|")}>...`);
    return 2;
  }
  let status = 0;
  for (const name of names) {
    const content = generate(name, repoRoot);
    const output = TARGETS[name].output;
    const path = join(repoRoot, output);
    const count = sectionCount(content);
    if (check) {
      const message = staleMessage(path, output, content);
      if (message) {
        console.error(message);
        status = 1;
      } else {
        console.log(`${output} is current: ${count} license and notice files checked`);
      }
    } else {
      writeFileSync(path, content);
      console.log(`wrote ${output}: ${count} license and notice files`);
    }
  }
  return status;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    process.exitCode = main(process.argv.slice(2));
  } catch (error) {
    console.error(`third-party-notices: ${error.message}`);
    process.exitCode = 1;
  }
}
