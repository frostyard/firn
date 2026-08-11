# Documentation

Docs are split by the question they answer:

| Directory | Question | Contents |
| --- | --- | --- |
| [adr/](adr/) | **Why** did we choose this? | Architecture Decision Records — immutable once accepted; superseded, never edited |
| [design/](design/) | **How** does it fit together? | Living documents describing the current architecture |
| [specs/](specs/) | **What exactly** is the contract? | Precise, testable interface definitions |
| [plans/](plans/) | **When/in what order** do we build? | Roadmaps and phase plans; updated as work lands |

## Index

### Decisions (ADRs)

- [0001 — Record architecture decisions](adr/0001-record-architecture-decisions.md)
- [0002 — Agent-portable instruction surface](adr/0002-agent-portable-instruction-surface.md)
- [0003 — Rewrite fisherman as firn rather than hard-forking](adr/0003-rewrite-fisherman-as-firn.md)
- [0004 — Single installer for all snosi images, UEFI floor, optional security features](adr/0004-single-installer-scope-and-support-matrix.md)
- [0005 — Versioned, sectioned TOML recipe as the single configuration surface](adr/0005-toml-recipe-model.md)
- [0006 — Install-time, offline-first flatpak provisioning on both paths](adr/0006-install-time-offline-first-flatpaks.md)
- [0007 — TUI-only frontend in a single binary, one progress protocol](adr/0007-tui-only-frontend-single-binary.md)
- [0008 — /var filesystem choice for A/B installs](adr/0008-ab-var-filesystem-choice.md)
- [0009 — A/B installs run only against an isolated partition namespace](adr/0009-ab-installs-require-partition-isolation.md)
- [0010 — One installer ISO for all image families, built in the snosi repo](adr/0010-single-installer-iso-in-snosi.md)
- [0011 — Adopt the frostyard Go repository conventions](adr/0011-adopt-frostyard-go-conventions.md)

### Design

- [Firn architecture](design/architecture.md)

### Specs

- [Firn recipe schema (version 1)](specs/recipe-schema.md)
- [Firn progress protocol (version 1)](specs/progress-protocol.md)

### Plans

- [Firn roadmap](plans/roadmap.md)

## Conventions

- **New docs start from their category's `TEMPLATE.md`** (in each directory).
- New decision → new ADR with the next number; if it reverses an old one, mark
  the old one `Superseded by NNNN` rather than editing it.
- Design docs are updated in place to always reflect reality.
- Specs change only alongside the code that implements them.
- Cross-links between categories are mandatory in both directions — see the
  documentation rules in [AGENTS.md](../AGENTS.md) (CLAUDE.md/GEMINI.md are
  symlinks to it, ADR-0002).
- Adding a doc means adding it to the index above.
