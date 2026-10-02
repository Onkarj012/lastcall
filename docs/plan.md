# CLIProxyAPI: use quota before it expires

Status: draft. Updated: 2026-10-02. Route: bounded implementation proposal, not authorization to execute.

## Summary

Keep upstream CLIProxyAPI for inference, authentication, and protocol updates. Add persistent usage reporting through CPA-Manager-Plus and a small controller that prioritizes usable accounts whose weekly allowance resets soonest. Investigate local model registration separately so supported new model IDs need not wait for an upstream catalog change.

Destination: one locally hosted setup with visible usage history, explainable reset-first routing, and a tested path for registering new models. Start with Claude and Codex. Leave Antigravity and xAI routing untouched.

Sequence: prove v8 compatibility in isolation; connect dashboard; observe controller decisions without writes; test and enable priority changes in isolation; prove model registration; request separate live activation.

Primary risk: incorrect or stale quota data could change account selection unexpectedly. Keep native availability/cooldown checks authoritative, make unknown data ineligible for new controller decisions, and start in dry-run mode.

Completion proof: deterministic scheduling tests pass; dashboard survives restart with history intact; one opted-in account pair demonstrates expected selection; a locally registered supported model completes a request without a core patch. If model registration cannot dispatch through existing credentials, report that goal blocked rather than replace it with an alias to an older model.

## Evidence

Verified locally on 2026-10-02:

- Port 8317 has an existing loopback listener. Executable path points to Homebrew CLIProxyAPI; installed symlink resolves to 8.0.10. Running-process release has not been independently confirmed.
- Selected configuration fields show round-robin routing, session affinity disabled, and remote management disabled.
- Credential-file metadata contains two Claude, four Codex, one Antigravity, and one xAI entries. These counts do not establish validity, paid status, or current allowance.
- No configuration was modified, management queue consumed, credentials printed, or running service restarted during planning.

Verified from upstream documentation or source, not exercised locally:

- Current CLIProxyAPI fetches upstream-owned model catalogs at startup and every three hours. This avoids some binary upgrades, but not dependency on catalog publication. Codex catalog updater also fetches hosted JSON, not authenticated provider discovery.
- scode/kd implements weekly-reset priority ordering for enabled Claude accounts and applies changes through legacy management APIs. It is an implementation reference, not a drop-in Codex/macOS solution.
- CPA-Manager-Plus documents SQLite-backed usage and native macOS distributions. It recommends CPA 7.3.4 or newer but does not explicitly verify 8.0.10 compatibility.
- v8 management usage-queue reads remove events. Configuration mutations can migrate legacy configuration. New integrations should prefer v8 endpoints; field-level priority support needs verification against the installed release.
- Model Registrar plugins can register IDs and metadata. Documentation does not establish dispatch of added IDs through existing built-in OAuth executors.

Evidence references:

