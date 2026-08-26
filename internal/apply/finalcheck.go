package apply

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/five82/spindle/internal/language"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

// avSyncDriftToleranceMS bounds how far the delivered output's primary-audio
// start offset may move relative to the ripped source. Reel validates its own
// encode, but audio refinement, commentary disposition, and subtitle muxing
// all rewrite the file afterwards, so apply re-measures against the source.
const avSyncDriftToleranceMS = 100.0

// muxDurationToleranceSec bounds the duration change a subtitle mux may
// introduce. mkvmerge copies streams, so a real change means a truncated mux.
const muxDurationToleranceSec = 1.0

// finalExpectation records what the apply stage intended for one output so the
// post-rewrite probe is checked against that plan instead of against whatever
// the file happens to contain.
type finalExpectation struct {
	key         string
	encodedPath string
	// encodedDuration is the video duration measured before subtitle muxing.
	// Zero means it could not be determined and the mux check is skipped.
	encodedDuration float64
	// commentary holds the audio-stream ordinals handed to the commentary
	// disposition pass. Empty means no track should carry the comment flag.
	commentary []int
	// keptAudio is the refinement plan's audio track count. Zero means
	// refinement produced no plan and the count is not enforced.
	keptAudio int
}

// verifyFinalOutputs probes every file the organizer will deliver and checks
// it against the apply stage's plan and the ripped source. Failures flag the
// episode and the item for review — the item still completes, because review
// routing is what acts on the verdict — and the verdict is persisted so the
// external audit reads it instead of recomputing it from artifacts that
// staging cleanup has already deleted.
func verifyFinalOutputs(
	ctx context.Context,
	sess *stage.Session,
	expectations []finalExpectation,
	sourceAudioIndex int,
) (*ripspec.FinalValidation, error) {
	logger := logs.Default(sess.Logger)
	verdict := &ripspec.FinalValidation{Passed: true}

	for _, exp := range expectations {
		entry := checkFinalOutput(ctx, sess.Env, exp, sourceAudioIndex)
		if entry.Error != "" {
			logger.Warn("final output could not be probed",
				"event_type", "final_validation_unavailable",
				"error_hint", entry.Error,
				"impact", "delivered output was not verified",
				"episode_key", exp.key,
			)
		}
		if entry.AVSync != nil && entry.AVSync.Error != "" {
			logger.Warn("A/V sync check unavailable",
				"event_type", "final_validation_unavailable",
				"error_hint", entry.AVSync.Error,
				"impact", "output audio timing was not compared against the ripped source",
				"episode_key", exp.key,
			)
		}
		for _, failure := range entry.FailedChecks {
			logger.Warn("final output validation failed",
				"event_type", "final_validation_failed",
				"error_hint", failure,
				"impact", "output routed to review",
				"episode_key", exp.key,
			)
			if err := flagForReview(sess, exp.key, "final_validation: "+failure); err != nil {
				return nil, err
			}
		}

		result := "passed"
		reason := "output matches the ripped source and the apply plan"
		switch {
		case len(entry.FailedChecks) > 0:
			result = "flagged_for_review"
			reason = strings.Join(entry.FailedChecks, "; ")
		case entry.Error != "" || (entry.AVSync != nil && entry.AVSync.Error != ""):
			result = "incomplete"
			reason = "output or ripped source could not be probed"
		}
		logger.Info("final output validated",
			"decision_type", logs.DecisionFinalValidation,
			"decision_result", result,
			"decision_reason", reason,
			"episode_key", exp.key,
			"output_path", entry.OutputPath,
		)

		if !entry.Passed {
			verdict.Passed = false
		}
		verdict.Entries = append(verdict.Entries, entry)
	}
	return verdict, nil
}

// checkFinalOutput probes the delivered file once (plus the ripped source once
// for the sync comparison) and reports every check that did not hold.
func checkFinalOutput(
	ctx context.Context,
	env *ripspec.Envelope,
	exp finalExpectation,
	sourceAudioIndex int,
) ripspec.FinalValidationEntry {
	outputPath := exp.encodedPath
	muxed := false
	if asset, ok := env.Assets.FindAsset(ripspec.AssetKindSubtitled, exp.key); ok && asset.IsCompleted() {
		outputPath = asset.Path
		muxed = asset.SubtitlesMuxed
	}
	entry := ripspec.FinalValidationEntry{EpisodeKey: exp.key, OutputPath: outputPath, Passed: true}

	output, err := ffprobe.Inspect(ctx, "", outputPath)
	if err != nil {
		entry.Error = err.Error()
		return entry
	}

	entry.AVSync = checkAVSync(ctx, env, exp.key, output, sourceAudioIndex)
	if entry.AVSync.Error == "" && !entry.AVSync.Passed {
		entry.FailedChecks = append(entry.FailedChecks, fmt.Sprintf(
			"av_sync drift %.0fms exceeds %.0fms", entry.AVSync.DriftMilliseconds, avSyncDriftToleranceMS))
	}
	if err := validateAudioDurations(exp.key, output); err != nil {
		entry.FailedChecks = append(entry.FailedChecks, err.Error())
	}
	entry.FailedChecks = append(entry.FailedChecks, checkSubtitleLayout(env, exp.key, muxed, output)...)
	entry.FailedChecks = append(entry.FailedChecks, checkCommentaryLabels(exp.commentary, output)...)
	entry.FailedChecks = append(entry.FailedChecks, checkAudioLayout(exp.keptAudio, output)...)
	if muxed {
		entry.FailedChecks = append(entry.FailedChecks, checkMuxDuration(exp.encodedDuration, output)...)
	}

	entry.Passed = len(entry.FailedChecks) == 0
	return entry
}

