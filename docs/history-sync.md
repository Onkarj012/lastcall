# History sync between HP and Mac

Sync is off by default. Enable it with `-sync-config /absolute/path/to/sync.json` on each machine.
Both machines record locally and exchange missing observations when they are online together.
No cloud storage or public endpoint is needed. This guide does not install or restart services.

## Setup

Build the same version on both machines:

```sh
go build -o bin/lastcall ./cmd/lastcall
```

Create a shared random sync token on one machine. Keep it separate from the CPA management key:

```sh
mkdir -p "$HOME/.config/lastcall"
chmod 700 "$HOME/.config/lastcall"
umask 077
openssl rand -hex 32 > "$HOME/.config/lastcall/history-sync-token"
```

Transfer that file privately to the other machine, using your existing SSH/SFTP connection. Store it
at `~/.config/lastcall/history-sync-token` with permission `600`. Use the same token on both machines.
Do not put it in Git, paste it into chat, or run the generation command again on the second machine.

Confirm both machines' current Tailscale addresses with `tailscale status`. These addresses were
observed during development and may need updating if a device is re-registered.

Mac `~/.config/lastcall/sync.json`:

```json
{
  "listen": "100.65.201.15:8319",
  "peer": "http://100.79.10.88:8319",
  "token_file": "/Users/onkarj012/.config/lastcall/history-sync-token"
}
```

HP `~/.config/lastcall/sync.json`, replacing `/home/YOUR_USER` with the real home directory:

```json
{
  "listen": "100.79.10.88:8319",
  "peer": "http://100.65.201.15:8319",
  "token_file": "/home/YOUR_USER/.config/lastcall/history-sync-token"
}
```

Config paths are absolute; `~` and environment variables inside JSON are not expanded.
Peer addresses can also use the device's Tailscale DNS name ending in `.ts.net` with an explicit port.
Allow TCP port 8319 between these devices in your Tailscale policy and host firewall if needed.
HTTP travels through Tailscale's encrypted network. The listener accepts only an explicit Tailscale or
loopback IP. Keep the existing UI on `127.0.0.1:8318`; never expose its account controls for sync.

At your next approved service restart, add this flag to the existing command on each machine:

```sh
./bin/lastcall -sync-config "$HOME/.config/lastcall/sync.json"
```

Keep any existing `-state`, `-key`, `-cpa`, and other flags. In particular, keep the HP's existing state
path so its history stays in place. The default state path is still the existing macOS path.
Enable only one LastCall process per history directory.

## Behavior and verification

The peer listener exposes only `GET /v1/history/manifest` and `POST /v1/history/day`, authenticated with
the shared token. It cannot access CPA, manage accounts, or read credentials. Transfers include account
hashes, timestamps, plans, allowance windows, and safe failure markers. They do not include emails,
provider tokens, management keys, prompts, or completions.

Sync runs on startup and every minute. If Tailscale has not started yet, binding retries every minute.
If the other machine is offline, local recording continues. Both histories catch up when the machines
can reach each other. They must be online together at least once within the 30-day retention period.
Expired or never-recorded observations cannot be recovered.

The History page's existing retention note shows the last successful sync in IST or a retry message.
`GET /api/history` also includes `sync.enabled`, `sync.last_success`, and `sync.error`. A successful
sync means all days compared in that pass were exchanged; a new local poll can still await the next pass.
To verify, let each machine record while disconnected, reconnect them, and check both histories after
one minute. The page reads saved observations and does not force a provider poll.

Only differing UTC days transfer. A day is merged atomically under the same lock as local recording;
retries keep each identical observation once. Readings taken independently at different times remain
separate observations of the same account, which still counts once in the pool. Confirmed identities
merge across machines. Unconfirmed logins may remain separate until their provider scope is known.
An account absent from the local CPA appears under Removed accounts, as existing history does, and
does not count toward the local current pool.

Each daily file and transfer is bounded to 32 MiB. Oversized, corrupt, incompatible, or invalid imports
report a sync failure without replacing the local day. Both peers should run this protocol version.
Concurrent new readings can require another pass before fingerprints match.

## Disable or rotate

Remove `-sync-config` at the next service restart to turn sync off. Existing local history remains.
To rotate the shared token, create a new token file privately, copy it to both machines, and restart both
sync-enabled processes. Tokens and config are loaded at startup.
