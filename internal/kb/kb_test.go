package kb

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sudodrift/keyburn/internal/fakeapi"
)

var testNow = time.Date(2026, 10, 26, 15, 30, 0, 0, time.UTC)

func testEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

var goodEnv = testEnv(map[string]string{"OPENAI_ADMIN_KEY": fakeapi.OpenAIKey, "ANTHROPIC_ADMIN_KEY": fakeapi.AnthropicKey})

func testConfig(t *testing.T, base string, extra string) *Config {
	t.Helper()
	dir := t.TempDir()
	body := "state: " + filepath.Join(dir, "state.json") + `
openai: {enabled: true, base_url: "` + base + `", spend_today_usd: 50}
anthropic: {enabled: true, base_url: "` + base + `", spend_today_usd: 50}
dormant:
  - {provider: openai, key_id: key_old_laptop, note: old laptop}
` + extra
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(body), 0o600)
	cfg, err := LoadConfig(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func fake(t *testing.T, s *fakeapi.Scenario) (*fakeapi.Server, string) {
	t.Helper()
	f := fakeapi.New(s)
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func ids(alerts []Alert, onlyNew bool) []string {
	var out []string
	for _, a := range alerts {
		if !onlyNew || a.New {
			out = append(out, a.ID)
		}
	}
	sort.Strings(out)
	return out
}

func check(t *testing.T, cfg *Config, env func(string) string) *Result {
	t.Helper()
	res, err := Check(context.Background(), Options{Config: cfg, Env: env, Now: testNow}, "test")
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestDefaultScenarioAlerts(t *testing.T) {
	_, url := fake(t, fakeapi.Default(testNow))
	cfg := testConfig(t, url, "")
	res := check(t, cfg, goodEnv)
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	want := []string{
		"dormant-used:openai:key_old_laptop:2026-10-26",
		"spend-today:anthropic:2026-10-26",
		"spike:anthropic:apikey_01leak:2026-10-26",
		"spike:openai:key_ci_deploy:2026-10-26",
	}
	if got := ids(res.Alerts, true); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("alerts:\n got %v\nwant %v", got, want)
	}
	for _, p := range res.Providers {
		if p.Provider == "anthropic" && (p.SpendTodayUSD < 73.50 || p.SpendTodayUSD > 73.51) {
			t.Errorf("anthropic spend today %.4f, want 73.505 (7350.5 cents)", p.SpendTodayUSD)
		}
		if p.Provider == "openai" && p.KeysListed != 5 {
			t.Errorf("openai keys listed %d, want 5", p.KeysListed)
		}
	}
}

func TestSecondRunSendsNothingNew(t *testing.T) {
	_, url := fake(t, fakeapi.Default(testNow))
	cfg := testConfig(t, url, "")
	check(t, cfg, goodEnv)
	res := check(t, cfg, goodEnv)
	if n := ids(res.Alerts, true); len(n) != 0 {
		t.Errorf("second run has new alerts %v", n)
	}
	st, err := os.Stat(cfg.State)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("state mode %v, want 0600", st.Mode().Perm())
	}
}

func TestPaginationAndRequestShape(t *testing.T) {
	s := fakeapi.Default(testNow)
	s.PageSize = 2
	f, url := fake(t, s)
	cfg := testConfig(t, url, "")
	res := check(t, cfg, goodEnv)
	if len(res.Errors) != 0 {
		t.Fatal(res.Errors)
	}
	if got := len(ids(res.Alerts, true)); got != 4 {
		t.Errorf("%d alerts with small pages, want 4", got)
	}
	var costs, usage, cost, keys int
	for _, p := range f.Paths() {
		switch {
		case strings.HasPrefix(p, "/v1/organization/costs?"):
			costs++
			if !strings.Contains(p, "group_by=api_key_id") || !strings.Contains(p, "bucket_width=1d") || !strings.Contains(p, "start_time=") {
				t.Errorf("costs request %s", p)
			}
		case strings.HasPrefix(p, "/v1/organizations/usage_report/messages?"):
			usage++
			if !strings.Contains(p, "group_by%5B%5D=api_key_id") || !strings.Contains(p, "starting_at=2026-10-11T00%3A00%3A00Z") {
				t.Errorf("usage request %s", p)
			}
		case strings.HasPrefix(p, "/v1/organizations/cost_report?"):
			cost++
		case strings.Contains(p, "/api_keys?"):
			keys++
		}
	}
	// 16 days in pages of 2 is 8 pages each.
	if costs != 8 || usage != 8 || cost != 8 {
		t.Errorf("pages: costs %d usage %d cost %d, want 8 each", costs, usage, cost)
	}
	// proj_ci has 3 keys (2 pages), proj_web has 2 (1 page).
	if keys != 3 {
		t.Errorf("api key list pages %d, want 3", keys)
	}
}

func TestAdminKeysNeverLeak(t *testing.T) {
	// A server that echoes the auth headers back in an error body.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		io.WriteString(w, "bad: "+r.Header.Get("Authorization")+" "+r.Header.Get("x-api-key"))
	}))
	defer srv.Close()
	oai := "sk-admin-thisisafakeopenaiadminkey0001"
	ant := "sk-ant-admin01-thisisafakeanthropicadminkey"
	cfg := testConfig(t, srv.URL, "")
	res := check(t, cfg, testEnv(map[string]string{"OPENAI_ADMIN_KEY": oai, "ANTHROPIC_ADMIN_KEY": ant}))
	if len(res.Errors) != 2 {
		t.Fatalf("errors %v", res.Errors)
	}
	var b strings.Builder
	WriteJSON(&b, res)
	WriteText(&b, res, true)
	state, _ := os.ReadFile(cfg.State)
	b.Write(state)
	out := b.String()
	if strings.Contains(out, oai) || strings.Contains(out, ant) {
		t.Fatalf("admin key in output:\n%s", out)
	}
	if !strings.Contains(out, "[redacted]") {
		t.Error("expected the echoed key to be replaced with [redacted]")
	}
}

