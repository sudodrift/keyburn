# keyburn

keyburn spots a stolen AI API key from the provider's own usage data. It reads the official OpenAI and Anthropic Admin APIs, works out how much each API key used per day, and raises an alert when:

- a key's use today (or yesterday) is more than N times its own median day over the last two weeks
- a key you marked as dormant shows any use at all
- your spend so far today is above a limit you set

It is a single static binary meant to run from cron. Alerts go to stdout, and optionally to [ntfy](https://ntfy.sh), a webhook of your choice, or email through the local `sendmail`.

keyburn only reads. It never creates, changes or revokes keys, and it calls nothing but the provider APIs and the alert channels you configure.

## Why per key

A stolen key usually shows up as a bill. By then it has been used for hours or days. Both providers break usage down by API key, so a key that suddenly does fifty times its normal work, or a key that should be idle and is not, stands out within minutes of the data landing. Anthropic says its usage and cost data usually appears within 5 minutes; OpenAI's cost data comes in daily buckets.

## What it reads

**OpenAI** (Admin API, `Authorization: Bearer` with an admin key):

- `GET /v1/organization/costs` with `bucket_width=1d` and `group_by=api_key_id`: dollars per key per day. Costs that are not tied to a key count toward the daily total only.
- `GET /v1/organization/projects` and `GET /v1/organization/projects/{id}/api_keys`: every project key with its name, the redacted value OpenAI shows, and `last_used_at`. keyburn uses `last_used_at` to catch a dormant key before its cost shows up.

**Anthropic** (Admin API, `x-api-key` with an admin key and `anthropic-version: 2023-06-01`):

- `GET /v1/organizations/usage_report/messages` with `bucket_width=1d` and `group_by[]=api_key_id`: tokens per key per day. keyburn adds uncached input, both kinds of cache writes, cache reads and output. The cost report cannot be grouped by key, which is why the per key rules use tokens for Anthropic.
- `GET /v1/organizations/cost_report` with `bucket_width=1d`: the organization's spend per day, for the spend rule. Amounts come back in cents. Anthropic notes that Priority Tier costs are not in this report.

Every list is paged until the provider says there is no more. Days are UTC days, as the APIs use them. Exact endpoints, parameters and response fields are linked in [docs/sources.md](docs/sources.md).

## Admin keys

Both providers need an admin key for these endpoints. A normal API key will not work.

- Give keyburn the keys in the environment (`OPENAI_ADMIN_KEY`, `ANTHROPIC_ADMIN_KEY` by default) or in a file named in the config. A key file must be mode 0600 or tighter; keyburn refuses a file that group or others can read.
- keyburn never prints or logs the keys. Error text from a provider is scrubbed of the key before it is shown, and the state file holds no secrets.
- **OpenAI**: an organization owner creates admin keys in the organization settings, and OpenAI's help center says you choose the permissions the integration needs. Give keyburn's key read access to usage and to projects and API keys, and nothing that can write. Check the permission names in the dashboard when you create it.
- **Anthropic**: Claude Console admin keys cannot be limited. Anthropic's docs say they "do not have selectable scopes; every key carries full access to all endpoints that accept Admin API keys". So make a separate admin key just for keyburn, set an expiration on it, keep it in a 0600 file on one machine, and rotate it when it expires. Anthropic documents scopes for Claude Enterprise admin keys; if your keys have scopes, give keyburn the narrowest one that can still read the usage and cost reports.

## Install

```sh
git clone https://github.com/sudodrift/keyburn
cd keyburn
make build        # static binary at bin/keyburn, needs Go 1.27
make check        # gofmt, go vet, tests
```

keyburn runs on Linux and macOS.

## Config

`keyburn example-config` prints a commented config. Put it at `~/.config/keyburn/config.yaml` (or pass `--config`). The main settings:

| setting | default | meaning |
| --- | --- | --- |
| `rules.spike_factor` | 5 | alert when a key's day is above this many times its median day |
| `rules.baseline_days` | 14 | how many days before the checked day make up the median (3 to 28) |
| `rules.min_history_days` | 3 | keys first seen more recently than this are not checked for spikes |
| `rules.min_spike_usd` | 1.00 | OpenAI spikes below this many dollars are ignored |
| `rules.min_spike_tokens` | 100000 | Anthropic spikes below this many tokens are ignored |
| `openai.spend_today_usd`, `anthropic.spend_today_usd` | off | alert when today's spend is above this |
| `dormant` | none | key ids that should never be used |
| `names` | none | labels for key ids, useful for Anthropic whose report has ids only |
| `state` | `~/.local/state/keyburn/state.json` | where keyburn remembers what it already sent |

Unknown settings are errors, so a typo cannot quietly turn a rule off. `keyburn keys` lists every key id the providers report, with recent use, to help you fill in `dormant` and `names`.

A key with a zero median (normally idle) that crosses the floor counts as a spike. A key with no history yet is left alone until it has `min_history_days` of data.

## Alerts

- Each alert has an id made of the rule, provider, key and day. keyburn sends each id once and records it in the state file. Running every few minutes therefore sends one message per problem per day, not one per run.
- If a channel fails, the alerts stay unsent and the next run tries again. stdout always gets them.
- **ntfy**: POSTs to `<server>/<topic>` with `Title`, `Priority` (default `high`) and `Tags` headers, and a bearer token if you set one. On the public ntfy.sh server the topic name works like a password, so pick a long random one.
- **webhook**: POSTs JSON `{"tool", "title", "text", "alerts": [...]}` with an optional bearer token.
- **sendmail**: runs `sendmail -t -i` and writes a plain text mail with `To`, `From` and `Subject` headers.

Exit codes for `keyburn check`: 0 when there are no new alerts, 1 when there are, 2 when anything failed (a provider call, a channel, or the state file). Errors win over alerts, so a broken setup is never mistaken for a quiet day.

### cron

cron starts jobs with an almost empty environment, so keep the admin keys in 0600 files and name them in the config (`admin_key_file`). Then a crontab line needs nothing else:

```
*/15 * * * * /usr/local/bin/keyburn check >> $HOME/.local/state/keyburn/log 2>&1
```

Anthropic's guide says its usage API supports polling once per minute for sustained use; every 15 minutes is well inside that.

## Demo

**This demo runs against a fake server, not against OpenAI or Anthropic.** `internal/fakeapi` serves the same endpoints with responses in the shape shown in each provider's API reference, filled with made up numbers: the `ci-deploy` OpenAI key and the Anthropic key labeled `staging` spike today, the dormant `old-laptop` key was used an hour ago, and Anthropic spend today is $73.50. The admin keys are fake strings the fake server accepts. The webhook goes to a small local listener that writes what it receives to a file. Everything else below is a real run of the static binary.

```sh
go run ./internal/fakeapi/fakeapi-server 127.0.0.1:18789 &
export OPENAI_ADMIN_KEY=fake-openai-admin-key-for-tests KEYBURN_WEBHOOK_TOKEN=demo-webhook-token
printf 'fake-anthropic-admin-key-for-tests\n' > ~/.config/keyburn/anthropic-admin-key
chmod 644 ~/.config/keyburn/anthropic-admin-key     # too loose on purpose
```

`~/.config/keyburn/config.yaml`:

```yaml
# Demo config: both base_url values point at the local fake server.
openai:
  enabled: true
  base_url: http://127.0.0.1:18789
  spend_today_usd: 50
anthropic:
  enabled: true
  base_url: http://127.0.0.1:18789
  admin_key_file: ~/.config/keyburn/anthropic-admin-key
  spend_today_usd: 50
dormant:
  - provider: openai
    key_id: key_old_laptop
    note: old laptop, should be unused
names:
  apikey_01leak: staging
notify:
  webhook:
    url: http://127.0.0.1:18790/keyburn
    bearer_env: KEYBURN_WEBHOOK_TOKEN
```

A key file others can read is refused, and the run exits 2 even though OpenAI worked:

```
$ keyburn check --dry-run
keyburn 0.1.0  checked 2026-10-26T17:31:50Z
  openai     3 keys with use since 2026-10-11, 5 keys listed, spend today (UTC) $17.48

ALERT spike: OpenAI key "ci-deploy" (key_ci_deploy) used $14.20 on 2026-10-26 so far, 28.4x its 14-day median of $0.50
ALERT dormant-used: OpenAI key "old-laptop" (key_old_laptop) is marked dormant but was last used at 2026-10-26T16:21:49Z (old laptop, should be unused)
ERROR anthropic: /tmp/keyburn-demo/home/.config/keyburn/anthropic-admin-key has mode -rw-r--r--; run chmod 600 /tmp/keyburn-demo/home/.config/keyburn/anthropic-admin-key
exit=2
```

After `chmod 600 ~/.config/keyburn/anthropic-admin-key`:

```
$ keyburn keys
OpenAI (usd per day, UTC)
  key_ci_deploy  "ci-deploy"  sk-proj-****ploy  project "CI"  last used 2026-10-26T17:26:49Z
      today $14.20, window total $21.70
  key_nightly  "nightly-batch"  sk-proj-****htly  project "CI"  last used 2026-10-26T08:31:49Z
      today $0.64, window total $12.64
  key_old_laptop  "old-laptop"  sk-proj-****ptop  project "Web app"  last used 2026-10-26T16:21:49Z
      today $0.00, window total $0.00
  key_unused  "spare"  sk-proj-****used  project "CI"  never used
      today $0.00, window total $0.00
  key_web_prod  "web-prod"  sk-proj-****prod  project "Web app"  last used 2026-10-26T17:29:49Z
      today $2.48, window total $48.98
Anthropic (tokens per day, UTC)
  apikey_01app
      today 1.6M tokens, window total 31.6M tokens
  apikey_01eval
      today 240k tokens, window total 4.7M tokens
  apikey_01leak  "staging"
      today 4.2M tokens, window total 5.0M tokens
exit=0
```

```
$ keyburn check
keyburn 0.1.0  checked 2026-10-26T17:31:50Z
  openai     3 keys with use since 2026-10-11, 5 keys listed, spend today (UTC) $17.48
  anthropic  3 keys with use since 2026-10-11, spend today (UTC) $73.50

ALERT spike: OpenAI key "ci-deploy" (key_ci_deploy) used $14.20 on 2026-10-26 so far, 28.4x its 14-day median of $0.50
ALERT dormant-used: OpenAI key "old-laptop" (key_old_laptop) is marked dormant but was last used at 2026-10-26T16:21:49Z (old laptop, should be unused)
ALERT spike: Anthropic key "staging" (apikey_01leak) used 4.2M tokens on 2026-10-26 so far, 84.0x its 14-day median of 50k tokens
ALERT spend-today: Anthropic spend today (UTC) is $73.50, above your limit of $50.00
Notified: [webhook]
exit=1
```

What the webhook received:

```json
{
  "authorization": "Bearer demo-webhook-token",
  "body": {
    "tool": "keyburn",
    "title": "keyburn: 4 new alerts",
    "text": "OpenAI key \"ci-deploy\" (key_ci_deploy) used $14.20 on 2026-10-26 so far, 28.4x its 14-day median of $0.50\nOpenAI key \"old-laptop\" (key_old_laptop) is marked dormant but was last used at 2026-10-26T16:21:49Z (old laptop, should be unused)\nAnthropic key \"staging\" (apikey_01leak) used 4.2M tokens on 2026-10-26 so far, 84.0x its 14-day median of 50k tokens\nAnthropic spend today (UTC) is $73.50, above your limit of $50.00\n",
    "alerts": [
      {
        "id": "spike:openai:key_ci_deploy:2026-10-26",
        "rule": "spike",
        "provider": "openai",
        "key_id": "key_ci_deploy",
        "key_name": "ci-deploy",
        "day": "2026-10-26",
        "value": 14.2,
        "unit": "usd",
        "baseline_median": 0.5,
        "threshold": 2.5,
        "message": "OpenAI key \"ci-deploy\" (key_ci_deploy) used $14.20 on 2026-10-26 so far, 28.4x its 14-day median of $0.50",
        "new": true
      },
      {
        "id": "dormant-used:openai:key_old_laptop:2026-10-26",
        "rule": "dormant-used",
        "provider": "openai",
        "key_id": "key_old_laptop",
        "key_name": "old-laptop",
        "day": "2026-10-26",
        "value": 0,
        "unit": "last_used_at",
        "threshold": 0,
        "message": "OpenAI key \"old-laptop\" (key_old_laptop) is marked dormant but was last used at 2026-10-26T16:21:49Z (old laptop, should be unused)",
        "new": true
      },
      {
        "id": "spike:anthropic:apikey_01leak:2026-10-26",
        "rule": "spike",
        "provider": "anthropic",
        "key_id": "apikey_01leak",
        "key_name": "staging",
        "day": "2026-10-26",
        "value": 4200000,
        "unit": "tokens",
        "baseline_median": 50000,
        "threshold": 250000,
        "message": "Anthropic key \"staging\" (apikey_01leak) used 4.2M tokens on 2026-10-26 so far, 84.0x its 14-day median of 50k tokens",
        "new": true
      },
      {
        "id": "spend-today:anthropic:2026-10-26",
        "rule": "spend-today",
        "provider": "anthropic",
        "day": "2026-10-26",
        "value": 73.505,
        "unit": "usd",
        "threshold": 50,
        "message": "Anthropic spend today (UTC) is $73.50, above your limit of $50.00",
        "new": true
      }
    ]
  }
}
```

The next run finds the same problems but has already sent them:

```
$ keyburn check
keyburn 0.1.0  checked 2026-10-26T17:31:50Z
  openai     3 keys with use since 2026-10-11, 5 keys listed, spend today (UTC) $17.48
  anthropic  3 keys with use since 2026-10-11, spend today (UTC) $73.50

No new alerts.
(4 alert(s) already sent by an earlier run; use --all to show them)
exit=0
```

```
$ ls -l ~/.local/state/keyburn/
total 4
-rw------- 1 user user 638 Oct 26 13:31 state.json
```

```
$ keyburn check --json --all | head -40
{
  "tool": "keyburn",
  "version": "0.1.0",
  "checked_at": "2026-10-26T17:31:50Z",
  "providers": [
    {
      "provider": "openai",
      "unit": "usd",
      "keys_listed": 5,
      "keys_with_use": 3,
      "first_day": "2026-10-11",
      "spend_today_usd": 17.48
    },
    {
      "provider": "anthropic",
      "unit": "tokens",
      "keys_listed": 0,
      "keys_with_use": 3,
      "first_day": "2026-10-11",
      "spend_today_usd": 73.505
    }
  ],
  "alerts": [
    {
      "id": "spike:openai:key_ci_deploy:2026-10-26",
      "rule": "spike",
      "provider": "openai",
      "key_id": "key_ci_deploy",
      "key_name": "ci-deploy",
      "day": "2026-10-26",
      "value": 14.2,
      "unit": "usd",
      "baseline_median": 0.5,
      "threshold": 2.5,
      "message": "OpenAI key \"ci-deploy\" (key_ci_deploy) used $14.20 on 2026-10-26 so far, 28.4x its 14-day median of $0.50",
      "new": false
    },
    {
      "id": "dormant-used:openai:key_old_laptop:2026-10-26",
      "rule": "dormant-used",
...
```

## Limits

- OpenAI cost data is daily. A key used heavily in the last hour shows up when OpenAI updates today's bucket; `last_used_at` is quicker but only says that a key was used, not how much.
- Anthropic usage covers the Messages API. Use from the Console or Workbench has no API key id and is not checked per key.
- keyburn compares a key with its own past. A key stolen long ago and used steadily since then looks normal; the dormant list and the spend limit are the guard for that.
- Spend limits and alerts inside each provider's console are still worth setting. keyburn adds per key checks on top; it does not replace them.

## License

MIT, see [LICENSE](LICENSE).
