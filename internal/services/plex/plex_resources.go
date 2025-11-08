package plex

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var plexResourcesEndpoint = "https://plex.tv/api/v2/resources?includeHttps=1"

type plexResourceList struct {
	Resources []plexResource `xml:"resource"`
}

type plexResource struct {
	Name             string                   `xml:"name,attr"`
	AccessToken      string                   `xml:"accessToken,attr"`
	ClientIdentifier string                   `xml:"clientIdentifier,attr"`
	Provides         string                   `xml:"provides,attr"`
	Connections      []plexResourceConnection `xml:"connections>connection"`
}

type plexResourceConnection struct {
	URI      string `xml:"uri,attr"`
	Protocol string `xml:"protocol,attr"`
	Local    string `xml:"local,attr"`
	Relay    string `xml:"relay,attr"`
	Address  string `xml:"address,attr"`
	Port     string `xml:"port,attr"`
}

func resolvePlexServerURL(ctx context.Context, authToken, clientID, desiredToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, plexResourcesEndpoint, nil)
	if err != nil {
		return "", fmt.Errorf("build plex resources request: %w", err)
	}
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("X-Plex-Token", strings.TrimSpace(authToken))
	applyStandardHeaders(req, clientID)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch plex resources: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("plex resources returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var list plexResourceList
	if err := xml.NewDecoder(resp.Body).Decode(&list); err != nil {
		return "", fmt.Errorf("decode plex resources: %w", err)
	}

	desiredToken = strings.TrimSpace(desiredToken)
	for _, res := range list.Resources {
		accessToken := strings.TrimSpace(res.AccessToken)
		if desiredToken != "" && accessToken != "" && accessToken != desiredToken {
			continue
		}

		if !strings.Contains(res.Provides, "server") {
			continue
		}

		if url := selectBestConnection(res.Connections); url != "" {
			return url, nil
		}
	}
	for _, res := range list.Resources {
		if !strings.Contains(res.Provides, "server") {
			continue
		}
		if url := selectBestConnection(res.Connections); url != "" {
			return url, nil
		}
	}
	return "", errors.New("matching plex server not found in resources response")
}

func selectBestConnection(connections []plexResourceConnection) string {
	bestScore := -1
	bestURL := ""
	for _, conn := range connections {
		uri := strings.TrimSpace(conn.URI)
		if uri == "" {
			continue
		}
		protocol := strings.ToLower(strings.TrimSpace(conn.Protocol))
		score := 0
		if protocol == "https" {
			score += 50
		} else if protocol != "" {
			score -= 10
		}

		if strings.Contains(uri, ".plex.direct") {
			score += 30
		}

		if parseBool(conn.Local) {
			score += 5
		}
		if parseBool(conn.Relay) {
			score -= 5
		}

		if score > bestScore {
			bestScore = score
			bestURL = strings.TrimRight(uri, "/")
		}
	}
	return bestURL
}

func parseBool(value string) bool {
	if value == "" {
		return false
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		return false
	}
	return b
}
