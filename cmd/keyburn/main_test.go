package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sudodrift/keyburn/internal/fakeapi"
	"github.com/sudodrift/keyburn/internal/kb"
)

var now = time.Date(2026, 10, 26, 15, 30, 0, 0, time.UTC)

func setup(t *testing.T, s *fakeapi.Scenario) (cfg string, env map[string]string) {
	t.Helper()
	srv := httptest.NewServer(fakeapi.New(s))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	cfg = filepath.Join(dir, "config.yaml")
	os.WriteFile(cfg, []byte("state: "+filepath.Join(dir, "state.json")+"\nopenai: {enabled: true, base_url: \""+srv.URL+
		"\"}\nanthropic: {enabled: true, base_url: \""+srv.URL+"\", spend_today_usd: 50}\n"), 0o600)
	return cfg, map[string]string{"HOME": dir, "OPENAI_ADMIN_KEY": fakeapi.OpenAIKey, "ANTHROPIC_ADMIN_KEY": fakeapi.AnthropicKey}
}

func call(env map[string]string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	c := run(args, &out, &errb, func(k string) string { return env[k] }, func() time.Time { return now })
	return c, out.String(), errb.String()
}

func TestExitCodes(t *testing.T) {
	cfg, env := setup(t, fakeapi.Default(now))
	if c, _, _ := call(env); c != 2 {
		t.Errorf("no args: %d", c)
	}
	if c, out, _ := call(env, "check", "--config", cfg); c != 1 || !strings.Contains(out, "ALERT spike") {
		t.Errorf("first check: exit %d\n%s", c, out)
	}
	if c, out, _ := call(env, "check", "--config", cfg); c != 0 || !strings.Contains(out, "No new alerts.") {
		t.Errorf("second check: exit %d\n%s", c, out)
	}
	if c, out, _ := call(env, "check", "--config", cfg, "--all"); c != 0 || !strings.Contains(out, "SENT BEFORE") {
		t.Errorf("--all: exit %d\n%s", c, out)
	}
	env["OPENAI_ADMIN_KEY"] = "wrong"
	if c, out, _ := call(env, "check", "--config", cfg); c != 2 || !strings.Contains(out, "HTTP 401") || strings.Contains(out, "wrong") {
		t.Errorf("bad key: exit %d\n%s", c, out)
	}
	if c, _, errs := call(env, "check", "--config", filepath.Join(t.TempDir(), "none.yaml")); c != 2 || errs == "" {
		t.Errorf("missing config: exit %d", c)
	}
}

func TestQuietScenarioExitsZero(t *testing.T) {
	s := fakeapi.Default(now)
	s.OpenAICost["key_ci_deploy"][0] = 0.5
	s.AnthropicTokens["apikey_01leak"][0] = 50000
	s.AnthropicCents[0] = 900
	s.Keys[1].LastUsedAgo = 90 * 24 * time.Hour
	cfg, env := setup(t, s)
	if c, out, _ := call(env, "check", "--config", cfg); c != 0 {
		t.Errorf("quiet day: exit %d\n%s", c, out)
	}
}

func TestDryRunAndJSON(t *testing.T) {
	cfg, env := setup(t, fakeapi.Default(now))
	c, out, _ := call(env, "check", "--config", cfg, "--dry-run", "--json")
	var res struct {
		Alerts []struct{ ID string } `json:"alerts"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || c != 1 || len(res.Alerts) != 3 {
		t.Errorf("dry run json: exit %d, %d alerts, %v", c, len(res.Alerts), err)
	}
	if _, err := os.Stat(filepath.Join(env["HOME"], "state.json")); err == nil {
		t.Error("dry run wrote the state file")
	}
	if c, _, _ := call(env, "check", "--config", cfg); c != 1 {
		t.Error("alerts were marked sent by the dry run")
	}
}

func TestKeysAndExample(t *testing.T) {
	cfg, env := setup(t, fakeapi.Default(now))
	c, out, _ := call(env, "keys", "--config", cfg)
	if c != 0 || !strings.Contains(out, "key_old_laptop") || !strings.Contains(out, "apikey_01leak") {
		t.Errorf("keys: exit %d\n%s", c, out)
	}
	c, out, _ = call(env, "example-config")
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte(out), 0o600)
	// Parse only; running check on it would call the real APIs.
	if _, err := kb.LoadConfig(p, "/home/x"); err != nil || c != 0 {
		t.Errorf("example config does not parse: %v", err)
	}
}
