// Package kb holds keyburn's logic: config, the provider clients, the alert
// rules, the state file and the notifiers.
package kb

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the YAML config file.
type Config struct {
	State     string            `yaml:"state"`
	Rules     Rules             `yaml:"rules"`
	OpenAI    ProviderConfig    `yaml:"openai"`
	Anthropic ProviderConfig    `yaml:"anthropic"`
	Dormant   []DormantKey      `yaml:"dormant"`
	Names     map[string]string `yaml:"names"`
	Notify    Notify            `yaml:"notify"`
}

// Rules holds the alert thresholds.
type Rules struct {
	// SpikeFactor is N in "a key's use today is above N times its median".
	SpikeFactor float64 `yaml:"spike_factor"`
	// BaselineDays is how many full days before the day checked make up the
	// median.
	BaselineDays int `yaml:"baseline_days"`
	// MinHistoryDays: a key first seen fewer days ago than this has no
	// baseline yet and is not checked for spikes.
	MinHistoryDays int `yaml:"min_history_days"`
	// MinSpikeUSD and MinSpikeTokens are floors, so a key that goes from
	// 2 cents to 20 cents does not page anyone.
	MinSpikeUSD    float64 `yaml:"min_spike_usd"`
	MinSpikeTokens float64 `yaml:"min_spike_tokens"`
}

// ProviderConfig is the part of the config for one provider.
type ProviderConfig struct {
	Enabled       bool    `yaml:"enabled"`
	AdminKeyEnv   string  `yaml:"admin_key_env"`
	AdminKeyFile  string  `yaml:"admin_key_file"`
	BaseURL       string  `yaml:"base_url"`
	SpendTodayUSD float64 `yaml:"spend_today_usd"`
	// Projects limits the OpenAI key list to these project ids. Empty means
	// every active project.
	Projects []string `yaml:"projects"`
}

// DormantKey is a key that should not be used at all.
type DormantKey struct {
	Provider string `yaml:"provider"`
	KeyID    string `yaml:"key_id"`
	Note     string `yaml:"note"`
}

// Notify lists the optional alert channels. Alerts always go to stdout.
type Notify struct {
	Ntfy     *Ntfy     `yaml:"ntfy"`
	Webhook  *Webhook  `yaml:"webhook"`
	Sendmail *Sendmail `yaml:"sendmail"`
}

type Ntfy struct {
	Server    string `yaml:"server"`
	Topic     string `yaml:"topic"`
	TokenEnv  string `yaml:"token_env"`
	TokenFile string `yaml:"token_file"`
	Priority  string `yaml:"priority"`
}

type Webhook struct {
	URL string `yaml:"url"`
	// BearerEnv names an environment variable whose value is sent as
	// "Authorization: Bearer <value>".
	BearerEnv  string `yaml:"bearer_env"`
	BearerFile string `yaml:"bearer_file"`
}

type Sendmail struct {
	Path string   `yaml:"path"`
	From string   `yaml:"from"`
	To   []string `yaml:"to"`
}

// Defaults.
const (
	DefaultOpenAIURL    = "https://api.openai.com"
	DefaultAnthropicURL = "https://api.anthropic.com"
)

// LoadConfig reads and checks a config file. Unknown fields are errors, so a
// typo does not silently turn a rule off.
func LoadConfig(path, home string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if err := c.fill(home); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	return &c, nil
}

func expand(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

func (c *Config) fill(home string) error {
	if c.State == "" {
		c.State = "~/.local/state/keyburn/state.json"
	}
	c.State = expand(c.State, home)
	r := &c.Rules
	if r.SpikeFactor == 0 {
		r.SpikeFactor = 5
	}
	if r.BaselineDays == 0 {
		r.BaselineDays = 14
	}
	if r.MinHistoryDays == 0 {
		r.MinHistoryDays = 3
	}
	if r.MinSpikeUSD == 0 {
		r.MinSpikeUSD = 1
	}
	if r.MinSpikeTokens == 0 {
		r.MinSpikeTokens = 100000
	}
	if r.SpikeFactor <= 1 {
		return fmt.Errorf("rules.spike_factor must be above 1")
	}
	// Anthropic returns at most 31 daily buckets per page and keyburn asks
	// for the baseline plus yesterday and today.
	if r.BaselineDays < 3 || r.BaselineDays > 28 {
		return fmt.Errorf("rules.baseline_days must be between 3 and 28")
	}
	if c.OpenAI.AdminKeyEnv == "" {
		c.OpenAI.AdminKeyEnv = "OPENAI_ADMIN_KEY"
	}
	if c.Anthropic.AdminKeyEnv == "" {
		c.Anthropic.AdminKeyEnv = "ANTHROPIC_ADMIN_KEY"
	}
	if c.OpenAI.BaseURL == "" {
		c.OpenAI.BaseURL = DefaultOpenAIURL
	}
	if c.Anthropic.BaseURL == "" {
		c.Anthropic.BaseURL = DefaultAnthropicURL
	}
	c.OpenAI.AdminKeyFile = expand(c.OpenAI.AdminKeyFile, home)
	c.Anthropic.AdminKeyFile = expand(c.Anthropic.AdminKeyFile, home)
	if !c.OpenAI.Enabled && !c.Anthropic.Enabled {
		return fmt.Errorf("enable openai, anthropic or both")
	}
	for i, d := range c.Dormant {
		if d.Provider != "openai" && d.Provider != "anthropic" {
			return fmt.Errorf("dormant[%d]: provider must be openai or anthropic", i)
		}
		if d.KeyID == "" {
			return fmt.Errorf("dormant[%d]: key_id is required", i)
		}
	}
	n := &c.Notify
	if n.Ntfy != nil {
		if n.Ntfy.Topic == "" {
			return fmt.Errorf("notify.ntfy.topic is required")
		}
		if n.Ntfy.Server == "" {
			n.Ntfy.Server = "https://ntfy.sh"
		}
		n.Ntfy.TokenFile = expand(n.Ntfy.TokenFile, home)
	}
	if n.Webhook != nil {
		if !strings.HasPrefix(n.Webhook.URL, "https://") && !strings.HasPrefix(n.Webhook.URL, "http://") {
			return fmt.Errorf("notify.webhook.url must be an http or https URL")
		}
		n.Webhook.BearerFile = expand(n.Webhook.BearerFile, home)
	}
	if n.Sendmail != nil {
		if len(n.Sendmail.To) == 0 {
			return fmt.Errorf("notify.sendmail.to is required")
		}
		if n.Sendmail.Path == "" {
			n.Sendmail.Path = "/usr/sbin/sendmail"
		}
	}
	return nil
}
