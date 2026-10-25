# Runtime evidence: Codex "Fast mode" / `service_tier=priority`

Author: teammate `runtime-evidence` (task-2). Read-only mining of local Codex client state on this Windows host.
All file content treated as untrusted data. No tokens, JWTs, emails, or prompt bodies reproduced. SQLite DBs copied to `scratch/sqlite-copy/` before querying with `D:\software\miniconda\python.exe` (Python 3.14, sqlite 3.53.2).

## 0. Inventory (sizes as found)

| Path | Size | Role |
|---|---|---|
| `C:\Users\zfx\.codex\models_cache.json` | 398,806 B | model catalog cache (server `/models` response) |
| `C:\Users\zfx\.codex\config.toml` | 11,534 B | client config (`service_tier = "default"`) |
| `C:\Users\zfx\.codex\auth.json` | 4,233 B | ChatGPT OAuth (claims inspected only, secrets redacted) |
| `C:\Users\zfx\.codex\logs_2.sqlite` (+`-wal` 29.6 MB) | 91.9 MB | tracing/log DB, table `logs`, 19,854 rows, span 2026-10-20T12:10:57Z → 2026-10-21T17:53:13Z |
| `C:\Users\zfx\.codex\state_5.sqlite` (+`-wal`) | 51.2 MB | app-state DB, 17 tables; `threads` (662 rows) |
| `C:\Users\zfx\.codex\.codex-global-state.json` | 758,634 B | Electron/Desktop persisted state (server model config, UI atoms) |
| `C:\Users\zfx\.codex\queue_1.sqlite` | 1.17 MB | `queued_items` = 0 rows (no request metadata) |
| `C:\Users\zfx\.codex\sessions\**`, `archived_sessions\**` | 149 `.jsonl`, 495 MB | rollout transcripts |

## A. Model catalog: who advertises Fast/priority

`models_cache.json` top-level keys: `fetched_at`, `etag`, `client_version`, `identity`, `models`. `models` = **9** entries, all identical in tier metadata:

| slug | display_name | priority | visibility | supported_in_api | service_tiers | additional_speed_tiers | available_access_programs |
|---|---|---|---|---|---|---|---|
| `gpt-6.1-sol` | GPT-6.1-Sol | 1 | list | true | `[{"id":"priority","name":"Fast","description":"2x speed, increased usage"}]` | `["fast"]` | `{"cyber":["standard"]}` |
| `gpt-6-astra` | GPT-6-Astra | 2 | list | true | same, "2x speed, increased usage" | `["fast"]` | `{"cyber":["standard"]}` |
| `gpt-6-sol` | GPT-6-Sol | 4 | list | true | `[{"id":"priority","name":"Fast","description":"1.5x speed"}]` | `["fast"]` | `{"cyber":["standard"]}` |
| `gpt-reserve` | GPT-Reserve | 4 | **hide** | true | "1.5x speed, increased usage" | `["fast"]` | `{"cyber":["standard"]}` |
| `gpt-6-luna` | GPT-6-Luna | 5 | list | true | "1.5x speed" | `["fast"]` | `{"cyber":["standard"]}` |
| `gpt-5.6-sol` | GPT-5.6-Sol | 6 | list | true | "1.5x speed, increased usage" | `["fast"]` | `{"cyber":["standard"]}` |
| `gpt-5.6-terra` | GPT-5.6-Terra | 8 | list | true | "1.5x speed, increased usage" | `["fast"]` | `{"cyber":["standard"]}` |
| `gpt-5.6-luna` | GPT-5.6-Luna | 9 | list | true | "1.5x speed, increased usage" | `["fast"]` | `{"cyber":["standard"]}` |
| `codex-auto-review` | Codex Auto Review | 43 | **hide** | true | "1.5x speed, increased usage" | `["fast"]` | `{"cyber":["standard"]}` |

Findings:
- **Every** catalog model carries `service_tiers` with `id: "priority"`, `name: "Fast"` **and** `additional_speed_tiers: ["fast"]`. No model is excluded at catalog level.
- `available_access_programs` is uniform `{"cyber": ["standard"]}` — a program-scoped marker, not a per-model Fast gate. Related server feature flag `feature.api_key_cyber_access_programs` = `"false"` on every one of the 1,107 `feedback_tags` rows carrying the `service_tier` tag (3,547 `feedback_tags` rows exist in total).
- `supported_in_api: true`, `visibility` differs only for the two hidden models.
- No catalog model exposes a tier id `"fast"`; the API-facing id is **`priority`**, while the client UI value is **`fast`** (see §3/§6).

