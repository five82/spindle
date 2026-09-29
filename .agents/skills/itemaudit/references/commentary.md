### Phase 7: Commentary Track Validation (when `phase_commentary` is true)

Analyze commentary decisions from `analysis.decision_groups` and audio streams from `media[]`:

1. **From decisions**: Find `decision_type=commentary_classification`, `commentary_stereo_filter`, `commentary_remapping`, and `commentary_disposition` groups
2. **Expected behavior**:
   - All non-primary English or unknown-language audio tracks are candidates, not just 2-channel tracks. Explicit non-English tracks are filtered before transcription.
   - Every candidate is classified before similarity can exclude it. High similarity alone is NOT proof of duplicate audio: mixed commentary can contain extensive program dialogue. Exclusion requires both a non-commentary classification and similarity at or above the configured threshold; the reason distinguishes a stereo downmix from a multichannel duplicate/core.
   - Commentary uses `typesafe/jev-1.13` through OpenRouter's System One API, with one typed Choice question and P(commentary) >= 0.65. Read `analysis.decision_groups[].entries[].extras.commentary_probability` and the comparison in `decision_reason`. The digest already renders both; `analysis.audio_summary.commentary_decisions` also preserves the evidence in JSON.
   - The probability is NOT Jev's separate distribution-confidence statistic, the old chat-model confidence, or an episode-match score. A commentary acceptance at 0.65-0.79 is valid, not an automatic low-confidence finding. Jev returns no free-text explanation; reasons such as `Jev commentary probability 0.7 >= 0.65` are the intended diagnostic.
   - For Jev-classified tracks, `envelope.attributes.audio_analysis.commentary_tracks[].confidence` (and the per-episode equivalent) stores P(commentary). On a conservative error fallback it is zero, meaning classification was unavailable, not that the track was confidently rejected. Read its `reason` and the matching warning.
   - Each candidate is transcribed ONCE (batched WhisperX invocation); the same raw transcript feeds similarity and Jev classification. The classifier sees the title and first 4,000 bytes of SRT, so its decision cannot prove that no commentary exists later in the track. There is no separate classification transcription or separate WhisperX model.
   - The primary track fingerprint comes from the shared transcript artifact when one exists (`commentary_stereo_filter` with `decision_result=artifact_reused`); otherwise the primary is transcribed once and recorded as the artifact (`envelope.assets.transcript`)
   - Missing or blank transcripts and API/schema failures conservatively preserve the affected candidate as commentary. Read WARN `event_type=commentary_detection_failed` and its impact; do not confuse this fallback with a successful Jev classification. If the whole candidate batch fails, ALL candidates are preserved (`reason: "transcription failed"`); report the batch failure as the root cause, not per-track defects. First rule out a cancelled analysis run followed by a successful rerun (see Phase 2); only treat fallback labels as a final-output defect if they survive the rerun.
   - `commentary_llm_start` / `commentary_llm_complete` remain the item-specific timing events despite the switch to Jev; their names do not mean the old chat model was used.

3. **Refinement impact**: Check `decision_type=commentary_remapping` — shows how many commentary tracks survived audio refinement. `remapped_count=0` means all commentary tracks were lost during refinement.

4. **Cross-reference with blu-ray.com** (only when `phase_external_validation` is true):
   - Check "Audio" section of disc review for commentary count
   - Compare against our detection count

5. **Verify in media probes**: Count audio streams with `disposition.comment=1` in `media[].probe.streams`

6. **Cross-episode commentary consistency** (TV only):
   - Compare program-audio profiles across episodes; commentary tracks may legitimately appear on only some episodes. Check their labels/dispositions against `final_validation` rather than treating differing total audio counts as a defect.
