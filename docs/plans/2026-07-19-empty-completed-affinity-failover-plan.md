# Soft Upstream Failure Failover (Empty Completed + Capacity/Null-Code + Affinity) Plan

**Goal:** Make multi-channel failover work for **soft upstream failures** that today either look like success or fail without switching channels—especially empty completed answers, xAI capacity/`code: null` errors, and session affinity sticky binding.

**Inputs:**

1. **Empty completed:** Grok Responses stream ends `response.completed` with only a `reasoning` output (no `message` / tool call). Client sees empty/aborted answer; gateway treats as success.
2. **Capacity / null code:**  
   `Error Code null: The model is currently at capacity due to high demand. Please try again in a few minutes, or use a higher service tier for priority processing: https://docs.x.ai/developers/advanced-api-usage/priority-processing`  
   Flow aborts; multi-channel + affinity often do not recover the request.
3. Product need: multi-channel config must provide fault tolerance; affinity must not pin a session to a channel that just soft-failed.
4. Sibling plan: `docs/plans/2026-07-19-silent-upstream-termination-retry-plan.md` (mid-stream hang / scanner abort). **This plan absorbs its Task 3 capacity/null-code classification and affinity interaction**; hang detection remains in the sibling plan.

**Assumptions:**

- **Soft failure classes in scope (phase 1):**
  | Class | Signal | Client-facing internal code | Retry shape |
  |---|---|---|---|
  | Empty completed | `status=completed` + no usable output | `ErrorCodeEmptyResponse` | Prefer **switch-channel** (503) |
  | Capacity / overload | message keywords + null/empty/`null`/`unknown_error` code and/or 429/500/503 | `ErrorCodeUpstreamCapacity` (new) or normalized 503 | **Same-channel then switch** |
  | Null-code non-capacity 5xx | code null + no capacity keywords + status 5xx | keep status; retry by status ranges | existing status retry |
- **Usable output** = non-empty assistant text **or** tool/function/custom-tool call. Reasoning-only is not usable.
- **In-request retry only if zero client payload flushed** for the attempt. Any flushed assistant SSE/JSON (incl. `reasoning_content`) commits the stream → no same-request switch.
- Soft failures **clear affinity** and **block affinity record**; they **override** `SkipRetryOnFailure` so sticky Codex/Claude-style rules cannot trap the session.
- Soft failures **must not auto-ban** the channel (capacity is transient; empty completed is model quirk).
- Prefer reusing `types.ErrorCodeEmptyResponse`. Add `types.ErrorCodeUpstreamCapacity = "upstream_capacity"`.
- Phase 1 paths: OpenAI Responses (+ chat-via-responses), shared `RelayErrorHandler` / stream error extraction, affinity middleware. Native chat empty stream follow-up if cheap.

**Architecture:** Shared **soft-failure control plane** (client-commit flag, affinity unusable/clear, retry override, no auto-ban) plus two **classifiers**:

1. **Usable-output / empty-completed** on final Responses payloads.
2. **Upstream error normalize** for OpenAI-style errors (`code: null` + capacity message → transient capacity).

Handlers/error paths set the soft-failure error → controller retries (when zero-write) → affinity not sticky for next turn.

**Tech Stack:** Go, Gin, `controller/relay.go`, `service/channel_affinity.go`, `service/error.go`, `service/relayconvert/internal/oai_responses/`, `types.NewAPIError`, testify.

---

## Coverage matrix (what this plan supports)

| Upstream symptom | Today (typical) | After this plan |
|---|---|---|
| Reasoning-only `completed` | Success; affinity sticky | Soft fail; clear affinity; switch if zero-write |
| xAI capacity, `code: null` | May surface error; affinity skip-retry can block switch; status/mapping edge cases | Classify capacity → 503-class retry; clear affinity; same-channel then switch |
| `code: null` + generic 500 body | Retry only if status in ranges; no message normalize | Keep status retry; optional message log; capacity keywords still classify |
| Mid-stream hang / EOF zero-chunk | Often `nil` error | **Sibling plan** (not this file) |
| True 400 validation | No retry | Unchanged |
| Usable tool_calls only | Success | Unchanged success |
| Capacity **after** client chunks written | Cannot safely switch | No in-request switch; clear affinity for **next** request |

