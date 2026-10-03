# 0017 — Publish Debian packages through frostyard/apt-publisher

- **Status:** Accepted
- **Date:** 2026-10-03

## Context

- Firn's tag workflow published `frostyard-firn` with repogen's
  `publish-to-r2` action (`package-type: deb`), then dispatched `build` to
  frostyard/snosi with `ORG_PAT` and `continue-on-error`. The action's pin
  (`6648671`) predates repogen's deb refusal and writes the legacy `stable`
  suite at the bucket root: v0.6.0's release run (36861895977) wrote
  `dists/stable` on 2026-10-01.
- [core ADR-0055](https://github.com/frostyard/core/blob/main/docs/adr/0055-publish-debian-packages-through-the-apt-publisher.md)
  makes frostyard/apt-publisher the only writer of Frostyard's Debian
  metadata, at `https://repository.frostyard.org/debian/` with codenames
  `trixie` and `forky`. Signed `stable` must stay unchanged through at least
  2027-09-30. The org's `R2_*` and `REPOGEN_GPG_KEY` secrets are no longer
  available to firn, so the repogen step cannot run.
- [core ADR-0056](https://github.com/frostyard/core/blob/main/docs/adr/0056-rebuild-images-after-apt-publication.md)
  moves the snosi `build` dispatch into apt-publisher, after the packages are
  installable. snosi's installer ISO
  ([ADR-0010](0010-single-installer-iso-in-snosi.md)) installs a pinned
  `frostyard-firn` from `/debian/` `trixie`.
- apt-publisher publishes every `.deb` asset of a release. For a producer
  registered `attested=yes`, it first checks each file with
  `gh attestation verify --source-ref refs/tags/<tag> --deny-self-hosted-runners`.
- The rolling `dev` pre-release (`snapshot.yml`,
  [ADR-0011](0011-adopt-frostyard-go-conventions.md)) also carries
  `frostyard-firn` `.deb` files, with a `1.0.0` dev version that sorts above
  every real release.

## Decision

- After `make verify`, `.github/workflows/release.yml` runs three steps on a
  tag push:
  1. GoReleaser Pro creates the GitHub release.
  2. `actions/attest-build-provenance` attests `checksums.txt` and every
     asset, the `.deb` files included. The workflow grants `id-token: write`
     and `attestations: write`.
  3. A `publish-deb` `repository_dispatch` to frostyard/apt-publisher carries
     `repo` and `tag` and is authenticated by `APT_PUBLISH_TOKEN`. It has no
     guard and is not `continue-on-error`.
- Firn holds no signing, R2 or CDN credential, does not call repogen, and
  does not dispatch to snosi.
- apt-publisher registers firn for `frostyard-firn` only, codenames `trixie`
  and `forky`, `attested=yes`, notifying `frostyard/snosi`. Firn is a static
  (CGO-disabled) binary with no `Depends`, so one unmarked version serves
  both codenames.
- `cmd/firn-cli/release_workflow_contract_test.go` pins this shape. It reads
  the workflow with `go.yaml.in/yaml/v3` in test code only; the module is
  already linked into firn through clix and viper, so the binary gains
  nothing.

## Consequences

- The first tag after this change needs firn's apt-publisher registration
  and the `APT_PUBLISH_TOKEN` secret. Without the token the release run
  fails at the request; without the registration apt-publisher refuses it.
  Both are recoverable: re-run the firn job if the request never went out,
  or apt-publisher's publish run if it refused.
- snosi rebuilds only once the new firn is installable, instead of racing
  its publication.
- Every firn `.deb` that apt-publisher publishes comes from a tag workflow
  run of this repository. It refuses the `dev` pre-release and any
  hand-uploaded asset, because neither is attested for a tag.
- Releases up to v0.6.0 have no attestations, so apt-publisher cannot
  publish them. `trixie` already holds 0.3.0–0.6.0 for amd64 from the
  import of `stable`. `forky`, and arm64 in either codename, get firn from
  the first release after this change.
- Provenance binds a package to a tag of this repository, not to a reviewed
  commit. The `v*` tag ruleset limits who creates release tags; the workflow
  itself runs on any tag.
- Re-running the release job after a successful publication rebuilds the
  assets (`release.mode: replace`). If the bytes differ, apt-publisher
  refuses them for a version it already serves. Retry a failed publication
  from apt-publisher's publish run instead.
- ADR-0011's consequences that name repogen packaging and the `R2_*` release
  secrets no longer hold.

## Alternatives considered

- **Keep the repogen step:** repogen refuses `.deb` files at current pins,
  and the old pin writes the frozen `stable` suite, contrary to core
  ADR-0055.
- **Register `attested=no` and skip provenance:** apt-publisher would then
  publish any `.deb` asset of any firn release, including the `dev`
  pre-release, whose version sorts above every real release.
- **Send the request from a separate job,** as frostyard/incus does, so
  re-running failed jobs only re-sends it: kept as one job to match updex
  and core's frostyard-go-repo skill (ADR-0011: updex wins).

## References

- Shapes: [design/architecture.md](../design/architecture.md#operational-notes)
  (release and distribution), `.github/workflows/release.yml`,
  `cmd/firn-cli/release_workflow_contract_test.go`, `AGENTS.md`
- Builds on: [ADR-0010](0010-single-installer-iso-in-snosi.md),
  [ADR-0011](0011-adopt-frostyard-go-conventions.md) (whose publication
  consequences this replaces)
- Org: [core ADR-0055](https://github.com/frostyard/core/blob/main/docs/adr/0055-publish-debian-packages-through-the-apt-publisher.md),
  [core ADR-0056](https://github.com/frostyard/core/blob/main/docs/adr/0056-rebuild-images-after-apt-publication.md),
  [core ADR-0021](https://github.com/frostyard/core/blob/main/docs/adr/0021-sha-pinned-actions-and-least-privilege-ci.md),
  [core Plan 0009 Phase 4](https://github.com/frostyard/core/blob/main/docs/plans/0009-debian-publication-through-apt-publisher.md)
