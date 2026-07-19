# TTFT-Weighted Channel Selection Implementation Plan

**Goal:** Bias same-priority channel picks toward lower first-token latency (TTFT) by multiplying configured weight with a latency factor, without changing priority tiers or the existing retry/exclude semantics.

**Inputs:** Product discussion: record per-channel average first-token latency and apply extra weight so faster channels receive higher selection share within the same priority.

**Assumptions:**

- Feature is **off by default** to avoid sudden traffic shifts.
- Phase 1 uses an **in-process EWMA** of successful stream TTFT keyed by `channel_id` (optionally refined by model). No new DB table in phase 1.
- Existing `perf_metrics` stays model+group (marketplace UI). Channel routing does **not** read that table hot-path.
- `channels.response_time` (channel test) is **not** used for routing in phase 1.
- Formula: `effectiveWeight = max(1, round(baseWeight * factor))` where `baseWeight` is current configured weight (same +10 / smoothing rules as today after the effective weight is computed).
- Cold start / insufficient samples: `factor = 1.0`.
- Only **successful stream** requests with a real first response contribute samples.
- Priority order, same-priority exhaustion, same-channel backoff, and auto/cross-group selection are **unchanged**.

**Architecture:** On successful stream completion, record TTFT (`FirstResponseTime - StartTime`) into a small in-memory EWMA store keyed by channel (and model when present). During same-priority weighted random selection in cache and DB paths, convert each candidate’s configured weight into `effectiveWeight` via `factor = clamp(refTtft / avgTtft, minFactor, maxFactor)` using a reference TTFT (peer median or configured tau). Faster channels get `factor > 1`, slower get `factor < 1`.

**Tech Stack:** Go, existing Gin relay path, `model` channel selection (`channel_cache.go`, `ability.go`), optional admin options via `model/option.go` + Routing Reliability UI.

---

## File Map

- Create: `service/channel_ttft.go` — EWMA store, observe API, factor calculation
- Create: `service/channel_ttft_test.go` — EWMA, cold start, clamp, effective weight
- Create: `setting/operation_setting/ttft_routing.go` — defaults and accessors for routing knobs (or place vars in `common/constants.go` if matching RetryTimes style)
- Modify: `pkg/perf_metrics/metrics.go` / `RecordRelaySample` callers path — pass channel id into a **routing** observer (prefer calling `service.ObserveChannelTtft` from success settle paths next to existing `RecordRelaySample`)
- Modify: `service/text_quota.go`, `service/quota.go` (and any other success `RecordRelaySample` sites) — after successful settle, observe TTFT for routing
- Modify: `model/channel_cache.go` — use effective weight when summing/selecting same-priority candidates
- Modify: `model/ability.go` — same for DB selection path
- Modify: `model/option.go` — load/persist config keys if admin-tunable
- Modify: `web/default/.../routing-reliability-section.tsx` (+ types/registry/defaults) — optional UI toggles
- Test: unit tests above; optional table tests around weight selection with fixed RNG seed if extractable

---

## Tasks

### Task 1: Config knobs and defaults

**Outcome:** Runtime config exists with safe defaults; feature disabled unless enabled.

**Files:**

- Create: `setting/operation_setting/ttft_routing.go` (preferred) **or** Modify: `common/constants.go`
- Modify: `model/option.go` (if using OptionMap admin options)

**Steps:**

- [ ] Add:
  - `TtftRoutingEnabled` default `false`
  - `TtftRoutingMinSamples` default `20`
  - `TtftRoutingEwmaAlpha` default `0.2` (or fixed constant if not exposed)
  - `TtftRoutingMinFactor` default `0.5`
  - `TtftRoutingMaxFactor` default `2.0`
  - `TtftRoutingRefMs` default `500` (tau / reference TTFT when peer median unavailable)
- [ ] Wire OptionMap get/set like `RetryTimes` if exposed in admin UI
- [ ] Document: only affects **intra-priority** weight, not priority ladder

