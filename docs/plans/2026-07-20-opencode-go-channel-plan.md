# OpenCode Go Channel Implementation Plan

**Goal:** Add a new relay channel `OpenCode Go` (type 59) that proxies chat requests to the opencode-go subscription endpoint `https://opencode.ai/zen/go/v1`, routing per-model between its two upstream protocols (OpenAI Chat Completions and Anthropic Messages).

**Inputs:** Analysis of the opencode source tree (`/home/julian/workspace/source/opencode`, esp. `packages/opencode/test/tool/fixtures/models-api.json` lines 33606+ and `packages/core/src/session/runner/model.ts`) and of new-api channel/adaptor architecture (`relay/channel/`, `constant/channel.go`, `relay/relay_adaptor.go`).

**Assumptions:**

- Auth is a plain API key (the `OPENCODE_API_KEY` from the opencode.ai console). Verified in opencode source: only the main `opencode` provider has an OAuth device flow; `opencode-go` is API-key-only. No OAuth/refresh logic is needed.
- The per-model protocol mapping is static for v1 (hardcoded set, mirroring the opencode remote config fixture). Opencode maintains it remotely; new-api cannot fetch it at runtime.
- Anthropic-protocol models are assumed to reject `/chat/completions` upstream (opencode's client routes them to `/messages` exclusively), so per-model routing is required rather than client-format routing.
- Out of scope for v1: OpenAI Responses API (`/v1/responses`), Gemini-format client requests, embeddings, images, audio, rerank, async tasks. The fixture shows chat-only models; these return `not implemented` errors, matching the moonshot adaptor precedent.
- No default entries in `setting/ratio_setting/model_ratio.go`: upstream is a flat-fee subscription, and unknown models fall back to the global default ratio. Admins price per their own policy. (Verified: none of the 19 model IDs collide with existing ratio entries.)

**Architecture:**

One new adaptor package `relay/channel/opencodego` implementing `channel.Adaptor`. For each request it resolves the upstream protocol from the mapped upstream model name (`info.UpstreamModelName`, falling back to the request model) against a hardcoded Anthropic-protocol model set, then delegates URL building, header setup, request conversion, and response handling to the existing `openai.Adaptor` or `claude.Adaptor`. This mirrors the moonshot/deepseek delegation pattern, except those route by client `RelayFormat` while opencode-go routes by model protocol. Response conversion back to the client's format is already handled by the delegated handlers (`info.FinalRequestRelayFormat` + `relayconvert.ConvertResponse`, see `relay/channel/openai/relay-openai.go:291` and `relay/channel/claude/adaptor.go:125`).

Upstream facts (verified against opencode source):

- Upstream endpoint prefix: `https://opencode.ai/zen/go/v1`
- Channel stored default base (platform convention, no `/v1`): `https://opencode.ai/zen/go` — adaptor and model-fetch append `/v1`
- OpenAI protocol: `POST {storedBase}/v1/chat/completions`, header `Authorization: Bearer <key>`, SSE stream, supports `stream_options.include_usage`
- Anthropic protocol: `POST {storedBase}/v1/messages`, headers `x-api-key: <key>` + `anthropic-version`, SSE stream
- Model list: `GET {storedBase}/v1/models` with Bearer auth (generic new-api model-fetch path; no channel-specific case)
- Client headers the opencode TUI sends to zen/go (`packages/opencode/src/session/llm/request.ts:185-204`, consumed by the zen server `packages/console/app/src/routes/zen/util/handler.ts:108-111`):
  - `x-opencode-session` — FUNCTIONAL: drives `stickyId` for sticky upstream-provider selection (`handler.ts:151`), i.e. same session → same provider → better prompt-cache locality. Server fallback when absent: workspaceID (per API key), then client IP.
  - `x-opencode-request`, `x-opencode-client`, `x-opencode-project` — observability only (server `logger.metric`); the server strips all four `x-opencode-*` headers before forwarding to the real provider (`handler.ts:238-241`).
  - `User-Agent: opencode/<version>` — metrics only. All four headers are optional server-side (`?? ""`).
- Anthropic-protocol models: `minimax-m2.5`, `minimax-m2.7`, `minimax-m3`, `qwen3.5-plus`, `qwen3.6-plus`, `qwen3.7-plus`, `qwen3.7-max`
- OpenAI-protocol models: `deepseek-v4-flash`, `deepseek-v4-pro`, `glm-5`, `glm-5.1`, `glm-5.2`, `kimi-k2.5`, `kimi-k2.6`, `kimi-k2.7-code`, `mimo-v2-omni`, `mimo-v2-pro`, `mimo-v2.5`, `mimo-v2.5-pro`

**Tech Stack:** Go 1.22+ backend (Gin, existing relay/channel framework), React 19 default frontend (`web/default`, Rsbuild), React 18 classic frontend (`web/classic`).

---

## File Map

- Create: `relay/channel/opencodego/constants.go` — model list, channel name, Anthropic-protocol model set
- Create: `relay/channel/opencodego/adaptor.go` — `Adaptor` implementing `channel.Adaptor` via per-model delegation
- Create: `relay/channel/opencodego/adaptor_test.go` — protocol routing, auth headers, conversion delegation tests
- Modify: `constant/channel.go` — `ChannelTypeOpenCodeGo = 59`, `ChannelBaseURLs` entry, `ChannelTypeNames[59]`
- Modify: `constant/api_type.go` — `APITypeOpenCodeGo` before `APITypeDummy`
- Modify: `common/api_type.go` — `ChannelTypeOpenCodeGo` → `APITypeOpenCodeGo` mapping case
- Modify: `relay/relay_adaptor.go` — import + `GetAdaptor` case returning `&opencodego.Adaptor{}`
- Modify: `relay/common/relay_info.go` — add type 59 to `streamSupportedChannels` (line ~345)
- Modify: `web/default/src/features/channels/constants.ts` — `CHANNEL_TYPES[59]`, `CHANNEL_TYPE_DISPLAY_ORDER`, `MODEL_FETCHABLE_TYPES`
- Modify: `web/default/src/features/channels/lib/channel-type-config.ts` — `CHANNEL_TYPE_CONFIGS[59]` entry
- Modify: `web/classic/src/constants/channel.constants.js` — channel option `{ value: 59, label: 'OpenCode Go' }`

## Tasks

### Task 1: Backend channel type registration

**Outcome:** Type 59 exists end-to-end in backend constants and the adaptor factory; project compiles.

**Files:**

- Modify: `constant/channel.go`
- Modify: `constant/api_type.go`
- Modify: `common/api_type.go`
- Modify: `relay/relay_adaptor.go`
- Modify: `relay/common/relay_info.go`

**Steps:**

- [ ] `constant/channel.go`: add `ChannelTypeOpenCodeGo = 59` after `ChannelTypeAdvancedCustom = 58` (keep `ChannelTypeDummy` last — it is a count sentinel, line 59). Add `"OpenCode Go"` to `ChannelTypeNames` (map at line ~147). Append `https://opencode.ai/zen/go` (no `/v1`) to `ChannelBaseURLs` at index 59 (array at line ~68; verify length/index alignment — it is indexed by type ID). Aligns with other OpenAI-style channels whose stored base omits `/v1`.
- [ ] `constant/api_type.go`: add `APITypeOpenCodeGo` in the iota block immediately before `APITypeDummy` (line ~40).
- [ ] `common/api_type.go`: add `case constant.ChannelTypeOpenCodeGo: return constant.APITypeOpenCodeGo` to the `ChannelType2APIType` switch.
- [ ] `relay/common/relay_info.go`: add `constant.ChannelTypeOpenCodeGo: true` to `streamSupportedChannels` (map at line ~345). Rationale: the OpenAI-protocol endpoint supports `stream_options.include_usage`; this flag lets new-api auto-inject it. The Claude-protocol path reports usage natively per-event.
- [ ] `relay/relay_adaptor.go`: import `relay/channel/opencodego`, add `case constant.APITypeOpenCodeGo: return &opencodego.Adaptor{}` to `GetAdaptor` (switch at lines 54-100). This file will not compile until Task 2 lands; implement Tasks 1-2 together before building.

**Validation:**

- Run: `go build ./...`
- Expected: compiles (after Task 2 package exists).

### Task 2: `opencodego` adaptor package

**Outcome:** The adaptor relays OpenAI-format and Claude-format chat requests to the correct upstream protocol per model, with correct auth headers, streaming and non-streaming responses, and usage/billing intact.

**Files:**

- Create: `relay/channel/opencodego/constants.go`
- Create: `relay/channel/opencodego/adaptor.go`

**Steps:**

- [ ] Every new Go file starts with the required 2-line `ABOUTME:` header comment (per project convention).
- [ ] `constants.go`:
  - `ChannelName = "opencodego"`
  - `ModelList` = the 19 fixture model IDs listed in the header (order: group by vendor, e.g. deepseek, glm, kimi, mimo, minimax, qwen).
  - `anthropicModels` = `map[string]bool` with the 7 Anthropic-protocol model IDs. Name the constant/set clearly, e.g. `anthropicProtocolModels`.
  - Helper `isAnthropicProtocolModel(model string) bool` — exact match against the set.
- [ ] `adaptor.go` — `type Adaptor struct{}` implementing all `channel.Adaptor` methods (`relay/channel/adapter.go:14-38`):
  - Model resolution helper: prefer `info.UpstreamModelName` when `info.ChannelMeta != nil && info.UpstreamModelName != ""`, else fall back to the request's model field — copy the pattern from `moonshot.getUpstreamModelName` (`relay/channel/moonshot/adaptor.go:93-99`).
  - `Init(info)`: no-op (matches moonshot/deepseek).
  - `GetRequestURL(info)`: if the resolved model is Anthropic-protocol → `{info.ChannelBaseUrl}/v1/messages`; else → `{info.ChannelBaseUrl}/v1/chat/completions`. (`info.ChannelBaseUrl` defaults to `https://opencode.ai/zen/go` via `ChannelBaseURLs` — no `/v1`; the adaptor appends `/v1` like other OpenAI-style channels. Frontend/admin only enter the host path without `/v1`.)
  - `SetupRequestHeader(c, req, info)`: call `channel.SetupApiRequestHeader(info, c, req)`; then branch by resolved model protocol:
    - Anthropic: `req.Set("x-api-key", info.ApiKey)`; set `anthropic-version` from the incoming client header, defaulting to `2023-06-01`; call `claude.CommonClaudeHeadersOperation(c, req, info)` for `anthropic-beta` passthrough (mirrors `claude/adaptor.go:84-94`).
    - OpenAI: `req.Set("Authorization", "Bearer "+info.ApiKey)`.
  - User-Agent (both protocols): passthrough of the incoming client header only — if `c.Request.Header.Get("User-Agent")` is non-empty, `req.Set("User-Agent", ...)` with that value; if empty, set nothing (do not impersonate `opencode/<version>`, do not inject a `new-api/*` UA).
  - Header override/passthrough needs no adaptor work: the channel-level `header_override` setting (`Channel.HeaderOverride`, JSON with `{api_key}` / `{client_header:<name>}` placeholders, `*` and `re:`/`regex:` passthrough rules) is applied by `processHeaderOverride` inside the shared HTTP layer (`relay/channel/api_request.go:190`, called from the `DoApiRequest` family AFTER `SetupRequestHeader`, `api_request.go:319-325`), and explicit overrides win over passthrough and over adaptor-set headers. Using `channel.DoApiRequest` inherits it automatically, and the drawer's `header_override` field is type-agnostic (`channel-mutate-drawer.tsx:3945`) — no frontend change. Note the skip list (`api_request.go:67+`) never wildcard-passes credential headers (`authorization`, `x-api-key`), so auth stays adaptor-controlled.
  - Session-affinity header (mirrors the opencode client behavior above): the adaptor carries a per-request `sessionAffinity` string field. Populate it in `ConvertOpenAIRequest` from `request.PromptCacheKey` (`dto/openai_request.go:76`) and in `ConvertClaudeRequest` from `metadata.user_id` (`dto/claude.go:14-15,230`; unmarshal `Metadata` only when non-empty). In `SetupRequestHeader`, resolve with priority: (1) `a.sessionAffinity` from the request body; (2) the incoming client's `x-session-id` header (`c.Request.Header.Get("x-session-id")`) — tried ONLY when (1) is empty; (3) if both empty, set nothing. When resolved non-empty, `req.Set("x-opencode-session", value)` for BOTH protocols. Never fabricate a value (a random per-request ID would defeat stickiness and is worse than the server's workspaceID fallback). The struct field works because the relay flow calls Convert* before `DoApiRequest` on the same adaptor instance (verified in `api_request.go:307-319`). Note the symmetry: the opencode client itself sends `X-Session-Id` when talking to non-opencode providers (`request.ts:197`), so opencode pointed at new-api gets its session affinity forwarded to zen/go automatically. Do NOT send `x-opencode-request` / `x-opencode-client` / `x-opencode-project` — they are metrics-only on the server.
  - `ConvertOpenAIRequest`: Anthropic-protocol → delegate `claude.Adaptor{}.ConvertOpenAIRequest(c, info, request)` (converts to `dto.ClaudeRequest` via relayconvert). OpenAI-protocol → return `request, nil` unchanged.
  - `ConvertClaudeRequest`: Anthropic-protocol → return `request, nil` (native passthrough). OpenAI-protocol → delegate `openai.Adaptor{}.ConvertClaudeRequest(c, info, request)` (converts to OpenAI chat via relayconvert; verified at `openai/adaptor.go:56` and registry converter `anthropic_messages_to_openai_chat_completions`, `service/relayconvert/request_registry.go:76`).
  - `ConvertOpenAIResponsesRequest`, `ConvertGeminiRequest`, `ConvertAudioRequest`, `ConvertImageRequest`, `ConvertEmbeddingRequest`, `ConvertRerankRequest`: return `nil, errors.New("not implemented")` (moonshot precedent). Rationale: opencode-go exposes chat only; see Assumptions.
  - `DoRequest`: `return channel.DoApiRequest(a, c, info, requestBody)`.
  - `DoResponse`: branch by resolved model protocol (NOT `info.RelayFormat` — this is the key difference from moonshot):
    - Anthropic → `claude.Adaptor{}.DoResponse(c, resp, info)` (sets `info.FinalRequestRelayFormat = types.RelayFormatClaude`; its handlers convert to the client format).
    - OpenAI → `openai.Adaptor{}.DoResponse(c, resp, info)` (its handlers convert OpenAI responses/SSE to the client format, incl. Claude, via `relayconvert.ConvertResponse`, `relay-openai.go:291`).
  - `GetModelList` / `GetChannelName`: return `ModelList` / `ChannelName`.
