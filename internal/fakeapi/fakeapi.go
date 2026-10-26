// Package fakeapi is a fake of the parts of the OpenAI and Anthropic Admin
// APIs that keyburn reads. Its responses follow the shapes shown in each
// provider's API reference (see docs/sources.md). The numbers are made up.
// It is used by the tests and by the README demo; it never talks to a real
// provider.
package fakeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Admin keys the fake accepts. They are obviously not real.
const (
	OpenAIKey    = "fake-openai-admin-key-for-tests"
	AnthropicKey = "fake-anthropic-admin-key-for-tests"
)

// OpenAIKeyInfo is one project API key in the scenario.
type OpenAIKeyInfo struct {
	ID, Name, Project string
	// LastUsedAgo is how long before now the key was last used; zero
	// means never.
	LastUsedAgo time.Duration
}

// Scenario holds the numbers the fake serves. Daily values are indexed
// by days before today: [0] is today so far, [1] yesterday, and so on.
type Scenario struct {
	Now      time.Time
	Projects map[string]string // id -> name
	Keys     []OpenAIKeyInfo
	// OpenAICost: key id -> USD per day. The key "" is cost with no key.
	OpenAICost map[string][]float64
	// AnthropicTokens: api_key_id -> tokens per day. "" is Console use.
	AnthropicTokens map[string][]float64
	// AnthropicCents: cost per day in cents.
	AnthropicCents []float64
	// PageSize forces small pages so pagination is exercised.
	PageSize int
}

func steady(v float64, days int) []float64 {
	out := make([]float64, days)
	for i := range out {
		// a small, fixed wobble so the median is not trivial
		out[i] = v * (1 + 0.1*float64((i*7)%5-2))
	}
	return out
}

// Default is the scenario used in the README demo: one OpenAI key and one
// Anthropic key spike today, a dormant OpenAI key was used an hour ago,
// and Anthropic spend today is high.
func Default(now time.Time) *Scenario {
	const days = 40
	ci := steady(0.50, days)
	ci[0] = 14.20
	leak := steady(50000, days)
	leak[0] = 4200000
	app := steady(2000000, days)
	cents := make([]float64, days)
	for i := range cents {
		cents[i] = 900 + float64((i*13)%7)*40
	}
	cents[0] = 7350.5
	return &Scenario{
		Now:      now,
		Projects: map[string]string{"proj_web": "Web app", "proj_ci": "CI"},
		Keys: []OpenAIKeyInfo{
			{ID: "key_web_prod", Name: "web-prod", Project: "proj_web", LastUsedAgo: 2 * time.Minute},
			{ID: "key_old_laptop", Name: "old-laptop", Project: "proj_web", LastUsedAgo: 70 * time.Minute},
			{ID: "key_ci_deploy", Name: "ci-deploy", Project: "proj_ci", LastUsedAgo: 5 * time.Minute},
			{ID: "key_nightly", Name: "nightly-batch", Project: "proj_ci", LastUsedAgo: 9 * time.Hour},
			{ID: "key_unused", Name: "spare", Project: "proj_ci"},
		},
		OpenAICost: map[string][]float64{
			"key_web_prod":  steady(3.10, days),
			"key_ci_deploy": ci,
			"key_nightly":   steady(0.80, days),
			"":              steady(0.20, days),
		},
		AnthropicTokens: map[string][]float64{
			"apikey_01app":  app,
			"apikey_01eval": steady(300000, days),
			"apikey_01leak": leak,
			"":              steady(20000, days),
		},
		AnthropicCents: cents,
	}
}

// Server is the fake. It records every request path for tests.
type Server struct {
	mu       sync.Mutex
	S        *Scenario
	Requests []string
}

func New(s *Scenario) *Server { return &Server{S: s} }

func (f *Server) Paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.Requests...)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (f *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.Requests = append(f.Requests, r.URL.RequestURI())
	f.mu.Unlock()
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "GET only"})
		return
	}
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/v1/organization/"):
		if r.Header.Get("Authorization") != "Bearer "+OpenAIKey {
			writeJSON(w, 401, map[string]any{"error": map[string]any{"message": "Incorrect API key provided.", "type": "invalid_request_error", "param": nil, "code": "invalid_api_key"}})
			return
		}
		f.openai(w, r)
	case strings.HasPrefix(p, "/v1/organizations/"):
		if r.Header.Get("x-api-key") != AnthropicKey || r.Header.Get("anthropic-version") == "" {
			writeJSON(w, 401, map[string]any{"type": "error", "error": map[string]any{"type": "authentication_error", "message": "invalid x-api-key"}})
			return
		}
		f.anthropic(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *Server) today() time.Time { return f.S.Now.UTC().Truncate(24 * time.Hour) }

