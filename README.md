# Firn

**Agentic documentation and issue-to-PR pipeline for software repositories.**

Firn is two tools and a workflow:

- **mentat** — scans your repo, identifies logical domains, generates per-domain `SKILL.md` documentation using an LLM, keeps it fresh as code changes
- **pipeline** — watches GitHub for labeled issues, generates structured specs (product.md + tech.md), opens implementation PRs, and verifies them against the spec before requesting review

Start with `docs/PHILOSOPHY.md` — it explains the problem, the research foundation, and why this system is designed the way it is.

---

## Status

Wave 1 in progress. See [STATE.md](STATE.md) for the current task graph.

| Component | Status |
|---|---|
| mentat CLI scaffold | ✅ |
| mentat repo scanner | ✅ |
| mentat LLM classifier | ✅ |
| mentat SKILL.md generator | ✅ |
| pipeline CLI scaffold | ✅ |
| pipeline config | ✅ |
| pipeline issue watcher | ✅ |
| pipeline spec generator | ✅ |
| GoReleaser Pro release pipeline | ✅ |
| CI documentation validation | ✅ |

---

## Install

Not yet released. Wave 1 is still in progress.

Once released:

```bash
go install github.com/frostyard/firn/mentat/cmd/mentat@latest
go install github.com/frostyard/firn/pipeline/cmd/pipeline@latest
```

---

## Contributing

Firn uses a structured contribution workflow. Please read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a PR.

The short version: architectural decisions need ADRs, implementation tasks touching 3+ files need exec plans, and CI will fail if either is missing.

---

## License

MIT — see [LICENSE](LICENSE).
