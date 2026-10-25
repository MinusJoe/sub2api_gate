# Forced "fast" tier (group.forced_service_tier) — source-side trace

Author: teammate `fast-path-tracer` (task-1). Repo: `D:\projects\sub4api`, HEAD `c6c626e6b`.
Method: read the Go source and tests; read-only for tracked files. Go toolchain is **not installed**
on this host (`go` not on PATH), so no test run — everything below is code reading, tests cited only
as corroboration. All paths relative to `backend/`.

Note on file names: the task brief mentioned `openai_fast_policy*.go`; in this tree there is **no**
non-test implementation file with that name. All fast-policy/forced-tier code lives in
`internal/service/openai_gateway_request_body.go`; tests are `openai_fast_policy_test.go`,
`openai_fast_policy_ws_test.go`, `openai_fast_service_tier_test.go`, `openai_oauth_passthrough_test.go`.

---

## 1. End-to-end trace (group field → outbound body)

### 1.1 Admin write path

| step | location |
|---|---|
| Create/Update request fields (`forced_service_tier`, legacy `force_openai_fast`) | `internal/handler/admin/group_handler.go:268-269`, `:347-348`; merge of legacy bool → tier at `:186-212` (create `:752`, update `:900`) |
| Service-side sanitize/normalize | `internal/service/admin_group.go:363-374` `sanitizeGroupOpenAIFast` (clears the field when platform ∉ {openai, composite}); `:376-389` `normalizeGroupForcedServiceTierValue` (`""|default|priority|fast|ultrafast`, unknown → "") |
| Create persists | `admin_group.go:639`; update overlay `:1025-1027`; sanitize call `:1080-1081`; repo write `internal/repository/group_repo.go:139`, `:324` |
| DB column | `migrations/246_group_forced_service_tier.sql` (backfills `force_openai_fast = true` → `'priority'`); the migrations runner keys `schema_migrations` by **filename**, so the duplicate `246_` prefixes are safe (`internal/repository/migrations_runner.go:201`) |
| Admin API DTO (response) | `internal/handler/dto/types.go:173-176`; mapper `dto/mappers.go:151-152` (`ForceOpenAIFast` is derived: tier == "priority") |

### 1.2 Read path → auth cache snapshot v25 → middleware

| step | location |
|---|---|
| Repo projection includes the column | `internal/repository/api_key_repo.go:213` (`GetByKeyForAuth` explicit Select), `:1027` (`groupEntityToService`; `Hydrated: true` set at `:988`) |
| Auth snapshot struct | `internal/service/api_key_auth_cache.go:106-108` (`forced_service_tier`, `free_openai_ultrafast`); version constant `internal/service/api_key_auth_cache_impl.go:17` (`apiKeyAuthSnapshotVersion = 25`) |
| Snapshot build / materialize | `api_key_auth_cache_impl.go:421` (build), `:489` (`Hydrated: true`), `:524` (materialize) |
| Stale-version behavior | `api_key_auth_cache_impl.go:328-330`: any snapshot whose `Version != 25` is **discarded as a cache miss** (DB re-read). So a pre-deploy v24 snapshot cannot serve a stale empty tier; it only costs one DB read. Group updates invalidate by group id: `admin_group.go:1100-1102` → `internal/service/api_key_auth_cache_invalidate.go:26-36` |
| Middleware → request context | `internal/server/middleware/api_key_auth.go:166-167` (user id), `:183` (simple mode), `:281` (normal), `:383-392` `setGroupContext` (skips invalid group; `ctxkey.Group`) |

`IsGroupContextValid` (`internal/service/group.go:227-242`) requires non-nil, `ID > 0`,
`Hydrated == true`, `Platform != ""`, `Status != ""`.

### 1.3 Service-side resolution

`internal/service/openai_gateway_request_body.go:2046-2068` `openAIGroupForcedServiceTier(ctx, account, model)`:

