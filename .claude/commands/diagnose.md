# Spindle Diagnostic Skill

Diagnose Spindle processing issues by analyzing structured logs.

## Usage

`/diagnose <item_id>` - Diagnose a specific queue item
`/diagnose` - Diagnose daemon-level issues

## Procedure

1. Check daemon status: `spindle status`
2. Get item info (if item specified): `spindle queue describe <id> --json`
3. Check for debug logs first (they have richer context):
   - Look for `log_dir/debug/items/<item>.log` matching the item ID
   - Debug logs may exist from a previous diagnostic run even if daemon is now stopped or running in normal mode
4. Fall back to normal logs if no debug logs exist: `log_dir/items/<item>.log`
5. Search for problem patterns (see catalog below)
6. Report findings with severity and recommendations

**Always prefer debug logs when they exist** - they contain full decision context regardless of current daemon state.

## Normal vs Diagnostic Mode

| Mode | How to Enable | Log Level | Log Paths | Use Case |
|------|---------------|-----------|-----------|----------|
| Normal | `spindle start` | INFO | `log_dir/items/*.log` | Production, decision summaries only |
| Diagnostic | `spindle start --diagnostic` | DEBUG | `log_dir/debug/items/*.log` | Full candidate scoring, per-track analysis |

**Diagnostic mode creates:**
- `log_dir/debug/spindle-<runID>.log` - DEBUG daemon log (JSON)
- `log_dir/debug/items/<item>.log` - DEBUG per-item logs (JSON)
- `log_dir/debug/spindle.log` - symlink to current run
- Session ID (UUID) for correlation across logs

**When to use diagnostic mode:**
- Episode misidentification investigation (need candidate scoring)
- Track selection problems (need per-track analysis)
- Preset decision issues (need LLM payloads)
- Subtitle alignment failures (need WhisperX debug output)

**Debug logs persist** - even after the daemon stops or restarts in normal mode, debug logs from previous diagnostic runs remain in `log_dir/debug/`. Always check for them.

## Log Access

**Via API** (daemon running):
```bash
# Warnings for specific item
curl "http://127.0.0.1:7487/api/logs?item=<ID>&level=WARN&lane=*"

# Decision events (shows decision_type, decision_result, decision_reason)
curl "http://127.0.0.1:7487/api/logs?item=<ID>&decision_type=*&lane=*"

# Daemon-level issues
curl "http://127.0.0.1:7487/api/logs?daemon_only=1&level=WARN"

# Full DEBUG logs (if log_level=debug in config)
curl "http://127.0.0.1:7487/api/logs?item=<ID>&level=DEBUG&lane=*"
```

**Via log file** (daemon may be stopped):
- Debug logs (check first): `log_dir/debug/items/YYYYMMDDTHHMMSS-<id>-<slug>.log`
- Normal logs (fallback): `log_dir/items/YYYYMMDDTHHMMSS-<id>-<slug>.log`
- Current debug symlink: `log_dir/debug/spindle.log`

**Always check for debug logs first** - they persist from previous diagnostic runs and contain full decision context that INFO logs omit.

## Problem Pattern Catalog

| Pattern | Stage | Log Fingerprint | Impact |
|---------|-------|-----------------|--------|
| Duplicate fingerprint | Identification | `decision_type=duplicate_fingerprint` | Item silently rejected |
| Low TMDB confidence | Identification | `decision_type=tmdb_confidence` with low score | Wrong title match |
| Episode misidentification | Identification | `decision_type=episode_match` with low score | Wrong S##E## labels |
| Preset fallback | Encoding | `alert=preset_decider_fallback` | Suboptimal encoding settings |
| Episode subtitle failure | Subtitles | `event_type=episode_subtitle_failed` | Some episodes missing subtitles |
| SRT validation issues | Subtitles | `event_type=srt_validation_issues` | Malformed subtitles |
| Missing encoded episodes | Organizer | `event_type=organizer_missing_encoded` | Incomplete library |
| Subtitle move failure | Organizer | `event_type=subtitle_move_failed` | No subtitles in Jellyfin |
| Library unavailable | Organizer | `decision_reason=library_unavailable` | Content in review_dir |

**DEBUG-only patterns** (require diagnostic mode):

| Pattern | Stage | Log Fingerprint | What It Reveals |
|---------|-------|-----------------|-----------------|
| TMDB candidate scoring | Identification | `decision_type=tmdb_search` | All candidates + scores |
| Episode runtime matching | Identification | `decision_type=episode_runtime_match` | Duration comparisons |
| Track selection | Ripping | `decision_type=track_select` | Per-track keep/skip reasons |
| Preset LLM response | Encoding | `decision_type=preset_llm` | LLM prompt/response |
| Subtitle ranking | Subtitles | `decision_type=subtitle_rank` | Candidate scoring |

## Diagnostic Report Format

Output your findings in this format:

```
## Diagnostic Report for Item #<id>

**Status:** <status> | **NeedsReview:** <bool> | **ReviewReason:** <reason>
**Diagnostic Mode:** active/inactive

### Issues Found

**[CRITICAL/WARNING/INFO] <Pattern Name>**
- Evidence: <log excerpt>
- Impact: <description>
- Recommendation: <action>

### Decision Trace (if diagnostic mode)
<key decisions with decision_type, decision_result, decision_reason>

### Log Summary
- WARN events: <count>
- Decision events: <list>
- Alert flags: <list>
```
