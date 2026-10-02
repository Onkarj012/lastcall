# CLIProxyAPI v8 management API contracts for lastcall

Research date: 2026-10-02. Read-only, from public GitHub.

## Pinned sources

| Name used below | Repo | Ref | Commit SHA |
| --- | --- | --- | --- |
| **CPA** | router-for-me/CLIProxyAPI | tag `v8.0.10` | `6fecc6e5567912661654a4eaf9b8f5436facd1c2` |
| **Panel** | router-for-me/Cli-Proxy-API-Management-Center | tag `v1.25.2` (= `main` HEAD on 2026-10-02, = `releases/latest`) | `752e0ee772220ce49aae1221a3f39f23236590d7` |

Why this panel version: CPA v8.0.10 does not pin a panel version. It downloads `management.html` from the panel repo's `releases/latest` (CPA `internal/managementasset/updater.go:29`). Today that is v1.25.2 (asset digest `sha256:b6ea0bbd…ce041`). The panel builds every URL as `<base>/v8/management` (Panel `src/utils/connection.ts:7,14-18`). Note that CPA v8.0.11 was published 2026-10-02. Everything below is v8.0.10.

Citation format: `CPA path:line` or `Panel path:line`. Tags:
- **[src]** means read directly in the pinned source.
- **[inf]** means inferred from source but not executed or observed live.

All management paths below are relative to `/v8/management`. Routes are registered in CPA `internal/api/server_management_v8.go:12-63`. The official route table is CPA `docs/management-api-v8.md:86-113`.

---

## 1. Credentials list: `GET /credentials`

**Handler:** CPA `internal/api/handlers/management/auth_files.go:103-174`. The per-entry builder is at `:637-788`.

**Query params [src]:**
- `name` and `auth_index` filter results (`:117-118`, matched at `:292-303`).
- `page` and `page_size` turn on pagination (`:176-198`, default page size 50 at `:27`).

**Top-level shape [src]** (`:173`, paginated variant `:218-227`):

```json
{
  "observed_at": "2026-10-02T10:00:00Z",
  "files": [ /* entries */ ],
  "total": 12, "page": 1, "page_size": 50, "has_more": false   // only when page/page_size given
}
```

Without pagination, entries are sorted case-insensitively by `name` (`:168-172`).

**Entry shape [src]** (`auth_files.go:655-787`). Keys marked "optional" are only present when the value exists.

```json
{
  "id": "claude-user@example.com.json",          // auth.ID (:656)
  "auth_index": "a1b2c3d4",                       // stable index used by every other endpoint (:657)
  "name": "claude-user@example.com.json",         // FileName, falls back to ID (:650-653)
  "type": "claude",                               // == provider (:659)
  "provider": "claude",                           // (:660)
  "label": "",                                    // (:661)
  "status": "active",                             // reconciled: active | error | disabled | ... (:654,662)
  "status_message": "",                           // (:663)
  "disabled": false,                              // (:664)
  "unavailable": false,                           // reconciled cooldown flag (:665)
  "runtime_only": false,                          // (:666)
  "source": "file",                               // "file" if path exists, else "memory" (:667,728-740)
  "size": 1234,                                   // file size in bytes (:668,732)
  "success": 812,                                 // lifetime success counter (in-memory) (:670)
  "failed": 3,                                    // lifetime failure counter (:671)
  "recent_requests": [ {"time":"14:10-14:20","success":4,"failed":0} /* x20 */ ],  // (:672)
  "quota": { "observed_at": "...", "signals": { "Anthropic-Ratelimit-Unified-5h-Utilization": "0.42" } },  // (:673)
  "model_quotas": { "<model>": { "observed_at": "...", "signals": {} } },   // optional (:674-676)
  "supports_quota": true, "quota_provider": "<provider>",                    // optional, plugin quota providers only (:681-694)
  "quota_probe": { },                                                        // optional (:695-700)
  "email": "user@example.com",                    // optional: metadata.email > attributes.email > attributes.account_email (:701-703, :947-965)
  "project_id": "my-gcp-project",                 // optional: metadata.project_id > attributes.project_id (:704-706, :888-905)
  "account_type": "oauth",                        // optional: "oauth" | "api_key" (:707-714)
  "account": "user@example.com",                  // optional: OAuth = email; API-key = THE API KEY ITSELF (types.go:587-610)
  "created_at": "...", "updated_at": "...", "modtime": "...",   // modtime is overwritten by file mtime (:715-721, :733)
  "last_refresh": "...",                          // optional (:722-724)
  "next_retry_after": "...",                      // optional, only if the cooldown is in the future (:725-727)
  "path": "/abs/path/to/file.json",               // optional (:728-730)
  "id_token": {                                   // CODEX ONLY: parsed claims, not the raw JWT (:744-746, :907-945)
    "chatgpt_account_id": "...", "plan_type": "plus",
    "chatgpt_subscription_active_start": "...", "chatgpt_subscription_active_until": "..."
  },
  "priority": 10,                                 // optional int: attributes.priority, else metadata.priority (:747-766)
  "note": "...",                                  // optional (:767-777)
  "weight": 1,                                    // optional int64 (:778-780)
  "websockets": true,                             // optional (:781-783)
  "request_retry": 2,                             // optional (:784-786)
  "cooldowns": [ ] | null                         // added by ListAuthFiles; null in Home mode (:145-148, :161-164)
}
```