**Validation:**

- Run: `go test ./setting/operation_setting -count=1` (or package housing config)
- Expected: package builds; defaults load

### Task 2: In-memory channel TTFT EWMA store

**Outcome:** Process can observe TTFT and query avg/sample count per channel (and model).

**Files:**

- Create: `service/channel_ttft.go`
- Create: `service/channel_ttft_test.go`

**Steps:**

- [ ] Define store with `sync.Map` or sharded maps; entry fields: `ewmaMs float64`, `samples int64`, `updatedAt`
- [ ] Key: `channelId` only for phase 1 **or** `channelId + "\x00" + model` if model-scoped (recommend **channel+model** when `OriginModelName` non-empty, else channel-only)
- [ ] `ObserveChannelTtft(channelId int, model string, ttftMs int64)`:
  - ignore if `!TtftRoutingEnabled` (optional: still record when disabled for warm cache — **assume record only when enabled** to save CPU)
  - ignore `ttftMs < 0`
  - EWMA: first sample sets value; later `ewma = alpha*ttft + (1-alpha)*ewma`
  - increment samples
- [ ] `GetChannelTtft(channelId int, model string) (avgMs float64, samples int64, ok bool)`
- [ ] `LatencyFactor(avgMs float64, samples int64, peerRefMs float64) float64`:
  - if `samples < MinSamples` or `avgMs <= 0` → `1.0`
  - `ref = peerRefMs` if `>0` else `TtftRoutingRefMs`
  - `factor = ref / avgMs`
  - clamp to `[MinFactor, MaxFactor]`
- [ ] `EffectiveWeight(baseWeight int, factor float64) int`:
  - if baseWeight <= 0, treat as 0 before smoothing (preserve current zero-weight semantics)
  - `out = int(math.Round(float64(baseWeight) * factor))`
  - if baseWeight > 0 && out < 1 → `1`

**Validation:**

- Run: `go test ./service -run 'Ttft|LatencyFactor|EffectiveWeight' -count=1`
- Expected: cold start factor 1; faster than ref → factor > 1; clamp works; EWMA moves toward new samples

### Task 3: Record TTFT on successful stream completion

**Outcome:** Live traffic updates the store with real first-token latency.

**Files:**

- Modify: `service/text_quota.go` (success `RecordRelaySample`)
- Modify: `service/quota.go` (other success paths)
- Optionally Modify: `pkg/perf_metrics/metrics.go` only if centralizing; **prefer service-side observe** to avoid changing marketplace schema

**Steps:**

- [ ] Next to existing success `perfmetrics.RecordRelaySample(relayInfo, true, ...)`:
  - if stream and `relayInfo.HasSendResponse()` (or equivalent)
  - `ttft := FirstResponseTime.Sub(StartTime).Milliseconds()`
  - `service.ObserveChannelTtft(relayInfo.ChannelId, relayInfo.OriginModelName, ttft)`
- [ ] Do **not** observe on failed requests in phase 1
- [ ] Do **not** observe non-stream / no first response
- [ ] Guard nil `ChannelMeta` / zero channel id

**Validation:**

- Run: `go test ./service -count=1`
- Expected: existing tests pass; new unit test can call Observe + Get without relay

### Task 4: Apply effective weight in channel selection (cache + DB)

**Outcome:** Same-priority random pick prefers lower TTFT when enabled and samples sufficient.

**Files:**

- Modify: `model/channel_cache.go` — `GetRandomSatisfiedChannel`
- Modify: `model/ability.go` — `GetChannel`
- Test: `model/channel_user_agent_test.go` or new `model/channel_ttft_weight_test.go`

**Steps:**

- [ ] Extract helper in `model` or call `service` carefully (avoid import cycles):
  - If `model` → `service` cycle: put store in `pkg/channellatency` or `common`/`setting` package used by both
  - **Preferred package layout to avoid cycles:** `pkg/channellatency` (store + factor), imported by `service` (observe) and `model` (weight)