// checkAVSync compares the primary audio's offset from video in the ripped
// source against the delivered output. A missing source or an unreadable
// probe is unavailable, not a failure: the check cannot judge either way.
func checkAVSync(
	ctx context.Context,
	env *ripspec.Envelope,
	key string,
	output *ffprobe.Result,
	sourceAudioIndex int,
) *ripspec.AVSyncCheck {
	check := &ripspec.AVSyncCheck{}
	ripped, ok := env.Assets.FindAsset(ripspec.AssetKindRipped, key)
	if !ok || strings.TrimSpace(ripped.Path) == "" {
		check.Error = "ripped source asset unavailable"
		return check
	}
	check.SourcePath = ripped.Path
	source, err := ffprobe.Inspect(ctx, "", ripped.Path)
	if err != nil {
		check.Error = err.Error()
		return check
	}
	return compareAVStartTimes(check, source, output, sourceAudioIndex)
}

func compareAVStartTimes(check *ripspec.AVSyncCheck, source, output *ffprobe.Result, sourceAudioIndex int) *ripspec.AVSyncCheck {
	sourceVideo, sourceAudio, err := primaryAVStartTimes(source, sourceAudioIndex)
	if err != nil {
		check.Error = "source: " + err.Error()
		return check
	}
	outputVideo, outputAudio, err := primaryAVStartTimes(output, -1)
	if err != nil {
		check.Error = "output: " + err.Error()
		return check
	}
	check.SourceAudioOffsetSec = sourceAudio - sourceVideo
	check.OutputAudioOffsetSec = outputAudio - outputVideo
	check.DriftMilliseconds = (check.OutputAudioOffsetSec - check.SourceAudioOffsetSec) * 1000
	check.Passed = math.Abs(check.DriftMilliseconds) <= avSyncDriftToleranceMS
	return check
}

// primaryAVStartTimes returns the first video stream's start time and the
// start time of the audio stream at the given ordinal. A negative ordinal
// selects the default-flagged audio stream, falling back to the first one.
func primaryAVStartTimes(result *ffprobe.Result, audioIndex int) (float64, float64, error) {
	if result == nil {
		return 0, 0, fmt.Errorf("probe unavailable")
	}
	var video, audio, firstAudio, defaultAudio *ffprobe.Stream
	audioOrdinal := 0
	for i := range result.Streams {
		stream := &result.Streams[i]
		switch stream.CodecType {
		case "video":
			if video == nil {
				video = stream
			}
		case "audio":
			if firstAudio == nil {
				firstAudio = stream
			}
			if defaultAudio == nil && stream.Disposition["default"] == 1 {
				defaultAudio = stream
			}
			if audioOrdinal == audioIndex {
				audio = stream
			}
			audioOrdinal++
		}
	}
	if video == nil {
		return 0, 0, fmt.Errorf("video stream unavailable")
	}
	if audioIndex < 0 {
		audio = defaultAudio
		if audio == nil {
			audio = firstAudio
		}
	}
	if audio == nil {
		return 0, 0, fmt.Errorf("primary audio stream unavailable")
	}
	videoStart, err := strconv.ParseFloat(video.StartTime, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("video start time unavailable")
	}
	audioStart, err := strconv.ParseFloat(audio.StartTime, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("primary audio start time unavailable")
	}
	return videoStart, audioStart, nil
}