**Facts that matter for lastcall:**
- **[src]** The response has **no `metadata` or `attributes` objects**, no raw tokens, and no `plan_type` key for non-Codex providers. The panel's resolvers still probe `file.metadata` / `file.attributes` (Panel `src/utils/quota/resolvers.ts:31-105`). Against v8.0.10 those probes are dead code [inf].
- **[src]** `recent_requests` always has 20 buckets of 10 minutes each, so it covers 200 minutes. Labels are local-time `HH:MM-HH:MM` (CPA `sdk/cliproxy/auth/types.go:153-156,240-287`).
- **[src]** `account` holds the raw API key for API-key credentials. The panel deliberately never displays it (Panel `src/features/authFiles/identity.ts:8-11`, `src/services/api/authFiles.ts:267-268`). lastcall should do the same.
- **[src]** Passive `quota.signals` are only populated for `claude`, `codex` and `devin` (CPA `sdk/cliproxy/auth/quota_signals.go:17-24`). The signals come from upstream response headers on real traffic: `anthropic-ratelimit-unified-*`, `x-codex-*`, `retry-after` and `x-ratelimit-*` (`:149-193`). This gives a free quota hint with no extra upstream calls.

**How the panel derives display fields [src]:**
- **Identity line.** The fallback chain is `email`, then `projectId`, then filename without `.json` (Panel `src/features/authFiles/identity.ts:49-62`). The panel never parses an email out of the filename (`:12-15`).
- **Quota card title.** It uses `file.name`. Devin is the exception and gets `name · email|auth_index` (Panel `src/utils/quota/identity.ts:18-23`).
- **Provider.** `provider ?? type`, lowercased, `_` turned into `-`. `x-ai` and `grok` map to `xai`, and `kimi-ai` maps to `kimi` (Panel `src/utils/quota/validators.ts:7-14`).
- **Plan.** Claude, Antigravity and xAI get their plan from live upstream calls (section 2). Codex uses the usage response `plan_type` first, then the list entry `id_token.plan_type` (Panel `src/features/quota/providers/codex/data.ts:447,462`, `src/utils/quota/resolvers.ts:64-105`).
- **Normalisation.** The panel copies snake_case fields into camelCase (`authIndex`, `statusMessage`, `priority`, `weight`, `email`, `projectId`, `successCount`, `failureCount`, `recentRequests`). It also dedupes entries by quota cache key (Panel `src/services/api/authFiles.ts:257-338`).

---

## 2. Quota

### 2.1 Endpoint used per provider

**[src] The panel does not use `/plugins/:id/quota` for claude, codex, antigravity or xai.** Each provider adapter calls `POST /v8/management/requests/api-call`, and CPA proxies the request to the provider's own usage endpoint, substituting `$TOKEN$` (Panel `src/services/api/apiCall.ts:79-96`; adapters in `src/features/quota/providers/*/data.ts`). A code search of the panel repo for `/quota` turns up only route/UI paths. `plugins.ts` never calls `/plugins/:id/quota` (Panel `src/services/api/plugins.ts:260-310`).

**`/plugins/:id/quota` [src].** `:id` is an **installed plugin's ID**, not a provider name.
- GET takes `?auth_index=`. POST takes body `{auth_index}`. DELETE resets.
- It returns `404 {"error":"quota provider not found for plugin"}` unless that plugin registered a quota provider (CPA `internal/api/handlers/management/plugin_quota.go:237-298`).
- The response is `pluginapi.QuotaFetchResponse`: `{subscription?, summary?: [{key,label,value,unit?,format?,currency?}], serverTimeOffsetMs?, groups?}` (CPA `sdk/pluginapi/types.go:1692-1707`).
- Built-in providers have no plugin, so this endpoint is not an option for them.

### 2.2 `POST /requests/api-call` contract [src]

CPA `internal/api/handlers/management/api_tools.go:31-47,50-240`. The doc comment there says `/v0/management/api-call`. The v8 route is `/requests/api-call` (CPA `server_management_v8.go:31`).

Request:
```json
{
  "auth_index": "a1b2c3d4",            // also accepted: "authIndex", "AuthIndex" (:33-35). The panel sends "authIndex".
  "method": "GET",
  "url": "https://api.anthropic.com/api/oauth/usage",
  "header": { "Authorization": "Bearer $TOKEN$", "anthropic-beta": "oauth-2025-04-20" },
  "data": "{...raw body string...}",   // optional
  "proxy_url": "direct"                // optional; overrides credential and global proxy (:70-71, :81-85)
}
```
Response (always HTTP 200 when the call itself went through):
```json
{ "status_code": 200, "header": { "Content-Type": ["application/json"] }, "body": "<upstream body as STRING>" }
```

**Behaviour [src]:**
- `$TOKEN$` is replaced in header values and in `data` (`:165-188`).
- Token resolution (`:254-293`):
  - antigravity refreshes the OAuth access token first (`:277-280`, `:529`).
  - xai resolves or refreshes (`:287-290`, `:295`).
  - meta has its own resolver.
  - Everything else uses `metadata.access_token`, then `attributes.api_key`, `attributes.session_token`, then `metadata.token`, `id_token`, `cookie`.
  - Claude and Codex do **not** refresh on this path. They rely on CPA's background refresh [src `:292`].