- [ ] Follow AGENTS.md backend rules: JSON via `common.*` wrappers (the adaptor itself should not need any), early returns, no single-use helpers beyond the two documented above (protocol resolution is a durable domain concept used by 5+ methods).

**Validation:**

- Run: `go build ./... && go vet ./relay/channel/opencodego/`
- Expected: clean build, no vet findings.

### Task 3: Backend adaptor tests

**Outcome:** Table-driven tests lock in the per-model routing contract and conversion delegation.

**Files:**

- Create: `relay/channel/opencodego/adaptor_test.go`

**Steps:**

- [ ] Use `testify/require` for setup/fatal, `testify/assert` for value checks (project convention).
- [ ] `GetRequestURL` table test: for each of the 7 Anthropic-protocol models expect `{base}/messages`; for a sample of OpenAI-protocol models (`glm-5.2`, `kimi-k2.6`, `deepseek-v4-pro`) and an unknown model expect `{base}/chat/completions`. Cover `UpstreamModelName` set vs. unset (fallback to request model).
- [ ] `SetupRequestHeader` test: Anthropic model → `x-api-key` present, `anthropic-version` defaults to `2023-06-01`, no `Authorization` header; OpenAI model → `Authorization: Bearer <key>`, no `x-api-key`.
- [ ] Session-affinity tests: `ConvertOpenAIRequest` with `prompt_cache_key: "abc"` then `SetupRequestHeader` → `x-opencode-session: abc` (both protocols); `ConvertClaudeRequest` with `metadata.user_id` set → same header; body fields absent but client sends `x-session-id: sid` → header is `sid`; body fields present AND client sends `x-session-id` → body value wins; neither source present → header absent.
- [ ] User-Agent tests: client sends `User-Agent: my-agent/1.0` → upstream header is `my-agent/1.0` (both protocols); client sends no UA → upstream header unset (empty).
- [ ] `ConvertOpenAIRequest` test: for `minimax-m3` the result is `*dto.ClaudeRequest`; for `glm-5.2` the request passes through unchanged (same pointer).
- [ ] `ConvertClaudeRequest` test: for `minimax-m3` passthrough; for `glm-5.2` the result is `*dto.GeneralOpenAIRequest` with model preserved.
- [ ] Unsupported methods (`ConvertGeminiRequest`, `ConvertAudioRequest`, `ConvertImageRequest`, `ConvertEmbeddingRequest`, `ConvertOpenAIResponsesRequest`) return errors.
- [ ] Do NOT re-test response conversion internals (already covered by `claude`/`openai` adaptor tests); a delegation smoke test asserting `FinalRequestRelayFormat == types.RelayFormatClaude` after Anthropic-path `DoResponse` against an `httptest` server returning a minimal Claude JSON response is sufficient.