---

## File Map

- Create: `service/relayconvert/internal/oai_responses/usable_output.go` (+ test) — empty-completed / usable output
- Create: `service/upstream_error_classify.go` (+ test) — capacity / null-code classification
- Modify: `types/error.go` — `ErrorCodeUpstreamCapacity`
- Modify: `relay/common/relay_info.go` — `ClientPayloadWritten`, `EmptyCompleted`, `AffinityUnusable` (and optional soft-fail reason)
- Modify: `relay/helper/common.go` and/or Responses stream send path — set client-commit on flush
- Modify: `relay/channel/openai/chat_via_responses.go` — empty completed + stream error capacity path
- Modify: `relay/channel/openai/relay_responses.go` / stream handlers that surface upstream error events
- Modify: `service/error.go` (`RelayErrorHandler`) — normalize null-code + capacity after parse
- Modify: `controller/relay.go` — retry eligibility; affinity skip override for soft-fail codes; no auto-ban
- Modify: `service/channel.go` (`ShouldDisableChannel`) — exclude empty/capacity soft codes
- Modify: `service/channel_affinity.go` + `middleware/distributor.go` — no record on unusable; clear on soft fail
- Modify: admin log generation — markers for empty/capacity/affinity
- Optional: `service/empty_response_guard.go` — short-window empty counter
- Test: `controller/relay_retry_test.go`, affinity tests, classify tests, handler tests

## Out of Scope

- TTFT weighted selection
- Full silent mid-stream hang implementation (sibling plan Tasks 1–2)
- Auto-ban on soft failure
- Buffer-until-usable for reasoning streams (later hardening)
- Client i18n copy redesign
- Changing default affinity rule definitions globally (only soft-fail overrides)

---

## Desired behavior

```
attempt ends
  ├─ transport / do_request_failed
  │    → existing same-channel then switch
  ├─ HTTP/SSE error body (OpenAI-style)
  │    ├─ classify capacity (null code OK) → ErrorCodeUpstreamCapacity / 503
  │    │    → clear affinity; override SkipRetryOnFailure
  │    │    → zero-write? same-channel then switch : no in-request switch
  │    ├─ other 429/502/503 → existing (+ clear affinity on failure path)
  │    └─ 400 validation → no retry
  ├─ completed / 200 body or stream final
  │    ├─ usable output → success; RecordChannelAffinity
  │    └─ empty completed
  │         → clear affinity; AffinityUnusable
  │         → zero-write? ErrorCodeEmptyResponse 503 → switch-channel
  │         → else committed stream; next request unbound
  └─ mid-stream hang / zero-chunk EOF
       → sibling silent-termination plan
```

---

## Tasks

### Task 1: Usable-output classifier

**Outcome:** Deterministic empty-completed detection for Responses payloads (Grok reasoning-only fixture).

**Files:**

- Create: `service/relayconvert/internal/oai_responses/usable_output.go`
- Create: `service/relayconvert/internal/oai_responses/usable_output_test.go`

**Steps:**

- [x] `IsUsableResponsesOutput(resp)` — non-empty extracted text **or** tool-like output items
- [x] `IsEmptyCompletedResponses(resp)` — status completed (or final completed event) **and** not usable
- [x] Incomplete/failed/cancelled ≠ empty-completed
- [x] Table tests: reasoning-only; message text; function_call only; empty message+reasoning; content_filter incomplete; nil

**Validation:**

- Run: `go test ./service/relayconvert/internal/oai_responses/ -count=1 -run 'Usable|EmptyCompleted'`
- Expected: PASS

---

### Task 2: Upstream capacity / null-code classifier

