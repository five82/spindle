# Spindle Documentation

Status: Active contract.

This directory contains the documentation that is intended to stay current for
Spindle's feature-complete and bugfix-focused phase. The old clean-room rewrite
specifications were intentionally removed from the working tree; git history is
the archive for that material.

## Source of truth

Use this hierarchy when code and docs appear to disagree:

1. Tests define exact behavior where practical.
2. Code defines implementation details.
3. Active contract docs define user-visible behavior, operational policy, and
   durable architecture boundaries.
4. ADRs explain why major decisions were made.
5. Deleted rewrite specs in git history are historical only.

## Active docs

| Document | Purpose |
|----------|---------|
| [ARCHITECTURE.md](ARCHITECTURE.md) | System shape, pipeline stages, storage, dependencies, and package map |
| [API.md](API.md) | Stable CLI workflows and HTTP API contracts |
| [CONFIG.md](CONFIG.md) | Config loading, validation, derived paths, and feature gates |
| [CONTENT_ID.md](CONTENT_ID.md) | TV episode identification intent and review policy |
| [DEVELOPMENT.md](DEVELOPMENT.md) | Development workflow, tests, logging, package boundaries, doc policy |
| [user/workflow.md](user/workflow.md) | Operator-facing lifecycle and recovery guide |
| [adr/](adr/) | Architecture decision records |
| [proposals/](proposals/) | Non-normative future ideas |

## Documentation statuses

- **Active contract**: keep current. These docs describe behavior users,
  operators, external consumers, or future agents should rely on.
- **Implementation reference**: summarizes stable semantics and points to code
  or tests for exact behavior.
- **Proposal**: future idea, not implemented unless accepted by an ADR or active
  doc update.
- **ADR**: accepted decision record. ADRs explain context and trade-offs; code
  and active docs still define current behavior.

## When to update docs

A code change needs active documentation updates when it changes:

- CLI workflows users need to know.
- HTTP API endpoints, request bodies, or response fields used by consumers.
- Config options or operational file locations.
- Pipeline stage order, stage skip/failure/review semantics, or output layout.
- Subtitle output policy.
- Logging/notification behavior that affects observability.
- External dependency expectations.

A focused test is usually enough for parser changes, algorithm edge cases,
queue/RipSpec exactness, and bugfixes that do not alter a documented contract.

Major architecture changes should add or update an ADR.