func dayVal(v []float64, back int) float64 {
	if back < 0 || back >= len(v) {
		return 0
	}
	return v[back]
}

// window returns the bucket starts from start to today, cut to limit and
// paged by the page token (a bucket index).
func (f *Server) window(start time.Time, limit int, page string) (days []time.Time, next string) {
	first := start.UTC().Truncate(24 * time.Hour)
	var all []time.Time
	for d := first; !d.After(f.today()); d = d.AddDate(0, 0, 1) {
		all = append(all, d)
	}
	off, _ := strconv.Atoi(page)
	size := limit
	if f.S.PageSize > 0 && f.S.PageSize < size {
		size = f.S.PageSize
	}
	if off > len(all) {
		off = len(all)
	}
	end := off + size
	if end >= len(all) {
		return all[off:], ""
	}
	return all[off:end], strconv.Itoa(end)
}

func (f *Server) back(d time.Time) int { return int(f.today().Sub(d).Hours() / 24) }

func intParam(r *http.Request, name string, def, max int) (int, error) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > max {
		return 0, fmt.Errorf("%s must be between 1 and %d", name, max)
	}
	return n, nil
}

func (f *Server) openai(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := r.URL.Path
	bad := func(msg string) {
		writeJSON(w, 400, map[string]any{"error": map[string]any{"message": msg, "type": "invalid_request_error"}})
	}
	switch {
	case p == "/v1/organization/costs":
		st, err := strconv.ParseInt(q.Get("start_time"), 10, 64)
		if err != nil {
			bad("start_time is required")
			return
		}
		if q.Get("bucket_width") != "" && q.Get("bucket_width") != "1d" {
			bad("bucket_width must be 1d")
			return
		}
		limit, err := intParam(r, "limit", 7, 180)
		if err != nil {
			bad(err.Error())
			return
		}
		byKey := q.Get("group_by") == "api_key_id"
		days, next := f.window(time.Unix(st, 0), limit, q.Get("page"))
		var data []map[string]any
		for _, d := range days {
			b := f.back(d)
			results := []map[string]any{}
			keys := make([]string, 0, len(f.S.OpenAICost))
			for k := range f.S.OpenAICost {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var total float64
			for _, k := range keys {
				v := dayVal(f.S.OpenAICost[k], b)
				total += v
				if byKey && v > 0 {
					var id any
					if k != "" {
						id = k
					}
					results = append(results, map[string]any{"object": "organization.costs.result",
						"amount": map[string]any{"value": v, "currency": "usd"}, "line_item": nil, "project_id": nil,
						"api_key_id": id, "quantity": nil, "quantity_unit": nil})
				}
			}
			if !byKey {
				results = append(results, map[string]any{"object": "organization.costs.result",
					"amount": map[string]any{"value": total, "currency": "usd"}, "line_item": nil, "project_id": nil,
					"api_key_id": nil, "quantity": nil, "quantity_unit": nil})
			}
			data = append(data, map[string]any{"object": "bucket", "start_time": d.Unix(),
				"end_time": d.AddDate(0, 0, 1).Unix(), "results": results})
		}
		var np any
		if next != "" {
			np = next
		}
		writeJSON(w, 200, map[string]any{"object": "page", "data": data, "has_more": next != "", "next_page": np})
	case p == "/v1/organization/projects":
		var ids []string
		for id := range f.S.Projects {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		var items []map[string]any
		for _, id := range ids {
			items = append(items, map[string]any{"id": id, "object": "organization.project", "name": f.S.Projects[id],
				"created_at": 1711471533, "archived_at": nil, "status": "active"})
		}
		f.list(w, r, items)
	case strings.HasPrefix(p, "/v1/organization/projects/") && strings.HasSuffix(p, "/api_keys"):
		pid := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/organization/projects/"), "/api_keys")
		if _, ok := f.S.Projects[pid]; !ok {
			writeJSON(w, 404, map[string]any{"error": map[string]any{"message": "No such project", "type": "invalid_request_error"}})
			return
		}
		var items []map[string]any
		for _, k := range f.S.Keys {
			if k.Project != pid {
				continue
			}
			var last any
			if k.LastUsedAgo > 0 {
				last = f.S.Now.Add(-k.LastUsedAgo).Unix()
			}
			items = append(items, map[string]any{"object": "organization.project.api_key",
				"redacted_value": "sk-proj-****" + k.ID[len(k.ID)-4:], "name": k.Name, "created_at": 1711471533,
				"last_used_at": last, "id": k.ID, "owner_project_access": "active",
				"owner": map[string]any{"type": "user", "user": map[string]any{"id": "user_fake", "name": "Fake User",
					"email": "user@example.invalid", "role": "owner", "created_at": 1711471533}}})
		}
		f.list(w, r, items)
	default:
		http.NotFound(w, r)
	}
}

// list serves an OpenAI style list with after/limit paging.
func (f *Server) list(w http.ResponseWriter, r *http.Request, items []map[string]any) {
	limit, err := intParam(r, "limit", 20, 100)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": map[string]any{"message": err.Error(), "type": "invalid_request_error"}})
		return
	}
	if f.S.PageSize > 0 && f.S.PageSize < limit {
		limit = f.S.PageSize
	}
	start := 0
	if after := r.URL.Query().Get("after"); after != "" {
		for i, it := range items {
			if it["id"] == after {
				start = i + 1
			}
		}
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	page := items[start:end]
	if page == nil {
		page = []map[string]any{}
	}
	resp := map[string]any{"object": "list", "data": page, "has_more": end < len(items)}
	if len(page) > 0 {
		resp["first_id"] = page[0]["id"]
		resp["last_id"] = page[len(page)-1]["id"]
	}
	writeJSON(w, 200, resp)
}

func (f *Server) anthropic(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bad := func(msg string) {
		writeJSON(w, 400, map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error", "message": msg}})
	}
	start, err := time.Parse(time.RFC3339, q.Get("starting_at"))
	if err != nil {
		bad("starting_at is required")
		return
	}
	if q.Get("bucket_width") != "" && q.Get("bucket_width") != "1d" {
		bad("only 1d buckets are faked")
		return
	}
	limit, err := intParam(r, "limit", 7, 31)
	if err != nil {
		bad(err.Error())
		return
	}
	days, next := f.window(start, limit, strings.TrimPrefix(q.Get("page"), "page_"))
	var data []map[string]any
	for _, d := range days {
		b := f.back(d)
		results := []map[string]any{}
		switch r.URL.Path {
		case "/v1/organizations/usage_report/messages":
			byKey := false
			for _, g := range q["group_by[]"] {
				if g == "api_key_id" {
					byKey = true
				}
			}
			keys := make([]string, 0, len(f.S.AnthropicTokens))
			for k := range f.S.AnthropicTokens {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				v := dayVal(f.S.AnthropicTokens[k], b)
				if v == 0 {
					continue
				}
				var id any
				if k != "" && byKey {
					id = k
				}
				// split the total the way real traffic splits
				in := float64(int(v * 0.6))
				cread := float64(int(v * 0.2))
				c5m := float64(int(v * 0.05))
				results = append(results, map[string]any{"api_key_id": id, "uncached_input_tokens": in,
					"cache_creation":          map[string]any{"ephemeral_1h_input_tokens": 0, "ephemeral_5m_input_tokens": c5m},
					"cache_read_input_tokens": cread, "output_tokens": v - in - cread - c5m,
					"server_tool_use": map[string]any{"web_search_requests": 0}, "model": nil, "workspace_id": nil,
					"service_tier": nil, "context_window": nil})
			}
		case "/v1/organizations/cost_report":
			if v := dayVal(f.S.AnthropicCents, b); v > 0 {
				results = append(results, map[string]any{"amount": strconv.FormatFloat(v, 'f', -1, 64), "currency": "USD",
					"cost_type": nil, "description": nil, "model": nil, "service_tier": nil, "token_type": nil,
					"workspace_id": nil, "context_window": nil, "inference_geo": nil, "speed": nil})
			}
		default:
			http.NotFound(w, r)
			return
		}
		data = append(data, map[string]any{"starting_at": d.Format(time.RFC3339),
			"ending_at": d.AddDate(0, 0, 1).Format(time.RFC3339), "results": results})
	}
	var np any
	if next != "" {
		np = "page_" + next
	}
	writeJSON(w, 200, map[string]any{"data": data, "has_more": next != "", "next_page": np})
}
