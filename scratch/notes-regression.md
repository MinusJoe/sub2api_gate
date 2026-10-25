# Regression archaeology — "forced fast mode no longer takes effect"

Author: teammate `regression-archaeologist` (task-3). Repo `D:\projects\sub4api`, HEAD `c6c626e6b`
(VERSION 1.1.14, no v1.1.12+ tags), analysis time 2026-10-22T03:0x JST. Read-only for tracked
source; this note is the only file written. Data provenance: git history/blobs and tracked source are
shipped-artifact evidence; `scratch/8203.patch`, `scratch/patch_*.py`, `scratch/*_out.txt` are
teammate artifacts (untrusted input, cited as such); `scratch/notes-fast-path.md` and
`scratch/notes-runtime.md` are other teammates' conclusions, not evidence.

## 0. Verdict

* The forced-tier **injection code has no functional regression** between the last release where the
  old boolean `force_openai_fast` existed (`v1.1.11`, 2026-10-17) and HEAD. Commit `ea23ccfed` is a
  faithful transplant of upstream PR #8203 (commit `7c064ede6`): same ordering (forced write before
  the global Fast/Flex policy), same WS handling, same model allowlist semantics (allowlist only
  gates `ultrafast`, never `priority`/fast). No piece was dropped.
* The only runtime-affecting change in the window `v1.1.11..HEAD` is `ea23ccfed`; `f77956b50`,
  `97efa08e2`, `c6c626e6b` are release/usage-echo/UI-only. **No upstream code sync happened in this
  window** — `.github/upstream-sync.json` only had its `sub4api_version` string bumped, three times.
* Therefore a *change* in observed behavior is best explained by state/config/observation, not by the
  rewrite: (1) `groups.forced_service_tier` empty (legacy bool is no longer read, only migration 246
  bridges it; empty is a silent no-op), (2) a global Fast/Flex policy rule that filters/overrides
  `priority` after the forced write (silent, no log), (3) an `openai_bps` account/group platform
  (forced tier structurally impossible), or (4) misreading the 1.1.13/1.1.14 echo row — for OAuth
  Codex the upstream echo `default` is documented as non-authoritative even when Fast is honored.

## 1. Timeline (commit/version → behavior)

| date (JST/UTC) | commit / tag | version | behavior delta |
|---|---|---|---|
| 2026-09-30 | `a3ece4348` v1.1.8 | 1.1.8 | old boolean `force_openai_fast` present (migration `232` blob `aaf0f4f4`); `openAIGroupForcesFast` at `openai_gateway_forward.go:654`, `openai_gateway_request_body.go:2040/2067/2193`; WS v2 adapter already calls the WS fast-policy helper |
| 2026-10-10 | `058640418` v1.1.10 | 1.1.10 | same old bool blob; WS v2 adapter blob `da24f497`; no change to fast paths |
| 2026-10-17 | `ff59be9ac` → `2084c5f73` **v1.1.11** | 1.1.11 | last upstream sync: SUB2API `f2669c8c…` → `b0a02a330…` (0.2.17), 329 files changed incl. `openai_gateway_forward.go`, `openai_gateway_request_body.go`, `openai_ws_v2_passthrough_adapter.go` (conflict), `openai_codex_models_service.go` (conflict). Old bool unchanged; WS adapter blob `1dbcbec1` |
| 2026-10-21 23:04 | `b9475ee7e` + `ea23ccfed` | (1.1.12 dev) | PR #8203 cherry-pick: `forced_service_tier` (`''/default/priority/ultrafast`) replaces boolean; migration `246`; auth snapshot v24→v25; ultrafast model allowlist; billing 6x; admin dropdown |
| 2026-10-22 00:05 | `f77956b50` | **1.1.12** | release commit: VERSION/package/upstream-sync metadata + `changelog/1.1.12` only |
| 2026-10-22 01:35 | `97efa08e2` | **1.1.13** | usage echo recording: `usage_logs.upstream_service_tier` (migration `247`), admin DTO, `openai_service_tier_echo_mismatch` log; VERSION bump. No tier-injection change |
| 2026-10-22 02:37 | `c6c626e6b` HEAD | **1.1.14** | admin usage tooltip always shows echo row; VERSION bump. Backend untouched |

