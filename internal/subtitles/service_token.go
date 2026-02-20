package subtitles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"spindle/internal/logging"
	"spindle/internal/services"
	"spindle/internal/services/whisperx"
)

var (
	defaultHTTPClient       = &http.Client{Timeout: 10 * time.Second}
	errPyannoteUnauthorized = errors.New("pyannote token unauthorized")
)

const huggingFaceWhoAmIEndpoint = "https://huggingface.co/api/whoami-v2"

func (s *Service) configuredVADMethod() string {
	if s != nil && s.config != nil && strings.EqualFold(strings.TrimSpace(s.config.Subtitles.WhisperXVADMethod), whisperx.VADMethodPyannote) {
		return whisperx.VADMethodPyannote
	}
	return whisperx.VADMethodSilero
}

func (s *Service) ensureTokenReady(ctx context.Context) error {
	if s == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "token", "Subtitle service unavailable", nil)
	}
	if s.configuredVADMethod() != whisperx.VADMethodPyannote {
		return nil
	}
	if strings.TrimSpace(s.hfToken) == "" {
		return services.Wrap(services.ErrConfiguration, "subtitles", "validate vad", "pyannote VAD selected but no Hugging Face token configured (set whisperx_hf_token)", nil)
	}

	s.tokenOnce.Do(func() {
		if s.hfCheck == nil {
			s.tokenErr = services.Wrap(services.ErrConfiguration, "subtitles", "pyannote auth", "Token validator unavailable", nil)
			return
		}
		result, err := s.hfCheck(ctx, s.hfToken)
		if err != nil {
			s.tokenErr = err
			if s.whisperxSvc != nil {
				s.whisperxSvc.SetVADMethod(whisperx.VADMethodSilero)
			}
			return
		}
		s.tokenResult = &result
	})

	if s.tokenErr != nil {
		if !s.tokenFallbackLogged && s.logger != nil {
			s.logger.Warn("pyannote authentication failed; falling back to silero",
				logging.Error(s.tokenErr),
				logging.String(logging.FieldEventType, "pyannote_auth_failed"),
				logging.String(logging.FieldErrorHint, "verify whisperx_hf_token or switch whisperx_vad_method"),
			)
			s.tokenFallbackLogged = true
		}
		return nil
	}

	if !s.tokenSuccessLogged && s.tokenResult != nil && s.logger != nil {
		account := strings.TrimSpace(s.tokenResult.Account)
		if account == "" {
			account = "huggingface"
		}
		s.logger.Debug("pyannote authentication verified", logging.String("account", account))
		s.tokenSuccessLogged = true
	}

	return nil
}

func defaultTokenValidator(ctx context.Context, token string) (tokenValidationResult, error) {
	if strings.TrimSpace(token) == "" {
		return tokenValidationResult{}, services.Wrap(services.ErrConfiguration, "subtitles", "pyannote auth", "Empty Hugging Face token", nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, huggingFaceWhoAmIEndpoint, nil)
	if err != nil {
		return tokenValidationResult{}, services.Wrap(services.ErrTransient, "subtitles", "pyannote auth", "Failed to build validation request", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		return tokenValidationResult{}, services.Wrap(services.ErrTransient, "subtitles", "pyannote auth", "Failed to contact Hugging Face", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var payload struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return tokenValidationResult{}, services.Wrap(services.ErrTransient, "subtitles", "pyannote auth", "Failed to parse Hugging Face response", err)
		}
		account := strings.TrimSpace(payload.Name)
		if account == "" {
			account = "huggingface"
		}
		return tokenValidationResult{Account: account}, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		base := services.Wrap(services.ErrValidation, "subtitles", "pyannote auth", fmt.Sprintf("Hugging Face rejected token (%s)", resp.Status), nil)
		return tokenValidationResult{}, fmt.Errorf("%w: %w", errPyannoteUnauthorized, base)
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return tokenValidationResult{}, services.Wrap(services.ErrTransient, "subtitles", "pyannote auth", fmt.Sprintf("Unexpected Hugging Face response: %s", msg), nil)
	}
}