func TestLoadSecret(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "k")
	os.WriteFile(p, []byte("from-file-value\n"), 0o644)
	if _, err := LoadSecret(testEnv(nil), "X", p); err == nil || strings.Contains(err.Error(), "from-file-value") {
		t.Errorf("a 0644 key file was accepted or leaked: %v", err)
	}
	os.Chmod(p, 0o600)
	if v, err := LoadSecret(testEnv(nil), "X", p); err != nil || v != "from-file-value" {
		t.Errorf("0600 file: %q %v", v, err)
	}
	if v, _ := LoadSecret(testEnv(map[string]string{"X": "from-env"}), "X", p); v != "from-env" {
		t.Errorf("env should win, got %q", v)
	}
	if _, err := LoadSecret(testEnv(nil), "X", ""); err == nil {
		t.Error("no env and no file should fail")
	}
}

func series(vals map[int]float64, days int) map[string]float64 {
	out := map[string]float64{}
	today := testNow.Format(DayFormat)
	for back := 0; back < days; back++ {
		v, ok := vals[back]
		if !ok {
			v = vals[-1] // default
		}
		if v != 0 {
			out[dayAdd(today, -back)] = v
		}
	}
	return out
}

func evalOne(t *testing.T, s map[string]float64, unit string) []Alert {
	t.Helper()
	cfg := &Config{OpenAI: ProviderConfig{Enabled: true}}
	if err := cfg.fill("/nonexistent"); err != nil {
		t.Fatal(err)
	}
	d := Data{Provider: "openai", Unit: unit, PerKey: map[string]map[string]float64{"k": s}}
	return Evaluate(cfg, []Data{d}, newState(), testNow)
}