1. `ctx == nil || account == nil || account.Platform != PlatformOpenAI` → `""` (`:2051`)
2. `group := ctx.Value(ctxkey.Group).(*Group)` (`:2054`); `!IsGroupContextValid(group) || !groupSupportsOpenAIFast(group.Platform)` → `""` (`:2055`)
   - `groupSupportsOpenAIFast` = platform openai **or** composite (`admin_group.go:359-361`)
3. `tier := normalizedOpenAIServiceTierValue(group.ForcedServiceTier)` (`:2058`)
   - `priority` | `default` → returned as-is (`:2060-2061`)
   - `ultrafast` → returned **only if** `openAIModelSupportsForcedUltrafast(model)` (`:2062-2065`)
4. everything else (including `""`, `flex`, unknown) → `""`

`openAIModelSupportsForcedUltrafast` (`:2070-2077`):
`isOpenAIGPT6AstraModel(model) || openai.IsGPT61SolModelSpelling(model) || getNormalizedCodexModel(model) == "gpt-5.6-sol"`.

### 1.4 HTTP `/v1/responses` (native, non-passthrough)

Entry: `internal/handler/openai_gateway_handler.go` handler validates the client tier
(`:505` → `ValidateOpenAIServiceTierField`, `openai_gateway_request_body.go:1871-1888`), builds
`attemptBody`, then calls `h.gatewayService.Forward(c.Request.Context(), c, account, attemptBody)`
(`openai_gateway_handler.go:816`). `c.Request.Context()` carries `ctxkey.Group` from the middleware:
there is no other group injection on this path (`ensureCompositeTargetPlatform` only writes a
*resolved target platform* key, `internal/handler/composite_platform.go:13-23`).

Inside `Forward` (`internal/service/openai_gateway_forward.go:21`), the only tier rewrite sits at
`:653-687`, **before** the body is serialized/built:

```
:653  rawTier := requestView.ServiceTier                       // body snapshot (openaiRequestView, :1051-1124 in request_body.go)
:654  forcedTier := openAIGroupForcedServiceTier(ctx, account, upstreamModel)
:655  if forcedTier != "" {
:656      rawTier = forcedTier
:657      if requestView.ServiceTier != forcedTier { markPatchSet("service_tier", forcedTier) }
:658  }
:661  if rawTier != "" { ... evaluateOpenAIFastPolicy(...) ... }   // global policy, see §3
:685  else if shouldForceOpenAIFastPriorityForMissingTier(...) { markPatchSet("service_tier","priority") }
```

`upstreamModel` at that point is the mapped/upstream slug: `resolveOpenAIForwardMappedModels`
(`:379`), compact fallback override (`:380-384`), Codex transform override (`:567-569`).

Patches are applied to the body at `:712-733` (or the decoded map is mutated when patch mode was
disabled); `buildUpstreamRequest` is invoked at `:1050` and sets the OAuth routing hint from the
**final body**: `setOpenAICodexRoutingHintFromBody(req.Header, account, body)` at `:1536`.

Result/billing tier is extracted from the final body: `:1218` `serviceTier :=
extractOpenAIServiceTierFromBody(body)`, `:1326-1327` (`UpstreamResponseServiceTier` +
`ServiceTier`).

### 1.5 Passthrough HTTP path

`Forward` returns early to `forwardOpenAIPassthrough` when `account.IsOpenAIPassthroughEnabled()`
(`openai_gateway_forward.go:261-291`). That path applies the **same helper**:
`internal/service/openai_gateway_passthrough.go:267` `applyOpenAIFastPolicyToBody(ctx, account, policyModel, body)`,
then builds the request unchanged (`buildUpstreamRequestOpenAIPassthrough`, `:582-748`, hint at `:741`).

### 1.6 Non-`Forward` HTTP entries (all funnel through the helper, which contains the same forced step)

`applyOpenAIFastPolicyToBody` (`openai_gateway_request_body.go:2095-2153`):
forced write `:2099-2106` → missing-tier rule `:2107-2116` → global policy `:2118-2152`.