**Outcome:** xAI-style capacity messages with `code: null` become a stable retryable soft failure, not a dead-end “Error Code null” only.

**Files:**

- Create: `service/upstream_error_classify.go`
- Create: `service/upstream_error_classify_test.go`
- Modify: `types/error.go` — add `ErrorCodeUpstreamCapacity ErrorCode = "upstream_capacity"`
- Modify: `service/error.go` — apply classify in `RelayErrorHandler` after OpenAI error parse
- Modify: stream error branches that call `types.WithOpenAIError` (Responses `response.failed` / `response.error`, OpenAI chat stream error objects) to run the same normalize helper

**Steps:**

- [x] Named keyword constants (lowercase substring match), phase 1 fixed list at least:
  - `at capacity`
  - `high demand`
  - `overloaded`
  - `temporarily unavailable`
  - `server is busy`
  - `priority processing` (xAI capacity page hint; optional but include for this exact message)
- [x] `IsNullOrUnknownUpstreamCode(code any/string)` — true for nil, `""`, `"null"`, `"unknown_error"`
- [x] `ClassifyUpstreamOpenAIError(msg, code, httpStatus) SoftFailKind`:
  - capacity if keywords match **and** (null/unknown code **or** status in {429,500,502,503} **or** status 0)
  - else none
- [x] `NormalizeSoftUpstreamError(err *types.NewAPIError) *types.NewAPIError`:
  - if capacity → set `errorCode=upstream_capacity`, prefer `StatusCode=503` (preserve original message)
  - do not rewrite validation 400s that merely contain “try again” without capacity keywords + null/5xx context
- [x] Unit fixtures:
  - exact user message + code null + status 500/503/429 → capacity
  - code null + “invalid api key” + 401 → not capacity
  - code null + empty message + 500 → not capacity (status retry only)
  - normal 429 rate limit without keywords → unchanged (still retryable by status)

**Validation:**

- Run: `go test ./service/ -count=1 -run 'UpstreamError|Capacity|NullCode'`
- Expected: xAI capacity fixture classifies to `upstream_capacity` / 503

---

### Task 3: Client-commit + soft-fail flags on RelayInfo

**Outcome:** Zero-write vs committed stream is explicit; affinity gate has a single unusable flag.

**Files:**

- Modify: `relay/common/relay_info.go`
- Modify: Responses→Chat send path / `relay/helper/common.go` as needed

**Steps:**

- [x] Fields: `ClientPayloadWritten bool`, `EmptyCompleted bool`, `AffinityUnusable bool`, optional `SoftFailReason string`
- [x] Do **not** use `ReceivedResponseCount` as client-commit (counts upstream events)
- [x] Set `ClientPayloadWritten` on successful client flush of converted assistant events
- [x] Helpers: `CanSoftFailRetry() bool` → `AffinityUnusable/soft fail && !ClientPayloadWritten` as needed by handlers

**Validation:**

- Run: package tests for relay/common or covered by handler tests
- Expected: existing tests pass

---

### Task 4: Empty completed in Responses handlers

**Outcome:** Reasoning-only completed is not a success for retry/affinity.

**Files:**

- Modify: `relay/channel/openai/chat_via_responses.go` (non-stream, buffered, stream)
- Modify: native responses handler if it finalizes full response objects
- Test: `relay/channel/openai/chat_via_responses_test.go`

**Steps:**

- [x] On final response: if empty completed → set flags, `ClearCurrentChannelAffinityCache`, admin reason
- [x] Zero-write → `return nil, NewOpenAIError(..., ErrorCodeEmptyResponse, 503)`
- [x] Already written → no in-request retry; still affinity clear/block; keep committed stream
- [x] Never “write error JSON then return nil” (avoid Gemini non-stream anti-pattern that blocks controller retry)

**Validation:**

- Run: `go test ./relay/channel/openai/ -count=1 -run 'EmptyCompleted|ResponsesToChat'`
- Expected: non-stream/buffered empty → 503 empty_response; committed stream does not double-write

