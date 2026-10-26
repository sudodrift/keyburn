package kb

import (
	"fmt"
	"sort"
	"time"
)

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func dayAdd(day string, n int) string {
	t, _ := time.Parse(DayFormat, day)
	return t.AddDate(0, 0, n).Format(DayFormat)
}

func daysBetween(a, b string) int {
	ta, _ := time.Parse(DayFormat, a)
	tb, _ := time.Parse(DayFormat, b)
	return int(tb.Sub(ta).Hours() / 24)
}

func fmtAmount(v float64, unit string) string {
	if unit == "usd" {
		return fmt.Sprintf("$%.2f", v)
	}
	switch {
	case v >= 1e6:
		return fmt.Sprintf("%.1fM tokens", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.0fk tokens", v/1e3)
	}
	return fmt.Sprintf("%.0f tokens", v)
}

func keyLabel(d Data, id string, names map[string]string) string {
	name := names[id]
	for _, k := range d.Keys {
		if k.ID == id && name == "" {
			name = k.Name
		}
	}
	if name == "" {
		return id
	}
	return fmt.Sprintf("%q (%s)", name, id)
}

func keyName(d Data, id string, names map[string]string) string {
	if n := names[id]; n != "" {
		return n
	}
	for _, k := range d.Keys {
		if k.ID == id {
			return k.Name
		}
	}
	return ""
}

// Evaluate applies the three rules to what was fetched. It checks today
// (UTC, so far) and yesterday, because usage data can arrive late. It also
// records in st the first day each key was seen, which the spike rule needs.
func Evaluate(cfg *Config, data []Data, st *State, now time.Time) []Alert {
	today := now.UTC().Format(DayFormat)
	days := []string{dayAdd(today, -1), today}
	r := cfg.Rules
	dormant := map[string]DormantKey{}
	for _, d := range cfg.Dormant {
		dormant[d.Provider+":"+d.KeyID] = d
	}
	var out []Alert
	for _, d := range data {
		pname := providerNames[d.Provider]
		floor := r.MinSpikeTokens
		if d.Unit == "usd" {
			floor = r.MinSpikeUSD
		}
		ids := make([]string, 0, len(d.PerKey))
		for id := range d.PerKey {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			series := d.PerKey[id]
			sk := d.Provider + ":" + id
			for day, v := range series {
				if v > 0 && (st.FirstSeen[sk] == "" || day < st.FirstSeen[sk]) {
					st.FirstSeen[sk] = day
				}
			}
			label := keyLabel(d, id, cfg.Names)
			if dk, ok := dormant[sk]; ok {
				for _, day := range days {
					if v := series[day]; v > 0 {
						out = append(out, Alert{ID: "dormant-used:" + sk + ":" + day, Rule: "dormant-used",
							Provider: d.Provider, KeyID: id, KeyName: keyName(d, id, cfg.Names), Day: day, Value: v, Unit: d.Unit,
							Message: fmt.Sprintf("%s key %s is marked dormant but used %s on %s%s",
								pname, label, fmtAmount(v, d.Unit), day, note(dk))})
					}
				}
				continue
			}
			first := st.FirstSeen[sk]
			for _, day := range days {
				v := series[day]
				if first == "" || daysBetween(first, day) < r.MinHistoryDays || v < floor {
					continue
				}
				var base []float64
				for i := r.BaselineDays; i >= 1; i-- {
					bd := dayAdd(day, -i)
					if bd >= first {
						base = append(base, series[bd])
					}
				}
				m := median(base)
				limit := r.SpikeFactor * m
				if v <= limit {
					continue
				}
				var why string
				if m == 0 {
					why = fmt.Sprintf("its %d-day median is zero", len(base))
				} else {
					why = fmt.Sprintf("%.1fx its %d-day median of %s", v/m, len(base), fmtAmount(m, d.Unit))
				}
				so := ""
				if day == today {
					so = " so far"
				}
				out = append(out, Alert{ID: "spike:" + sk + ":" + day, Rule: "spike", Provider: d.Provider, KeyID: id,
					KeyName: keyName(d, id, cfg.Names), Day: day, Value: v, Unit: d.Unit, Baseline: m, Threshold: limit,
					Message: fmt.Sprintf("%s key %s used %s on %s%s, %s", pname, label, fmtAmount(v, d.Unit), day, so, why)})
			}
		}
		// Dormant keys whose last_used_at falls on yesterday or today (OpenAI
		// only). This catches use that the cost data has not shown yet.
		for _, k := range d.Keys {
			sk := d.Provider + ":" + k.ID
			dk, ok := dormant[sk]
			if !ok || k.LastUsed == nil {
				continue
			}
			day := k.LastUsed.UTC().Format(DayFormat)
			if day != days[0] && day != days[1] {
				continue
			}
			out = append(out, Alert{ID: "dormant-used:" + sk + ":" + day, Rule: "dormant-used", Provider: d.Provider,
				KeyID: k.ID, KeyName: keyName(d, k.ID, cfg.Names), Day: day, Unit: "last_used_at",
				Message: fmt.Sprintf("%s key %s is marked dormant but was last used at %s%s",
					pname, keyLabel(d, k.ID, cfg.Names), k.LastUsed.UTC().Format(time.RFC3339), note(dk))})
		}
		limit := cfg.OpenAI.SpendTodayUSD
		if d.Provider == "anthropic" {
			limit = cfg.Anthropic.SpendTodayUSD
		}
		if v := d.SpendByDay[today]; limit > 0 && v > limit {
			out = append(out, Alert{ID: "spend-today:" + d.Provider + ":" + today, Rule: "spend-today", Provider: d.Provider,
				Day: today, Value: v, Unit: "usd", Threshold: limit,
				Message: fmt.Sprintf("%s spend today (UTC) is %s, above your limit of %s", pname, fmtAmount(v, "usd"), fmtAmount(limit, "usd"))})
		}
	}
	return dedupe(out)
}

// dedupe keeps one alert per id (a dormant key can be caught by both usage
// and last_used_at on the same day).
func dedupe(in []Alert) []Alert {
	seen := map[string]bool{}
	var out []Alert
	for _, a := range in {
		if !seen[a.ID] {
			seen[a.ID] = true
			out = append(out, a)
		}
	}
	return out
}

func note(d DormantKey) string {
	if d.Note == "" {
		return ""
	}
	return " (" + d.Note + ")"
}
