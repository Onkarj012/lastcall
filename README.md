# lastcall

Usage page and account switchboard for [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) v8.
Shows every account's quota (combined per provider) and lets you pause, disable, enable and re-login
accounts. Reset-first routing is planned; `internal/routing` holds the planner but nothing calls it yet.

It replaces CPAMC for daily use. It never edits CPA config; CPAMC stays at `/management.html`.

## Run

```sh
# once: store the CPA management key (prompted, not echoed)
mkdir -p ~/.config/lastcall && chmod 700 ~/.config/lastcall
read -rs "k?CPA management key: " && printf %s "$k" > ~/.config/lastcall/management-key && chmod 600 ~/.config/lastcall/management-key && unset k

go build -o bin/lastcall ./cmd/lastcall
./bin/lastcall            # http://127.0.0.1:8318
```

Flags: `-listen`, `-cpa` (default `http://127.0.0.1:8317`), `-key`, `-state`
(default `~/Library/Application Support/lastcall/state.json`), `-quota-every` (default `5m`).

## How it talks to CPA

All calls go through the backend; the browser never holds the key.

| Feature | CPA v8 endpoint |
|---|---|
| Accounts, status, request counts | `GET /credentials` (every 15s) |
| Quota | `POST /requests/api-call` → each provider's usage API, same as CPAMC (every 5 min, Refresh forces it, 30s minimum gap) |
| Disable / enable / timed pause | `PATCH /credentials/status` |
| Login / re-login | `GET /oauth/auth-url?is_webui=true`, `GET /oauth/status` |

Contracts and sources: `docs/api-contracts.md`.

## Safety

- A rejected key stops all calls until the key file changes (5 bad tries bans localhost for 30 min).
- Timed pauses are saved to disk and resume after restarts; a pause only re-enables an account that is still disabled.
- State-changing endpoints require an `X-Lastcall` header so other websites can't trigger them.
