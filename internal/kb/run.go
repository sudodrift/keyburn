package kb

import (
	"context"
	"fmt"
	"time"
)

// Options for one check.
type Options struct {
	Config *Config
	Env    func(string) string
	Now    time.Time
	DryRun bool // no notifications, no state write
}

// ProviderSummary is a short line per provider for the report.
type ProviderSummary struct {
	Provider      string  `json:"provider"`
	Unit          string  `json:"unit"`
	KeysListed    int     `json:"keys_listed"`
	KeysWithUse   int     `json:"keys_with_use"`
	FirstDay      string  `json:"first_day"`
	SpendTodayUSD float64 `json:"spend_today_usd"`
}

// Result is everything one check produced.
type Result struct {
	Tool      string            `json:"tool"`
	Version   string            `json:"version"`
	CheckedAt string            `json:"checked_at"`
	Providers []ProviderSummary `json:"providers"`
	Alerts    []Alert           `json:"alerts"`
	Notified  []string          `json:"notified"`
	Errors    []string          `json:"errors"`
	Data      []Data            `json:"-"`
}

// Fetch pulls usage, cost and key data from every enabled provider. An
// error from one provider does not stop the other.
func Fetch(ctx context.Context, cfg *Config, env func(string) string, now time.Time) ([]Data, []string) {
	days := cfg.Rules.BaselineDays + 2
	today := now.UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -(days - 1))
	var data []Data
	var errs []string
	if cfg.OpenAI.Enabled {
		d, err := fetchOpenAI(ctx, cfg, env, start, days)
		if err != nil {
			errs = append(errs, "openai: "+err.Error())
		} else {
			data = append(data, d)
		}
	}
	if cfg.Anthropic.Enabled {
		d, err := fetchAnthropic(ctx, cfg, env, start, days)
		if err != nil {
			errs = append(errs, "anthropic: "+err.Error())
		} else {
			data = append(data, d)
		}
	}
	return data, errs
}

func fetchOpenAI(ctx context.Context, cfg *Config, env func(string) string, start time.Time, days int) (Data, error) {
	key, err := LoadSecret(env, cfg.OpenAI.AdminKeyEnv, cfg.OpenAI.AdminKeyFile)
	if err != nil {
		return Data{}, err
	}
	c := NewOpenAI(cfg.OpenAI.BaseURL, key)
	perKey, total, err := c.Costs(ctx, start, days)
	if err != nil {
		return Data{}, err
	}
	keys, err := c.Keys(ctx, cfg.OpenAI.Projects)
	if err != nil {
		return Data{}, err
	}
	return Data{Provider: "openai", Unit: "usd", PerKey: perKey, SpendByDay: total, Keys: keys}, nil
}

func fetchAnthropic(ctx context.Context, cfg *Config, env func(string) string, start time.Time, days int) (Data, error) {
	key, err := LoadSecret(env, cfg.Anthropic.AdminKeyEnv, cfg.Anthropic.AdminKeyFile)
	if err != nil {
		return Data{}, err
	}
	c := NewAnthropic(cfg.Anthropic.BaseURL, key)
	usage, err := c.Usage(ctx, start, days)
	if err != nil {
		return Data{}, err
	}
	cost, err := c.Cost(ctx, start, days)
	if err != nil {
		return Data{}, err
	}
	return Data{Provider: "anthropic", Unit: "tokens", PerKey: usage, SpendByDay: cost}, nil
}

// Check runs one full check: fetch, evaluate, notify new alerts, save state.
func Check(ctx context.Context, o Options, version string) (*Result, error) {
	cfg := o.Config
	st, err := LoadState(cfg.State)
	if err != nil {
		return nil, err
	}
	notifiers, err := Notifiers(cfg.Notify, o.Env)
	if err != nil {
		return nil, err
	}
	res := &Result{Tool: "keyburn", Version: version, CheckedAt: o.Now.UTC().Format(time.RFC3339),
		Alerts: []Alert{}, Notified: []string{}, Errors: []string{}}
	data, errs := Fetch(ctx, cfg, o.Env, o.Now)
	res.Data = data
	res.Errors = append(res.Errors, errs...)
	today := o.Now.UTC().Format(DayFormat)
	for _, d := range data {
		ps := ProviderSummary{Provider: d.Provider, Unit: d.Unit, KeysListed: len(d.Keys), SpendTodayUSD: d.SpendByDay[today],
			FirstDay: dayAdd(today, -(cfg.Rules.BaselineDays + 1))}
		for _, s := range d.PerKey {
			for _, v := range s {
				if v > 0 {
					ps.KeysWithUse++
					break
				}
			}
		}
		res.Providers = append(res.Providers, ps)
	}
	alerts := Evaluate(cfg, data, st, o.Now)
	var fresh []Alert
	for i := range alerts {
		if _, sent := st.Sent[alerts[i].ID]; !sent {
			alerts[i].New = true
			fresh = append(fresh, alerts[i])
		}
	}
	res.Alerts = alerts
	if o.DryRun {
		return res, nil
	}
	allSent := true
	if len(fresh) > 0 {
		for _, n := range notifiers {
			if err := n.Send(ctx, fresh); err != nil {
				allSent = false
				res.Errors = append(res.Errors, fmt.Sprintf("notify %s: %v", n.Name(), err))
			} else {
				res.Notified = append(res.Notified, n.Name())
			}
		}
	}
	// If a channel failed, leave the alerts unsent so the next run tries
	// again. stdout always gets them.
	if allSent {
		for _, a := range fresh {
			st.Sent[a.ID] = o.Now.UTC().Format(time.RFC3339)
		}
	}
	if err := st.Save(cfg.State, o.Now); err != nil {
		res.Errors = append(res.Errors, "state: "+err.Error())
	}
	return res, nil
}
