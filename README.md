# lastcall

A local usage page and account switchboard for [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) (CPA) v8.

If you run several Claude, Codex, Antigravity or Grok subscriptions through CPA, lastcall shows how much
of each one is left, combines accounts per provider, and lets you pause, disable, enable, re-login and
spend banked limit resets without opening CPA's management panel.

It is a read-mostly companion. Inference still goes straight to CPA; lastcall never edits CPA's config,
and the full panel stays at `/management.html`.

## Features

- **Provider totals.** Pinned providers get a header card that adds up their accounts: two accounts make a
  `/ 200%` pool, with the next 5-hour and weekly reset.
- **One card per account.** Same layout for every provider: status, 5-hour and weekly limits with time to
  reset, banked resets, plan renewal date. Cards are grouped by provider and sized to equal rows.
- **Pause and toggle.** Pause for 30 minutes, 2 hours, or until the account's 5-hour or weekly reset.
  Timed pauses survive restarts. Disable and enable stay until you change them.
- **Banked resets.** Claude grants and Codex reset credits show their count; the popup lists each one with
  its expiry and can spend one (with a confirm).
- **Login.** Add an account or re-login an existing one through CPA's OAuth flow.

| Provider | Quota source | Banked resets |
|---|---|---|
| Claude | `api.anthropic.com/api/oauth/usage` | yes (Claude grants) |
| Codex | `chatgpt.com/backend-api/wham/usage` | yes (reset credits) |
| Antigravity | `cloudcode-pa … retrieveUserQuotaSummary` | no |
| Grok | `cli-chat-proxy.grok.com/v1/billing` | no |

The reset endpoints are undocumented. lastcall calls them the same way Claude Code and the Codex CLI do,
so a provider change can break them without notice.

## Requirements

- macOS (state and log paths assume it; the code itself is portable Go)
- Go 1.26+
- CLIProxyAPI v8 with the management API enabled, listening on `127.0.0.1:8317`

## Install and run

Run these in zsh (macOS's default shell); the `read` prompt syntax is zsh-only.

```zsh
# once: store the CPA management key (prompted, not echoed)
mkdir -p ~/.config/lastcall && chmod 700 ~/.config/lastcall
read -rs "k?CPA management key: " && printf %s "$k" > ~/.config/lastcall/management-key \
  && chmod 600 ~/.config/lastcall/management-key && unset k

go build -o bin/lastcall ./cmd/lastcall
./bin/lastcall            # open http://127.0.0.1:8318
```

The UI is embedded in the binary. To run lastcall in the background and start it at login, see
[docs/service-plan.md](docs/service-plan.md).

### Flags

| Flag | Default | |
|---|---|---|
| `-listen` | `127.0.0.1:8318` | Keep it on loopback; anyone who can reach it can disable your accounts. |
| `-cpa` | `http://127.0.0.1:8317` | CPA base URL. |
| `-key` | `~/.config/lastcall/management-key` | File holding the CPA management key. Re-read when it changes. |
| `-state` | `~/Library/Application Support/lastcall/state.json` | Pins and timed pauses. |
| `-quota-every` | `5m` | Provider quota poll interval. |

## How it talks to CPA

Every call goes through the Go backend; the browser never sees the management key.

| What | CPA v8 endpoint |
|---|---|
| Accounts and status | `GET /credentials`, every 15 s |
| Quota and resets | `POST /requests/api-call` → each provider's usage API with the account's own token, every 5 min (Refresh forces it, 30 s minimum gap) |
| Disable, enable, timed pause | `PATCH /credentials/status` |
| Login and re-login | `GET /oauth/auth-url?is_webui=true`, `GET /oauth/status` |

Endpoint contracts and their sources are in [docs/api-contracts.md](docs/api-contracts.md).

## Safety

- A rejected key stops all calls until the key file changes, because 5 bad tries make CPA ban localhost for 30 minutes.
- A timed pause saves its timer before disabling the account, and only re-enables an account that is still disabled when the timer ends.
- State-changing endpoints require an `X-Lastcall` header, so other websites can't trigger them.
- Spending a reset always asks for confirmation; providers report a refusal (nothing to reset, cooldown) without using the reset.

## Layout

```
cmd/lastcall      entry point and flags
internal/cpa      CPA management API client
internal/quota    per-provider quota and reset adapters
internal/server   polling loops, pauses, HTTP API
internal/state    JSON state file (pins, pauses)
internal/routing  reset-first routing planner (not wired up yet)
web               embedded UI (plain HTML, CSS, JS)
docs              API contracts, original plan, service plan
mocks             design mockups, not served
```

## Roadmap

- Run as a login service ([plan](docs/service-plan.md)).
- Reset-first routing: give the account whose weekly allowance resets soonest the highest CPA priority.

## License

[MIT](LICENSE)