## D. `identity` / `client_version` verbatim (non-secret)

```
fetched_at     = "2026-10-21T17:51:02.938976500Z"
etag           = "W/\"094e399bfb37eb6db2cfd48547eed37b\""
client_version = "0.163.0"
identity       = "75dd36e12848dd3a4989cd908d02b4837e4a1d12723883587f8e293a88726ce7"
```

- `identity` is a 64-hex-char opaque digest. Tested sha256/sha1/md5 over account-id, user-id, org-id, subject and common concatenations — **no match**, i.e. server-side/opaque, not locally derivable.
- What the client actually keys on (from `logs`, target `codex_http_client::client`):
  - model fetch: `GET https://chatgpt.com/backend-api/codex/models?client_version=0.163.0` → `200`, `content-length: 567356`, `etag: W/"094e399bfb37eb6db2cfd48547eed37b"`, header `x-models-etag` returned on every `/responses` reply; `codex_models_manager::cache` logs `cache_ttl_secs=300`, `cached_version=Some("0.163.0")`, `expected_version="0.163.0"`.
  - Desktop shell identity: app-server spans carry `app_server.client_name="Codex Desktop"`, `app_server.client_version="26.1016.51333"` (distinct from library `0.163.0`).
  - Account identity: `auth.json` id_token claims → `chatgpt_plan_type = "pro"`, `chatgpt_account_id = "22954eee-…94fe"`, `organizations=[{id:"org-nNW4…9VTe", role:"owner", title:"Personal"}]`, `iss=https://auth.openai.com`, `aud=["app_EMoamEEZ73f0CkXaXp7hrann"]`, `auth_provider=google`. Also `chatgpt_subscription_active_start=2026-08-24T18:33:54Z`, `chatgpt_subscription_active_until=2026-09-24T18:33:54Z` (already past at token-issue `iat=2026-10-18T03:16:19Z` — see caveats).
  - Response-side entitlement headers (106 captured `/responses` replies, all `responses_http` transport, 2026-10-21T10:56–13:28Z): `x-codex-plan-type: "pro"`, `x-codex-active-limit: "premium"`, `x-codex-credits-has-credits: "True"`, `x-codex-credits-balance: "63000.0000000000"`, `x-codex-credits-unlimited: "False"`, `x-codex-primary-used-percent: "29"…"34"`, `x-codex-primary-window-minutes: "10080"`, `x-codex-secondary-*` empty/0. **No header names or values mention `service_tier`.**
- Server-side per-account config (Desktop, `.codex-global-state.json` → `electron-persisted-atom-state["chatgpt-work-models-v1/user-<uid>/<acct>"]`, `fetchedAt = 2026-10-21T15:55:31Z`): `title = "ChatGPT Pro"`, `model_picker_version = 2`, `workspace_model_policy = null`, `internal_groups = null`, `default_model_slug = "gpt-6.1-sol-wm"`, `eligible_codex_model_slugs = ["gpt-5.6-sol","gpt-5.6-terra","gpt-5.6-luna","gpt-6-astra","gpt-6.1-sol","gpt-6-sol","gpt-6-luna"]`, `model_upgrades = {"gpt-5.6-sol-wm":"gpt-6.1-sol-wm","gpt-5.6-luna-wm":"gpt-6-luna-wm","gpt-6-sol-wm":"gpt-6.1-sol-wm"}`.
  - `versions`: 7 (6.1 Sol, 6 Astra, 6 Sol, 6 Luna, 5.6 Sol, 5.6 Terra, 5.6 Luna), **40 intelligence_presets total**, `preset_type = "available"` ×40, `presets_without_fast = 0`.
  - Each preset: `service_tier_options = [{"service_tier":"standard","title":"标准","subtitle":"默认用量"},{"service_tier":"fast","title":"快速","subtitle":"用量更高","icon_src":"<svg…>"}]`, `default_service_tier = "standard"`, `thinking_effort ∈ {min,standard,extended,xhigh,max,ultra}`, `lane = "thinking_plus_plus"`.
  - **Conclusion (D): the backend tells this Pro account that Fast is offered on every model/preset, with Standard as default — i.e. the gate is data-driven from the backend, not a local allowlist.**

## B. Do logs/state/rollouts contain `service_tier` request/response records? Does upstream echo priority?