- [CLIProxyAPI v8 management API](https://help.router-for.me/management/apiv8)
- [CLIProxyAPI model updater](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/registry/model_updater.go)
- [Codex catalog updater](https://github.com/router-for-me/CLIProxyAPI/blob/main/internal/registry/codex_client_models_updater.go)
- [Model Registrar extension](https://help.router-for.me/plugin/model-registrar.html)
- [kd priority implementation](https://github.com/scode/kd/blob/main/src/cmd/cli_proxy_api/plan.rs)
- [kd monitor implementation](https://github.com/scode/kd/blob/main/src/cmd/cli_proxy_api/monitor.rs)
- [CPA-Manager-Plus](https://github.com/seakee/CPA-Manager-Plus)

These URLs track mutable upstream content. Pin exact release/source revisions during implementation before relying on behavior.

## Decisions

User goals are settled: stay current with upstream releases; use newly available models without waiting for catalog commits where protocol permits; improve usage reporting; spend usable allowance before reset.

Proposed defaults for approval:

- Stay local on macOS. No VPS, public endpoint, or replacement proxy.
- Keep upstream binary unchanged initially. Reuse CPA-Manager-Plus before building a dashboard.
- Build a small standalone Go controller using standard library where practical. Deploy later as an explicitly approved launchd service; no automatic service installation during development.
- Support Claude and Codex adapters first. Opt accounts into controller ownership explicitly.
- Prioritize weekly expiry within operator-defined priority tiers. Five-hour limits remain eligibility constraints; do not combine incompatible reset windows into one score.
- Use local, reversible configuration and explainable decisions rather than consumption predictions or adaptive weights.
- No automatic production upgrades. Test exact upstream releases before offering user-run cutover commands.

## Scope

In scope: isolated compatibility checks, persistent dashboard, quota normalization, dry-run scheduler, reversible account-priority writes, local model-registration feasibility, narrow tests, and an operator runbook.

Out of scope: provider authentication rewrites, inference middleware, forced switching of in-flight streams, quota-limit bypass, paid-credit activation, multi-user hosting, all-provider scheduling, custom dashboard redesign, automatic production restart, public publication, and deployment.

Planning artifacts are local and contain no secrets. Implementation must not read or copy raw OAuth tokens when management-mediated quota access works. Any isolated live-provider test uses explicitly selected accounts and must not start competing credential-refresh processes against production credential files.

## Requirements and design

### Components and ownership

Existing clients continue talking to CLIProxyAPI. CPA-Manager-Plus reads its usage queue into its own SQLite database. Controller independently reads account/quota metadata and writes only opted-in accounts' priority fields. Controller never consumes usage events, proxies inference, or clears quota cooldowns.

Give exactly one collector ownership of the destructive usage queue. Inventory existing consumers first, including any quota-snapshot automation. Do not infer that a quota viewer consumes usage events. If another collector owns the queue, resolve ownership before starting CPAMP collection.

Controller exposes a local decision report through its CLI and structured logs: stable account identifier, provider, quota freshness, window reset, eligibility, existing/planned priority, and reason. Avoid a second dashboard in the first version.

Use management key from a restricted local file or environment reference. Never write secrets, raw provider responses, emails, prompts, or completions into public artifacts or routine logs. Bind dashboard to loopback and retain its own admin authentication.

### Quota adapters

Normalize each provider response into typed snapshots: stable account ID, observation timestamp, short/weekly used fraction, reset timestamps, provider eligibility information, and parse/error status. Preserve missing values as unknown, never zero.

Prefer quota operations mediated by CLIProxyAPI. Establish exact Claude and Codex response contracts with redacted fixtures before implementation. If management-mediated access cannot provide trustworthy data, stop that adapter and report the gap before requesting raw credential access.

Poll every five minutes by default, plus shortly after a known reset. Deduplicate concurrent refreshes, back off on quota-endpoint errors and 429 responses, and cap requests per account. Default freshness limit is fifteen minutes. Both intervals remain configuration values, not assumptions about provider update frequency.

### Reset-first policy

1. Work independently per provider and operator priority tier. Do not reprioritize unrelated accounts or change model eligibility.
2. Require fresh, valid snapshots for every opted-in account in that pool before writing a new ordering. Any failed/missing snapshot freezes writes for that pool and emits a degraded-state reason.
3. Among usable accounts, prefer the earliest future weekly reset. Usable means both known relevant windows have remaining allowance and native credential state does not report disabled/cooling/unavailable. Expose optional reserve thresholds; default reserve is zero so the controller does not deliberately strand quota.
4. Compare reset times in one-minute buckets. Preserve stable account ordering inside ties. Never predict replenishment merely because a timestamp has passed; refresh first. Unknown weekly reset means no new ordering, not highest priority.
5. Keep operator tiers ahead of computed reset order. Preserve native routing behavior among equal-priority credentials. Unsupported models and native cooldowns remain the proxy's responsibility, including between polls.
6. Recompute after a reset or eligibility change. Do not touch active requests. Avoid repeated writes when relative ordering is unchanged.

If snapshots become stale after an earlier successful priority update, record that the last applied ordering remains in effect. Do not claim native default ordering automatically resumes when the controller stops. Operator can explicitly restore captured priorities.

### Safe priority writes

Use stable credential identities, not display names alone. Verify exact v8 priority mutation payload and persistence in an isolated instance. Do not copy kd's legacy endpoint blindly.

Persist an atomic local state file containing original priorities, last controller-written values, configuration identity, and pending/reconciled changes. No credentials in this file. Take exclusive ownership of scheduling for opted-in accounts; other automation must not write their priorities.

Read current values before applying a diff. Unexpected external edits stop writes for that pool. Apply minimal changes, read back, and verify. If a multi-account update partially fails, stop and reconcile observed state; do not blindly retry or assert atomicity. Record rollback information before the first write.

An explicit restore operation restores only accounts whose current values still match controller-owned values. Leave new accounts and user edits untouched. If the API has no conditional write, document residual race risk and require a single writer.

### Usage dashboard

Evaluate pinned CPA-Manager-Plus Full Mode separately; do not replace the management panel served by production CLIProxyAPI. Verify request/account/model views, token and cache metrics, latency, failures, reset displays, and history persistence against the chosen CPA build.

Start with thirty-day detailed retention if supported, configurable by the operator. Do not add custom pruning to a third-party database. Display price-derived cost as an estimate, not subscription billing. Label absent telemetry rather than synthesize zeros.

Verify ingestion throughput and queue retention together. Test what happens when collector downtime exceeds retention, and expose/document unrecoverable gaps rather than claim complete history.

If v8 compatibility requires a small, bounded adapter fix, propose that exact patch. If compatibility requires substantial dashboard work, stop and compare an alternative before expanding scope.

### New-model path and upgrades

First test local registration of one known, supported model ID through documented v8 extension points. Verify listing, credential/model matching, real non-streaming and streaming execution, and tool calls where that model supports them. Check metadata and context limits rather than invent them.

Then test an entitled new model absent from the hosted catalog, if one is available. If none is available, validate the registration mechanism but label release-day behavior unproven. An alias to an existing model is not success.

If registration cannot reach an existing OAuth executor, stop this phase with concrete evidence. Present a narrowly scoped core patch as a separate decision, not permission to maintain an open-ended fork. New provider protocols or capabilities remain upstream code work.

Maintain a pinned known-good version set, test releases on an unused loopback port with isolated configuration/state, and retain rollback instructions. Never perform unattended Homebrew upgrades or restart the live proxy from an agent session.

## Phases

### 1. Establish isolated compatibility

Pin running/installed versions and candidate dashboard release. Inventory existing collectors, config schema, and account scopes without dumping secrets. Confirm free test ports, use fixture credentials first, and keep all test state separate. Prove management auth, priority read/write persistence, quota response contracts, and queue behavior against the target release.

Exit: endpoint contract and isolation procedure verified, or named compatibility blocker recorded. No production mutation.

### 2. Add usage reporting

Run CPAMP against the isolated proxy with one collector. Generate synthetic usage and verify account/model totals, failed requests, reset information where available, restart persistence, retention behavior, and authentication. Record any missing metric.

Exit: dashboard handles the selected CPA release and preserves history. Do not connect its collector to production yet.

### 3. Implement dry-run controller

Build typed adapters, pure scheduling function, freshness/error policy, local state handling, and decision report. Use fixtures for Claude and Codex. Dry-run is default and makes no priority writes.

Exit: expected rankings and degraded-state reports pass deterministic tests; a sanitized sample decision report is reviewable.

### 4. Prove reversible control

Exercise v8 priority writes in isolation. Test reset rollover, quota exhaustion, unknown data, account removal, external edits, partial write failures, process restart, and conditional restoration. Use authorized real-account smoke tests only after confirming credential isolation and expected provider consumption.

Exit: intended account is selected for an eligible request and original priorities can be restored without overwriting user edits. No in-flight stream disruption observed in the bounded test.

### 5. Prove independent model registration

Test the documented plugin registration path against the pinned CPA release. Verify listing and actual execution separately. Record whether release-day availability is proven, partially proven, or blocked. Keep core unchanged unless a subsequent scope decision approves a patch.

Exit: executable registration proof, or documented blocker with smallest proposed change.

### 6. Prepare rollout, stop before activation

Write local start/stop instructions, exact pinned versions, backup locations, ownership rules, dashboard URL, retention limits, controller restore command, and rollback procedure. Provide prospective launchd definitions only within the implementation project; do not install them without explicit activation authorization.

Exit: user receives separate activation checklist. User performs any required live proxy restart; agent never restarts port 8317. Branch/commit/PR actions require their own authorization under this planning workflow.

## Acceptance and verification

Smallest checks protecting real behavior:

- Pure scheduler tests cover earliest-reset ordering, minute ties, operator tiers, short-window exhaustion, weekly exhaustion, stale/unknown/reset-past snapshots, unsupported providers, and unchanged-order no-ops.
- Redacted provider fixtures cover missing fields, null windows, timezone offsets, malformed responses, and rate-limit errors.
- One isolated management integration suite covers minimal priority writes, persistence after isolated restart, partial failure reconciliation, external-edit protection, and restoration.
- One dashboard integration check proves single-consumer ingestion, account/model totals, visible failure data, and SQLite restart persistence. Record loss outside queue retention.
- One model-registration smoke sequence checks model listing, actual upstream model identity where observable, streaming, and supported tool calls. Explicitly record provider consumption and unavailable release-day test cases.
- One bounded account-selection check proves native cooldown/model eligibility is honored with computed priorities. Logs must explain the choice without containing credentials or request content.
- No production service changes, public publication, installs, or real-account test calls are part of this planning run.

Do not mark all goals complete if new-model dispatch remains blocked or only fixture tests have run. Report completed, blocked, and skipped items separately.

## Risks

- v8 API/config migration: isolate compatibility tests and avoid incidental config writes to the running instance.
- Stale or provider-specific quota semantics: typed adapters, freshness limits, whole-pool write freeze, and explicit degraded-state reporting.
- Priority ownership conflicts: explicit opt-in, single writer, read-before-write verification, and restore guards; document lack of server-side compare-and-swap if applicable.
- Queue consumption conflicts or dropped events: one collector, verified throughput/retention, and visible gaps.
- OAuth refresh conflicts: never run two proxy processes against shared writable production credential files. Use fixtures before separately authorized real-account testing.
- Plugin registration without usable execution: test dispatch early in that phase; stop rather than add misleading aliases.
- Fork maintenance growth: upstream core remains unchanged by default; dashboard compatibility fixes and core patches need bounded scope decisions.
- Provider terms and quota boundaries: use only authorized accounts and respect provider limits. Routing changes do not increase contractual entitlement.

## Open questions

No additional user decision blocks drafting or isolated feasibility work. Defaults above remain proposals pending approval.

Evidence gates before live readiness: installed v8 priority contract; CPAMP compatibility; management-mediated Codex quota access; model registrar dispatch into existing OAuth executors; shared-scope or model-specific quota behavior. These require tests, not guesses.

Non-blocking preferences: dashboard visual refinements, optional quota reserve, longer usage retention, and eventual hosting outside the Mac. Defer until core behavior works.

## Approval boundary

This artifact remains draft. Local HTML is an exact-content rendering of this Markdown; metadata records hashes. No publication, implementation, handoff, branch, commit, issue, PR, service installation, or live activation follows from plan creation.

Proposed future publication retention: seven days, only if publication is explicitly requested. No public page exists.

Next requested authorization: approve this design and begin isolated implementation through phase 5, then prepare the phase 6 runbook. This excludes production activation, proxy restart, public publication, executor handoff, and repository workflow actions. If delegation is requested later, first load routing instructions and obtain the required handoff authorization.