**Validation:**

- Run: `go test ./relay/channel/opencodego/... -count=1`
- Expected: all tests pass.

### Task 4: Default frontend (`web/default`) channel UI

**Outcome:** Admins can create/edit an OpenCode Go channel with correct defaults, icon, and the "fetch models" button works against `{base}/models`.

**Files:**

- Modify: `web/default/src/features/channels/constants.ts`
- Modify: `web/default/src/features/channels/lib/channel-type-config.ts`

**Steps:**

- [ ] `constants.ts`: add `59: 'OpenCode Go'` to `CHANNEL_TYPES` (map at lines 24-90); insert `59` into `CHANNEL_TYPE_DISPLAY_ORDER` near other subscription/coding channels (e.g. after 57 Codex); add `59` to `MODEL_FETCHABLE_TYPES` (set at line 382 — backend model fetch hits `{baseURL}/models` with Bearer, which opencode-go supports).
- [ ] `channel-type-config.ts`: add `CHANNEL_TYPE_CONFIGS[59]` with `name: CHANNEL_TYPES[59]`, a fitting existing icon key (check available icons in `channel-utils.ts`; reuse a generic/subscription-style icon if no opencode-specific one exists), `defaultBaseUrl: 'https://opencode.ai/zen/go/v1'`, and `hints.key` / `hints.baseUrl` text noting the key comes from the opencode.ai console (`OPENCODE_API_KEY`).
- [ ] No changes to `channel-mutate-drawer.tsx`: plain API-key channel, no special credential UI needed (unlike Codex). No `TYPE_TO_KEY_PROMPT` entry needed (single key, not a delimited format).
- [ ] If any new user-facing string is introduced in JSX, wrap in `t('...')` and run `bun run i18n:sync` per the i18n skill; prefer reusing existing hint mechanics so no new keys are required.