- Errors:
  - 400 `missing method`, `missing url`, `invalid url`, `auth token not found`, `auth credential not found for auth_index`, or `auth token refresh failed`.
  - 502 `request failed`.
- Upstream timeout is 60s (`:23`).

### 2.3 Claude (Panel `src/features/quota/providers/claude/data.ts`, constants `src/utils/quota/constants.ts:107-131`)

| Call | Method / URL | Headers |
| --- | --- | --- |
| usage | `GET https://api.anthropic.com/api/oauth/usage` | `User-Agent: claude-cli/2.1.280 (external, cli)`, `Authorization: Bearer $TOKEN$`, `Content-Type: application/json`, `anthropic-beta: oauth-2025-04-20` |
| plan | `GET https://api.anthropic.com/api/oauth/profile` | same |

The panel fires both calls in parallel. A failed profile call only drops the plan (data.ts:164-202).

**Fields read [src]:**
- **Windows.** The panel reads keys `five_hour`, `seven_day`, `seven_day_oauth_apps`, `seven_day_opus`, `seven_day_sonnet`, `seven_day_cowork` and `iguana_necktie` ("7-day Fable"). Each one is `{utilization, resets_at}` (constants.ts:119-131; types `src/types/quota.ts:100-136`).
- **"7-day Fable 5".** The panel prefers a `limits[]` entry with `kind == "weekly_scoped"`, `scope.model.display_name` in {`fable`, `fable 5`} (case-insensitive), and a numeric `percent`. Among matches it takes the one with `is_active == true`, otherwise the first. This entry replaces `iguana_necktie` (data.ts:40-53, 63, 82-97).
- **Percent semantics.** `utilization` and `percent` are **USED percent on a 0–100 scale**. The UI shows remaining = `100 - used`, clamped (Panel `ClaudeQuotaBody.tsx:46-50`).
- **Reset time.** `resets_at` is ISO-8601. Unix seconds and milliseconds are also tolerated (Panel `src/utils/quota/resetInstants.ts:23-68`).
- **Period.** `five_hour` is 5h. Every other key is 168h (resetInstants.ts:85-87).
- **Extra usage.** `extra_usage: {is_enabled, monthly_limit, used_credits, utilization}` (types/quota.ts:119-124).
- **Plan, from profile** (data.ts:131-155):
  - `organization.organization_type=="claude_team"` and `subscription_status=="active"` gives `plan_team`.
  - Otherwise `account.has_claude_max` gives `plan_max`, `has_claude_pro` gives `plan_pro`, and both false gives `plan_free`.
- **Reset grants (Anthropic "cedar_ember").** Read from `GET /api/oauth/usage?cedar_ember=1&skip_spend=1` through api-call (Panel `src/services/api/claudeResetGrants.ts:1-80`). This is optional. I did not trace the claim flow.

### 2.4 Codex (Panel `src/features/quota/providers/codex/data.ts`, constants `constants.ts:133-145`)

**Base headers:**
- `Authorization: Bearer $TOKEN$`
- `Content-Type: application/json`
- `User-Agent: codex-tui/0.149.1 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; 0.149.1)`
- `Chatgpt-Account-Id: <id>` when an account ID is known (data.ts:305-314). The ID comes from list `id_token.chatgpt_account_id` (resolvers.ts:31-62).

| Call | Method / URL | Notes |
| --- | --- | --- |
| usage | `GET https://chatgpt.com/backend-api/wham/usage` | required |
| renewal | `GET https://chatgpt.com/backend-api/subscriptions?account_id=<id>` | 8s timeout, reads `active_until` (data.ts:316-358) |
| reset credits | `GET https://chatgpt.com/backend-api/wham/rate-limit-reset-credits` | extra headers `Accept: application/json`, `OpenAI-Beta: codex-1`, `Originator: Codex Desktop` (data.ts:360-414) |
| manual reset | `POST https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume` | body `{"redeem_request_id":"<uuid>"}` (data.ts:490-515). Only offered when available count > 0 (data.ts:528) |

**Usage payload [src]** (types/quota.ts:35-97):
- `plan_type`
- `credits: {has_credits, unlimited, balance}`
- `rate_limit`, `code_review_rate_limit` and `additional_rate_limits[]`. Each is `{allowed, limit_reached, primary_window, secondary_window}`. `additional_rate_limits[]` entries also carry `limit_name` / `metered_feature`.
- Each window is `{used_percent, limit_window_seconds, reset_after_seconds, reset_at}`.
- `rate_limit_reset_credits: {available_count, applicable_available_count}`.

**Parsing [src]:**
- Windows are classified by `limit_window_seconds`. 18000 is the 5-hour window. 604800 is weekly. 28–31 days is monthly (the team/secondary label). Without that field, the panel falls back to primary = 5h and secondary = weekly (data.ts:64-237).
- `used_percent` is **USED %, 0–100**. If it is missing and `limit_reached` is true or `allowed` is false, the panel assumes 100 (data.ts:107-109). The UI shows `100 - used` (CodexQuotaBody.tsx:178-182).
- Reset is `reset_at`, then `reset_after_seconds` added to now (data.ts:111-113). `reset_at` may be unix seconds or ms; values < 1e11 are treated as seconds (resetInstants.ts:39-44).
- Credit balance is a numeric string. `unlimited` is a boolean (data.ts:290-303).
- Reset credits are counted only when `reset_type == "codex_rate_limits"`, `status == "available"` and `expires_at` is present (Panel `src/utils/quota/resetCredits.ts:42-61`).

