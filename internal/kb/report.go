package kb

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"
)

func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// WriteText prints the result for a person or a cron mail. With all false,
// alerts that were sent on an earlier run are left out.
func WriteText(w io.Writer, r *Result, all bool) {
	fmt.Fprintf(w, "keyburn %s  checked %s\n", r.Version, r.CheckedAt)
	for _, p := range r.Providers {
		fmt.Fprintf(w, "  %-9s  %d keys with use since %s", p.Provider, p.KeysWithUse, p.FirstDay)
		if p.Provider == "openai" {
			fmt.Fprintf(w, ", %d keys listed", p.KeysListed)
		}
		fmt.Fprintf(w, ", spend today (UTC) %s\n", fmtAmount(p.SpendTodayUSD, "usd"))
	}
	shown := 0
	old := 0
	for _, a := range r.Alerts {
		if !a.New && !all {
			old++
			continue
		}
		if shown == 0 {
			fmt.Fprintln(w)
		}
		shown++
		tag := "ALERT"
		if !a.New {
			tag = "SENT BEFORE"
		}
		fmt.Fprintf(w, "%s %s: %s\n", tag, a.Rule, a.Message)
	}
	if shown == 0 {
		fmt.Fprintln(w, "\nNo new alerts.")
	}
	if old > 0 {
		fmt.Fprintf(w, "(%d alert(s) already sent by an earlier run; use --all to show them)\n", old)
	}
	if len(r.Notified) > 0 {
		fmt.Fprintf(w, "Notified: %v\n", r.Notified)
	}
	for _, e := range r.Errors {
		fmt.Fprintf(w, "ERROR %s\n", e)
	}
}

// WriteKeys prints every key with its use per day, to help fill in the
// dormant list and names in the config.
func WriteKeys(w io.Writer, data []Data, names map[string]string, now time.Time) {
	today := now.UTC().Format(DayFormat)
	for _, d := range data {
		fmt.Fprintf(w, "%s (%s per day, UTC)\n", providerNames[d.Provider], d.Unit)
		ids := map[string]bool{}
		for id := range d.PerKey {
			ids[id] = true
		}
		for _, k := range d.Keys {
			ids[k.ID] = true
		}
		var list []string
		for id := range ids {
			list = append(list, id)
		}
		sort.Strings(list)
		for _, id := range list {
			var total float64
			for _, v := range d.PerKey[id] {
				total += v
			}
			fmt.Fprintf(w, "  %s", id)
			if n := keyName(d, id, names); n != "" {
				fmt.Fprintf(w, "  %q", n)
			}
			for _, k := range d.Keys {
				if k.ID == id {
					if k.Redacted != "" {
						fmt.Fprintf(w, "  %s", k.Redacted)
					}
					if k.ProjectName != "" {
						fmt.Fprintf(w, "  project %q", k.ProjectName)
					}
					if k.LastUsed != nil {
						fmt.Fprintf(w, "  last used %s", k.LastUsed.UTC().Format(time.RFC3339))
					} else {
						fmt.Fprint(w, "  never used")
					}
				}
			}
			fmt.Fprintf(w, "\n      today %s, window total %s\n", fmtAmount(d.PerKey[id][today], d.Unit), fmtAmount(total, d.Unit))
		}
	}
}
