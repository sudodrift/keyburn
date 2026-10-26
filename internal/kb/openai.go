package kb

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// OpenAI reads the OpenAI Admin API: costs grouped by api_key_id, and the
// API keys of each project with their last_used_at time.
type OpenAI struct{ api *api }

func NewOpenAI(base, adminKey string) *OpenAI {
	return &OpenAI{newAPI(base, map[string]string{"Authorization": "Bearer " + adminKey}, adminKey)}
}

type oaiCostPage struct {
	Data []struct {
		StartTime int64 `json:"start_time"`
		EndTime   int64 `json:"end_time"`
		Results   []struct {
			Amount *struct {
				Value    float64 `json:"value"`
				Currency string  `json:"currency"`
			} `json:"amount"`
			APIKeyID  *string `json:"api_key_id"`
			ProjectID *string `json:"project_id"`
		} `json:"results"`
	} `json:"data"`
	HasMore  bool    `json:"has_more"`
	NextPage *string `json:"next_page"`
}

// Costs returns USD per key per UTC day from start on, and the total per
// day. Costs not tied to a key (api_key_id null) count only in the total.
func (o *OpenAI) Costs(ctx context.Context, start time.Time, days int) (perKey map[string]map[string]float64, total map[string]float64, err error) {
	perKey = map[string]map[string]float64{}
	total = map[string]float64{}
	q := url.Values{}
	q.Set("start_time", strconv.FormatInt(start.Unix(), 10))
	q.Set("bucket_width", "1d")
	q.Set("group_by", "api_key_id")
	q.Set("limit", strconv.Itoa(days))
	for i := 0; i < maxPages; i++ {
		var p oaiCostPage
		if err := o.api.get(ctx, "/v1/organization/costs?"+q.Encode(), &p); err != nil {
			return nil, nil, err
		}
		for _, b := range p.Data {
			day := time.Unix(b.StartTime, 0).UTC().Format(DayFormat)
			for _, r := range b.Results {
				if r.Amount == nil {
					continue
				}
				if r.Amount.Currency != "" && r.Amount.Currency != "usd" {
					return nil, nil, fmt.Errorf("OpenAI costs: unexpected currency %q", r.Amount.Currency)
				}
				total[day] += r.Amount.Value
				if r.APIKeyID != nil && *r.APIKeyID != "" {
					if perKey[*r.APIKeyID] == nil {
						perKey[*r.APIKeyID] = map[string]float64{}
					}
					perKey[*r.APIKeyID][day] += r.Amount.Value
				}
			}
		}
		if !p.HasMore || p.NextPage == nil || *p.NextPage == "" {
			return perKey, total, nil
		}
		q.Set("page", *p.NextPage)
	}
	return nil, nil, fmt.Errorf("OpenAI costs: more than %d pages", maxPages)
}

type oaiList[T any] struct {
	Data    []T    `json:"data"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

type oaiProject struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type oaiKey struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	RedactedValue string `json:"redacted_value"`
	CreatedAt     int64  `json:"created_at"`
	LastUsedAt    *int64 `json:"last_used_at"`
}

func list[T any](ctx context.Context, a *api, path string) ([]T, error) {
	var out []T
	q := url.Values{}
	q.Set("limit", "100")
	for i := 0; i < maxPages; i++ {
		var p oaiList[T]
		if err := a.get(ctx, path+"?"+q.Encode(), &p); err != nil {
			return nil, err
		}
		out = append(out, p.Data...)
		if !p.HasMore || p.LastID == "" {
			return out, nil
		}
		q.Set("after", p.LastID)
	}
	return nil, fmt.Errorf("%s: more than %d pages", path, maxPages)
}

// Keys lists the API keys of the given projects, or of every active
// project when projects is empty.
func (o *OpenAI) Keys(ctx context.Context, projects []string) ([]KeyInfo, error) {
	names := map[string]string{}
	if len(projects) == 0 {
		ps, err := list[oaiProject](ctx, o.api, "/v1/organization/projects")
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			if p.Status == "" || p.Status == "active" {
				projects = append(projects, p.ID)
				names[p.ID] = p.Name
			}
		}
	}
	var out []KeyInfo
	for _, pid := range projects {
		ks, err := list[oaiKey](ctx, o.api, "/v1/organization/projects/"+url.PathEscape(pid)+"/api_keys")
		if err != nil {
			return nil, err
		}
		for _, k := range ks {
			ki := KeyInfo{Provider: "openai", ID: k.ID, Name: k.Name, Project: pid, ProjectName: names[pid],
				Redacted: k.RedactedValue}
			if k.LastUsedAt != nil && *k.LastUsedAt > 0 {
				t := time.Unix(*k.LastUsedAt, 0).UTC()
				ki.LastUsed = &t
			}
			out = append(out, ki)
		}
	}
	return out, nil
}
