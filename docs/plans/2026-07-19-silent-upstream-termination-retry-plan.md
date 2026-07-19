# Silent Upstream Termination Retry Plan

**Goal:** Make “upstream dies without a usable error code” (e.g. `code: null`, capacity message, mid-stream hang-up) correctly enter the existing retry path **when safe**, without double-writing streams or inventing credits.

**Context:** Same-channel exponential backoff + exclude/switch-channel already exist. Gaps remain when:

1. HTTP body has `error.code: null` (message still meaningful).
2. Stream opens as `200 + text/event-stream` then aborts; `DoResponse` often returns `nil` so controller never retries.
3. Stream already flushed bytes to the client — in-request channel switch is unsafe.

**Out of scope (this plan):**

- Changing priority / weight selection (TTFT plan is separate).
- Exponential backoff formula changes (already shipped on `feat/same-channel-backoff-retry`).
- Client-visible product copy rewrites beyond mapping to a stable internal error.

**Assumptions:**

- **Zero-write** means no successful write of upstream payload/SSE chunk to the client for this attempt (`ReceivedResponseCount == 0` and no equivalent “already flushed” flag).
- Once any chunk is written, **no same-request retry** (neither same-channel nor switch-channel).
- Capacity-like messages are treated as **503-class transient** for retry eligibility only; client status may still reflect upstream when already committed to a response.
- Defaults stay conservative: feature pieces can ship behind clear helpers; keyword list is small and optional to configure later.

---

## Desired behavior

```
upstream attempt ends
  ├─ transport failure (do_request_failed)
  │    → same-channel backoff → then exclude + switch (existing)
  ├─ HTTP error body (code null/unknown OK)
  │    ├─ status 429/502/503 → same-channel then switch
  │    ├─ capacity/overload keywords → treat as 503-class → same-channel then switch
  │    └─ 400/401/etc → no retry
  ├─ stream / body path
  │    ├─ zero-write + abnormal end (timeout / scanner_error / panic / 0-chunk capacity)
  │    │    → return NewAPIError to controller → same-channel then switch
  │    └─ already wrote to client
  │         → DO NOT retry; surface error event if possible; log stream_status
  └─ success
```

---

## File Map (planned)

| Area | Files | Change |
|---|---|---|
| Stream failure → error | `relay/channel/openai/relay-openai.go`, other stream handlers as needed, possibly small helper in `relay/helper/` | After `StreamScannerHandler`, if abnormal + zero-write → return `*types.NewAPIError` |
| Stream status | `relay/common/stream_status.go` | Optional helpers: `ShouldFailAttempt()`, `IsZeroWriteFailure()` |
| Capacity keywords | `setting/operation_setting/` or `service/` | Normalize message → transient retry class |
| Same-channel eligibility | `controller/relay.go` (`shouldSameChannelRetry*`) | Include capacity-normalized / stream-abort error codes |
| Switch eligibility | existing `shouldRetry` / status ranges | Prefer status 503 after normalize; avoid expanding ranges blindly |
| Tests | `relay/helper/stream_scanner_test.go`, new handler tests, `controller/relay_retry_test.go` | Zero-write fail; written-no-retry; capacity null code |
| Config (optional phase) | `common/constants.go`, `model/option.go`, Routing Reliability UI | Keyword list / enable flag if product wants tunability |
| Docs | this plan | Living checklist |

---

## Tasks

### Task 1: Zero-write stream failure surfaces as relay error

**Outcome:** Abnormal stream ends that never wrote to the client return an error to `controller.Relay`, so same-channel backoff and channel switch can run.

**Files:**

- Modify: `relay/common/stream_status.go` (helpers)
- Modify: `relay/channel/openai/relay-openai.go` (`OaiStreamHandler` end path)
- Modify: other high-traffic stream handlers that currently always `return usage, nil` (Claude/Gemini/etc. only if same pattern)
- Test: stream scanner / openai stream tests

**Steps:**

- [ ] Define “abnormal end” using `StreamStatus`:
  - fail: `timeout`, `scanner_error`, `panic`, `ping_fail`
  - soft-normal today: `eof` without `done` — decide explicitly:
    - **phase 1 recommend:** treat `eof` + `ReceivedResponseCount == 0` as failure
    - `eof` + count > 0: no retry (already wrote or incomplete but committed)
  - keep client disconnect (`client_gone`) as **non-retry** (local)
- [ ] Add helper e.g. `StreamAttemptFailed(info) (bool, errorCode, statusCode)`
- [ ] In `OaiStreamHandler` (and siblings): if failed + zero-write → `return nil, types.NewOpenAIError(..., ErrorCodeBadResponse / new ErrorCodeStreamAborted, 503 or 500)`
- [ ] Do **not** change success path when `done`/`eof` with content and no hard errors

**Validation:**

- Unit: zero-chunk timeout/EOF → non-nil error
- Unit: at least one chunk then timeout → nil or non-retry local error; controller would not switch if already written (Task 2)

---

### Task 2: Already-written responses never switch channel

**Outcome:** In-request retry is impossible after any client write of upstream stream data.

**Files:**

- Modify: `controller/relay.go` and/or stream handlers
- Possibly set a context flag when first SSE/object is written (`helper.StringData` / `ObjectData`)