### B1. What exists (all local sources searched exhaustively)
1. `logs_2.sqlite`, table `logs(id, ts, ts_nanos, level, target, feedback_log_body, module_path, file, line, thread_id, process_uuid, estimated_bytes)`. `feedback_log_body` LIKE `%service_tier%` → **1,109 rows**; parsed as structured `tags_json` on target `feedback_tags`:
   - `"service_tier":"default"` — **671 rows**, 2026-10-20T12:11:11Z → 2026-10-21T17:22:21Z, 65 threads, models `{gpt-6-astra:476, codex-auto-review:76, gpt-6-luna:62, gpt-6.1-sol:57}`.
   - `"service_tier":"priority"` — **436 rows**, **2026-10-21T15:56:58Z → 2026-10-21T17:21:50Z**, 17 threads, models `{gpt-6.1-sol:284, codex-auto-review:152}`.
   - Exact sanitized fragment: `…"rollout_budget.sampling_token_weight":"unset","service_tier":"priority","token_budget.auto_compact_fallback_buffer_tokens":"unset"…`
   - Same tag rows carry `feature.fast_mode = "true"` and `feature.ultrafast_mode = "true"` (present on 1,107/1,107 rows → constant for this account, so the feature flags are ON and are *not* what changed).
2. `logs_2.sqlite`, target `codex_core::client`, TRACE: `non-incremental request reason="service_tier_changed" previous_item_type="none" current_item_type="none"` — exactly **2 rows** (2026-10-21T16:26:55Z, 16:27:43Z), alongside other reasons `connection_closed`(61), `text_changed`(38), `restored_history`(4), `reasoning_changed`(4), `model_changed`(1). Proves `service_tier` participates in the request fingerprint / metadata the client sends on `POST /responses` (websocket transport, `turn.has_metadata_header=true`).
3. Rollouts (`sessions`/`archived_sessions` `*.jsonl`): the only tier field is `event_msg.payload.thread_settings.service_tier` ∈ {`"default"` (1,192 events), `"priority"` (187 events, 14 files)}; `model_provider_id` is always `"openai"` (1,379 settings events). Examples of per-turn flips: `rollout-2026-09-22T19-32-49-01a0c812…` sequence `default → priority → default`; `rollout-2026-10-21T00-55-51-01a15f2d…` sequence `priority → default → priority → default`. `priority` was applied to **6 different models** (`codex-auto-review` 146, `gpt-6.1-sol` 16, `gpt-6-astra` 12, `gpt-6-luna` 7, `gpt-5.6-sol` 4, `gpt-6-sol` 2 events) — i.e. the client forwarded the chosen tier regardless of model.
4. `state_5.sqlite`: no tier column. `threads(id, rollout_path, model, reasoning_effort, tokens_used, cli_version, model_provider, originator, …)`; 662 rows, `model_provider ∈ {openai:459, custom:203}` (custom rows are older CLI ≤0.153.4, no provider config remains in `config.toml`).
5. `queue_1.sqlite.queued_items` = 0 rows.

### B2. Upstream echo: **not observable in any local artifact**
- No response-side field named `service_tier` exists anywhere: the 1,109 log hits are all client-side `feedback_tags` (+2 `service_tier_changed` trace lines). Full-body LIKE scan found zero occurrences on `codex_api::*`, `codex_core::stream_events_utils`, `codex_http_client::*` bodies.
- Only 106 `/responses` HTTP replies logged headers at all (all `responses_http` transport, 10:56–13:28Z, before the priority window). During the priority window the client used `responses_websocket` (`wss://chatgpt.com/backend-api/codex/responses`, `transport="responses_websocket"`) and the client **does not log websocket response headers/frames**; no echo value to compare.
- `token_count` rollout events expose `rate_limits{plan_type, limit_id, credits, primary, secondary, …}` and `info{last_token_usage,total_token_usage,…}` — **no tier field**, so no echo there either.
- Indirect (not proof of honor): inside the 5,092 s priority window the log DB records **0 ERROR and 0 WARN**, 2,624 INFO / 1,961 DEBUG / 1,441 TRACE, incl. 1,883 `codex_core::stream_events_utils` events and 78 websocket connect/stream events → priority turns streamed normally, no denial/fallback/entitlement error.
- Client-computed stat (`.codex-global-state.json`, atom `fast-mode-personalized-estimate`): `{"estimatedSavedMs": 9902628, "rolloutCountWithCompletedTurns": 50, "computedAtMs": 1790087163056}` (= computed 2026-09-22T14:26:03Z) — the client's own estimate that Fast saved ~2.75 h across 50 rollouts; a *client heuristic*, not an upstream echo.
- **Therefore question B is partially answered: request-side `service_tier` is well evidenced (`priority` sent on 436 turns); upstream echo (priority vs default vs absent) is UNKNOWN from client-local data.** The Lead's gateway DB (`backend` patch `upstream_service_tier` column) is the right place to answer it.

