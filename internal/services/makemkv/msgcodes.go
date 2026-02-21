package makemkv

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
)

// MakeMKV MSG codes. These are emitted as MSG:code,... lines on stdout
// in --robot mode. Codes >= 5000 are disc/rip-level messages; codes < 5000
// are general informational or I/O-level messages.
const (
	MsgReadError            = 2003 // Read error (classify by text)
	MsgWriteError           = 2019 // Write error (fatal if "No such file")
	MsgTitleError           = 5003 // Single title save failed
	MsgRipCompleted         = 5004 // "N titles saved, M failed"
	MsgDiscOpenError        = 5010 // Can't open disc
	MsgEvalExpiredTooOld    = 5021 // License/app too old (fatal)
	MsgRipSummary           = 5037 // Copy complete summary
	MsgEvalPeriodExpired    = 5052 // Eval period warning
	MsgEvalExpiredShareware = 5055 // Shareware expired (fatal)
	MsgBackupFailed         = 5080 // Backup mode failed
)

// msgHandler processes MSG lines from MakeMKV output and tracks rip outcome.
type msgHandler struct {
	logger      *slog.Logger
	cancelRip   context.CancelCauseFunc
	savedCount  int
	failedCount int
	fatalErr    error
	readErrors  int
	bareErrors  int
}

// handleMSG dispatches a single MSG line to the appropriate handler based on
// the numeric code. It replaces the inline code >= 5000 check in executeRip.
func (h *msgHandler) handleMSG(line string) {
	code := parseMSGCode(line)
	text := parseMSGText(line)

	switch code {
	case MsgReadError:
		h.handleReadError(text)
	case MsgWriteError:
		h.handleWriteError(text)
	case MsgTitleError:
		h.handleTitleError(text)
	case MsgRipCompleted:
		h.handleRipCompleted(line, text)
	case MsgDiscOpenError:
		h.handleDiscOpenError(text)
	case MsgEvalExpiredTooOld, MsgEvalExpiredShareware:
		h.handleLicenseExpired(code, text)
	case MsgRipSummary:
		h.handleRipSummary(text)
	case MsgEvalPeriodExpired:
		h.handleEvalWarning(text)
	case MsgBackupFailed:
		h.handleBackupFailed(text)
	default:
		if code >= 5000 {
			if h.logger != nil {
				h.logger.Warn("makemkv disc message",
					slog.String("event_type", "makemkv_disc_message"),
					slog.Int("msg_code", code),
					slog.String("msg_text", text),
				)
			}
		} else {
			if h.logger != nil {
				h.logger.Debug("makemkv message",
					slog.Int("msg_code", code),
					slog.String("msg_text", text),
				)
			}
		}
	}
}

func (h *msgHandler) handleReadError(text string) {
	h.readErrors++
	upper := strings.ToUpper(text)
	var classification string
	switch {
	case strings.Contains(upper, "TRAY OPEN"):
		classification = "tray_open"
	case strings.Contains(upper, "L-EC UNCORRECTABLE"):
		classification = "uncorrectable_read"
	case strings.Contains(upper, "HARDWARE ERROR"):
		classification = "hardware_error"
	default:
		classification = "read_error"
	}
	if h.logger != nil {
		h.logger.Warn("makemkv read error",
			slog.String("event_type", "makemkv_read_error"),
			slog.String("error_hint", "disc may have physical damage or drive issue"),
			slog.String("impact", "rip may produce corrupted or incomplete output"),
			slog.String("classification", classification),
			slog.Int("read_error_count", h.readErrors),
			slog.String("msg_text", text),
		)
	}
}

func (h *msgHandler) handleBareError(line string) {
	h.bareErrors++
	upper := strings.ToUpper(line)
	var classification string
	switch {
	case strings.Contains(upper, "L-EC UNCORRECTABLE"):
		classification = "uncorrectable_read"
	case strings.Contains(upper, "HARDWARE ERROR"):
		classification = "hardware_error"
	case strings.Contains(upper, "MEDIUM ERROR"):
		classification = "medium_error"
	default:
		classification = "disc_error"
	}
	if h.logger != nil {
		h.logger.Warn("makemkv disc error",
			slog.String("event_type", "makemkv_disc_error"),
			slog.String("error_hint", "disc may have physical damage or drive issue"),
			slog.String("impact", "output may be corrupted or incomplete"),
			slog.String("classification", classification),
			slog.Int("disc_error_count", h.bareErrors),
			slog.String("detail", line),
		)
	}
}

func (h *msgHandler) handleWriteError(text string) {
	if h.logger != nil {
		h.logger.Error("makemkv write error",
			slog.String("event_type", "makemkv_write_error"),
			slog.String("msg_text", text),
		)
	}
	if strings.Contains(text, "No such file") {
		h.fatalErr = &ServiceMsgError{
			Code:    MsgWriteError,
			Message: text,
			Hint:    "check that the output directory exists and is writable",
		}
		h.cancelRip(h.fatalErr)
	}
}

