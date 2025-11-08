package plex

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"spindle/internal/config"
)

type resolvingTokenProvider interface {
	TokenProvider
	ClientIdentifier() string
	AuthorizationToken() string
	ResolvedPlexURL() string
	SaveResolvedPlexURL(string) error
}

// CheckAuth verifies that the configured Plex server accepts the current authorization token.
func CheckAuth(ctx context.Context, cfg *config.Config, client HTTPDoer, provider TokenProvider) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	if provider == nil {
		return errors.New("token provider is nil")
	}

	configuredURL := strings.TrimRight(strings.TrimSpace(cfg.PlexURL), "/")
	if configuredURL == "" {
		return errors.New("plex_url not configured")
	}

	var requester HTTPDoer
	if client != nil {
		requester = client
	} else {
		requester = &http.Client{Timeout: 10 * time.Second}
	}

	token, err := provider.Token(ctx)
	if err != nil {
		return err
	}

	activeURL := configuredURL
	var resolver resolvingTokenProvider
	if candidate, ok := provider.(resolvingTokenProvider); ok {
		resolver = candidate
		if resolved := strings.TrimSpace(candidate.ResolvedPlexURL()); resolved != "" {
			activeURL = resolved
		} else if urlParts, parseErr := url.Parse(configuredURL); parseErr == nil && strings.EqualFold(urlParts.Scheme, "http") {
			if resolved, err := ensureResolvedPlexURL(ctx, candidate, token); err == nil && strings.TrimSpace(resolved) != "" {
				activeURL = resolved
			}
		}
	}

	doRequest := func(target string) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target+"/library/sections", nil)
		if err != nil {
			return fmt.Errorf("build plex auth request: %w", err)
		}
		req.Header.Set("X-Plex-Token", token)
		req.Header.Set("Accept", "application/xml")
		req.Header.Set("User-Agent", userAgent)

		clientID := ""
		if withID, ok := provider.(interface {
			ClientIdentifier() string
		}); ok {
			clientID = withID.ClientIdentifier()
		}
		applyStandardHeaders(req, clientID)

		resp, err := requester.Do(req)
		if err != nil {
			return fmt.Errorf("plex auth request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized {
			_, _ = io.Copy(io.Discard, resp.Body)
			return ErrAuthorizationMissing
		}
		if resp.StatusCode >= http.StatusBadRequest {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			return fmt.Errorf("plex auth request returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	if err := doRequest(activeURL); err == nil {
		return nil
	} else if resolver == nil || !needsPlexDirectFallback(err) {
		return err
	}

	resolvedURL, resolveErr := ensureResolvedPlexURL(ctx, resolver, token)
	if resolveErr != nil {
		return err
	}

	if err := doRequest(resolvedURL); err != nil {
		return err
	}
	return nil
}

func needsPlexDirectFallback(err error) bool {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return false
	}

	var hostnameErr *x509.HostnameError
	if errors.As(urlErr.Err, &hostnameErr) && certificateHasPlexDirect(hostnameErr.Certificate) {
		return true
	}

	var verifyErr *tls.CertificateVerificationError
	if errors.As(urlErr.Err, &verifyErr) {
		for _, cert := range verifyErr.UnverifiedCertificates {
			if certificateHasPlexDirect(cert) {
				return true
			}
		}
		if verifyErr.Err != nil && strings.Contains(strings.ToLower(verifyErr.Err.Error()), "plex.direct") {
			return true
		}
	}

	if strings.Contains(strings.ToLower(urlErr.Error()), "plex.direct") {
		return true
	}
	return false
}

func ensureResolvedPlexURL(ctx context.Context, provider resolvingTokenProvider, token string) (string, error) {
	if resolved := strings.TrimSpace(provider.ResolvedPlexURL()); resolved != "" {
		return resolved, nil
	}
	authToken := strings.TrimSpace(provider.AuthorizationToken())
	if authToken == "" {
		authToken = token
	}
	resolved, err := resolvePlexServerURL(ctx, authToken, provider.ClientIdentifier(), token)
	if err != nil {
		return "", err
	}
	if err := provider.SaveResolvedPlexURL(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func certificateHasPlexDirect(cert *x509.Certificate) bool {
	if cert == nil {
		return false
	}
	for _, name := range cert.DNSNames {
		if strings.Contains(strings.ToLower(name), "plex.direct") {
			return true
		}
	}
	return false
}
