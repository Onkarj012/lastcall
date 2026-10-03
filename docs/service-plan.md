# Plan: run lastcall as a login service

Status: proposed, not implemented.

## Goal

lastcall should behave like CPA does on this Mac: start at login, come back if it crashes, and keep
running across sleep, reboots and long idle stretches, with no terminal open.

## How CPA does it today

`brew services start cliproxyapi` installed `~/Library/LaunchAgents/sh.brew.cliproxyapi.plist`:

- `RunAtLoad = true` starts it when the agent loads, which is at login.
- `KeepAlive = true` makes launchd restart it whenever it exits.
- `ProgramArguments` points at the stable Homebrew path `/opt/homebrew/opt/cliproxyapi/bin/cliproxyapi`.

It's a per-user LaunchAgent, not a system LaunchDaemon. That's the right model for lastcall too: the key
file, state file and browser all belong to the logged-in user, and lastcall listens on loopback only.

## Approach

### Phase 1: LaunchAgent managed by lastcall itself (do this now)

Add two subcommands to the binary, so no hand-edited plist is needed:

```sh
lastcall service install     # copy binary, write plist, load it
lastcall service uninstall   # unload, remove plist (keeps state and key)
lastcall service status      # loaded? PID? last exit code? port answering?
```

`install` does:

1. Copy the running binary to a stable path, `~/.local/bin/lastcall`. Never point launchd at `./bin/`
   in the repo; a rebuild or `git clean` would break the service.
2. Write `~/Library/LaunchAgents/dev.onkarj012.lastcall.plist`:

   ```xml
   <key>Label</key>             <string>dev.onkarj012.lastcall</string>
   <key>ProgramArguments</key>  <array><string>/Users/…/.local/bin/lastcall</string></array>
   <key>RunAtLoad</key>         <true/>
   <key>KeepAlive</key>         <true/>
   <key>ThrottleInterval</key>  <integer>10</integer>
   <key>ProcessType</key>       <string>Background</string>
   <key>StandardOutPath</key>   <string>~/Library/Logs/lastcall.log</string>
   <key>StandardErrorPath</key> <string>~/Library/Logs/lastcall.log</string>
   ```

   Paths are expanded to absolute ones; launchd doesn't expand `~`. Any non-default flags given to
   `install` (for example `-listen`) are written into `ProgramArguments`.
3. Load it with `launchctl bootstrap gui/$UID <plist>`, replacing an older copy first with
   `launchctl bootout`.
4. Wait up to 5 s for `GET /api/snapshot` to answer, then print the URL.

Upgrading is the same command: rebuild, then `lastcall service install` again.

### Phase 2: Homebrew formula (when the repo is public and tagged)

Publish a tap (`onkarj012/tap`) with a formula that builds from a release tag and declares:

```ruby
service do
  run [opt_bin/"lastcall"]
  keep_alive true
  log_path var/"log/lastcall.log"
  error_log_path var/"log/lastcall.log"
end
```

Then `brew services start lastcall` works exactly like CPA, and `brew upgrade` handles new versions.
Phase 1's subcommands stay for people who build from source.

## What the code already handles

These parts are already safe to run unattended:

- **CPA down or starting later.** Credentials are retried every 15 s and the first quota sweep starts once
  accounts appear, so login-time ordering between CPA and lastcall doesn't matter.
- **Missing key.** The key file is read on demand and re-read when it changes; the UI shows a banner until then.
- **Rejected key.** Calls stop until the key file changes, so a restart loop can't burn CPA's 5-try ban.
- **Timed pauses.** Saved to disk and resumed after a restart.
- **Sleep and wake.** Tickers just fire late; pause resume compares wall-clock times.
- **SIGTERM.** launchd stops the agent with SIGTERM, which already triggers a clean HTTP shutdown.

## Gaps to close in Phase 1

1. **Port already taken.** Right now `ListenAndServe` fails and the process exits, so launchd retries every
   `ThrottleInterval`. That's acceptable, but the log line should name the port and say another lastcall
   may be running.
2. **Log growth.** stdout logging is light (startup and errors), but it should be capped. Rotate
   `~/Library/Logs/lastcall.log` at startup when it passes 5 MB, keeping one `.1` file.
3. **Version visibility.** Add `-version` and show the version in the UI footer, so `service status`
   can tell whether the running copy is the one just built.

## Verification

- `lastcall service install`, then `launchctl print gui/$UID/dev.onkarj012.lastcall` shows `state = running`.
- `kill -9 <pid>`: a new PID answers on 8318 within about 10 s.
- `brew services stop cliproxyapi`: lastcall stays up and shows "Can't reach CLIProxyAPI". Restart CPA
  and data returns within 15 s without touching lastcall.
- Log out and back in, or reboot: the page loads without running anything by hand.
- `lastcall service uninstall`: the agent is gone from `launchctl list`, and the state file and key remain.

## Out of scope

- A system-wide LaunchDaemon or running before login: CPA doesn't, and the key lives in the user's home.
- A menu-bar app. The page is the UI; a bookmark or `open http://127.0.0.1:8318` is enough.