**Steps:**

- [ ] Introduce a clear signal: `info.ReceivedResponseCount > 0` **or** new `info.ClientBytesCommitted` set on first successful client write
- [ ] `shouldRetry` / `shouldSameChannelRetry`: if committed → false
- [ ] Stream path: if committed + later failure → return error that carries `ErrOptionWithSkipRetry()` (or dedicated check)
- [ ] Log `stream_status` remains (already in log other)

**Validation:**

- Table test: committed=true + 503-class error → no same-channel, no switch
- Integration-style handler test: first chunk then abort → no second channel in `use_channel`

---

### Task 3: Capacity / null-code message normalization

**Status:** Implemented in `docs/plans/2026-07-19-empty-completed-affinity-failover-plan.md` (Tasks 2/5/6). Hang detection remains in this plan.

**Outcome:** Messages like “model is currently at capacity… try again” retry as transient overload even when `code` is null.

**Files:**

- Create: `service/upstream_error_classify.go` (+ test) **or** under `setting/operation_setting/`
- Modify: `service/error.go` (`RelayErrorHandler`) and/or stream error extraction
- Modify: `controller/relay.go` eligibility if classification is error-code based

**Steps:**

- [x] Keyword list (lowercase match), phase 1 fixed constants, e.g.:
  - `at capacity`, `high demand`, `overloaded`, `temporarily unavailable`, `server is busy`, `priority processing`
- [x] When OpenAI error code is null/empty/`null`/`unknown_error` **or** status is 500/503/429, and message matches → classify as **transient capacity**
- [x] Map classification to:
  - internal `ErrorCode` e.g. `upstream_capacity` (new) **or** keep status 503
  - `shouldSameChannelRetry` true
  - `shouldRetry` true (status 503 or explicit branch)
- [x] Do not disable channel on capacity (ensure not in auto-disable keywords/status)

**Validation:**

- `code: null` + capacity message + HTTP 500 → same-channel eligible
- `code: null` + validation message + HTTP 400 → not eligible

---

### Task 4: Align same-channel eligibility with classified errors

**Outcome:** Same-channel backoff covers transport + capacity + 429/502/503 without treating all 500s as same-channel.

**Files:**

- Modify: `controller/relay.go` (`shouldSameChannelRetry`, task variant)
- Modify: `controller/relay_retry_test.go`

**Steps:**

- [ ] Keep allowlist: network/`do_request_failed`, 429, 502, 503
- [ ] Add: `upstream_capacity` / stream zero-write abort codes from Tasks 1–3
- [ ] Explicitly **exclude** generic 500 without classification
- [ ] Task relay: mirror classification for `do_request_failed` + capacity

**Validation:**

- Existing `TestShouldSameChannelRetryIncludesDoRequestFailed` still passes
- New cases for capacity and stream-abort codes

---

### Task 5: Regression tests and rollout checklist

**Outcome:** Safe to enable defaults without behavior surprise when `SameChannelRetryTimes=0` (switch-only still gets zero-write errors).

**Files:**

- Tests from Tasks 1–4
- Optional: admin docs / comment in Routing Reliability that stream retries only apply pre-write

**Steps:**

- [ ] Matrix:

  | Case | SameChannelRetryTimes=0 | >0 |
  |---|---|---|
  | zero-write stream abort | switch if RetryTimes allows | same-channel then switch |
  | written stream abort | no retry | no retry |
  | capacity null code HTTP 500 | switch (after classify) | same-channel then switch |
  | plain 400 | no | no |

- [ ] Manual smoke: xAI-style capacity message (non-stream + stream if reproducible)
- [ ] Confirm pre-consume refund still runs when controller receives error (existing defer)

**Validation:**

- `go test ./controller ./relay/... ./service -count=1` (scoped packages)
- No change when stream succeeds normally

---

### Task 6 (optional later): Configurable keywords / UI

**Outcome:** Ops can tune capacity keywords without deploy.

**Only if product asks after phase 1.**

- Option keys + Routing Reliability fields
- i18n via `add-missing-keys.mjs` workflow

---

## Implementation order

1. Task 1 — zero-write fail up  
2. Task 2 — commit barrier (no retry after write)  
3. Task 3 — capacity normalize  
4. Task 4 — eligibility glue  
5. Task 5 — tests / smoke  
6. Task 6 — optional config UI  

**Do not implement in this session** (user request: plan + todos only).

---

## Risks

| Risk | Mitigation |
|---|---|
| False positive on incomplete but valid streams | Gate on zero-write + explicit end reasons |
| Double billing on stream retry | Only retry when zero-write; refund path already on controller error |
| Treating all 500 as same-channel | Classification required; generic 500 stays switch-only or no-retry per status ranges |
| Provider-specific capacity wording | Start small keyword set; optional Task 6 |
| Handlers other than OpenAI still swallow errors | Inventory stream handlers that always return nil |

---

## Success criteria

- [ ] Zero-write abnormal stream ends reach `controller` retry loop.
- [ ] Any client write for the attempt blocks further in-request retries.
- [ ] `code: null` capacity-style failures retry like 503-class transients.
- [ ] Successful streams and client disconnects unchanged.
- [ ] Defaults do not enlarge same-channel retries beyond existing allowlist + new classified codes.