### 2.5 Antigravity (Panel `src/features/quota/providers/antigravity/data.ts`, constants `constants.ts:67-105`)

**Headers:**
- `Authorization: Bearer $TOKEN$`
- `Content-Type: application/json`
- `User-Agent: antigravity/cli/1.0.13 (aidev_client; os_type=darwin; arch=arm64)`

**Quota call [src].** `POST <url>` with body `{"project":"<project_id>"}`. The panel tries these in order until one returns 2xx with non-empty groups (data.ts:144-193):
1. `https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary`
2. `https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:retrieveUserQuotaSummary`
3. `https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary`

**`project_id` source [src].** The panel checks list `project_id`, then metadata and attributes. As a last resort it calls **`GET /credentials/download?name=`** and parses `project_id` from the raw file (data.ts:41-94). That last step ships the full credential to the browser. lastcall should avoid it, or do it server-side.

**Plan [src].** `POST https://daily-cloudcode-pa.googleapis.com/v1internal:loadCodeAssist` with body `{"metadata":{"ideType":"ANTIGRAVITY"}}`. The panel reads `paidTier` (falling back to `currentTier`) `{id,name}`. Tier ID mapping: `free-tier` is free, `g1-pro-tier` is pro, `g1-ultra-tier` is ultra, `g1-ultra-lite-tier` is ultra-lite (Panel `src/services/api/antigravitySubscription.ts:28-87`).

**Payload and parsing [src]** (types/quota.ts:11-33; Panel `src/utils/quota/builders.ts:67-123`):
- Shape is `groups[] {displayName, description, buckets[] {bucketId, displayName, window, resetTime, remainingFraction, description}}`. These are the model groups.
- `remainingFraction` is **REMAINING as a 0–1 fraction**. A string like `"42%"` is also accepted and divided by 100 (`parsers.ts:37-49`). The UI shows `fraction × 100` as remaining (AntigravityQuotaBody.tsx:189-190).
- `resetTime` is ISO.
- `window` is one of `5h`/`five-hour`/`five_hour` (5h) or `weekly`/`week` (168h).
- Server clock skew is computed from the response `Date` header (data.ts:96-106).

### 2.6 xAI / Grok (Panel `src/features/quota/providers/xai/data.ts`, constants `constants.ts:155-177`)

**CLI headers:**
- `Authorization: Bearer $TOKEN$`
- `x-xai-token-auth: xai-grok-cli`
- `x-grok-client-version: 0.2.91`
- `accept: */*`
- `user-agent: grok-pager/0.2.91 grok-shell/0.2.91 (macos; aarch64)`
- `x-userid: <sub>` when the panel can find a user ID (data.ts:42-82).

| Call | URL | Notes |
| --- | --- | --- |
| weekly billing | `GET https://cli-chat-proxy.grok.com/v1/billing?format=credits` | |
| monthly billing | `GET https://cli-chat-proxy.grok.com/v1/billing` | runs in parallel, then merged (data.ts:215-223) |
| plan | `GET https://cli-chat-proxy.grok.com/v1/user?include=subscription` reads `subscriptionTier`. `GET …/v1/settings` reads `subscription_tier_display` | background enrichment (data.ts:175-194, 243-247) |
| paid fallback | `GET https://api.x.ai/v1/me` plus `POST https://api.x.ai/v1/chat/completions` `{"model":"grok-4.5","messages":[{"role":"user","content":"ping"}],"max_tokens":1,"stream":false}` | headers `Authorization: Bearer $TOKEN$`, `accept: application/json`. **This spends a real request** (data.ts:104-150) |

**Billing payload [src].** The panel reads `config` (types/quota.ts:377-418; builder `builders.ts:470-562`):
- `currentPeriod {type,start,end}`
- `creditUsagePercent`
- `productUsage[] {product, usagePercent}`
- `monthlyLimit`, `used`, `onDemandCap`, `onDemandUsed` and `prepaidBalance`, each either `{val}` or a number
- `billingPeriodStart` and `billingPeriodEnd`

**Semantics [src]:**
- Weekly percent = `creditUsagePercent`, **USED %, 0–100**, but only when `currentPeriod.type` contains "weekly". The UI shows `100 - used` (XaiQuotaBody.tsx:137-141).
- Monthly used % = `min(used, monthlyLimit) / monthlyLimit × 100`.
- **Pay-as-you-go:** on-demand used % = `onDemandUsed / onDemandCap × 100`. When `onDemandUsed` is absent it is derived as `max(0, used - monthlyLimit)` (builders.ts:505-526).
- Money values are **cents**, displayed as `/100` USD (XaiQuotaBody.tsx:16-22).
- Plan fallback by `monthlyLimit`: 15000 is SuperGrok and 150000 is SuperGrok Heavy (XaiQuotaBody.tsx:51-73). Tier keywords: `heavy` maps to elite, `supergrok`/`premium` to premium (builders.ts:454-468).
- Weekly reset = `currentPeriod.end` as ISO. Period length is computed as end minus start (builders.ts:441-452).