Call sites:
- passthrough `openai_gateway_passthrough.go:267`
- native `/v1/chat/completions` `openai_gateway_chat_completions.go:319` (then builder `:344`)
- raw chat-completions upstream `openai_gateway_chat_completions_raw.go:118`
- `/v1/messages` bridge `openai_gateway_messages.go:321`
- responses→chat fallback `openai_gateway_responses_chat_fallback.go:95`
- grok chat bridge `openai_gateway_grok_chat_bridge.go:583`

### 1.7 HTTP request routed to an upstream WebSocket (ws_v2)

`Forward` applies patches/tier as above, then at `openai_gateway_forward.go:791-909` calls
`forwardOpenAIWSV2(ctx, c, account, wsReqBody, ...)` with the policy-applied body
(`wsReqBody` from `ensureReqBody()` at `:793`, which applies the pending patches).
`openai_ws_forwarder_v2.go:65` builds the `response.create` payload from that map; the handshake
routing hint is built from the **payload**: `:154-165` passes `payload["model"]`,
`payload["service_tier"]`, and `openai_ws_forwarder_payload.go:207` synthesizes
`x-codex-routing-hint`.

### 1.8 Client WebSocket `/responses` (client→gateway WS)

Entry `internal/handler/openai_gateway_handler.go:2426` `ResponsesWebSocket`; group was set on
`c.Request.Context()` by the middleware, the ingress lease ctx is `context.WithCancelCause(ctx)`
(values preserved, `internal/service/concurrency_service.go:282`), and
`ProxyResponsesWebSocketFromClient(ctx, ...)` is called with it (`:3237`).

- ctx_pool/shared/dedicated: every client frame goes through `parseClientPayload`
  (`internal/service/openai_ws_forwarder_ingress.go:231`), forced+policy at **`:442`**
  (`applyOpenAIFastPolicyToWSResponseCreate(ctx, account, upstreamModel, normalized)`).
  Later turns: `:767` / `:1914` call the same closure; per-turn routing hint is rebuilt from the
  policy-applied payload at `:1918-1942`.
- passthrough: `proxyResponsesWebSocketV2Passthrough` (`openai_ws_v2_passthrough_adapter.go:666`);
  first frame policy at **`:773`**, every subsequent frame at **`:1105`**; handshake headers built
  once at `:844-856` from `firstClientMessage` model/tier.
- HTTP-bridge mode: `prepareOpenAIWSHTTPBridgeBody` (`openai_ws_http_bridge.go:245-259`) does no
  tier work, but the frame it receives is already the policy output of `parseClientPayload`
  (`openai_ws_forwarder_ingress.go:442`); the builder is
  `buildUpstreamRequestOpenAIPassthrough` (`:502-521`).

WS helper: `applyOpenAIFastPolicyToWSResponseCreate` (`openai_gateway_request_body.go:2204-2279`):
only frames with top-level `type == "response.create"` are inspected (`:2216-2225`); invalid JSON is
passed through (`:2213`); forced write `:2226-2233`; missing-tier rule `:2234-2243`; global policy
`:2245-2278`.

---

## 2. Ordered predicates that can skip/override the forced tier

Ordered by position in the request path. "Skip" = forced tier never written; "override" = written
then changed/removed by something later.

