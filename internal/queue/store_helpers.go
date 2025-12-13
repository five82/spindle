package queue

import (
	"database/sql"
	"errors"
	"time"
)

const itemColumns = "id, source_path, disc_title, status, media_info_json, ripped_file, encoded_file, final_file, background_log_path, active_episode_key, error_message, created_at, updated_at, progress_stage, progress_percent, progress_message, encoding_details_json, drapto_preset_profile, rip_spec_data, disc_fingerprint, metadata_json, last_heartbeat, needs_review, review_reason"

func scanItem(scanner interface{ Scan(dest ...any) error }) (*Item, error) {
	var (
		id               int64
		sourcePath       sql.NullString
		discTitle        sql.NullString
		statusStr        string
		mediaInfo        sql.NullString
		rippedFile       sql.NullString
		encodedFile      sql.NullString
		finalFile        sql.NullString
		backgroundLog    sql.NullString
		activeEpisodeKey sql.NullString
		errorMessage     sql.NullString
		createdRaw       sql.NullString
		updatedRaw       sql.NullString
		progressStage    sql.NullString
		progressPercent  sql.NullFloat64
		progressMessage  sql.NullString
		encodingDetails  sql.NullString
		draptoPreset     sql.NullString
		ripSpec          sql.NullString
		fingerprint      sql.NullString
		metadata         sql.NullString
		lastHeartbeatRaw sql.NullString
		needsReview      sql.NullInt64
		reviewReason     sql.NullString
	)

	if err := scanner.Scan(
		&id,
		&sourcePath,
		&discTitle,
		&statusStr,
		&mediaInfo,
		&rippedFile,
		&encodedFile,
		&finalFile,
		&backgroundLog,
		&activeEpisodeKey,
		&errorMessage,
		&createdRaw,
		&updatedRaw,
		&progressStage,
		&progressPercent,
		&progressMessage,
		&encodingDetails,
		&draptoPreset,
		&ripSpec,
		&fingerprint,
		&metadata,
		&lastHeartbeatRaw,
		&needsReview,
		&reviewReason,
	); err != nil {
		return nil, err
	}

	item := &Item{
		ID:                  id,
		SourcePath:          sourcePath.String,
		DiscTitle:           discTitle.String,
		Status:              Status(statusStr),
		MediaInfoJSON:       mediaInfo.String,
		RippedFile:          rippedFile.String,
		EncodedFile:         encodedFile.String,
		FinalFile:           finalFile.String,
		BackgroundLogPath:   backgroundLog.String,
		ActiveEpisodeKey:    activeEpisodeKey.String,
		ErrorMessage:        errorMessage.String,
		ProgressStage:       progressStage.String,
		ProgressPercent:     progressPercent.Float64,
		ProgressMessage:     progressMessage.String,
		EncodingDetailsJSON: encodingDetails.String,
		DraptoPresetProfile: draptoPreset.String,
		RipSpecData:         ripSpec.String,
		DiscFingerprint:     fingerprint.String,
		MetadataJSON:        metadata.String,
	}
	if needsReview.Valid {
		item.NeedsReview = needsReview.Int64 != 0
	}
	item.ReviewReason = reviewReason.String

	if created, err := parseTimeString(createdRaw.String); err == nil {
		item.CreatedAt = created
	}
	if updated, err := parseTimeString(updatedRaw.String); err == nil {
		item.UpdatedAt = updated
	}
	if lastHeartbeatRaw.Valid {
		if heartbeat, err := parseTimeString(lastHeartbeatRaw.String); err == nil {
			item.LastHeartbeat = &heartbeat
		}
	}
	return item, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	v := value.UTC().Format(time.RFC3339Nano)
	return v
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func parseTimeString(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, errors.New("empty")
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02 15:04:05", value)
}

func makePlaceholders(count int) string {
	if count <= 0 {
		return ""
	}
	placeholders := make([]byte, 0, count*2)
	for i := 0; i < count; i++ {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
	}
	return string(placeholders)
}