func TestSpikeRule(t *testing.T) {
	cases := []struct {
		name string
		vals map[int]float64
		want []string
	}{
		{"steady", map[int]float64{-1: 2}, nil},
		{"five times is not above five times", map[int]float64{-1: 2, 0: 10}, nil},
		{"spike today", map[int]float64{-1: 2, 0: 10.5}, []string{"spike:openai:k:2026-10-26"}},
		{"spike yesterday", map[int]float64{-1: 2, 1: 30}, []string{"spike:openai:k:2026-10-25"}},
		{"below the floor", map[int]float64{-1: 0.01, 0: 0.9}, nil},
		{"new key has no baseline", map[int]float64{0: 40, 1: 1, 2: 1}, nil},
		{"idle key wakes up", map[int]float64{0: 25, 10: 1}, []string{"spike:openai:k:2026-10-26"}},
	}
	for _, c := range cases {
		got := ids(evalOne(t, series(c.vals, 16), "usd"), false)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	if m := median([]float64{5, 1, 3, 2}); m != 2.5 {
		t.Errorf("median %v", m)
	}
}

func TestDormantByUsage(t *testing.T) {
	cfg := &Config{Anthropic: ProviderConfig{Enabled: true},
		Dormant: []DormantKey{{Provider: "anthropic", KeyID: "apikey_old"}}}
	cfg.fill("/x")
	d := Data{Provider: "anthropic", Unit: "tokens", PerKey: map[string]map[string]float64{
		"apikey_old": {testNow.Format(DayFormat): 12},
	}}
	got := ids(Evaluate(cfg, []Data{d}, newState(), testNow), false)
	if len(got) != 1 || got[0] != "dormant-used:anthropic:apikey_old:2026-10-26" {
		t.Errorf("got %v", got)
	}
}

func TestDormantOldLastUsedIsQuiet(t *testing.T) {
	cfg := &Config{OpenAI: ProviderConfig{Enabled: true}, Dormant: []DormantKey{{Provider: "openai", KeyID: "key_x"}}}
	cfg.fill("/x")
	old := testNow.AddDate(0, -3, 0)
	d := Data{Provider: "openai", Unit: "usd", Keys: []KeyInfo{{Provider: "openai", ID: "key_x", LastUsed: &old}}}
	st := newState()
	if a := Evaluate(cfg, []Data{d}, st, testNow); len(a) != 0 {
		t.Fatalf("old last use alerted on first run: %v", ids(a, false))
	}
	newer := testNow.Add(-time.Minute)
	d.Keys[0].LastUsed = &newer
	if a := Evaluate(cfg, []Data{d}, st, testNow); len(a) != 1 {
		t.Fatalf("new last use did not alert")
	}
}

func TestNotifiers(t *testing.T) {
	var mu sync.Mutex
	got := map[string]*http.Request{}
	bodies := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got[r.URL.Path] = r
		bodies[r.URL.Path] = string(b)
		mu.Unlock()
	}))
	defer srv.Close()
	dir := t.TempDir()
	mailOut := filepath.Join(dir, "mail.txt")
	script := filepath.Join(dir, "sendmail")
	os.WriteFile(script, []byte("#!/bin/sh\necho \"$@\" > "+mailOut+".args\ncat > "+mailOut+"\n"), 0o700)
	tokFile := filepath.Join(dir, "ntfy-token")
	os.WriteFile(tokFile, []byte("tk_fakentfytoken"), 0o600)

	_, url := fake(t, fakeapi.Default(testNow))
	cfg := testConfig(t, url, `notify:
  ntfy: {server: "`+srv.URL+`", topic: keyburn-test, token_file: "`+tokFile+`"}
  webhook: {url: "`+srv.URL+`/hook", bearer_env: HOOK_TOKEN}
  sendmail: {path: "`+script+`", from: "keyburn@example.invalid", to: ["me@example.invalid"]}
`)
	env := testEnv(map[string]string{"OPENAI_ADMIN_KEY": fakeapi.OpenAIKey, "ANTHROPIC_ADMIN_KEY": fakeapi.AnthropicKey, "HOOK_TOKEN": "hooktoken123"})
	res := check(t, cfg, env)
	if len(res.Errors) != 0 || len(res.Notified) != 3 {
		t.Fatalf("errors %v notified %v", res.Errors, res.Notified)
	}
	n := got["/keyburn-test"]
	if n == nil || n.Method != "POST" || n.Header.Get("Title") != "keyburn: 4 new alerts" ||
		n.Header.Get("Priority") != "high" || n.Header.Get("Authorization") != "Bearer tk_fakentfytoken" {
		t.Errorf("ntfy request wrong: %+v", n)
	}
	if strings.Count(bodies["/keyburn-test"], "\n") != 4 {
		t.Errorf("ntfy body: %q", bodies["/keyburn-test"])
	}
	h := got["/hook"]
	if h == nil || h.Header.Get("Authorization") != "Bearer hooktoken123" || h.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("webhook request wrong")
	}
	var p WebhookPayload
	if err := json.Unmarshal([]byte(bodies["/hook"]), &p); err != nil || len(p.Alerts) != 4 || p.Tool != "keyburn" {
		t.Errorf("webhook payload: %v %+v", err, p)
	}
	mail, _ := os.ReadFile(mailOut)
	args, _ := os.ReadFile(mailOut + ".args")
	if strings.TrimSpace(string(args)) != "-t -i" || !strings.Contains(string(mail), "To: me@example.invalid\n") ||
		!strings.Contains(string(mail), "Subject: keyburn: 4 new alerts\n") {
		t.Errorf("mail args %q body %q", args, mail)
	}
}