## C. Any client-side gating rule visible in data?

- `config.toml` line 5: `service_tier = "default"` (model `gpt-6.1-sol`, `model_reasoning_effort="xhigh"`, `model_context_window=1000000`). **There is no `[model_providers.*]` / `base_url` / `wire_api` override** in the current config, so the client talks to the default OpenAI endpoints (`chatgpt.com/backend-api/codex/...`, seen in logs).
- The client has **no local allowlist**: `models_manager` never logs tier metadata (`additional_speed_tiers` appears 0 times), and all 9 catalog models advertise `priority`, so a model-based client gate would be invisible and is unsupported by data.
- Available gating evidence is **server-driven**:
  1. Backend model config (`chatgpt-work-models-v1`) for this account = `title "ChatGPT Pro"`, 40/40 presets `available`, each with `standard`+`fast` options, default `standard` (fetched 2026-10-21T15:55:31Z, ~87 s before the first `priority` turn at 15:56:58Z — consistent with a Desktop session start/plan refresh unblocking the toggle).
  2. Feature flags from server: `feature.fast_mode="true"`, `feature.ultrafast_mode="true"` on every row.
  3. UI atoms: `has-seen-fast-mode-announcement=true`, `has-seen-fast-mode-home-banner=true`, `has-dismissed-priority-filter-coachmark-v1=true`, `composer-recent-model-configurations-v1=[{model:"gpt-6.1-sol",reasoningEffort:"ultra",serviceTier:"priority"},{model:"gpt-6.1-sol",reasoningEffort:"xhigh",serviceTier:"priority"},{model:"gpt-6-astra",reasoningEffort:"xhigh",serviceTier:null}]` (selected tier persists per thread; `null` = standard/unset).
  4. Entitlement headers on replies: `x-codex-plan-type: "pro"`, `x-codex-active-limit: "premium"`, credits balance 63,000, `x-codex-credits-unlimited: "False"`.
- Tier naming/equivalence (from web pages captured in a rollout, see §6): client UI `fast` ⇄ API `service_tier: "priority"`; persisted client value is `"priority"`/`"default"` (never `"fast"` in `thread_settings`).
- No error strings found: LIKE searches for `entitle` (0 rows), `downgrade` (0), `unsupported` (0), `not supported` (17 rows — all unrelated), `plan_type` (106 rows — header dumps only), `service_tier` in ERROR/WARN (0). Explicit checks inside the priority window: `rate limit` 0, `quota` 0, `denied` 0, `limit_id` 0; the raw `429`/`402`/`403` substring hits (43/19/53) are all **spurious** — UUID fragments, timestamps, base64 in `x-codex-turn-state`/`codex.otel` payloads — not HTTP statuses. Client never logged "priority not available"/"entitlement".

## 6. External documentation encountered in client data (untrusted web content, cited as reference only)

Captured as `response_item.payload.type=custom_tool_call_output` from `tools.web__run` search results in `C:\Users\zfx\.codex\archived_sessions\rollout-2026-10-14T20-18-31-01a13a22-a452-77a0-a591-a34f47884a44.jsonl` (line 28/42/50; original pages `developers.openai.com`; crawled 2026-10-14):

> "Priority processing was renamed Fast mode on July 30, 2026. … You can use either `service_tier: "priority"` or `service_tier: "fast"` in your API requests to access this functionality."
> "To opt in to Fast mode for an individual request, set the `service_tier` parameter to `fast`. Setting `service_tier` to `priority` provides the same behavior for supported models."
> Ultrafast: "Set `model` to `gpt-6-astra` or `gpt-6.1-sol` and `service_tier` to `ultrafast` in each `response.create` event." … "Fast mode: Get up to 2.5× faster speeds in the API."
> Codex `/fast` command: "Switch between standard and faster execution when available. Availability can depend on the connected host, app version, and account configuration."

These are **external docs, not shipped behavior**, and were not authored by the client; kept separate from the runtime evidence above.

## 7. Answers