| # | predicate | where | effect | pinning test |
|---|---|---|---|---|
| P1 | request body empty (HTTP) | `request_body.go:2096`; not applicable to `Forward` (body always non-empty for a response request) | skip | — |
| P2 | account is BPS → `Forward` returns `forwardOpenAIBPS` **before** the tier block | `openai_gateway_forward.go:22-24`; BPS also rejects `service_tier` other than auto/default: `openai_bps_protocol.go:156-162` | skip (and a client-sent priority would 400) | `openai_bps_test.go` (no forced-tier case) |
| P3 | account platform is Grok → `forwardGrokResponses` before the tier block | `forward.go:162-164` | skip | `openai_fast_policy_test.go:422-437` (helper-level: forced composite group + grok account ⇒ body untouched) |
| P4 | upstream protocol resolves to Anthropic / ChatCompletions → different function, policy applied there with the same helper | `forward.go:190-198`; `openai_gateway_messages.go:321`, `openai_gateway_responses_chat_fallback.go:95` | applied, not skipped (but a different model argument is used) | `openai_gateway_messages_chat_fallback_test.go`, `openai_gateway_responses_chat_fallback_test.go` (tier echo cases only) |
| P5 | account nil / **platform != openai** | `request_body.go:2051` | skip | `openai_fast_policy_test.go:422-437` |
| P6 | `ctxkey.Group` missing (any caller that does not pass the authenticated request ctx) | `request_body.go:2054` | skip | implicit; all e2e tests inject group manually |
| P7 | group invalid (`ID<=0`, `Hydrated=false`, empty Platform/Status) | `group.go:227-242`; `request_body.go:2055` | skip | `openai_fast_policy_test.go:409-420` (no Hydrated ⇒ no write) |
| P8 | group platform not openai/composite | `admin_group.go:359-374` (cleared on save), `request_body.go:2055` at runtime | skip | `openai_fast_policy_test.go:439-454` (anthropic group) |
| P9 | stored tier not one of `default|priority|fast|ultrafast` (e.g. legacy rows written before validation, direct DB edit, DTO bypass) | `admin_group.go:378-389`; runtime normalize `request_body.go:2058`; `normalizeOpenAIServiceTier` `:1819-1837` | skip | table `openai_fast_policy_test.go:360` ("follow keeps client tier"); admin normalize `admin_service_group_test.go:1354-1360` |
| P10 | forced `ultrafast` + model **not** in allowlist (see §5) | `request_body.go:2062-2065`, `:2070-2077` | skip (client tier kept) | `openai_fast_policy_test.go:358-359`; WS `openai_fast_policy_ws_test.go:137` |
| P11 | **global Fast/Flex policy** evaluated after the forced write: `filter` removes it, `block` 403s, `force_priority` rewrites it (even over a forced `default`); a rule scoped `oauth`/`apikey`/`bedrock` or model-whitelisted can do this asymmetrically | HTTP `forward.go:661-684` + helper `:2118-2152`; WS `:2245-2278` | override | `openai_fast_policy_test.go:396-407` (filter wins over forced priority); `:480-498` (`all`+filter strips); `openai_oauth_passthrough_test.go:1056-1087` (e2e Forward: forced priority + priority filter ⇒ field absent); WS block `openai_fast_policy_ws_test.go:766-...` |
| P12 | global `missing` + `force_priority` rule cannot fire when the group already forced a tier (forced write makes `service_tier` non-empty; the missing branch is `else if`) | `forward.go:661/685`, helper `:2107-2116` | no double-apply; forced `default` also suppresses it | `openai_fast_policy_test.go:232-263` |
| P13 | WS: frame `type != "response.create"` (or invalid JSON) → untouched | `request_body.go:2213-2225` | skip for that frame | `openai_fast_policy_ws_test.go:183-195`, `:255-295` |
| P14 | WS passthrough: `model` for the policy is the **client-side** model (`openAIWSPassthroughPolicyModelForFrame`, adapter `:92-101`), falling back to `capturedSessionModel` (`:1098-1104`); mapping happens separately (`:1069-1077`). A client alias not matching the ultrafast allowlist skips forced ultrafast. | adapter `:773`, `:1105` | skip (ultrafast only) | `openai_fast_policy_ws_test.go:379-470` (session-model fallback), `:1041-1130` (per-turn model/tier) |
| P15 | WS long session: policy/forced tier read the **connect-time** group; a group tier changed mid-connection only takes effect on reconnect. Policy settings are also snapshotted per session (`withOpenAIFastPolicyContext`, `request_body.go:2016-2034`; called `openai_ws_forwarder_ingress.go:99-103`). The per-turn billing group refresh (`openai_gateway_handler.go:2231-2240`) only swaps a **same-ID** group loaded with full columns (`api_key_repo.go:111-127` + `groupEntityToService`), so it does not change the tier value. | `openai_gateway_handler.go:2176-2240`; ingress `:96-103` | stale-by-design | comments `request_body.go:2016-2022`; no test |
| P16 | Admin save from a platform ≠ openai/composite silently clears the tier (`sanitizeGroupOpenAIFast`) — an admin changing platform later loses the setting | `admin_group.go:367-371`, `:1080-1081` | skip after next read | `admin_service_group_test.go:1367-1378` (`updated.ForcedServiceTier` empty when not openai/composite) |
| P17 | Legacy compatibility: update payload that carries only `force_openai_fast=false` clears the tier; create with `forced_service_tier=""` + legacy true → priority | `handler/admin/group_handler.go:186-212` | override at write time | `handler/admin/group_handler_openai_fast_test.go:10-36` |
| P18 | Auth cache version mismatch does **not** produce stale values: version ≠ 25 is a miss and re-reads (`impl.go:328-330`); group update invalidates the cache (`admin_group.go:1100-1102`) | — | no stale tier | `api_key_auth_cache_openai_fast_test.go:10-35`; `repository/api_key_repo_openai_fast_projection_integration_test.go:18-44` |