**Paid detection [src]** (Panel `src/utils/quota/xaiPaid.ts:82-102`). A credential counts as paid if both `using_api` and `prefix=="paid"` are present, or if any JWT has `tier >= 1`. The v8 list response carries neither `prefix`, `using_api`, nor tokens. So with v8.0.10 the panel effectively always tries billing first, and only probes paid-health when billing yields nothing [inf].

### 2.7 Built-in `/routing/cooldown/reset`
`POST {auth_index}` clears CPA's own cooldown and returns `{status:"ok", auth_index, models:[...]}` (Panel `src/services/api/authFiles.ts:517-562`; CPA route `server_management_v8.go:32`). It does not reset upstream quota.

---

## 3. Enable/disable: `PATCH /credentials/status`

CPA `internal/api/handlers/management/auth_files_fields.go:28-147`.

Request [src]:
```json
{ "name": "claude-user@example.com.json", "auth_index": "a1b2c3d4", "disabled": true }
```
- `name` is required. It matches auth ID or FileName.
- `auth_index` is optional and disambiguates (`:65`, lookup `auth_files.go:305-330`).
- `disabled` is required as a boolean pointer (`:50-53`).

Responses [src]:
- `200 {"status":"ok","disabled":true}` (`:146`).
- Config API-key credentials instead return `{"status":"ok","disabled":true,"via":"config:excluded-models","excluded_pattern":"…"}` and rewrite config (`:97-126`).
- Plugin virtual credentials return `409` unless targeted by their source file name (`:70-95`).
- `404 {"error":"auth file not found"}`, `400`, `500`.

**Effect [src]:**
- `applyAuthDisabledState` sets `Disabled`, `Status=disabled`, `StatusMessage="disabled via management API"` and `Metadata["disabled"]=true` (`:238-255`).
- Then `Manager.Update`: it replaces the in-memory auth, `scheduler.upsertAuth` makes it unschedulable immediately (`isSchedulableAuth` rejects disabled, CPA `sdk/cliproxy/auth/scheduler.go:194-197`), and `m.persist` calls `store.Save` (CPA `sdk/cliproxy/auth/conductor_lifecycle.go:161-163, 265-284, 434-479`).

**Persisted to the JSON file? Yes [src].** The file store writes `auth.Metadata` with `disabled` set to `auth.Disabled` (CPA `sdk/auth/filestore.go:75-150`, lines 118/126/150). Two exceptions: runtime-only, config-API-key and plugin-virtual credentials are not persisted through this path (`conductor_lifecycle.go:441-451`). Persist failure is only logged and does not fail the request (`:281-283`).

**In-flight requests:** **not interrupted** [inf, strong]. `updateInternal` swaps the map entry and scheduler entry without touching execution contexts (`conductor_lifecycle.go:165-290`). A grep for `disabled`/`cancel(` in `conductor_execution.go`, `conductor_stream.go`, `conductor_home_execution.go` and `home_in_flight_publisher.go` found nothing tying disable to cancellation. Requests already holding the auth finish. New picks skip it.

---

## 4. Priority routing

**Supported: yes [src].**

| Question | Answer |
| --- | --- |
| Field name | `priority`, an integer, top-level in the auth JSON file. Loaded by `ApplyAuthPriorityMetadata` into `Metadata["priority"]` and `Attributes["priority"]` (CPA `sdk/cliproxy/auth/priority.go:11-42`; called from `internal/watcher/synthesizer/file.go:215-216` and `sdk/auth/filestore.go:301`). File values must be JSON numbers or numeric strings. |
| What the selector reads | `Attributes["priority"]` only. Missing or invalid means 0 (CPA `sdk/cliproxy/auth/selector.go:364-377`). |
| Ordering | **Higher value first.** Only the highest tier that has an available credential is eligible (`selector.go:552-580` picks max; scheduler `scheduler.go:1595-1597` sorts tiers descending; `pickReadyLocked` `:1318-1328`). Lower tiers are used only when every higher-tier credential is cooling down, disabled or excluded for the model. Negative values work (panel hint: "e.g. 10 or -1 … Larger value means higher priority", Panel `src/i18n/locales/en.json`). |
| Interaction with `routing.strategy` | Strategy applies **inside** the chosen tier. Inside a tier, entries are sorted by auth ID (`scheduler.go:1583-1586`, `selector.go:576-578`). `round-robin` rotates by ID successor (`selector.go:614-652`). `fill-first` always takes the first ID in the tier (`selector.go:812-821`, `scheduler.go:1377-1378`). `weighted-round-robin` uses `weight` (`scheduler.go:1379-1380`). Config key: `routing.strategy: round-robin | weighted-round-robin | fill-first` (CPA `config.example.yaml:127-132`). Plugin schedulers can opt in to see all tiers (`conductor_selection.go:45-57`). |
| Set via API | `PATCH /credentials/fields` with body `{"name":"<file>","priority":10}`. The panel exposes this (Panel `src/services/api/authFiles.ts:23-51,539-540`). The handler writes `Metadata["priority"]`, then `syncAuthFilePriorityAttribute` sets `Attributes["priority"]` (CPA `auth_files_fields.go:258-416, 620-622, 688-709`). Value `0` deletes the attribute, which is the same as the default. A non-integer value removes it. |
| Live without restart | **Yes [src].** `Manager.Update` calls `m.scheduler.upsertAuth(authClone)` (`conductor_lifecycle.go:273-275`), and the scheduler rebuilds meta with `priority: authPriority(auth)` (`scheduler.go:1051`). It is persisted to the file through `store.Save` (section 3). |