**Validation:**

- Run: `cd web/default && bun run build && bun run lint`
- Expected: build succeeds, no new lint errors.

### Task 5: Classic frontend (`web/classic`) channel option

**Outcome:** Classic theme also lists OpenCode Go in the channel type dropdown.

**Files:**

- Modify: `web/classic/src/constants/channel.constants.js`

**Steps:**

- [ ] Add `{ key: '59', value: 59, label: 'OpenCode Go', ... }` following the exact shape of the existing Codex entry (line ~188: verify surrounding fields such as `color`, `icon` before writing).

**Validation:**

- Run: `cd web/classic && bun run build`
- Expected: build succeeds.

## Final Validation

- Run: `go build ./... && go test ./relay/... -count=1`
- Expected: backend builds; relay tests pass.
- Run: `cd web/default && bun run build && cd ../classic && bun run build`
- Expected: both frontends build.
- Manual smoke (requires a real `OPENCODE_API_KEY`): create channel type 59, fetch models, then (1) `POST /v1/chat/completions` with `glm-5.2` stream+non-stream, (2) `POST /v1/messages` with `minimax-m3` stream+non-stream, (3) cross-format: `POST /v1/chat/completions` with `minimax-m3` and `POST /v1/messages` with `glm-5.2`. Expect correct answers, correct SSE framing per client format, and usage/quota deduction in logs.