- [ ] For each same-priority candidate:
  - `base := channel.GetWeight()` (cache) or `ability.Weight` (DB)
  - `avg, n, _ := store.Get(...)`
  - `factor := LatencyFactor(avg, n, ref)` where `ref` is peer median of candidates with enough samples, else `TtftRoutingRefMs`
  - `eff := EffectiveWeight(base, factor)` when enabled; else `base`
- [ ] Sum / random selection uses `eff` instead of raw weight
- [ ] Keep existing smoothing (+10 on DB path, cache zero-weight smoothing) applied to **effective** weights consistently
- [ ] Peer median: among candidates with `samples >= MinSamples`, median of avgMs; if none, use `TtftRoutingRefMs`

**Validation:**

- Run: `go test ./model -run 'Ttft|Weight|GetRandomSatisfiedChannel|GetChannel' -count=1`
- Expected: with fixed store values, lower TTFT channel selected more often under equal base weights (statistical or deterministic by injecting factor via test hooks)

### Task 5: Admin UI (optional but recommended)

**Outcome:** Operators can enable and tune without rebuild.

**Files:**

- Modify: `web/default/src/features/system-settings/models/routing-reliability-section.tsx`
- Modify: types / section-registry / index defaults
- Locales via i18n script (en/zh/fr/ja/ru/vi)

**Steps:**

- [ ] Fields: enabled, min samples, min/max factor, ref ms
- [ ] Descriptions: English source keys; note only same-priority share is affected
- [ ] Classic UI optional parity under Operation settings

**Validation:**

- Run: `cd web/default && bun run i18n:sync`
- Expected: keys present in all locales; form validates ranges

### Task 6: Observability

**Outcome:** Debuggable in production.

**Files:**

- Modify: selection debug logs optional in `GetRandomSatisfiedChannel` when `DebugEnabled`
- Optional: admin-only metrics dump endpoint **out of scope** unless needed

**Steps:**

- [ ] When debug on: log channel id, base weight, avg TTFT, factor, effective weight for chosen channel
- [ ] No PII; only channel ids and numeric stats

**Validation:**

- Manual: enable debug + feature; confirm log line on request

---

## Final Validation

- Run: `go test ./pkg/channellatency ./service ./model -count=1` (adjust package names to final layout)
- Expected: all pass
- Run: `go test ./controller -run 'TestIsSameChannelRetryStatusCode|TestSameChannel' -count=1`
- Expected: pass (no regression on retry path)
- Manual smoke:
  1. Feature off → selection identical to weight-only
  2. Feature on, two equal-weight same-priority channels, seed A with low TTFT / B high → A receives more traffic over N requests
  3. Samples below min → no bias
  4. Retry exclude path still exhausts same priority before falling back

---

## Failure Behavior

- Store missing entry → factor 1.0, no error
- Negative / absurd TTFT → ignore sample
- All candidates cold → pure configured weights
- Feature disabled mid-flight → stop applying factors; EWMA may stop updating (per Task 2 choice)
- Import cycle risk → fix by extracting `pkg/channellatency` rather than coupling model↔service

## Privacy and Security

- TTFT is operational telemetry (latency only); no prompt/content
- Admin options require existing admin option permissions
- Do not expose per-channel TTFT store on public APIs in phase 1

## Rollout Notes

1. Deploy with `TtftRoutingEnabled=false`
2. Optionally enable recording-only variant later; phase 1 can enable observe+route together
3. Enable on a canary node; compare channel mix and p95 TTFT
4. Start with mild clamps (`0.5–2.0`) and `MinSamples=20`
5. Multi-instance: EWMA is **per process** in phase 1 (each node learns locally). Phase 2 may share via Redis if needed

## Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Feedback loop (fast channel gets more load and slows down) | Clamp factors; optional periodic decay of EWMA; monitor |
| New channels starve or monopolize | Cold start factor=1 until MinSamples |
| Multi-instance inconsistent weights | Document local EWMA; Redis later |
| Stream-only bias ignores non-stream quality | Document scope; phase 2 may add total latency |
| Import cycles | `pkg/channellatency` isolation |

## Out of Scope

- Changing priority tiers based on TTFT
- Using marketplace `perf_metrics` aggregates for routing
- Using `channels.response_time` test field
- Persistence of EWMA across restarts (phase 1)
- Non-stream latency weighting
- Auto-disable based on TTFT

## Open Questions

- Model-scoped key vs channel-only: plan recommends channel+model when model present.
- Whether to update EWMA when feature disabled (warm cache): plan assumes only when enabled.
- Redis shared store: deferred to phase 2.

---

# 基于首字耗时（TTFT）的渠道加权选路 — 实现计划（中文）

**目标：** 在**同一优先级内**，根据渠道平均首字耗时（TTFT）对配置权重做乘性修正，让更快的渠道获得更高被选概率；不改变优先级阶梯，也不改变现有排除/同级换渠/同渠退避语义。

**输入：** 产品讨论——记录渠道首字平均耗时，并在现有权重上额外加权，耗时越少占比越高。

**假设：**

- 功能**默认关闭**，避免流量突变。
- 第一期用**进程内 EWMA** 维护渠道（建议 channel+model）TTFT，不新建 DB 表。
- 现有 `perf_metrics` 仍是 **model+group** 维度，仅供模型广场；选渠热路径**不读**该表。
- 第一期**不使用** `channels.response_time`（渠道测试耗时）。
- 公式：`effectiveWeight = max(1, round(baseWeight * factor))`（在现有 +10 / smoothing 规则之前或之后需统一，推荐先算 effective 再走现有 smoothing）。
- 冷启动 / 样本不足：`factor = 1.0`。
- 仅**成功且流式、已产生首包**的请求写入样本。
- 优先级顺序、同优先级耗尽、同渠退避、auto/跨分组逻辑**保持不变**。

**架构：** 成功流式结算时，将 `FirstResponseTime - StartTime` 写入内存 EWMA。同优先级加权随机时，用  
`factor = clamp(refTtft / avgTtft, minFactor, maxFactor)`  
把配置权重变为有效权重。更快的渠道 `factor > 1`，更慢 `factor < 1`。`ref` 优先用同批候选中足够样本的 TTFT 中位数，否则用配置参考值（如 500ms）。

**技术栈：** Go、现有 relay 结算路径、`model/channel_cache.go` 与 `model/ability.go` 选渠、可选运营配置与 Routing Reliability UI。

---

## 文件映射

- 新建：`pkg/channellatency/`（推荐）或 `service/channel_ttft.go` — EWMA 与 factor（**注意 model↔service 循环依赖，推荐独立 pkg**）
- 新建：对应 `*_test.go`
- 修改：成功结算处（`service/text_quota.go`、`service/quota.go` 等 `RecordRelaySample` 旁）— 调用 Observe
- 修改：`model/channel_cache.go`、`model/ability.go` — 使用 effective weight
- 修改：`model/option.go` + 前端 Routing Reliability — 可选开关与参数
- 测试：unit + 选渠权重行为

---

## 任务

### 任务 1：配置项与默认值

**结果：** 运行时配置齐全，默认关闭。

**步骤：**

- [ ] `TtftRoutingEnabled=false`
- [ ] `TtftRoutingMinSamples=20`
- [ ] `TtftRoutingMinFactor=0.5` / `MaxFactor=2.0`
- [ ] `TtftRoutingRefMs=500`
- [ ] `TtftRoutingEwmaAlpha=0.2`（可固定不暴露）
- [ ] 文档说明：只影响同优先级占比

**验证：** 配置包测试通过；默认关闭。