`PATCH /credentials/fields` response is `200 {"status":"ok"}`, or `400 {"error":"no fields to update"}`, `404`, `409` (plugin virtual).

Other patchable keys (Panel `authFiles.ts:23-51`): `request_retry` (int or null), `prefix`, `proxy_url`, `headers` (merge; empty value deletes), `weight` (int or null), `note`, `websockets`, `disabled`, `excluded_models`, `model_aliases`, `request_scoped_errors`, `disable_cooling`, `using_api`, `expired`. Dotted paths set nested metadata (CPA `auth_files_fields.go:517-540`).

---

## 5. OAuth login / re-login

**Start [src]:** `GET /oauth/auth-url?provider=<p>[&is_webui=true]` (CPA `auth_files_v8.go:11-36`).

| Provider | `provider=` value | Flow |
| --- | --- | --- |
| Claude | `claude` | PKCE redirect. Callback forwarder on port **54545** (CPA `auth_files_oauth_callback.go:18`) |
| Codex | `codex` | PKCE redirect. Callback forwarder on port **1455** (`:19`) |
| Antigravity | `antigravity` | redirect to `http://localhost:51121/oauth-callback` (CPA `internal/auth/antigravity/constants.go:8`; `auth_files_provider_oauth.go:374`) |
| xAI | `xai` | **device code** (`auth_files_provider_oauth.go:526-637`) |

Other values: `kimi`, `kimi-ai`, `devin`, `meta`, or a plugin provider ID. Anything else returns `404 {"error":"provider_not_found"}`.

Response shapes [src]:
```json
// claude / codex / antigravity  (auth_files_provider_oauth.go:195, 356, 523)
{ "status": "ok", "url": "https://claude.ai/oauth/authorize?...", "state": "<state>" }
// xai  (:627-636)
{ "status": "ok", "url": "<verification_uri_complete>", "state": "xai-<unixnano>", "flow": "device",
  "user_code": "ABCD-EFGH", "expires_in": 900 }
```

**Status [src]:** `GET /oauth/status?state=<state>` (CPA `auth_files_provider_oauth.go:952-1026`).
- `{"status":"wait"}` means pending.
- `{"status":"ok"}` means credential saved. An empty `state` also returns `ok` (`:953-956`).
- `{"status":"error","error":"<msg>"}`. Messages include `unknown or expired state`, `Timeout waiting for OAuth callback`, `Failed to exchange authorization code for tokens`, `Failed to save authentication tokens`, `State code error`, `Bad request`.
- `400 {"status":"error","error":"invalid state"}` for a malformed state.

Terminal states are `ok` and `error`. Session TTL is 30 min while pending and 1 min after completion (CPA `oauth_sessions.go:17-19`). The redirect flows wait 5 min for the callback file (`auth_files_provider_oauth.go:118-119`).

**Cancel [src]:** `DELETE /oauth/session?state=` returns `{"status":"ok","cancelled":true}` (`:949`).

**`is_webui` and the localhost callback [src]:**
- `is_webui` accepts `1`, `true`, `yes` or `on` (CPA `auth_files_oauth_callback.go:28-39`).
- When set, CPA starts a temporary HTTP forwarder on `0.0.0.0:<provider port>` that redirects the browser's localhost callback to CPA's own callback route. The background goroutine waits for the callback file and stops the forwarder when it finishes (`auth_files_provider_oauth.go:72-92`; forwarder `auth_files_oauth_callback.go:41-60`).
- The panel sets `is_webui=true` for `codex`, `claude`, `antigravity`, `xai` and `devin` (Panel `src/services/api/oauth.ts:31,42-52`). It maps `anthropic` to `claude` (`:33-39`).
- **Remote fallback.** When the browser is not on the CPA host, the localhost redirect fails. The user pastes the full redirect URL and the panel calls `POST /oauth/callback` with `{"provider":"claude","redirect_url":"http://localhost:54545/callback?code=…&state=…"}` (Panel `oauth.ts:66-73`). CPA also accepts `provider`, `state`, `code` and `error` as GET query params or POST fields, and infers the provider from the state. **This route needs no management key**, only a valid pending state (CPA `server_management_v8.go:14-15`; `docs/management-api-v8.md:12-13,130-135`). A 200 from the callback does not mean the exchange finished, so keep polling status.

**Re-login [inf].** There is no dedicated re-login endpoint. Running the same flow again saves to a file whose name is derived from the account (Claude: `CredentialFileName(email, orgUUID, accountUUID)`, `auth_files_provider_oauth.go:169`). So re-auth of the same account overwrites or updates the existing credential in place. I did not verify whether that preserves `priority`, `note` or `disabled`. `mergeExistingAuthFileMetadata` exists (`auth_files_fields.go:908`), but I did not read it.

