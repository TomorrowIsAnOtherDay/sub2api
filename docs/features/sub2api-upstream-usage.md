# Sub2API upstream usage for OpenAI API-key accounts

This branch is based on deployed v0.2.8 revision
`fd80b08c90b55edcad5b00171b53f08721d30da1`.

The account table now detects the Sub2API `/v1/usage` response on custom OpenAI
API-key upstreams and displays upstream-key usage alongside the existing local
account statistics. OpenAI OAuth accounts retain the Codex quota display.

## Behavior

- Automatic detection requires a recognized `mode`, `isValid`, and usage/quota
  data. Being an OpenAI API-key account alone does not establish that the
  upstream is Sub2API. Official provider hosts and empty/default base URLs are
  excluded. `extra.sub2api_usage_enabled = false` disables the probe per account.
- Probes run on the server with the account's existing API key, configured proxy,
  TLS profile, destination validation and HTTP transport. Both root and `/v1`
  base URLs are supported. Redirects are disabled and response bodies are capped
  at 1 MiB. Neither keys nor raw upstream error bodies are returned to the UI.
- Requests for the same account/credential identity are coalesced. Successful
  results are cached for three minutes, errors for one minute, and unsupported
  responses for thirty minutes. Manual refresh bypasses the normal cache after
  a five-second minimum cooldown. Cache entries are in memory, reset on server
  restart, and isolated across credential/proxy changes.
- Visible cells check for updates once a minute and support manual refresh.
  Failed refreshes retain the last successful snapshot with a stale label.
- The panel shows today's and total requests, tokens, and upstream-reported cost
  (preferring actual cost); configured quota and rate-limit windows; subscription
  daily/weekly/monthly limits; expiration; and positive reported wallet balance.
  No artificial reset time or quota percentage is invented when absent.
- A zero wallet balance is not displayed as exhausted: Sub2API simple mode can
  return zero while still allowing requests. Monitoring results do not change
  scheduling, account status, local billing, or rate multipliers.
- The statistics cover all clients sharing the upstream key and use upstream
  day boundaries. They are not the underlying Codex subscription's 5h/7d quota.
  Subscription/key monetary windows are likewise distinct from Codex quotas.

`/v1/sub2api/billing` remains independent: it supplies billing multipliers and
can be unavailable in simple mode even when `/v1/usage` works. A 403 can come
from a WAF and is shown as a probe error, not proof of invalid credentials.

## Validation

```sh
cd frontend
npx --yes pnpm@9 install --frozen-lockfile
npx --yes pnpm@9 exec vitest run src/components/account/__tests__/Sub2APIUsageCell.spec.ts src/components/account/__tests__/AccountUsageCell.spec.ts src/i18n/__tests__/localeKeyCompleteness.spec.ts
npx --yes pnpm@9 run build
cd ../backend
go test -race ./internal/service -run '^TestSub2APIUsage' -count=1
go test -tags=unit ./internal/service -run 'Test(Sub2APIUsage|AccountUsage|OpenAIUsage|UpstreamBillingProbe)' -count=1
go build -tags embed -o bin/server ./cmd/server
```

The existing Dockerfile embeds the built frontend in the Go server. For a
versioned image, run from the repository root:

```sh
docker build --build-arg VERSION=0.2.8-sub2api-usage --build-arg COMMIT="$(git rev-parse HEAD)" -t sub2api:0.2.8-sub2api-usage .
```

Build/test this image separately before changing the production app image. No
schema migration is introduced by this feature. Keep the existing image and
back up deployment configuration and database before any deployment.