func TestFailedNotifyRetriesNextRun(t *testing.T) {
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(502)
		}
	}))
	defer srv.Close()
	_, url := fake(t, fakeapi.Default(testNow))
	cfg := testConfig(t, url, "notify:\n  webhook: {url: \""+srv.URL+"\"}\n")
	res := check(t, cfg, goodEnv)
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "HTTP 502") {
		t.Fatalf("errors %v", res.Errors)
	}
	fail = false
	res = check(t, cfg, goodEnv)
	if n := len(ids(res.Alerts, true)); n != 4 || len(res.Notified) != 1 {
		t.Errorf("after a failed send: %d new alerts, notified %v", n, res.Notified)
	}
}

func TestConfigChecks(t *testing.T) {
	bad := map[string]string{
		"unknown field":   "openai: {enabled: true}\nspike: 3\n",
		"nothing enabled": "openai: {enabled: false}\n",
		"baseline":        "openai: {enabled: true}\nrules: {baseline_days: 60}\n",
		"factor":          "openai: {enabled: true}\nrules: {spike_factor: 0.5}\n",
		"dormant":         "openai: {enabled: true}\ndormant: [{provider: groq, key_id: x}]\n",
		"ntfy topic":      "openai: {enabled: true}\nnotify: {ntfy: {server: https://ntfy.sh}}\n",
		"webhook url":     "openai: {enabled: true}\nnotify: {webhook: {url: ftp://x}}\n",
	}
	for name, body := range bad {
		p := filepath.Join(t.TempDir(), "c.yaml")
		os.WriteFile(p, []byte(body), 0o600)
		if _, err := LoadConfig(p, "/home/x"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte("openai: {enabled: true, admin_key_file: ~/k}\n"), 0o600)
	c, err := LoadConfig(p, "/home/x")
	if err != nil {
		t.Fatal(err)
	}
	if c.State != "/home/x/.local/state/keyburn/state.json" || c.OpenAI.AdminKeyFile != "/home/x/k" ||
		c.OpenAI.BaseURL != DefaultOpenAIURL || c.Rules.SpikeFactor != 5 {
		t.Errorf("defaults not filled: %+v", c)
	}
}