---

## 6. Usage

**`GET /observability/usage/queue?count=N` [src]** (CPA `internal/api/handlers/management/usage.go:24-55`).
- `count` defaults to 1. It must be > 0, otherwise `400 {"error":"count must be a positive integer"}`.
- The response is a bare JSON array of records.
- **Popping is destructive.** `PopOldest` advances the head (CPA `internal/redisqueue/queue.go:88-96,199-224`). Two consumers steal from each other.
- **Retention.** Items older than `observability.usage.redis-usage-queue-retention-seconds` are pruned. Default 60s, max 3600 (`queue.go:9-11,55-63,226-241`; `config.example.yaml:1094-1097`). There is no count cap.
- The queue is enabled only when a management secret is configured, or in Home mode (`internal/api/server.go`, `server_reload.go`, found by code search).
- Records are produced only when `observability.usage.usage-statistics-enabled` is true. **The example config sets it to false** (`config.example.yaml:1090-1092`; applied in `cmd/server/main.go`; gate `internal/redisqueue/plugin.go:26`).
- **Gotcha.** If any RESP/Redis-protocol subscriber is connected, records go to the subscribers and are **not** enqueued for HTTP (`queue.go:65-76,141-160`).

Record shape [src] (`internal/redisqueue/plugin.go:135-223`):
```json
{
  "timestamp": "2026-10-02T10:00:00.123Z",
  "latency_ms": 2140, "ttft_ms": 380,
  "source": "...", "auth_index": "a1b2c3d4",
  "access_token_sha256": "...",
  "client_ip": "127.0.0.1", "resolved_client_ip": "...", "x_forwarded_for": "", "user_agent": "...",
  "tokens": { "input_tokens": 1200, "output_tokens": 340, "reasoning_tokens": 0, "cached_tokens": 0,
              "cache_read_tokens": 0, "cache_read_tokens_present": true, "cache_creation_tokens": 0, "total_tokens": 1540 },
  "failed": false, "generate": true, "stream": true,
  "fail": { "status_code": 200, "body": "" },
  "response_headers": { },
  "accounting_version": 1, "token_breakdown": { },
  "provider": "claude", "executor_type": "...", "model": "claude-...", "alias": "...",
  "endpoint": "/v1/messages", "auth_type": "oauth", "api_key": "<client api key>",
  "request_id": "...", "execution_id": "...", "trace_id": "...",
  "session_id": "...", "parent_session_id": "...", "node_kind": "...", "is_fork": false, "is_compaction": false,
  "reasoning_effort": "", "service_tier": "", "response_service_tier": "", "response_model": ""
}
```
`api_key` is the **client's proxy API key**, so treat it as a secret.

**Non-destructive aggregates in v8 [src]:**
- **Per credential.** `GET /credentials` gives `success` and `failed` (lifetime, in memory, reset on restart [inf]) plus `recent_requests` (20 × 10-min buckets).
- **Per upstream API key.** `GET /observability/usage/api-keys` returns `{ "<provider>": { "<base_url>|<api_key>": {success, failed, recent_requests[]} } }` (CPA `internal/api/handlers/management/api_key_usage.go:56-117`). It only covers `api_key`-kind credentials.
- There is **no** `/usage` or `/observability/usage/summary` route in v8 (route list CPA `server_management_v8.go:40-41`).

**Does the official panel consume the queue? No [src].** A code search for `usage/queue` in the panel repo returns nothing. The panel only calls `/observability/usage/api-keys` (Panel `src/services/api/apiKeyUsage.ts`; `tests/managementV8Api.test.ts:313`) and uses `recent_requests` from the credentials list. So lastcall can be the queue's only consumer, as long as no other tool polls it.

---

## 7. How CPA serves `management.html`

**Serving [src].**
- `GET /management.html` (CPA `internal/api/server_routes.go:55`) is handled by `serveManagementControlPanel` (CPA `internal/api/server_management.go:310-338`).
- It returns 404 if `cfg.Home.Enabled` or `management.disable-control-panel: true`.
- If the file is missing, CPA downloads it synchronously. Then it calls `c.File(path)`.

**File location [src].** CPA `internal/managementasset/updater.go:142-187`:
- env `MANAGEMENT_STATIC_PATH`, either a directory or a path ending in `management.html`;
- else `<writable path>/static/management.html`;
- else `<dir of config file>/static/management.html`.

**Download source [src]:**
- `management.panel-github-repository` takes a repo URL or a releases API URL. It is resolved to `…/releases/latest` (`updater.go:312-342`). The default is `https://api.github.com/repos/router-for-me/Cli-Proxy-API-Management-Center/releases/latest` (`:29`; `config.example.yaml:81-82`).
- CPA looks for an asset named exactly `management.html` (`:31,344-376`). It verifies the release asset `digest` with sha256 and aborts on mismatch (`:257-278`).
- If the file is missing and GitHub fails, CPA falls back to `https://cpamc.router-for.me/` **without digest verification** (`:30,293-301`).