// checkSubtitleLayout reconciles the output's subtitle streams against the
// subtitle stage's record for the episode. An adopted subtitle that was never
// muxed is not judged here: muxing is either disabled by config or failed,
// and the mux failure flags review on its own.
func checkSubtitleLayout(env *ripspec.Envelope, key string, muxed bool, output *ffprobe.Result) []string {
	streams := subtitleStreams(output)
	record := findSubtitleGenRecord(env, key)
	if record == nil {
		return nil
	}
	if strings.EqualFold(record.Source, "none") {
		if len(streams) > 0 {
			return []string{fmt.Sprintf("%d subtitle stream(s) present for a skipped subtitle", len(streams))}
		}
		return nil
	}
	if !muxed {
		return nil
	}
	if len(streams) != 1 {
		return []string{fmt.Sprintf("adopted subtitle expects 1 subtitle stream, found %d", len(streams))}
	}

	stream := streams[0]
	forced := stream.Disposition["forced"] == 1
	lang := strings.TrimSpace(stream.Tags["language"])
	title := stream.Tags["title"]

	var failures []string
	if !strings.EqualFold(stream.CodecName, "subrip") {
		failures = append(failures, fmt.Sprintf("subtitle codec %q is not subrip", stream.CodecName))
	}
	if lang == "" {
		failures = append(failures, "subtitle stream has no language tag")
	}
	if !subtitleLabelCorrect(lang, title, forced) {
		failures = append(failures, fmt.Sprintf("subtitle label %q does not identify language %q", title, lang))
	}
	if forced {
		failures = append(failures, "subtitle stream is flagged forced")
	}
	if stream.Disposition["default"] == 1 {
		failures = append(failures, "subtitle stream is flagged default")
	}
	return failures
}

// subtitleLabelCorrect requires a non-empty title that names the stream's
// language, and says "forced" when the stream is forced.
func subtitleLabelCorrect(lang, title string, forced bool) bool {
	title = strings.TrimSpace(title)
	if title == "" {
		return false
	}
	if forced && !strings.Contains(strings.ToLower(title), "forced") {
		return false
	}
	if lang == "" {
		return true
	}
	display := strings.ToLower(language.DisplayName(lang))
	return display == "" || strings.Contains(strings.ToLower(title), display) ||
		strings.Contains(strings.ToLower(title), strings.ToLower(lang))
}

func subtitleStreams(result *ffprobe.Result) []ffprobe.Stream {
	var out []ffprobe.Stream
	for _, stream := range result.Streams {
		if stream.CodecType == "subtitle" {
			out = append(out, stream)
		}
	}
	return out
}

// checkCommentaryLabels verifies that exactly the tracks the disposition pass
// targeted carry the comment flag and a "Commentary" title.
func checkCommentaryLabels(expected []int, output *ffprobe.Result) []string {
	wanted := make(map[int]bool, len(expected))
	for _, index := range expected {
		wanted[index] = true
	}

	var failures []string
	for index, stream := range output.AudioStreams() {
		flagged := stream.Disposition["comment"] == 1
		title := stream.Tags["title"]
		if !wanted[index] {
			if flagged {
				failures = append(failures, fmt.Sprintf("audio track %d carries the comment flag but is not commentary", index))
			}
			continue
		}
		if !flagged {
			failures = append(failures, fmt.Sprintf("commentary track %d is missing the comment flag", index))
		}
		if !strings.Contains(strings.ToLower(title), "commentary") {
			failures = append(failures, fmt.Sprintf("commentary track %d title %q lacks a Commentary label", index, title))
		}
	}
	return failures
}

// checkMuxDuration verifies the subtitle mux preserved the runtime it was
// handed. mkvmerge copies streams, so a changed duration means a bad mux.
// A duration neither side could measure is not judged.
func checkMuxDuration(encodedDuration float64, output *ffprobe.Result) []string {
	muxedDuration := videoDurationSeconds(output)
	if encodedDuration <= 0 || muxedDuration <= 0 {
		return nil
	}
	if math.Abs(muxedDuration-encodedDuration) <= muxDurationToleranceSec {
		return nil
	}
	return []string{fmt.Sprintf("subtitle mux changed duration %.3fs -> %.3fs", encodedDuration, muxedDuration)}
}

// checkAudioLayout verifies the surviving track count, the single default
// track, and that an English track is the default when one exists.
func checkAudioLayout(keptAudio int, output *ffprobe.Result) []string {
	streams := output.AudioStreams()
	if len(streams) == 0 {
		return []string{"output has no audio streams"}
	}

	var failures []string
	if keptAudio > 0 && len(streams) != keptAudio {
		failures = append(failures, fmt.Sprintf("audio stream count %d does not match the refinement plan's %d", len(streams), keptAudio))
	}
	hasEnglish := false
	englishIsDefault := false
	for index, stream := range streams {
		isDefault := stream.Disposition["default"] == 1
		if index == 0 && !isDefault {
			failures = append(failures, "first audio stream is not the default track")
		}
		if index > 0 && isDefault {
			failures = append(failures, fmt.Sprintf("non-primary audio track %d is also default", index))
		}
		if strings.HasPrefix(language.ToISO2(language.ExtractFromTags(stream.Tags)), "en") {
			hasEnglish = true
			if isDefault {
				englishIsDefault = true
			}
		}
	}
	if hasEnglish && !englishIsDefault {
		failures = append(failures, "default audio track is not English despite an English track being present")
	}
	return failures
}