---

### Task 5: Wire capacity normalize into error + stream failure paths

**Outcome:** Capacity/`code: null` errors always enter controller as retryable soft fails when HTTP/stream error is returned.

**Files:**

- Modify: `service/error.go`
- Modify: stream error handling in `relay/channel/openai/chat_via_responses.go`, `relay-openai.go` (if stream error object parsed), xAI adaptor only if it custom-parses errors
- Test: service + relay tests with capacity body fixtures

**Steps:**

- [x] After building `*types.NewAPIError` from upstream error JSON, call `NormalizeSoftUpstreamError`
- [x] Responses stream `response.failed` / `response.error` with capacity message → same normalize
- [x] Ensure showBodyWhenFail logging still works
- [x] If upstream returns capacity as **HTTP 200** with error object only (rare), detect in body handlers and convert to error (only if observed; otherwise document as follow-up)

**Validation:**

- Run: `go test ./service/ ./relay/channel/openai/ -count=1 -run 'Capacity|RelayError|Normalize'`
- Expected: fixture body produces `upstream_capacity` and 503

---

### Task 6: Controller retry policy for soft fails + no auto-ban

**Outcome:** Soft fails retry across channels; affinity skip does not trap; channels not auto-disabled.

**Files:**

- Modify: `controller/relay.go` (`shouldRetry`, `shouldSameChannelRetry`)
- Modify: `service/channel.go` (`ShouldDisableChannel`)
- Test: `controller/relay_retry_test.go`

**Steps:**

- [x] Define soft-fail code set: `ErrorCodeEmptyResponse`, `ErrorCodeUpstreamCapacity`
- [x] `shouldRetry`: if soft-fail code and retry budget and no `specific_channel_id` → **true even when** `ShouldSkipRetryAfterChannelAffinityFailure`
- [x] Status: soft fails use 503 so existing ranges also match; keep explicit code branch as belt-and-suspenders
- [x] `shouldSameChannelRetry`:
  - `upstream_capacity` → **true** (transient overload; backoff may help)
  - `empty_response` → **false** (prefer other channel)
- [x] On soft-fail error path: `ClearCurrentChannelAffinityCache` if not already cleared by handler
- [x] `ShouldDisableChannel`: return false for soft-fail codes (even if status 503 would otherwise match disable rules—today disable is mostly 401/keywords; still hard-exclude)
- [x] Exclude failed channel for remaining attempts via existing retry exclude machinery

**Validation:**

- Run: `go test ./controller/ ./service/ -count=1 -run 'Retry|EmptyResponse|Capacity|Disable'`
- Expected:
  - capacity → same-channel eligible + switch eligible
  - empty → switch only
  - affinity skip true + soft fail → still retry
  - soft fail → no auto-disable

---

### Task 7: Affinity record gate (session sticky fix)

**Outcome:** Soft failures never refresh sticky binding; next request reselects.

**Files:**

- Modify: `middleware/distributor.go` post-`c.Next()` record condition
- Modify: `service/channel_affinity.go` defensive checks in `RecordChannelAffinity`
- Test: affinity unit tests

**Steps:**

- [x] Record only if HTTP &lt; 400 **and not** `AffinityUnusable` / gin `channel_affinity_unusable`
- [x] Soft-fail handlers set gin key so middleware sees it after `c.Next()`
- [x] Failure path already may not record (status ≥ 400); capacity after normalize is 503 → no record; still **clear** sticky key so a previous success does not keep routing here
- [x] Admin info: `empty_completed` / `upstream_capacity` / `affinity_cleared` / `client_payload_written`

**Validation:**

- Run: `go test ./service/ -count=1 -run 'ChannelAffinity'`
- Expected: after soft fail, preferred affinity miss on next lookup

---

### Task 8 (optional): Short-window empty/capacity counters

