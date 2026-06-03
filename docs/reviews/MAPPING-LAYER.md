# Mapping layer — api→pulumi mappings as DATA, not code

**Directive (maintainer, 2026-06-03):** the editorial api→pulumi mappings that stabilize the Pulumi
surface across UniFi API versions must be **data** — an external, reviewable, diffable file — **not Go
code**. A generic, version-agnostic engine applies the data; the data file is the source of truth for
the public API surface.

This refines the Track-D decision ([TRACK-D-DECISION.md](TRACK-D-DECISION.md)): per-variant + polish is
the target shape; this doc governs *how* the editorial decisions behind that shape are stored.

## Why (cross-version stability)

- Resource/function token names — and other api→pulumi decisions — are **public API**. A UniFi spec
  bump causes *incidental* naming drift (a renamed component schema → a different auto-derived token).
  Without a stable mapping, a bump silently renames consumer-facing resources = a breaking change.
- A data mapping file **decouples the public Pulumi names from the spec's incidental naming** and
  becomes the explicit, version-controlled contract. The engine derives a sensible default; the data
  file *pins/overrides* for stability.

## Design

- **One declarative mapping file** (e.g. `provider/pkg/gen/mappings.yaml`) = the single source of truth
  for the editorial layer:
  - discriminator handling (which property; derive-const on/off per entity),
  - token names / entity prefixes per entity + variant,
  - function renames + irregular-plural exceptions,
  - immutable / `replaceOnChanges` fields,
  - description overrides (where synthesized text isn't good enough),
  - **exclusions** (fold today's `excludedPaths` in here too).
- **The gen passes become a generic engine** that reads this data and applies it. No naming/const/plural
  literals embedded in Go.
- **Derive by default, pin by exception.** The engine computes a default from the spec where it's
  unambiguous; the data file holds only the overrides/pins + anything not derivable. This keeps the data
  file small and the engine spec-driven.

## Relationship to the token golden

- `mappings.yaml` = **CONTROL** — decides the names.
- `provider/pkg/gen/testdata/tokens.txt` (the golden) = **GUARD** — asserts the emitted token set
  matches the reviewed set.
- **On a UniFi spec bump:** the engine derives/applies mappings; an entity with no mapping that is *not*
  cleanly derivable → engine flags "unmapped entity" (loud failure); the golden diff → human review;
  every already-mapped entity keeps its pinned token → **stable tokens across versions**.

## Sequencing (avoid premature schema design)

1. **Track-D Phase 1** (in progress) surfaces the *full* set of editorial mappings actually needed —
   its **CODEGEN-PURITY report is the inventory**. Its Go-literal maps are the seed content for the data
   file (a small, known refactor — not wasted work).
2. **New ticket S0.5 — mapping-data layer:** design `mappings.yaml` + a loader covering that inventory;
   migrate Phase-1's Go-literal maps into it; refactor `pass_discriminator` + `pass_token_rename` to read
   it; fold `excludedPaths` in.
3. **Do S0.5 BEFORE Track-D Phase 2** (descriptions, de-page, enum, replaceOnChanges) so those passes
   are data-driven from the start (immutable-fields + description overrides land in the data file too).

## Resulting hand-written surface

The only hand-maintained editorial artifact becomes `mappings.yaml` (data). The Go is a generic,
spec-version-agnostic engine + a loader. This is the strongest form of the project's "track the
abstract upstream, minimal opinionated code" thesis: the *opinions* are data; the *code* is a
mechanism.

## Open design questions (resolve when building S0.5)

- **One cumulative file vs per-version files?** Lean cumulative (one contract reconciled on each bump);
  per-version files fragment. Revisit only if a major version genuinely needs old+new tokens
  side-by-side.
- **Format:** YAML (human-friendly, comments) vs JSON (no new dep if reused). Loader is trivial either
  way.
- **Embedding:** the engine runs at build/codegen time, so the file can be read from disk or
  `//go:embed`-ed into the gen binary (mirrors how the 3 artifacts are embedded into the plugin).
