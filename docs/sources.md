# Sources

Every endpoint, parameter and response field keyburn uses comes from the providers' own documentation, read on October 26, 2026. Things that could not be confirmed are listed at the end.

## OpenAI

- Costs: [GET /v1/organization/costs](https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/usage/methods/costs). `start_time` (Unix seconds, inclusive) is required; `end_time` is exclusive; `bucket_width` supports only `1d`; `group_by` accepts `project_id`, `user_id`, `line_item`, `api_key_id` and `api_source`; `limit` is 1 to 180 buckets (default 7); `page` takes the `next_page` cursor. The response is a `page` of `bucket` objects with `start_time`, `end_time` and `results`, each result an `organization.costs.result` with `amount.value`, `amount.currency` ("usd") and `api_key_id` when grouped by key. The example request uses `Authorization: Bearer $OPENAI_ADMIN_KEY`.
- Projects: [GET /v1/organization/projects](https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/projects/methods/list), with `after`, `limit` (1 to 100) and `include_archived`. Items have `id`, `name` and `status`.
- Project API keys: [GET /v1/organization/projects/{project_id}/api_keys](https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/projects/subresources/api_keys/methods/list), with `after` and `limit` (1 to 100, default 20). Items are `organization.project.api_key` objects with `id`, `name`, `redacted_value`, `created_at` and `last_used_at`. The list has `has_more`, `first_id` and `last_id`.
- Admin keys: OpenAI's help center article [Admin API keys](https://help.openai.com/en/articles/9687866) says organization owners create admin keys in the organization settings and choose the permissions the integration needs. The [create admin key](https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/admin_api_keys/methods/create) API takes only a name and an expiry.

## Anthropic

- Usage: [GET /v1/organizations/usage_report/messages](https://platform.claude.com/docs/en/api/beta/organization/usage_report/retrieve_messages). `starting_at` (RFC 3339) is required; `bucket_width` is `1d`, `1h` or `1m`; `group_by[]` includes `api_key_id`; for daily buckets `limit` defaults to 7 and is at most 31; `page` takes `next_page`. Results carry `api_key_id`, `uncached_input_tokens`, `cache_creation.ephemeral_1h_input_tokens`, `cache_creation.ephemeral_5m_input_tokens`, `cache_read_input_tokens` and `output_tokens`.
- Cost: [GET /v1/organizations/cost_report](https://platform.claude.com/docs/en/api/beta/organization/cost_report/retrieve). `group_by[]` accepts only `description` and `workspace_id`, so cost cannot be split by key. `limit` is at most 31. `amount` is a decimal string in the lowest currency unit: "123.45" in USD is $1.23.
- Guide: [Usage and Cost API](https://platform.claude.com/docs/en/manage-claude/usage-cost-api). Its examples send `x-api-key: $ANTHROPIC_ADMIN_KEY` and `anthropic-version: 2023-06-01`, which keyburn uses. It also says data usually appears within 5 minutes, that the API supports polling once per minute for sustained use, that integrations should set a User-Agent, and that Priority Tier costs are not in the cost endpoint.
- Admin keys: [Admin API keys](https://platform.claude.com/docs/en/manage-claude/admin-api-keys). "Claude Console keys do not have selectable scopes; every key carries full access to all endpoints that accept Admin API keys." Keys start with `sk-ant-admin01-`, can be given an expiration, and Claude Enterprise admin keys have scopes.

## Alert channels

- ntfy: [Publishing](https://docs.ntfy.sh/publish/). Send the message as the body of a POST to the topic URL; `X-Title`/`Title`, `X-Priority`/`Priority` and `X-Tags`/`Tags` set the title, priority and tags; `Authorization: Bearer <token>` for protected topics. The docs also say that without sign-up "the topic is essentially a password".
- sendmail: Postfix [sendmail(1)](https://www.postfix.org/sendmail.1.html). `-t` takes the recipients from the message headers; `-i` stops a line with a single dot from ending the message.

## The fake server

`internal/fakeapi` copies the response shapes from the reference pages above: the example responses for costs, projects and project API keys on the OpenAI pages, and the example responses for the usage and cost reports on the Anthropic pages. The numbers in it are made up. It also checks the auth headers the way the real APIs are documented to need them.

## Not verified

- The exact names of the OpenAI admin key permissions that cover usage and project keys. The help article says you choose permissions but the article could not be fetched again by script (403), and the names were not checked in a dashboard.
- How the OpenAI costs endpoint wants several `group_by` values encoded. keyburn sends one, `group_by=api_key_id`, the form the reference uses in its field descriptions.
- Which scope a Claude Enterprise admin key needs for the usage and cost reports.
- Whether Anthropic includes today's partial bucket when `ending_at` is left out. keyburn leaves it out and the fake server includes today; the real behavior was not tested, since no real admin key was used.
- keyburn has not been run with a real admin key against the real OpenAI or Anthropic APIs. The tests and the demo use the fake server.
