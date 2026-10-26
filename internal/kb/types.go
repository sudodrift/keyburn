package kb

import "time"

// DayFormat is how UTC days are written everywhere: in the state file, in
// alerts and in output.
const DayFormat = "2006-01-02"

// KeyInfo is what the provider says about one API key. Only the provider's
// own redacted form of the key is kept, never the key.
type KeyInfo struct {
	Provider    string     `json:"provider"`
	ID          string     `json:"id"`
	Name        string     `json:"name,omitempty"`
	Project     string     `json:"project,omitempty"`
	ProjectName string     `json:"project_name,omitempty"`
	Redacted    string     `json:"redacted_value,omitempty"`
	LastUsed    *time.Time `json:"last_used_at,omitempty"`
}

// Data is what one run fetched from one provider.
type Data struct {
	Provider string `json:"provider"`
	// Unit of PerKey: "usd" for OpenAI (costs), "tokens" for Anthropic.
	Unit string `json:"unit"`
	// PerKey maps key id -> UTC day -> amount.
	PerKey map[string]map[string]float64 `json:"-"`
	// SpendByDay maps UTC day -> USD for the whole organization.
	SpendByDay map[string]float64 `json:"-"`
	Keys       []KeyInfo          `json:"-"`
}

// Alert is one thing worth telling a human about.
type Alert struct {
	ID        string  `json:"id"`
	Rule      string  `json:"rule"` // spike, dormant-used, spend-today
	Provider  string  `json:"provider"`
	KeyID     string  `json:"key_id,omitempty"`
	KeyName   string  `json:"key_name,omitempty"`
	Day       string  `json:"day"`
	Value     float64 `json:"value"`
	Unit      string  `json:"unit"`
	Baseline  float64 `json:"baseline_median,omitempty"`
	Threshold float64 `json:"threshold"`
	Message   string  `json:"message"`
	New       bool    `json:"new"`
}

var providerNames = map[string]string{"openai": "OpenAI", "anthropic": "Anthropic"}
