package kb

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// Anthropic reads the Claude Admin API usage and cost reports.
type Anthropic struct{ api *api }

// AnthropicVersion is the version header used in Anthropic's Usage and Cost
// API guide.
const AnthropicVersion = "2023-06-01"

func NewAnthropic(base, adminKey string) *Anthropic {
	return &Anthropic{newAPI(base, map[string]string{"x-api-key": adminKey, "anthropic-version": AnthropicVersion}, adminKey)}
}

type antPage[T any] struct {
	Data []struct {
		StartingAt string `json:"starting_at"`
		EndingAt   string `json:"ending_at"`
		Results    []T    `json:"results"`
	} `json:"data"`
	HasMore  bool    `json:"has_more"`
	NextPage *string `json:"next_page"`
}

type antUsage struct {
	APIKeyID            *string `json:"api_key_id"`
	UncachedInputTokens float64 `json:"uncached_input_tokens"`
	CacheCreation       struct {
		Ephemeral1h float64 `json:"ephemeral_1h_input_tokens"`
		Ephemeral5m float64 `json:"ephemeral_5m_input_tokens"`
	} `json:"cache_creation"`
	CacheReadInputTokens float64 `json:"cache_read_input_tokens"`
	OutputTokens         float64 `json:"output_tokens"`
}

type antCost struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

func antPages[T any](ctx context.Context, a *api, path string, q url.Values, each func(day string, r T) error) error {
	for i := 0; i < maxPages; i++ {
		var p antPage[T]
		if err := a.get(ctx, path+"?"+q.Encode(), &p); err != nil {
			return err
		}
		for _, b := range p.Data {
			t, err := time.Parse(time.RFC3339, b.StartingAt)
			if err != nil {
				return fmt.Errorf("%s: bad starting_at %q", path, b.StartingAt)
			}
			day := t.UTC().Format(DayFormat)
			for _, r := range b.Results {
				if err := each(day, r); err != nil {
					return err
				}
			}
		}
		if !p.HasMore || p.NextPage == nil || *p.NextPage == "" {
			return nil
		}
		q.Set("page", *p.NextPage)
	}
	return fmt.Errorf("%s: more than %d pages", path, maxPages)
}

// Usage returns tokens per API key per UTC day: uncached input, both cache
// write kinds, cache reads and output, added together. Usage from the
// Console or Workbench has no api_key_id and is skipped.
func (c *Anthropic) Usage(ctx context.Context, start time.Time, days int) (map[string]map[string]float64, error) {
	out := map[string]map[string]float64{}
	q := url.Values{}
	q.Set("starting_at", start.UTC().Format(time.RFC3339))
	q.Set("bucket_width", "1d")
	q.Set("group_by[]", "api_key_id")
	q.Set("limit", strconv.Itoa(days))
	err := antPages(ctx, c.api, "/v1/organizations/usage_report/messages", q, func(day string, r antUsage) error {
		if r.APIKeyID == nil || *r.APIKeyID == "" {
			return nil
		}
		n := r.UncachedInputTokens + r.CacheCreation.Ephemeral1h + r.CacheCreation.Ephemeral5m +
			r.CacheReadInputTokens + r.OutputTokens
		if out[*r.APIKeyID] == nil {
			out[*r.APIKeyID] = map[string]float64{}
		}
		out[*r.APIKeyID][day] += n
		return nil
	})
	return out, err
}

// Cost returns USD per UTC day. The API gives amounts in cents as decimal
// strings ("123.45" is $1.2345). Priority Tier costs are not in this report.
func (c *Anthropic) Cost(ctx context.Context, start time.Time, days int) (map[string]float64, error) {
	out := map[string]float64{}
	q := url.Values{}
	q.Set("starting_at", start.UTC().Format(time.RFC3339))
	q.Set("bucket_width", "1d")
	q.Set("limit", strconv.Itoa(days))
	err := antPages(ctx, c.api, "/v1/organizations/cost_report", q, func(day string, r antCost) error {
		if r.Currency != "" && r.Currency != "USD" {
			return fmt.Errorf("Anthropic cost report: unexpected currency %q", r.Currency)
		}
		cents, err := strconv.ParseFloat(r.Amount, 64)
		if err != nil {
			return fmt.Errorf("Anthropic cost report: bad amount %q", r.Amount)
		}
		out[day] += cents / 100
		return nil
	})
	return out, err
}
