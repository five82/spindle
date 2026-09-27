package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

const choiceModel = "typesafe/jev-1.13"

// Choice evaluates state against named criteria using Jev, independently of the
// configured chat model. It returns the validated probability of every option,
// not Jev's separate confidence statistic. Callers own their decision policy.
func (c *Client) Choice(ctx context.Context, state, instructions string, criteria map[string]string) (map[string]float64, error) {
	if c == nil {
		return nil, fmt.Errorf("llm client not configured")
	}
	if len(criteria) < 2 {
		return nil, fmt.Errorf("choice requires at least two criteria")
	}
	request := map[string]any{
		"model": choiceModel, "state": state,
		"questions": map[string]any{"decision": map[string]any{
			"type": "choice", "instructions": instructions, "criteria": criteria,
		}},
	}
	// System One is a sibling of chat completions, sharing the configured origin
	// and API prefix. This also keeps local OpenRouter-compatible test servers usable.
	endpoint := strings.TrimSuffix(strings.TrimRight(c.baseURL, "/"), "/chat/completions") + "/systemone"
	var probabilities map[string]float64
	err := c.complete(ctx, endpoint, choiceModel, request, func(body []byte) error {
		var resp struct {
			Answers map[string]struct {
				Type          string              `json:"type"`
				Choice        string              `json:"choice"`
				Confidence    *float64            `json:"confidence"`
				Probabilities map[string]*float64 `json:"probabilities"`
			} `json:"answers"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return fmt.Errorf("unmarshal choice response: %w", err)
		}
		answer := resp.Answers["decision"]
		if len(resp.Answers) != 1 || answer.Type != "choice" || answer.Confidence == nil || *answer.Confidence < 0 || *answer.Confidence > 1 || len(answer.Probabilities) != len(criteria) {
			return fmt.Errorf("invalid choice response")
		}
		winner := answer.Probabilities[answer.Choice]
		if winner == nil {
			return fmt.Errorf("choice missing from probabilities")
		}
		probabilities = make(map[string]float64, len(criteria))
		var total float64
		for option := range criteria {
			p := answer.Probabilities[option]
			if p == nil || *p < 0 || *p > 1 || *p > *winner {
				return fmt.Errorf("invalid choice probability for %q", option)
			}
			probabilities[option] = *p
			total += *p
		}
		if math.Abs(total-1) > 0.001 {
			return fmt.Errorf("choice probabilities do not sum to one")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return probabilities, nil
}
