# 0020 — Pin the preflight-selected digest for unsigned registry installs

- **Status:** Proposed
- **Date:** 2026-10-22

## Context

- `preflight-image` (`internal/steps/preflight.go`) calls
  `bootcimg.CheckAndPinImage` before any disk write. It inspects the local
  containers-storage copy first, then the registry, and prefers a valid
  local copy (`internal/bootcimg/verify.go`).
- A signed recipe (`image.cosign_pub_key` set) resolves the selected source
  to an immutable `sha256` digest, verifies that digest with cosign, and
  installs `repo@sha256:…`. `runBootcInstall` (`internal/steps/bootc.go`)
  still writes the recipe tag (or `target_ref`) as `--target-imgref`, so
  day-two updates follow the tag.
- An unsigned recipe only checks that the image is reachable or cached, then
  installs the tag. bootc resolves the tag again when it pulls. A tag that
  moves between preflight and the pull deploys an image that preflight did
  not inspect.
- [ADR-0018](0018-image-published-core-flatpaks-label.md) reads the core
  flatpak set from a label on the image preflight selects. For an unsigned
  recipe the deployed image can be one build newer than that label
  ([firn#109](https://github.com/frostyard/firn/issues/109)).
- Every TUI catalog entry carries a cosign key, so the gap affects only
  headless unsigned recipes.
- Non-registry references (`containers-storage:`, `oci:`, `oci-archive:`,
  `dir:`, `docker-archive:`) cannot be resolved independently; signed
  recipes already reject them (`IsRegistryRef`).

## Decision

For an unsigned recipe whose `image.ref` is a registry reference
(`IsRegistryRef`), preflight installs the digest it selected, as the signed
path does, without cosign verification:

- Firn selects the source by the existing rule: a valid local
  containers-storage copy wins, otherwise the registry image.
- When the selected source reports a valid `sha256` digest, preflight
  returns `repository@sha256:…` as the source reference. bootc installs that
  digest.
- When the selected source reports no valid digest, preflight keeps the tag
  and the install proceeds as before. Unsigned installs do not gain a new
  failure mode.
- A reference that already carries a digest is installed as given.
- Day-two updates follow `target_ref`, or `image.ref` when `target_ref` is
  empty. The installed system never tracks the pinned digest.
- Non-registry references keep their reference unchanged.
- Preflight emits an `info` progress event that names the pinned digest, so
  the install log shows what was deployed.

Cosign verification stays the only trust claim. A pinned unsigned digest is
reproducible, not verified.

## Consequences

- What preflight inspected is what gets installed, for every unsigned
  registry recipe. ADR-0018's core flatpak label always describes the
  deployed image when the digest pins.
- Unsigned installs become reproducible from the install log's digest.
- This changes what every unsigned registry recipe installs, not only the
  flatpak case: a tag that moves during the install no longer pulls the newer
  build.
- The podman and native bootc paths receive `repo@sha256:…` for unsigned
  recipes too. The signed path already proves both accept it.
- The fallback (no valid digest, keep the tag) leaves a small residual race
  for registries or local images that report no digest.
- [specs/recipe-schema.md](../specs/recipe-schema.md),
  [specs/core-flatpaks-label.md](../specs/core-flatpaks-label.md) and
  [design/architecture.md](../design/architecture.md) must describe the pin
  in the same change as the code.

## Alternatives considered

- **Keep the tag for unsigned recipes:** leaves the ADR-0018 label gap and
  makes unsigned installs non-reproducible.
- **Fail the install when no valid digest is found:** closes the residual
  race, but adds a new failure mode for unsigned recipes that work today.
- **Require a cosign key for every registry recipe:** a larger policy change
  that breaks every unsigned headless recipe.

## References

- Shapes: [design/architecture.md](../design/architecture.md),
  [specs/recipe-schema.md](../specs/recipe-schema.md),
  [specs/core-flatpaks-label.md](../specs/core-flatpaks-label.md)
- Builds on: [ADR-0014](0014-port-secure-install-schema-1-for-bootc.md),
  [ADR-0016](0016-bootc-only-recipe-contract.md),
  [ADR-0018](0018-image-published-core-flatpaks-label.md)
- Issue: [firn#109](https://github.com/frostyard/firn/issues/109)