### 任务 2：内存 EWMA 存储

**结果：** 可 Observe / 查询 avg 与样本数；可算 factor 与 effectiveWeight。

**步骤：**

- [ ] Key：`channelId + model`（model 空则仅 channel）
- [ ] 首样本直接赋值；其后 EWMA 更新
- [ ] 样本不足或无效 → factor=1
- [ ] `factor = clamp(ref/avg, min, max)`
- [ ] `effectiveWeight` 对正 base 至少为 1

**验证：** `go test` 覆盖冷启动、夹紧、EWMA 收敛。

### 任务 3：成功流式请求写入 TTFT

**结果：** 真实流量更新存储。

**步骤：**

- [ ] 在成功 `RecordRelaySample` 旁：`HasTtft` 时 Observe
- [ ] 失败 / 非流式 / 无首包：不写
- [ ] channelId 为 0：跳过

**验证：** service 测试通过。

### 任务 4：缓存与 DB 选渠应用有效权重

**结果：** 开启且样本足够时，同优先级更快渠道更容易被选中。

**步骤：**

- [ ] 两处选渠路径统一 effective weight
- [ ] peer ref = 候选中足够样本的 avg 中位数，否则 RefMs
- [ ] 保持现有 smoothing 语义
- [ ] 用 pkg 拆分避免 import cycle

**验证：** model 测试证明等权下低 TTFT 渠道被选概率更高（或注入 factor 的确定性测试）。

### 任务 5：管理端 UI（建议）

**结果：** 可开关与调参。

**步骤：**

- [ ] Routing Reliability 增加字段
- [ ] i18n 六语种脚本写入

**验证：** `bun run i18n:sync`；表单校验范围合法。

### 任务 6：可观测性

**结果：** Debug 日志可打印 base/avg/factor/eff。

**验证：** 打开 debug 后请求可见日志。

---

## 最终验证

- `go test` 覆盖 latency pkg、service、model
- 功能关：行为与纯 weight 一致
- 功能开：等权同优先级，快渠道占比更高
- 样本不足：无偏向
- 重试排除：仍同级耗尽再降级

## 失败行为

- 无统计：factor=1，不报错
- 非法样本：丢弃
- 全员冷启动：退回配置权重
- 循环依赖：抽到 `pkg/channellatency`

## 隐私与安全

- 仅延迟数值，无内容
- 管理配置沿用 option 权限
- 第一期不对公网暴露 per-channel TTFT API

## 上线说明

1. 默认关闭部署  
2. 金丝雀开启，对比渠道分布与 p95 TTFT  
3. 先用温和夹紧与 MinSamples=20  
4. 第一期 EWMA **按进程本地**；多副本不一致可接受，二期再 Redis  

## 风险与缓解

| 风险 | 缓解 |
|------|------|
| 正反馈（越快越打越多再变慢） | factor 夹紧、监控、可衰减 |
| 新渠道样本不足 | 冷启动 factor=1 |
| 多实例权重不一致 | 文档说明；二期 Redis |
| 仅流式有 TTFT | 文档范围；二期可加总延迟 |

## 不在范围

- 用 TTFT 改 priority  
- 用模型广场 `perf_metrics` 选渠  
- 用渠道测试 `response_time`  
- EWMA 持久化  
- 非流式延迟加权  
- 因 TTFT 自动禁用渠道  

## 待决问题

- Key 用 channel+model（推荐）还是仅 channel  
- 关闭功能时是否仍采样（计划：仅开启时采样）  
- 是否二期 Redis 共享  

---

## Implementation Order (both languages)

1. Config  
2. `pkg/channellatency` store + tests  
3. Observe on success stream  
4. Wire effective weight into cache + DB selection  
5. Admin UI + i18n  
6. Debug logs + rollout  

**Estimated effort:** ~1–2 days for phase 1 backend; +0.5 day UI/i18n.
