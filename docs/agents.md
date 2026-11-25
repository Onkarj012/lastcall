# Controlling accounts from an agent

Use `lastcall ctl` to pause, disable or enable an account from a shell. Every command exits 0 on success and 1 on
any error, so check the exit code first. On success, `list` prints a JSON array of accounts. The other commands print
a JSON object with `"ok": true`. Every error prints a JSON object with `"ok": false` and an `error` message.

```sh
lastcall ctl list
lastcall ctl pause <match> <duration|5h-reset|week-reset>
lastcall ctl disable <match>
lastcall ctl enable <match>
```

## When to use each command

- `pause` for a limited time. Use it when the user says "pause X for 3 hours" or "stop using X until its 5-hour reset".
  The pause ends by itself. It survives restarts of lastcall.
- `disable` for an account that should stay off until someone turns it back on. It has no end time.
- `enable` to turn an account back on. It also ends a pause early.
- `list` to see the account names, labels, providers and current status before you act.
  A paused account has `disabled: true` and `pause.resume_at` set.

## How to pick `<match>`

`<match>` can be:

- an exact account name, such as `antigravity-foo@gmail.com.json`
- an exact label (email), such as `foo@gmail.com`
- part of a label or name, such as `foo@gmail`
- a provider name, such as `claude`

Matching is case-insensitive. If a match finds no accounts, the error lists the available labels.
If a match finds more than one account, the command fails and lists them. It never acts on several accounts.
Run `list` and use the exact name in that case.

## Durations

- Go durations: `90m`, `3h`, `1h30m`. Partial minutes round up.
- `5h-reset` pauses until the account's 5-hour limit resets.
- `week-reset` pauses until the account's weekly limit resets.

If lastcall does not know that reset time, the command fails. Use a duration instead.

## Examples

```sh
lastcall ctl list
lastcall ctl pause foo@gmail 3h
lastcall ctl pause b@example 5h-reset
lastcall ctl disable antigravity-foo@gmail.com.json
lastcall ctl enable foo@gmail.com
```

Failure example:

```json
{"ok":false,"error":"\"claude\" matches 2 accounts: a@example.com (claude-a.json), b@example.com (claude-b.json); use an exact name"}
```

## Connecting to lastcall

`ctl` talks to lastcall on `http://127.0.0.1:8318`. For another address, add `-url`:

```sh
lastcall ctl -url http://127.0.0.1:8398 list
```

If lastcall is not running, the error says `lastcall is not reachable at <url>`.