## Failure Behavior

- Unknown/new model ID (not in either hardcoded set): routed to OpenAI Chat Completions by default. If opencode later adds an Anthropic-only model, requests fail with an upstream 4xx until the set is updated — surfaced to the client as a normal upstream error, no silent misbehavior.
- Invalid/expired API key: upstream 401/403 propagated through the standard error path.
- Requests for unsupported modes (embeddings/images/audio/rerank/responses/gemini): explicit `not implemented` error before any upstream call.
- Upstream protocol mismatch (e.g. Anthropic-protocol model removed upstream): standard relay error handling and retry/other-channel logic applies unchanged.

## Privacy and Security

- Credential is a single API key stored in `channel.Key`, same as most existing channels; no OAuth tokens, refresh flow, or extra secrets. Key is only sent to the channel's configured base URL over TLS.
- Admin-configured header overrides can inject arbitrary headers upstream (existing platform feature), but wildcard/regex passthrough cannot leak channel credentials: `authorization` and `x-api-key` are on the passthrough skip list (`relay/channel/api_request.go:67+`).
- No new logging of request/response bodies; billing/usage logs follow the existing pipeline.

## Rollout Notes

- No database migration: channel type is an integer column; GORM schema unchanged.
- After deploy, admins create a channel of type `OpenCode Go`, paste the opencode.ai console API key, use "fetch models" to populate the model list, then configure model ratios/prices per their own policy (no defaults shipped — see Assumptions).
- The hardcoded Anthropic-protocol set must be resynced when opencode publishes new models; source of truth: `GET https://opencode.ai/zen/go/v1/models` plus the opencode remote provider config. Possible follow-up (out of scope): a channel `other_settings` override list for Anthropic-protocol models.

## Risks and Mitigations

- Opencode changes the per-model protocol mapping remotely without notice — Mitigation: routing is centralized in one set + helper in `constants.go`; the failure mode is a visible upstream 4xx, and the model fetch button keeps the channel's model list current.
- Anthropic-protocol endpoint may require extra `anthropic-beta` headers for some models — Mitigation: header setup delegates to `claude.CommonClaudeHeadersOperation`, which passes through client `anthropic-beta` headers and channel Claude settings; flagged for the manual smoke test.
- Cross-format paths (OpenAI client → Anthropic upstream, Claude client → OpenAI upstream) depend on relayconvert fidelity — Mitigation: both directions are existing, tested converters used in production by other channels; Task 3 locks the delegation contract, and the smoke test covers all four combinations.

## Open Questions

- Does `https://opencode.ai/zen/go/v1` also expose the OpenAI Responses API (`/responses`)? Unverified; v1 returns not-implemented. Can be added later by delegating to `openai.Adaptor`'s Responses methods for OpenAI-protocol models.
- Should default model ratio entries be added for the 19 models despite the subscription being flat-fee? Current assumption: no; admins set prices. Confirm before implementation if reference pricing is desired.
