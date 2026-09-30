---
name: itemaudit
description:
  Diagnose a Spindle queue item or daemon issue from audit artifacts. Use for
  root-cause investigation, not for subtitle wording review or manual disc
  orchestration.
user-invocable: true
argument-hint: [item_id]
---

# Spindle Item Audit

`/itemaudit <item_id>` audits an item; `/itemaudit` investigates daemon-level
issues with `spindle status` and `spindle queue list`.

An audit is read-only. Trace each symptom to the earliest wrong decision or
violated invariant, distinguishing code bugs from source data, external
services, configuration, and operator actions. Group downstream effects under
their cause; do not prescribe hand-editing an output to hide a pipeline bug. If
asked to fix one, add a regression test based on the evidence, run project
checks, and reprocess/re-audit when the source is available. If reprocessing is
blocked, report exactly what remains unverified. Follow repository daemon,
deployment, and queue rules.

**Subtitle content is out of scope.** Never read, extract, sample, quote,
compare, or judge subtitle/transcript cue text. Check only pipeline integrity,
metadata, routing, muxing, stream format, dispositions, and labels. For wording
problems, suggest `spindle subtitle <mkv>` or the whisperx-subtitles skill; do
not run modifying commands during an audit.

## Start with the evidence

Run `spindle queue audit <item_id>`. It prints a deterministic text digest and
writes the full JSON to a temp path shown in the header. Read the entire digest,
then check gathering errors, `stage_gate`, and pre-flagged anomalies. The digest
is a starting point, not the audit: investigate every anomaly, warning, error,
and suspicious value, consulting the full JSON for omissions and raw evidence.
Always read the full JSON `transitions` for stage outcomes, retries, and
encoding substages; they are not in the digest's Events section. Use the
pre-computed `stage_gate.phase_*` booleans rather than re-deriving applicability
from the coarse item stage. For failed items, diagnose the failure without
padding the report with phases not reached.

For JSON drill-down, use `python3 << 'PYEOF'` with `json.load(open(path))`; not
`python3 -c "..."` or a pipe into the heredoc's stdin. Consult
[references/schema.md](references/schema.md) only for fields or stage gates you
need to interpret; the JSON schema is diagnostic, not a stable API.

## Read only applicable references

- [references/logs.md](references/logs.md): logs, decisions, lifecycle, and
  transitions (`phase_logs`, always true for items).
- [references/rip-and-episodes.md](references/rip-and-episodes.md): rip cache
  (`phase_rip_cache`), TV episode identification (`phase_episode_id`), and final
  routing if organizing was reached or review flags are implicated.
- [references/encode-and-subtitles.md](references/encode-and-subtitles.md):
  encoded output (`phase_encoded`), crop (`phase_crop`), subtitle pipeline
  (`phase_subtitles`), and applicable external crop validation. Read only the
  phase sections whose gates are true.
- [references/commentary.md](references/commentary.md): commentary track
  validation (`phase_commentary`), including external commentary comparison only
  when `phase_external_validation` is true.
- [references/patterns.md](references/patterns.md): optional diagnostic index
  for ambiguous symptoms or unexpected findings; not a prerequisite to every
  audit.
- [references/report.md](references/report.md): presentation rules, report
  template, and stage-gated execution checklist. Read when reporting.

Report only applicable phases. Lead with root causes and evidence, separate fact
from inference, and keep clean sections compact. Do not claim semantic
verification of TV identity from probability or reference-label metadata, or
subtitle wording from stream metadata. For dialogue-reference episode matching,
trace title trust, acquisition, and per-episode review routing separately from
the classifier probability.
