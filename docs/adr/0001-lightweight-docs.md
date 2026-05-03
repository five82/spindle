# ADR 0001: Lightweight Living Documentation

Status: Accepted
Date: 2026-05-03

## Context

Spindle used an extensive clean-room rewrite specification set while the Go
rewrite was being designed and implemented. Those docs were useful when the
implementation did not exist yet, but the project is now feature-complete and in
a bugfix-focused phase.

The rewrite specs had grown into a second codebase: thousands of lines of prose
covering exact fields, command flags, stage details, prompts, and implementation
choices that are now better represented by code and tests. Keeping that material
fully normative creates ongoing maintenance cost and increases the risk of stale
prose contradicting working code.

Spindle is a personal project. The documentation model should protect important
operator/API/architecture contracts without making every bugfix pay a large docs
tax.

## Decision

Delete the exhaustive rewrite specifications from the working tree and adopt a
lightweight living documentation model.

- Git history is the archive for deleted rewrite specs.
- Code and tests own exact implementation behavior.
- Active docs own user-visible contracts, operational guidance, architecture
  boundaries, integration assumptions, and hard policies.
- ADRs record major decisions and trade-offs.
- New or expanded specs are written only when they reduce future confusion more
  than they cost to maintain.

## Consequences

Benefits:

- Less documentation maintenance overhead.
- Lower risk of stale prose being treated as authoritative.
- Faster bugfix work.
- Clearer documentation surface for future agents and contributors.

Trade-offs:

- Some detailed rewrite-era prose is no longer browsable in the current tree.
- Historical context requires `git log` / `git show`.
- Important exact behavior needs tests or clear code rather than static prose.

Mitigations:

- Keep concise active docs for architecture, API, development policy, content ID,
  and operations.
- Add focused regression tests for important behavior changes.
- Use ADRs for major decisions.