- **A.** All 9 catalog models advertise `service_tiers=[{"id":"priority","name":"Fast",…}]` and `additional_speed_tiers=["fast"]`; uniform `available_access_programs={"cyber":["standard"]}`, `supported_in_api=true`; no per-model Fast gate in the catalog.
- **B.** Request-side tier is evidenced (`"service_tier":"priority"` on 436 turns / 17 threads / 15:56–17:21Z, with zero errors; `"service_tier":"default"` on 671 turns). **Upstream echo is not recorded anywhere client-side** (no response field/header; websocket replies unlogged) → echo behavior remains unknown from this host.
- **C.** Client-side config is `default`, per-thread tier overrides persist in `thread_settings`, and the tier is a request parameter (2 `service_tier_changed` traces). Gating is server-driven: backend account model config (ChatGPT Pro, all presets offer `standard`+`fast`), `feature.fast_mode/ultrafast_mode=true`, entitlement headers `plan-type: pro`, `active-limit: premium`.
- **D.** `identity = "75dd36e1…a88726ce7"` (opaque 64-hex), `client_version = "0.163.0"`, `etag = W/"094e399bfb37eb6db2cfd48547eed37b"`, `fetched_at = "2026-10-21T17:51:02.938976500Z"`; server is keyed by OAuth account (`chatgpt_plan_type: pro`) + `client_version=0.163.0` query param + Desktop client name/version `Codex Desktop`/`26.1016.51333`.

## 8. Unknowns / caveats

1. **Upstream echo (priority vs default vs absent) is unknown** — needs gateway/proxy capture (`upstream_service_tier` in the Lead's patched `usage_log`) or a websocket frame trace.
2. No local record of the actual `/responses` request body, so "client sends `service_tier:"priority"`" is inferred from `feedback_tags`, `thread_settings`, and the `service_tier_changed` trace — not from a captured payload.
3. `id_token` shows `chatgpt_subscription_active_until = 2026-09-24T18:33:54Z`, already elapsed, while headers say `plan-type: pro`; could be stale claim vs. renewals — unresolved, and it does not prevent fast-mode use.
4. `models_cache.json` mtime (2026-10-21T17:51:02Z) equals its `fetched_at`; the team's `scratch/patch_upstream_tier.py` patches only `backend/**` Go files (verified), so the cache is not team-modified — but only the `/models` response content-length (567,356 B) vs cache (398,806 B) can be compared, and field-level fidelity of the cache is unverified.
5. `thread_history_1.sqlite` (986 MB) was not inspectable (`mode=ro` open fails: `unable to open database file`; not copied). It may hold turn metadata not present elsewhere; currently unexplored.
6. Feature flags `fast_mode`/`ultrafast_mode` are constant `true` in all captured rows, so their *gating effect* (what happens when false) cannot be observed on this host.
7. `custom` provider threads (203 in `state_5.threads`, CLI ≤0.153.4) have no surviving provider config in `config.toml`; whether they carried `service_tier` through a different base URL is unknown.

## 9. Reproducible queries (Python `sqlite3`, DB copies in `scratch/sqlite-copy/`)

```sql
-- logs schema
SELECT name FROM sqlite_master WHERE type='table';
PRAGMA table_info(logs);
-- tier tags per turn
SELECT feedback_log_body FROM logs WHERE target='feedback_tags' AND feedback_log_body LIKE '%"service_tier":"priority"%';
-- tier participation in request fingerprint
SELECT * FROM logs WHERE feedback_log_body LIKE '%service_tier_changed%';
-- response headers per tier
SELECT * FROM logs WHERE feedback_log_body LIKE '%url=https://chatgpt.com/backend-api/codex/responses%';
-- model fetch
SELECT feedback_log_body FROM logs WHERE feedback_log_body LIKE '%api.path="models"%';
-- no echo anywhere
SELECT COUNT(*) FROM logs WHERE feedback_log_body LIKE '%"service_tier"%' AND target NOT IN ('feedback_tags','codex_core::client');
```

Rollout extraction: stream each `*.jsonl`, `json.loads` per line, read only `event_msg.payload.thread_settings` (`service_tier`, `model`, `model_provider_id`) and `token_usage_record`/`token_count` (`rate_limits`, `info`) keys; never print message/instruction bodies.

Scripts written by me (kept for audit): `scratch/extract_models.py`, `extract_models2.py`, `sqlite_schema.py`, `logs_search.py`, `logs_ctx.py`, `logs_tiers.py`, `scan_rollouts.py`, `gating_probe.py`, `gstate_models.py`, `probe2.py`–`probe6.py`, `probe_headers.py`, `probe_doc.py`, `probe_doc2.py`, `probe_identity.py`, `probe_tier_models.py`; full dumps in `scratch/*_out.txt`, doc excerpt in `scratch/doc_extract.txt`.