func (h *msgHandler) handleTitleError(text string) {
	if h.logger != nil {
		h.logger.Warn("makemkv title save failed",
			slog.String("event_type", "makemkv_title_error"),
			slog.String("error_hint", "one title failed but other titles may succeed"),
			slog.String("impact", "single title missing from output"),
			slog.String("msg_text", text),
		)
	}
}

func (h *msgHandler) handleRipCompleted(line, text string) {
	saved, failed := ParseMSGSprintf(line)
	h.savedCount = saved
	h.failedCount = failed
	if h.logger != nil {
		h.logger.Info("makemkv rip result",
			slog.String("event_type", "makemkv_rip_result"),
			slog.Int("titles_saved", saved),
			slog.Int("titles_failed", failed),
			slog.String("msg_text", text),
		)
	}
	if saved == 0 {
		h.fatalErr = &ServiceMsgError{
			Code:    MsgRipCompleted,
			Message: text,
			Hint:    "MakeMKV completed but saved 0 titles; check disc readability",
		}
	}
}

func (h *msgHandler) handleDiscOpenError(text string) {
	if h.logger != nil {
		h.logger.Warn("makemkv disc open error",
			slog.String("event_type", "makemkv_disc_open_error"),
			slog.String("error_hint", "disc may not be readable or drive may be busy"),
			slog.String("impact", "rip cannot proceed until disc is accessible"),
			slog.String("msg_text", text),
		)
	}
}

func (h *msgHandler) handleLicenseExpired(code int, text string) {
	if h.logger != nil {
		h.logger.Error("makemkv license expired",
			slog.String("event_type", "makemkv_license_expired"),
			slog.Int("msg_code", code),
			slog.String("msg_text", text),
		)
	}
	h.fatalErr = &ServiceMsgError{
		Code:    code,
		Message: text,
		Hint:    "update or register MakeMKV",
	}
	h.cancelRip(h.fatalErr)
}

func (h *msgHandler) handleRipSummary(text string) {
	if h.logger != nil {
		h.logger.Info("makemkv copy summary",
			slog.String("event_type", "makemkv_rip_summary"),
			slog.String("msg_text", text),
		)
	}
}

func (h *msgHandler) handleEvalWarning(text string) {
	if h.logger != nil {
		h.logger.Warn("makemkv evaluation period expiring",
			slog.String("event_type", "makemkv_eval_warning"),
			slog.String("error_hint", "MakeMKV evaluation period is expiring; consider purchasing a license"),
			slog.String("impact", "ripping will stop working when evaluation expires"),
			slog.String("msg_text", text),
		)
	}
}

func (h *msgHandler) handleBackupFailed(text string) {
	if h.logger != nil {
		h.logger.Error("makemkv backup failed",
			slog.String("event_type", "makemkv_backup_failed"),
			slog.String("msg_text", text),
		)
	}
}

// ServiceMsgError wraps a MakeMKV MSG code into an error with a hint.
type ServiceMsgError struct {
	Code    int
	Message string
	Hint    string
}

func (e *ServiceMsgError) Error() string {
	if e.Hint != "" {
		return e.Message + " (" + e.Hint + ")"
	}
	return e.Message
}

// ParseMSGSprintf extracts the saved and failed counts from a MSG:5004 line.
// MakeMKV sprintf fields are the 6th+ comma-separated fields (indices 5+).
// For MSG:5004, sprintf[0] = saved count, sprintf[1] = failed count.
func ParseMSGSprintf(line string) (saved, failed int) {
	if !strings.HasPrefix(line, "MSG:") {
		return 0, 0
	}
	payload := strings.TrimPrefix(line, "MSG:")

	// Walk through fields respecting quoted strings.
	fieldIdx := 0
	inQuote := false
	start := 0
	var sprintfFields []string

	for i := 0; i < len(payload); i++ {
		switch payload[i] {
		case '"':
			inQuote = !inQuote
		case ',':
			if !inQuote {
				if fieldIdx >= 5 {
					sprintfFields = append(sprintfFields, trimMSGField(payload[start:i]))
				}
				fieldIdx++
				start = i + 1
			}
		}
	}
	// Capture trailing field
	if fieldIdx >= 5 {
		sprintfFields = append(sprintfFields, trimMSGField(payload[start:]))
	}

	if len(sprintfFields) >= 1 {
		saved, _ = strconv.Atoi(strings.TrimSpace(sprintfFields[0]))
	}
	if len(sprintfFields) >= 2 {
		failed, _ = strconv.Atoi(strings.TrimSpace(sprintfFields[1]))
	}
	return saved, failed
}
