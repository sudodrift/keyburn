// Command keyburn watches OpenAI and Anthropic usage per API key, using
// each provider's own Admin API, and raises an alert when a key looks
// stolen: a sudden spike, use of a key you marked dormant, or high spend
// today. It is meant to run from cron.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sudodrift/keyburn/internal/kb"
)

var version = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv, time.Now)) }

const usage = `usage: keyburn <command> [flags]

Commands:
  check            fetch usage, apply the alert rules, notify, save state
  keys             list the keys the providers report, with recent use
  example-config   print a commented example config
  version          print the version

Flags:
  --config FILE    config file (default $XDG_CONFIG_HOME/keyburn/config.yaml)
  --state FILE     state file (overrides the config)
  --json           JSON output (check)
  --all            also show alerts that were sent on an earlier run (check)
  --dry-run        do not notify and do not write the state file (check)

Exit codes for check: 0 no new alerts, 1 new alerts, 2 error.
`

const exampleConfig = `# keyburn config. Admin keys never go in this file.
# state: ~/.local/state/keyburn/state.json

rules:
  spike_factor: 5         # alert when a key's use is above 5x its median day
  baseline_days: 14       # days that make up the median (3 to 28)
  min_history_days: 3     # newer keys are not checked for spikes
  min_spike_usd: 1.00     # ignore OpenAI spikes below this many dollars
  min_spike_tokens: 100000  # ignore Anthropic spikes below this many tokens

openai:
  enabled: true
  admin_key_env: OPENAI_ADMIN_KEY   # or admin_key_file: ~/.config/keyburn/openai-admin-key (mode 0600)
  spend_today_usd: 50               # 0 turns the rule off
  # projects: [proj_abc]            # default: every active project

anthropic:
  enabled: true
  admin_key_env: ANTHROPIC_ADMIN_KEY
  spend_today_usd: 50

# Keys that should not be used at all. Any use raises an alert.
dormant:
  - provider: openai
    key_id: key_abc123
    note: old laptop

# Labels for key ids in alerts (useful for Anthropic, whose usage report
# gives ids only).
names:
  apikey_01abc: production app

notify:
  # ntfy:
  #   server: https://ntfy.sh
  #   topic: pick-a-long-random-topic-name
  #   token_env: NTFY_TOKEN
  #   priority: high
  # webhook:
  #   url: https://example.com/hooks/keyburn
  #   bearer_env: KEYBURN_WEBHOOK_TOKEN
  # sendmail:
  #   path: /usr/sbin/sendmail
  #   from: keyburn@example.com
  #   to: [you@example.com]
`

func run(args []string, stdout, stderr io.Writer, env func(string) string, now func() time.Time) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version", "--version":
		fmt.Fprintln(stdout, "keyburn", version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "example-config":
		fmt.Fprint(stdout, exampleConfig)
		return 0
	case "check", "keys":
	default:
		fmt.Fprintf(stderr, "keyburn: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	fs := flag.NewFlagSet("keyburn "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config file")
	statePath := fs.String("state", "", "state file")
	asJSON := fs.Bool("json", false, "JSON output")
	all := fs.Bool("all", false, "show alerts sent before")
	dry := fs.Bool("dry-run", false, "no notifications, no state write")
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "keyburn: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "keyburn:", err)
		return 2
	}
	home := env("HOME")
	if *cfgPath == "" {
		base := env("XDG_CONFIG_HOME")
		if base == "" {
			if home == "" {
				return fail(errors.New("HOME is not set; use --config"))
			}
			base = filepath.Join(home, ".config")
		}
		*cfgPath = filepath.Join(base, "keyburn", "config.yaml")
	}
	cfg, err := kb.LoadConfig(*cfgPath, home)
	if err != nil {
		return fail(err)
	}
	if *statePath != "" {
		cfg.State = *statePath
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	t := now()

	if cmd == "keys" {
		data, errs := kb.Fetch(ctx, cfg, env, t)
		kb.WriteKeys(stdout, data, cfg.Names, t)
		for _, e := range errs {
			fmt.Fprintln(stderr, "keyburn:", e)
		}
		if len(errs) > 0 {
			return 2
		}
		return 0
	}

	res, err := kb.Check(ctx, kb.Options{Config: cfg, Env: env, Now: t, DryRun: *dry}, version)
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		if err := kb.WriteJSON(stdout, res); err != nil {
			return fail(err)
		}
	} else {
		kb.WriteText(stdout, res, *all)
	}
	if len(res.Errors) > 0 {
		return 2
	}
	for _, a := range res.Alerts {
		if a.New {
			return 1
		}
	}
	return 0
}