Fragility worth flagging (not currently reachable, but it is the one code shape that could silently
drop the forced write): `markPatchSet` is a no-op when `requestView.patchesDisabled && reqBody == nil`
(`openai_gateway_forward.go:318-327`). Today every `markDecodedModified()` before line 653 is
preceded by `ensureReqBody()`, so `reqBody != nil`; a future reorder that disables patches after a
`reqBody = nil` reset would silently lose `service_tier`.

---

## 3. Ordering: forced rewrite vs global Fast/Flex policy

All three integration points do **forced write first, global policy second**:

- HTTP `Forward`: `forward.go:653-660` → `:661-684`
- body helper: `request_body.go:2099-2106` → `:2107-2152`
- WS helper: `request_body.go:2226-2233` → `:2234-2278`

That matches the commit message of `ea23ccfed` ("rewrite service_tier before the global Fast/Flex
policy"). **No inverted ordering bug in the current code.**

But the consequence is that the group value is *not* authoritative: the global policy can strip
(`BetaPolicyActionFilter`), reject (`BetaPolicyActionBlock`), or rewrite to `priority`
(`OpenAIFastPolicyActionForcePriority`) whatever the group forced; and the *default* policy has no
rules (`settings_view.go:728-735`), so out of the box it passes. An admin who had configured
`service_tier=priority → filter/block` (a natural "stop users buying fast" rule) neutralizes
"forced fast" with no error and no log — a silent override, tested deliberately in
`openai_fast_policy_test.go:396-407` and `openai_oauth_passthrough_test.go:1056-1087`.

Related ordering facts:
- forced tier set to `default` counts as an *explicit* tier: it blocks the `missing`+`force_priority`
  path (`forward.go:685` is an `else if`).
- If the forced tier is `default`, it is written literally as `"default"`; the OAuth routing hint
  deliberately drops `default` (see §4) — semantic, not a conflict.
- Policy context (`openai_fast_policy_ctx`) is fetched once per WS session before any frame
  (`openai_ws_forwarder_ingress.go:99-103`), so in-session admin changes do not affect a live WS.

---

## 4. Outbound signals: body `service_tier` vs headers / `client_metadata`

What the code writes when forcing:
- **body (or WS `response.create` frame): `"service_tier": "priority" | "default" | "ultrafast"`**
  (`request_body.go:2101`, `:2110`, `:2137`; WS `:2228`, `:2237`, `:2264`).
- For OAuth-like accounts only: **`x-codex-routing-hint: model=<slug>[;tier=priority|flex|ultrafast]`**
  (`internal/service/openai_routing_hint.go:19-60`; synthesized from the final body at
  `openai_gateway_forward.go:1536` / passthrough `:741`; for upstream-WS at
  `openai_ws_forwarder_v2.go:154-165` + `openai_ws_forwarder_payload.go:207`; per turn in WS ingress
  at `openai_ws_forwarder_ingress.go:1918-1942`). `default`/auto/scale/missing are collapsed to a
  model-only hint (`openai_routing_hint.go:42-50`).
- `client_metadata` / `x-codex-turn-metadata`: **no tier is ever written there.** Greps found no
  writer that puts `service_tier` into `client_metadata`; the Codex fingerprint code only rewrites
  identity/session/turn fields (`openai_codex_fingerprint.go:390-409`, `:584-599`). So body vs
  client_metadata cannot disagree about the tier — there is only one tier channel in the body.

Disagreement analysis:
- HTTP and HTTP→upstream-WS: the hint is derived from the already-policy-applied body/payload, so
  header and body agree by construction.
- Client WS ctx_pool ingress: per-turn hint is rebuilt from the policy output
  (`openai_ws_forwarder_ingress.go:1918/1942`); only used on (re)dial, and rebuilt each turn.
- Client WS **passthrough**: the handshake hint is built **once** from the first frame
  (`openai_ws_v2_passthrough_adapter.go:844-856`), while later `response.create` frames are rewritten
  independently (`:1105`). If the client changes tier mid-session *and* the group does not force a
  tier, the connection's routing hint can be stale relative to a later frame. With a forced tier the
  tier is constant, so no disagreement. Also, routing hint participates in WS-pool affinity
  (`openai_ws_pool.go:2407-2432`), so a stale hint can also select a connection bucketed for the old
  hint — impact unverified.
- Upstream echo: `UpstreamResponseServiceTier` is the upstream-declared tier, recorded separately
  from the outbound `ServiceTier` (`internal/service/openai_gateway_usage.go:397-430` logs
  `openai_service_tier_echo_mismatch`). A code comment states the private ChatGPT Codex backend
  commonly reports `default` even for effective Fast turns
  (`internal/service/upstream_response_model.go:220-227`). So "upstream echo = default" does **not**
  prove the outbound rewrite failed; it is the expected echo for OAuth Codex. The admin UI row added
  by `97efa08e2`/`c6c626e6b` displays this echo and can be misread as "forcing no longer works".

---

## 5. Which models "accept" priority vs ultrafast (gateway-side)

- **priority (fast)**: `openAIGroupForcedServiceTier` has **no model gate** — any model on an
  OpenAI-platform account gets it (`request_body.go:2060-2061`). Model list is not consulted.
  The model catalog the gateway advertises to Codex *does* gate "Fast" to families
  `gpt-5.4`, `gpt-5.5`, `gpt-5.6*` and GPT-6 models (`openai_codex_models_service.go:569-578`,
  `isOpenAIGPT6Model`), i.e. the client UI won't offer Fast for other models, but the forced rewrite
  itself does not care.
- **ultrafast**: forced only for (`request_body.go:2070-2077`):
  - `isOpenAIGPT6AstraModel`: canonicalized `gpt-6`, `gpt-6-astra`, or any `gpt-6-astra-*`
    (`openai_model_alias.go:112-117`)
  - `openai.IsGPT61SolModelSpelling`: `gpt-6.1-sol` or `gpt-6.1-sol-<none|minimal|low|medium|high|xhigh|max|openai-compact>`
    (`internal/pkg/openai/constants.go:200-217`)
  - `getNormalizedCodexModel(model) == "gpt-5.6-sol"`: **exact** lookup in the static map
    (`openai_codex_transform.go:13-60`, `:1279-1288`; key = lower-cased last path segment) — so
    `gpt-5.6-sol-high`, `gpt-5.6-sol-openai-compact`, `gpt-5.6-sol-2026-…` and `-wm` style suffixes
    **do not** match, while the analogous `gpt-6.1-sol-*` / `gpt-6-astra-*` spellings do. This
    asymmetry is the most plausible way a forced *ultrafast* silently degrades to the client tier.
  Advertised to the client: ultrafast is only added to the Codex `/models` manifest for
  `gpt-5.6-sol` (`openai_codex_models_service.go:580-582`) and for API-key Astra on the official
  OpenAI base URL (`openai_codex_model_metadata.go:100-110`) — `gpt-6-astra` on OAuth and
  `gpt-6.1-sol` are forceable but not advertised.
- Cross-check with runtime teammate's notes (`scratch/notes-runtime.md`, untrusted data): the local
  Codex client's catalog default is `gpt-6.1-sol-wm` and it upgrades `gpt-5.6-sol-wm → gpt-6.1-sol-wm`;
  `gpt-6.1-sol-wm` does **not** match `IsGPT61SolModelSpelling`, and `gpt-5.6-sol-wm` does not match
  the exact map. Those notes also say the client used `responses_websocket` during the priority
  window, i.e. the WS ingress path in §1.8 is the one that matters for this user.

---

## 6. Assessment of "forcing fast no longer takes effect upstream"

Source-side, the forced priority write is present on every path I traced and is covered by
end-to-end Forward tests (`openai_oauth_passthrough_test.go:477-507` passthrough,
`:1022-1054` legacy OAuth HTTP, `:1056-1087` filter override). Ranked candidate explanations:

1. **Global Fast/Flex policy override** (filter/block/force on `priority`) — silent, intended by
   this design, no log when it fires; most common way a forced tier disappears.
2. **Not the same group/account as the admin thinks**: composite routing to BPS/grok skips forcing;
   non-openai platform account skips; group platform changed away from openai/composite clears the
   saved value; legacy `force_openai_fast=false` write clears it; stale WS session keeps connect-time
   group.
3. **Upstream echo misread**: the new `upstream_service_tier` row/log can show `default` for OAuth
   Codex even when `priority` was sent (documented expectation), which looks like the fix "stopped
   working".
4. **Ultrafast only**: model spellings outside the narrow allowlist (esp. `gpt-5.6-sol-*`/`-wm`)
   silently keep the client tier.
5. **Fragile patch path** (P-note above) — not reachable today.

Not verifiable from this repo / this session:
- Whether the ChatGPT Codex backend actually honors body `service_tier` for fast turns (echo is not
  proof either way; code says it often echoes `default`). REA on the client + gateway logs are needed.
- Whether WS-pool routing affinity keyed on the handshake hint measurably affects tier.
- I could not run `go test` (no Go toolchain), so no dynamic confirmation of any of the above.

## 7. Test map (corroboration only)

| behavior | test |
|---|---|
| forced tier modes (default/priority/ultrafast), ultrafast allowlist | `openai_fast_policy_test.go:342-375`; WS `openai_fast_policy_ws_test.go:125-149` |
| forced tier injects + overrides client tier (`fast`/`default`/unknown) | `openai_fast_policy_test.go:377-394`; WS `:151-163` |
| global policy wins over forced tier | `openai_fast_policy_test.go:396-407`; e2e `openai_oauth_passthrough_test.go:1056-1087` |
| Hydrated required | `openai_fast_policy_test.go:409-420` |
| only OpenAI-platform accounts | `:422-437` |
| group platform must be openai/composite | `:439-454` |
| e2e forced priority (passthrough) | `openai_oauth_passthrough_test.go:477-507` |
| e2e forced priority (legacy OAuth HTTP) | `:1022-1054` |
| WS `response.create` only, other frames untouched | `openai_fast_policy_ws_test.go:183-195`, `:255-295` |
| WS block/filter/normalize/non-string passthrough | `openai_fast_policy_ws_test.go:766-...`, `:918-...`, `:1008-1035` |
| WS per-turn tier/model semantics | `openai_fast_policy_ws_test.go:1041-1130` |
| middleware→Forward fast policy plumbing (global policy; **no group-forced-tier case**) | `internal/server/middleware/openai_fast_policy_forwarding_test.go:21-132` |
| compact path preserves tier + routing hint | `openai_compact_service_tier_test.go:15-99` |
| auth snapshot carries tier; repo projection carries tier | `api_key_auth_cache_openai_fast_test.go:10-35`; `internal/repository/api_key_repo_openai_fast_projection_integration_test.go:18-44` |
| admin create/update persists/clears tier | `admin_service_group_test.go:1345-1399`; `handler/admin/group_handler_openai_fast_test.go:10-36`; `handler/dto/group_mapper_openai_fast_test.go:10-23` |
| migration backfill | `migrations/group_forced_service_tier_migration_test.go:10-19` |
| echo/outbound tier separation | `openai_fast_service_tier_test.go:555-645`; `openai_gateway_usage.go:417-430` |