**Outcome:** Repeated soft fails on same channel/model temporarily reduce selection pressure without auto-ban.

**Files:**

- Create: `service/soft_fail_guard.go` (+ test)
- Hook: retry exclude or preferred-affinity skip

**Steps:**

- [ ] Separate counters or shared window for empty vs capacity (capacity may recover faster)
- [ ] Named constants for window/threshold
- [ ] Skip if product wants minimal diff after Tasks 1–7

**Validation:**

- Run: `go test ./service/ -count=1 -run 'SoftFailGuard'`
- Expected: threshold hit → temporary skip; TTL clears

---

### Task 9: Observability + plan cross-links

**Outcome:** Ops can grep soft-fail failover without full upstream dumps.

**Files:**

- Modify: log/admin info paths
- Modify: `docs/plans/2026-07-19-silent-upstream-termination-retry-plan.md` note that Task 3 capacity is implemented here (when done)

**Steps:**

- [x] Log line: `soft fail class=empty_completed|upstream_capacity channel=#... model=... client_written=bool`
- [x] Admin markers only enums/bools; no full reasoning text
- [x] Checklist update on both plans

**Validation:**

- Unit or manual assert keys present on fixtures

---

## Final Validation

- Run: `go test ./service/relayconvert/internal/oai_responses/ ./service/ ./relay/channel/openai/ ./controller/ -count=1`
- Expected: PASS

**Scenario matrix:**

| # | Setup | Expected |
|---|---|---|
| A | Channel A reasoning-only completed; B healthy; zero-write | Retry B; client gets B; affinity → B or empty until B success |
| B | Channel A xAI capacity `code: null`; B healthy; zero-write | Same-channel backoff optional then B; affinity cleared |
| C | Affinity SkipRetryOnFailure rule active + A soft-fails | Still switch (override) |
| D | Soft fail after client already received chunks | No in-request switch; affinity cleared; next request free |
| E | Tool-call-only success | No soft fail; affinity records |
| F | Real 400 validation | No retry |

## Failure Behavior

- Capacity keyword false positive → extra retries; mitigate with keyword+code/status gates.
- Capacity false negative → user still stuck; add keywords from production samples.
- Empty false positive (marks usable empty) → still broken failover; fixture locks reasoning-only.
- All channels soft-fail → exhaust retries; return final soft-fail error to client.
- Affinity cache delete fails → still block record via flag; log error.

## Privacy and Security

- Client error message can keep upstream capacity text (already user-visible) or genericize; do not add secrets.
- Admin markers: class enums only; no prompt dump beyond existing request-detail toggles.

## Rollout Notes

- No DB migration.
- Expect more multi-channel `use_channel` chains on free/capacity-limited providers (xAI free tier, etc.).
- Optional kill-switch later: `SoftFailFailoverEnabled` (default on) if ops need it.
- Implement **Tasks 2+5+6+7** even if empty-completed slips, if capacity is the higher prod pain—order by pain: capacity classify → affinity clear/override → empty completed.

## Risks and Mitigations

- **Affinity SkipRetryOnFailure** — explicit soft-fail override in `shouldRetry`.
- **503 auto-ban** — exclude soft-fail codes in `ShouldDisableChannel`.
- **Status mapping** rewriting 503→other — apply normalize **after** mapping or re-apply code-based retry branch.
- **Reasoning already streamed** — cannot switch mid-request; affinity clear is the session fix.
- **Overlap with silent-termination plan** — share client-commit flag naming; do not duplicate capacity Task 3 there after this ships.

## Open Questions

- Keep upstream capacity message text for clients vs generic `model at capacity`? **Default: keep upstream message, stable internal code.**
- Same-channel attempts for capacity when only one channel exists? **Default: yes, honor `SameChannelRetryTimes`.**
- Kill-switch for first deploy? **Default: no.**
- Is capacity ever returned as HTTP 200 + error object on your channels? If yes, add explicit body path in Task 5; if only non-2xx, HTTP handler path is enough.