**Auto-update [src].**
- A background check runs every 3h (`:34,61-105`). Syncs are throttled to one per 30s (`:33,203-217`).
- It is skipped when Home mode is on, when `disable-control-panel` is true, or when `management.disable-auto-update-panel: true` (`:107-121`; `config.example.yaml:73-75`).
- A local file whose hash differs from the latest release **gets overwritten** unless auto-update is disabled (`:236-283`).

**Config keys (v8 layout, under `management:`) [src]** (`config.example.yaml:59-82`; legacy name `remote-management:` at `:1171-1173`):
- `disable-control-panel` (bool)
- `disable-auto-update-panel` (bool)
- `panel-github-repository` (string)
- `allow-remote` and `secret-key`, which are about auth (section 8)

**Serving a local file. Yes [inf from src]**, with any of these options:
- (a) Put lastcall's built `management.html` at the resolved static path and set `management.disable-auto-update-panel: true`. The file is then served as-is. Without that flag, the 3h updater replaces it.
- (b) Set `MANAGEMENT_STATIC_PATH` to lastcall's file, together with (a)'s flag.
- (c) Point `panel-github-repository` at a lastcall GitHub repo whose latest release has a `management.html` asset with a digest.
- (d) Set `disable-control-panel: true` and host lastcall separately. The management API keeps working, but CORS was not checked (see open items).

---

## 8. Management auth

CPA `internal/api/handlers/management/handler.go:263-398`, plus availability middleware in `internal/api/server_management.go:204-227`.

**Headers [src].** `Authorization: Bearer <key>` (any non-Bearer `Authorization` value is used raw), or `X-Management-Key: <key>` (`:276-288`). The panel sends Bearer (Panel `src/services/api/client.ts:121-123`).

**Response headers [src].** These are on every authenticated route: `X-CPA-VERSION`, `X-CPA-COMMIT`, `X-CPA-BUILD-DATE`, `X-CPA-SUPPORT-PLUGIN` (`:268-271`). They are a cheap way to detect the version.

**Key sources [src]:**
- `management.secret-key` is bcrypt-hashed on startup if plaintext (`config.example.yaml:65-68`; compare `:390`).
- Env `MANAGEMENT_PASSWORD` is compared in constant time and also **forces allow-remote** (`:72-84, 318-320, 385-388`).
- A runtime local password applies to localhost only, for TUI (`:237-238, 376-383`).
- If no secret is configured, **all management routes return 404** (`server_management.go:213-227`; `config.example.yaml:67`). The middleware itself would return `403 "remote management key not set"` (`:367-369`).

**No localhost bypass [src].** Localhost still needs a key (`:264-265`; `config.example.yaml:62,66`). `allow-remote: false` only blocks non-127.0.0.1/::1 clients with `403 "remote management disabled"` (`:338-340`). The client IP comes from gin `c.ClientIP()` (`:273`). Whether trusted proxies are configured was not checked.

**Lockout [src].** After **5 failed attempts per client IP**, CPA bans the IP for **30 minutes**, returning `403 "IP banned due to too many failed attempts. Try again in <dur>"` (`:302-303, 324-335, 342-356`). **This applies to localhost too.** A missing key counts as a failure (`:371-374`). A wrong key returns `401 "invalid management key"`. A success resets the counter (`:358-365, 395`). Stale entries are purged hourly (`:33-37, 101-118`). For lastcall: never retry a bad key automatically.

**OAuth callback routes** (`/v8/management/oauth/callback`) skip the key check and validate the pending state instead (`server_management_v8.go:14-15`).

---

## Unverified / open

1. **Upstream payload units and fields seen live.** Everything in section 2 is what the panel *parses*, not what I saw on the wire. Unconfirmed:
   - Claude `utilization` is 0–100. The panel assumes it, but I did not observe it.
   - Codex `reset_at` is unix seconds. The panel accepts either.
   - xAI cent units, plus the product names in `productUsage`. "GrokBuild" is not referenced anywhere in panel code; it is presumably a `productUsage[].product` value [inf].
2. The keys inside `quota.signals` per provider, and whether they are enough to show 5h/7d percentages without upstream calls. I only read the header allow-list, not real values.
3. **CORS.** I did not check whether CPA sets CORS headers on `/v8/management/*` for a separately hosted lastcall origin. That decides between same-origin (served as `management.html`) and a lastcall backend proxy.
4. **Re-login metadata preservation.** I did not read `mergeExistingAuthFileMetadata` (`auth_files_fields.go:908-944`) or `saveTokenRecord`, so it is unknown whether priority, note and disabled survive an OAuth re-login.
5. Codex and Antigravity re-login flows beyond the start response. The Codex/Antigravity goroutines (`auth_files_provider_oauth.go:198-523`) were skimmed, not read in full.
6. **Disable and in-flight.** The "not interrupted" conclusion comes from absence in the files I grepped. I did not trace streaming executors to the end. A live test would settle it.
7. **`ClientIP` trust.** I did not check `gin` trusted-proxy config, so a reverse proxy in front of CPA could make every client look like localhost, or every client look remote.
8. The xAI `x-userid` header: the v8 list response gives no `sub`, so the panel probably never sends it. I did not check whether billing needs it.
9. Panel/CPA drift: v8.0.11 shipped 2026-10-02. Anything here may change in it.
10. **Claude reset-grant claim flow** (`claudeResetGrants.ts`, `resetGrantOperations.ts`): I only read the constants and types.