Note: tags exist only through `v1.1.11`; `git describe` = `v1.1.11-5-gc6c626e6b`. 1.1.12/13/14 are
commits/`backend/cmd/server/VERSION` bumps, not tagged releases, at analysis time.

## 2. Cherry-pick fidelity audit (`scratch/8203.patch` vs applied fork code)

Method: the patch is exactly upstream PR commit `7c064ede6` (`refs/remotes/pr/8203`, parent
`b0a02a330` = 0.2.17 tip = the fork's v1.1.11 sync target, author `stl25`, 2026-10-19). For each of
the 49 files in the patch, compare `git show 7c064ede6:<path>` with `git show ea23ccfed:<path>` after
normalizing the module path `Wei-Shaw/sub2api` → `MACOS-DO/sub4api`.

Result: **42/49 byte-identical**; 7 differ, every difference being a pre-existing fork customization,
none in tier semantics:

| file | only difference (fork-side) |
|---|---|
| `backend/ent/migrate/schema.go` | `openai_bps` added to provider enum (fork platform) |
| `backend/internal/handler/dto/mappers.go` | BPS credential state, codex-ticket redaction/list fields |
| `backend/internal/handler/dto/types.go` | BPS + `codex_turn_tickets`/`codex_ticket_latest_event` fields |
| `backend/internal/service/admin_group.go` | `PlatformOpenAIBPS` default-model candidates |
| `backend/internal/service/openai_gateway_forward.go` | BPS short-circuit (`:22-24`), locale normalize (`:26`), codex-ticket injection (`:1453`) |
| `backend/internal/service/settings_view.go` | codex-ticket settings fields |
| `frontend/src/types/index.ts` | `openai_bps`, BPS state, codex-ticket types, request timezone |

Ordering / allowlist / WS checks:
* Old and new forced-tier application exist at exactly the same three call sites: `v1.1.11` has
  `openAIGroupForcesFast` in `openai_gateway_forward.go:654` + `request_body.go:2046/2073/2199`;
  HEAD has `openAIGroupForcedServiceTier` at `forward.go:654`, `request_body.go:2050/2099/2226`.
* Forced write still precedes the global policy in the patch **pre-image** as well (old doc comment:
  "Before the global policy is evaluated, a trusted request Group with ForceOpenAIFast enabled
  unconditionally sets service_tier to priority"), in the patch post-image, and in HEAD
  (`forward.go:653-687`; `request_body.go:2099-2152`, `:2226-2278`). No inverted ordering.
* WS: upstream PR already parameterized `applyOpenAIFastPolicyToWSResponseCreate`; the fork applied
  it verbatim; both WS entry paths call it (`openai_ws_forwarder_ingress.go:442`,
  `openai_ws_v2_passthrough_adapter.go:773`/`:1105`).
* Model allowlist: only `ultrafast` is gated (`openAIModelSupportsForcedUltrafast`, `request_body.go:2070-2077`);
  `priority`/`default` are returned unconditionally (`:2059-2062`). Unchanged vs PR.
* Diff `7c064ede6..ea23ccfed` for tier-relevant files shows only module-path and pre-existing
  fork-customization lines (verified by normalized per-file comparison, not eyeballing).

**No cherry-pick-induced ordering/model/WS defect found.** The feature is materially identical to
upstream intent.

## 3. Lead's patch scripts (`scratch/patch_upstream_tier.py`, `scratch/patch_tests.py`)

These implement the **1.1.13 echo recording**, not the forced tier: they add
`usage_logs.upstream_service_tier` through repository insert/query, `service.UsageLog`,
`openai_gateway_usage.go`, DTO/mappers, and a mismatch log. They also adjust positional-INSERT tests
(`len(prepared.args)-5`, arg-type indexes) — i.e. they can mask a positional misalignment, so I
checked the production SQL by hand:

* `usage_log_repo_insert.go` INSERT has **63 columns and 63 placeholders**; both end
  `…, native_compaction_v2, created_at, upstream_service_tier`; the args builder also ends
  `createdAt, upstreamServiceTier` (`:1379-1380`). Column/arg order is aligned (the `62`/`61` in
  `make([]any, …)` at `:757`/`:1021` are capacity hints only).
* Consequence: the 1.1.13 echo channel is trustworthy for logging, but it cannot have changed the
  outbound tier. The new UI echo row *did* change what operators see (see §7 rank 4).

## 4. Upstream sync state — did a recent sync change client version / UA / headers?

Repo evidence: **no**. `.github/upstream-sync.json` still reads `repository Wei-Shaw/sub2api`,
`version 0.2.17`, `commit b0a02a330…`, `previous_commit f2669c8c…`; the only diffs in the window are
`sub4api_version 1.1.11→1.1.12→1.1.13→1.1.14` (`f77956b50`, `97efa08e2`, `c6c626e6b`).
`tmp/upstream-sync/manifest.json` documents the *last* sync: base `f2669c8c…`, target `b0a02a330…`;
329 changed files (incl. `openai_gateway_forward.go`, `openai_gateway_request_body.go`,
`openai_ws_forwarder_ingress.go`, `openai_ws_v2_passthrough_adapter.go`) and 3 deleted;
`conflicts.json` lists `openai_ws_v2_passthrough_adapter.go`, `openai_codex_models_service.go`,
`gateway_service.go`, `scheduler_cache.go`, README, 2 frontend specs. But that sync landed at
**v1.1.11**, i.e. before the window; v1.1.12+ imported nothing upstream.

Runtime (no commit involved) identity/version mechanisms that *could* change upstream trust; all
predate the window but are the thing to check if "worked before" spans 1.1.10→now:
* Outbound identity is force-rewritten for OAuth/ChatGPT paths: UA, `originator`, `version`,
  `OpenAI-Beta` (`openai_codex_identity.go:176-234`); toggle `gateway.disable_codex_identity_enforcement`
  (`:44-60`). Comments state upstream load-sheds stale/third-party identities at capacity and returns
  HTTP 200 + in-stream `server_is_overloaded` (`:44-53`) — a *load-shedding* risk, not a tier rewrite.
* Min accepted version `0.144.0`; lower → upstream 404 (issue #3901) (`:14-16`, `:166-172`).
* Fallback version constant `codexCLIVersion = "0.146.0"` (`openai_gateway_service.go:64`), while the
  real local client is `0.163.0` (runtime teammate's untrusted evidence). Effective version resolution
  override → `_synced` → constant (`setting_gateway_runtime.go:483-540`).
* Auto-sync task: `openai_codex_version_auto_sync_enabled` default true, 6 h interval, GitHub
  `openai/codex` `/releases/latest` → writes `openai_codex_client_version_synced`
  (`openai_codex_version_sync_service.go:12-23`, `:111-143`). Admin override key
  `openai_codex_client_version` (`domain_constants.go:701-708`); UA override
  `openai_codex_user_agent` (`:697-700`). These can change outbound UA/version **between releases** —
  the only repo-known way client identity can drift without a commit. No evidence in this repo that
  it downgrades `service_tier`.

## 5. `codex_cli_only` engine fingerprint signals (`domain_constants.go`)

Keys: `codex_cli_only_blacklist` / `_whitelist` / `_allow_app_server_clients` / 
`codex_cli_only_engine_fingerprint_signals` (`domain_constants.go:625-635`). Default signal JSON
(contract test `api_contract_test.go:915`): `header_prefix x-codex-` (required), header_exact
`session-id`/`thread-id`, body_path `client_metadata.x-codex-window-id`/`installation-id`. This is an
**inbound admission gate** (`openai_client_restriction_detector.go`) for accounts with
`extra.codex_cli_only=true`, with a global `gateway.force_codex_cli` bypass. It never touches the
outbound `service_tier`; a failure mode is 403/close, not a silent tier downgrade. Only side effect
worth knowing: the fork harvests/synthesizes `x-codex-*` fingerprint headers itself
(`openai_codex_fingerprint.go`, `openai_codex_ticket*`), so a mismatch here would show as client
rejection, not as "fast ignored".

## 6. Inventory of things that can strip/override `service_tier` after the forced write

1. **Global Fast/Flex policy (the only intended silent override).** Evaluated *after* the forced
   write: HTTP `forward.go:661-684`, helper `request_body.go:2099-2152`, WS `:2226-2278`.
   Action `filter` deletes the field, `force_priority` rewrites it, `block` 403s. Setting key
   `openai_fast_policy_settings` (`domain_constants.go:609-613`); default = no rules
   (`settings_view.go` defaults). Deliberately tested: `openai_fast_policy_test.go:396-407`
   ("GroupForceStillHonorsGlobalPolicy"), e2e `openai_oauth_passthrough_test.go:1056-1087`.
   Only `block` produces a log/audit; `filter`/`force_priority` are silent. Ordering is identical in
   v1.1.11, so this explains a regression only if a rule/setting changed since.
2. **BPS accounts short-circuit the tier block entirely.** `Forward` returns `forwardOpenAIBPS`
   before the tier code (`forward.go:22-24`); BPS rejects `service_tier` unless `auto`/`default`
   (`openai_bps_protocol.go:156-162`) and advertises no tiers (`openai_bps_catalog.go:34-35`).
   For `openai_bps` accounts the forced tier can never be applied (and a client-sent `priority` would
   400). Group platform `openai_bps` is also not `groupSupportsOpenAIFast` (`admin_group.go:359-361`),
   so `sanitizeGroupOpenAIFast` clears the field on save (`:363-374`).
3. **Bedrock** strips `service_tier` (`bedrock_request.go:822-831`) — different platform, not Codex.
4. **Compact path preserves it** (`normalizeOpenAICompactRequestBody`, `request_body.go:597-635`) —
   verified, not a stripper.
5. **Billing-side downgrade** does not alter the outbound body: `ResolveOpenAIServiceTierBilling` /
   `ApplyOpenAIServiceTierBillingResolution` (`service_tier_billing.go:63-100`) may lower the *billed*
   tier from the echo, but for OAuth-like Codex accounts `echo=default` is explicitly treated as
   non-authoritative and the requested tier is kept (`:68-86`); `billing.service_tier_downgraded` is
   logged (`:118-132`).
6. **Error passthrough / header allowlists:** no code path deletes `service_tier` in response to an
   upstream error (searched `DeleteBytes(*service_tier)`; only Bedrock and unknown-tier normalization
   strip it). `service_tier` is body/frame-only, so outbound header allowlists / identity enforcement
   (`openai_codex_identity.go:176-234`) cannot remove it. `ops_error_logger.go:2344` only classifies
   the fast-policy block message as a client business limit.
7. **Routing-hint asymmetry (not a strip):** `x-codex-routing-hint` is derived from the final body
   (`openai_routing_hint.go:19-60`; `forward.go:1536`); in WS passthrough the handshake hint is built
   once from the first frame (`openai_ws_v2_passthrough_adapter.go:844-856`) while later frames are
   rewritten independently (`:1105`). With a forced constant tier they agree; only affects which
   pooled WS connection is chosen if the client changes tier mid-session.

## 7. Mechanism ranking (most plausible first)

1. **Data/state: `groups.forced_service_tier` is empty on the affected group.** The new code reads
   only `ForcedServiceTier` (`request_body.go:2058`); the legacy `force_openai_fast` column is dead
   data after 1.1.12 and only migration `246` bridges it
   (`backend/migrations/246_group_forced_service_tier.sql:3-9`). Empty tier is a silent no-op
   (`request_body.go:2065-2067`), and legacy payloads can clear it
   (`handler/admin/group_handler.go:199-211`: `force_openai_fast:false` with no explicit tier →
   pointer to `""`). Also `sanitizeGroupOpenAIFast` clears it whenever the group platform is not
   `openai`/`composite` (`admin_group.go:363-374`). Ask for the `groups` table.
2. **Config: a global Fast/Flex policy rule filters or forces `priority`** (§6.1) — silent, no log;
   check `settings.openai_fast_policy_settings`. Because ordering was identical pre-1.1.12, this only
   explains a change if the rule/settings were added after the old boolean last worked.
3. **Platform/routing: traffic lands on `openai_bps` accounts or a non-openai/composite group**
   (§6.2) — forced tier impossible; check account platform and group platform.
4. **Observation artifact, not a regression:** the 1.1.13/1.1.14 "upstream tier (echo)" row logs the
   upstream-declared tier; for OAuth Codex that echo is commonly `default` even for effective Fast
   (`service_tier_billing.go:63-86` comment; runtime teammate found no echo records client-side).
   `openai_service_tier_echo_mismatch` firing therefore does **not** prove the rewrite failed.
5. **Identity/version drift (outside window, runtime-only):** gateway claims 0.146.0 by default while
   the real client is 0.163.0; auto-sync can change it between releases. Upstream distrusted identity
   is documented as load-shedding (`server_is_overloaded`, 200 + stream error) rather than silent tier
   downgrade; if operators see those overload errors, that is a separate identity issue.

## 8. Checkable diagnostics (for the Lead; no gateway DB in this workspace)

```sql
-- 1) is the forced tier actually persisted correctly?
SELECT id, name, platform, force_openai_fast, forced_service_tier,
       free_openai_fast, free_openai_ultrafast
FROM groups ORDER BY id;
-- 2) did the migrations run?
SELECT filename, checksum FROM schema_migrations
WHERE filename LIKE '246%' OR filename LIKE '247%';
-- 3) global policy + identity/version settings
SELECT key, value FROM settings WHERE key IN (
  'openai_fast_policy_settings', 'openai_codex_client_version',
  'openai_codex_client_version_synced', 'openai_codex_user_agent',
  'openai_codex_version_auto_sync_enabled', 'openai_codex_ticket_enabled');
-- 4) outbound vs echo after the change
SELECT created_at, account_id, model, service_tier, upstream_service_tier
FROM usage_logs
WHERE created_at > now() - interval '1 day'
ORDER BY created_at DESC LIMIT 100;
```
Log greps: `openai_service_tier_echo_mismatch`, `billing.service_tier_downgraded`,
`openai service_tier=`, `openai_fast_policy`, `server_is_overloaded`.
Also verify the gateway binary actually contains 1.1.12+ (`/api/.../version` or image tag): 1.1.12–1.1.14
are untagged commits in this repo, so "after upgrade" needs confirmation.

## 9. Explicit unknowns

* No gateway database/state dump in this workspace → `groups.forced_service_tier`,
  `schema_migrations`, `settings.*` and `usage_logs.*` cannot be inspected here; all four diagnostics
  above are unverified.
* No gateway logs → cannot count policy filters, echo mismatches, or billing downgrades; cannot see
  whether requests were WS (`openai_ws_mode`) and which path served them.
* Upstream behavior for ChatGPT OAuth Codex `service_tier=priority` (honored vs silently downgraded
  vs ignored) is unknown from repo data; the echo is documented as non-authoritative for these
  accounts, so it cannot settle it either way.
* Whether the user is on 1.1.11, 1.1.12, 1.1.13 or 1.1.14 (tags stop at v1.1.11) and whether the
  running binary/DB matches HEAD — unknown; the "window" above is commit-level.
* Could not run `go test` (Go is vendored under `tmp/toolchain` but not on PATH in this session);
  findings are source+committed-test based, not dynamically confirmed.

## 10. Reproduce

```
# fidelity audit (42 identical / 7 fork-customized; list printed)
python - <<'PY'
import re,subprocess
repo=r"D:\projects\sub4api"
patch=open(r"D:\projects\sub4api\scratch\8203.patch",encoding="utf-8").read()
files=re.findall(r"(?m)^diff --git a/(\S+) b/\S+$",patch)
def blob(rev,p): return subprocess.run(["git","-C",repo,"show",f"{rev}:{p}"],capture_output=True).stdout.decode("utf-8","replace")
for p in files:
    a=blob("7c064ede6",p).replace("Wei-Shaw/sub2api","MACOS-DO/sub4api"); b=blob("ea23ccfed",p)
    if a!=b: print("DIFF",p)
PY
# window inventory
git log --oneline --no-merges v1.1.11..HEAD
git show --stat ea23ccfed 97efa08e2 c6c626e6b f77956b50
git show 7c064ede6 --stat   # upstream PR commit behind scratch/8203.patch
```
