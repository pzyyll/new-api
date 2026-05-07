# 向上游渠道请求失败的重试逻辑分析

## 一、整体架构概览

系统采用**分层结构**处理上游 API 请求，重试逻辑贯穿于多个层级：

```
请求进入 → 中间件链 → Distribute(选择渠道) → Relay(重试循环) → 上游响应
                                ↑                        ↓
                          渠道选择与重试              失败时重试判断
                                ↑                        ↓
                     CacheGetRandomSatisfiedChannel  shouldRetry()
```

核心重试逻辑集中在以下文件中：

| 文件 | 作用 |
|------|------|
| `controller/relay.go` | 主重试循环与重试判断逻辑 |
| `service/channel_select.go` | 渠道选择（含跨分组重试） |
| `model/channel_cache.go` / `model/ability.go` | 基于优先级的加权随机渠道选择 |
| `setting/operation_setting/status_code_ranges.go` | 可配置的重试/禁用状态码范围 |
| `types/error.go` | 错误类型体系与重试标记 |
| `service/channel.go` | 渠道自动禁用逻辑 |
| `service/channel_affinity.go` | 渠道亲和性与重试互操作 |

---

## 二、重试配置体系

### 2.1 核心配置项

| 配置项 | 默认值 | 含义 | 配置方式 |
|--------|--------|------|----------|
| `RetryTimes` | **0** (不重试) | 最大重试次数 | UI: 设置 → 运维设置 → 通用设置 → 失败重试次数 |
| `RelayTimeout` | **0** (无超时) | 单次上游请求超时(秒) | 环境变量 `RELAY_TIMEOUT` |
| `StreamingTimeout` | **300** 秒 | 流式请求超时(秒) | 环境变量 `STREAMING_TIMEOUT` |
| `AutomaticDisableChannelEnabled` | **false** | 是否启用自动禁用渠道 | UI 运维设置 |
| `AutomaticEnableChannelEnabled` | **false** | 是否启用自动启用渠道 | UI 运维设置 |

**存储路径**：`RetryTimes` 定义在 `common/constants.go:132`，通过 `model/option.go:502-503` 从数据库 `options` 表加载到运行时。

### 2.2 状态码重试范围

默认自动重试状态码范围定义在 `setting/operation_setting/status_code_ranges.go:21-29`：

```go
var AutomaticRetryStatusCodeRanges = []StatusCodeRange{
    {Start: 100, End: 199},  // 1xx 信息类
    {Start: 300, End: 399},  // 3xx 重定向
    {Start: 401, End: 407},  // 认证/授权错误(不含400)
    {Start: 409, End: 499},  // 客户端错误(不含408)
    {Start: 500, End: 503},  // 服务端错误(不含504)
    {Start: 505, End: 523},  // 服务端错误(不含524)
    {Start: 525, End: 599},  // 服务端错误
}
```

**永远不会重试的状态码**（硬编码，不可配置）：

| 状态码 | 含义 |
|--------|------|
| 400 | 请求格式错误 |
| 408 | Azure 处理超时 |
| 504 | 网关超时 |
| 524 | Cloudflare 超时 |

**永远不会重试的错误码**（硬编码）：`ErrorCodeBadResponseBody`（上游响应体解析失败）。

### 2.3 渠道自动禁用范围

默认自动禁用状态码范围：

```go
var AutomaticDisableStatusCodeRanges = []StatusCodeRange{
    {Start: 401, End: 401},  // 仅 401 认证失败
}
```

---

## 三、重试主循环

### 3.1 标准 Relay 重试 (`controller/relay.go`)

入口函数 `Relay()` (第 67 行)，重试循环位于第 189-235 行：

```go
retryParam := &service.RetryParam{
    Ctx:        c,
    TokenGroup: relayInfo.TokenGroup,
    ModelName:  relayInfo.OriginModelName,
    Retry:      common.GetPointer(0),  // 从 0 开始
}

for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {
    relayInfo.RetryIndex = retryParam.GetRetry()

    // 1. 获取渠道
    channel, channelErr := getChannel(c, relayInfo, retryParam)
    if channelErr != nil {
        break  // 获取渠道失败，直接退出
    }

    // 2. 读取请求体（可重复读取）
    addUsedChannel(c, channel.Id)
    bodyStorage, bodyErr := common.GetBodyStorage(c)

    // 3. 执行转发
    switch relayFormat {
    case RelayFormatOpenAIRealtime: newAPIError = relay.WssHelper(c, relayInfo)
    case RelayFormatClaude:        newAPIError = relay.ClaudeHelper(c, relayInfo)
    case RelayFormatGemini:        newAPIError = geminiRelayHandler(c, relayInfo)
    default:                       newAPIError = relayHandler(c, relayInfo)
    }

    // 4. 成功则直接返回
    if newAPIError == nil {
        return
    }

    // 5. 失败处理：记录错误、可能禁用渠道
    processChannelError(c, ...)

    // 6. 判断是否继续重试
    if !shouldRetry(c, newAPIError, common.RetryTimes - retryParam.GetRetry()) {
        break
    }
}
```

**关键特性**：
- 每次重试获取一个**不同的渠道**（通过 `RetryParam` 控制优先级层级）
- **无延迟重试**：重试之间没有 `sleep`/backoff，立即尝试下一个渠道
- 如果 `common.RetryTimes = 0`（默认），循环体仅执行一次（`0 <= 0`），等效于不重试

### 3.2 Task 重试 (`controller/relay.go`)

`RelayTask()` 函数处理异步任务类渠道（Midjourney、Suno、Video 等），重试循环位于第 510-558 行：

```go
for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {
    // 支持锁定渠道（任务专属渠道）
    if lockedCh, ok := relayInfo.LockedChannel.(*model.Channel); ok {
        channel = lockedCh
        // 仅第0次跳过 setup，后续重试需要重新 setup 上下文
    } else {
        channel, channelErr = getChannel(c, relayInfo, retryParam)
    }

    result, taskErr = relay.RelayTaskSubmit(c, relayInfo)
    if taskErr == nil {
        break  // 成功
    }

    // 非本地错误时处理渠道错误
    if !taskErr.LocalError {
        processChannelError(c, ...)
    }

    if !shouldRetryTaskRelay(c, channel.Id, taskErr, ...) {
        break
    }
}
```

**与标准 Relay 的差异**：
- Task 重试使用独立的 `shouldRetryTaskRelay()` 判断函数
- 支持 `LockedChannel`（任务锁定到特定渠道）
- `LocalError`（本地错误如请求体读取失败）不触发 `processChannelError`

---

## 四、渠道选择与重试优先级

### 4.1 RetryParam 结构

```go
type RetryParam struct {
    Ctx          *gin.Context
    TokenGroup   string     // 使用的分组
    ModelName    string     // 请求的模型名
    Retry        *int       // 当前重试次数(指针，支持跨循环修改)
    resetNextTry bool       // 是否跳过下次递增（跨分组切换时使用）
}
```

`IncreaseRetry()` 方法在每次循环迭代时自动递增 `Retry`，除非 `resetNextTry` 为 true。

### 4.2 优先级分层策略

渠道选择采用**优先级分层 + 加权随机**策略：

1. 所有渠道按 `Priority` 降序排列，去重得到优先级层级列表
2. 第 N 次重试 → 选择第 N 个优先级层级（N 超过层级数时使用最低优先级）
3. 在目标优先级层级内，按 `Weight` 加权随机选择一个渠道
4. 权重为 0 的渠道也有均等机会（平滑因子 + 调整值 = 100）

```go
// model/channel_cache.go 简化逻辑
uniquePriorities := dedup(sortDesc(priorities))
targetPriority := uniquePriorities[retry % len(uniquePriorities)]
targetChannels := filter(channels, priority == targetPriority)
selectedChannel := weightedRandom(targetChannels, weight)
```

**示例**：假设某分组下有 3 个优先级层级 [100, 50, 0]，每个层级有若干渠道：
- 重试 #0 → 选择优先级 100 中的渠道
- 重试 #1 → 选择优先级 50 中的渠道
- 重试 #2 → 选择优先级 0 中的渠道
- 重试 #3+ → 仍选择优先级 0 中的渠道（N ≥ 层级数，使用最低优先级）

---

## 五、跨分组重试（Auto Group）

当 Token 的分组设置为 `"auto"` 且启用了 `CrossGroupRetry`，系统会依次尝试多个自动分组。

### 5.1 状态跟踪

使用两个 Context Key 跟踪跨分组状态：

| Context Key | 含义 |
|-------------|------|
| `ContextKeyAutoGroupIndex` | 当前正在重试的分组索引 |
| `ContextKeyAutoGroupRetryIndex` | 当前分组开始时的全局重试计数 |

### 5.2 重试流程示例（2 个分组，各 2 个优先级，RetryTimes=3）

```
Retry=0: 分组A, 优先级0 → 失败
Retry=1: 分组A, 优先级1 → 失败
Retry=2: 分组A 优先级用完 → 切换到分组B, 优先级0 → 失败
Retry=3: 分组B, 优先级1 → 失败/成功
```

### 5.3 切换分组的关键逻辑

当 `priorityRetry >= common.RetryTimes` 时（当前分组所有优先级已用完）：

```go
// 准备切换到下一个分组
common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i+1)
param.SetRetry(0)           // 重置重试计数器
param.ResetRetryNextTry()   // 跳过下次循环的递增（保持计数器为0）
```

---

## 六、重试判断逻辑（`shouldRetry`）

### 6.1 标准 Relay 判断 (`controller/relay.go:318-348`)

```go
func shouldRetry(c *gin.Context, err *types.NewAPIError, retryTimes int) bool
```

判断流程如下：

```
                ┌─────────────┐
                │ 错误为 nil？ │──是──→ 不重试
                └──────┬──────┘
                       │否
                ┌──────▼──────┐
                │ 渠道亲和性   │──是──→ 不重试
                │ 跳过重试？   │
                └──────┬──────┘
                       │否
                ┌──────▼──────┐
                │ 渠道错误？   │──是──→ 重试！
                │(code前缀     │
                │ "channel:") │
                └──────┬──────┘
                       │否
                ┌──────▼──────┐
                │ 标记跳过重试？│──是──→ 不重试
                │(skipRetry)  │
                └──────┬──────┘
                       │否
                ┌──────▼──────┐
                │ 剩余重试次数 │──≤0──→ 不重试
                │    > 0？    │
                └──────┬──────┘
                       │是
                ┌──────▼──────┐
                │ 指定了特定   │──是──→ 不重试
                │ 渠道ID？    │
                └──────┬──────┘
                       │否
                ┌──────▼──────┐
                │ 2xx 状态码？ │──是──→ 不重试
                └──────┬──────┘
                       │否
                ┌──────▼──────┐
                │ <100 或     │──是──→ 重试！
                │ >599？      │
                └──────┬──────┘
                       │否
                ┌──────▼──────┐
                │ 总是跳过？   │──是──→ 不重试
                │(504/524/    │
                │ BadResponse │
                │  Body)      │
                └──────┬──────┘
                       │否
                ┌──────▼──────┐
                │ 状态码在    │──是──→ 重试！
                │ 重试范围内？ │
                └──────┬──────┘
                       │否
                       ↓
                    不重试
```

### 6.2 Task 重试判断 (`controller/relay.go:607-647`)

`shouldRetryTaskRelay()` 的差异：

| 条件 | 标准 Relay | Task Relay |
|------|-----------|------------|
| channel error | 重试 | N/A（Task 使用不同的错误结构） |
| 429 Too Many Requests | 通过状态码范围判断 | **明确重试** |
| 307 Temporary Redirect | 通过状态码范围判断 | **明确重试** |
| 5xx（除 504/524） | 通过状态码范围判断 | **明确重试** |
| 400 Bad Request | 不重试（不在范围内） | **明确不重试** |
| 408 Request Timeout | 不重试（不在范围内） | **明确不重试** |
| LocalError | N/A | **明确不重试** |

---

## 七、错误类型体系

### 7.1 NewAPIError 结构

```go
type NewAPIError struct {
    Err            error        // 底层错误
    RelayError     any          // 上游原始错误对象
    skipRetry      bool         // 是否跳过重试（关键标记）
    recordErrorLog *bool        // 是否记录错误日志
    errorType      ErrorType    // 错误类型
    errorCode      ErrorCode    // 错误码
    StatusCode     int          // HTTP 状态码
    Metadata       json.RawMessage
}
```

### 7.2 关键判断方法

| 方法 | 判断逻辑 | 用途 |
|------|----------|------|
| `IsChannelError(err)` | `strings.HasPrefix(err.errorCode, "channel:")` | 判断是否为渠道级错误 |
| `IsSkipRetryError(err)` | `err.skipRetry == true` | 判断是否标记为不可重试 |
| `IsRecordErrorLog(err)` | `err.recordErrorLog != false` | 判断是否记录错误日志 |

### 7.3 错误码分类与重试行为

| 错误类别 | 示例错误码 | 重试行为 |
|----------|-----------|----------|
| **渠道错误** (前缀 `channel:`) | `channel:no_available_key`, `channel:invalid_key`, `channel:response_time_exceeded`, `channel:param_override_invalid` 等 | **总是重试**（由 `IsChannelError()` 判断） |
| **中继错误** | `invalid_request`, `get_channel_failed`, `gen_relay_info_failed`, `do_request_failed`, `json_marshal_failed`, `model_price_error`, `count_token_failed` | **不重试**（均使用 `ErrOptionWithSkipRetry()` 标记） |
| **请求错误** | `read_request_body_failed`, `convert_request_failed`, `access_denied` | **不重试**（均使用 `ErrOptionWithSkipRetry()` 标记） |
| **配额错误** | `insufficient_user_quota`, `pre_consume_token_quota_failed` | **不重试** |
| **响应错误** | `bad_response_status_code`, `bad_response`, `bad_response_body`, `empty_response`, `prompt_blocked`, `model_not_found` | **取决于上游 HTTP 状态码**（如 429 则重试，400 则不重试） |

### 7.4 上游错误处理流程

```
上游 HTTP 响应 → service.RelayErrorHandler() → 解析响应体
    ├── 成功解析 → 提取 HTTP Status Code
    │   └── 创建 NewAPIError(code="bad_response_status_code", statusCode=上游状态码)
    └── 解析失败 → 创建 NewAPIError(code="bad_response_body")
```

上游返回的具体 HTTP 状态码决定了重试行为：
- 如上游返回 429 → 状态码在重试范围内 → **重试**
- 如上游返回 400 → 状态码不在重试范围内 → **不重试**
- 如上游返回 504 → 硬编码永远不重试 → **不重试**

---

## 八、错误处理副作用

### 8.1 processChannelError (`controller/relay.go:350-395`)

每次上游失败后调用，执行两项异步操作：

1. **自动禁用渠道**（如果 `ShouldDisableChannel(err)` 且 `AutoBan` 为 true）
2. **记录错误日志**（如果 `ErrorLogEnabled` 为 true）

### 8.2 渠道自动禁用判断 (`service/channel.go:45-65`)

`ShouldDisableChannel()` 返回 true 的条件（需同时满足 `AutomaticDisableChannelEnabled = true`）：

```
        ┌─────────────────────┐
        │ 自动禁用功能已启用？ │──否──→ 不禁用
        └──────────┬──────────┘
                   │是
        ┌──────────▼──────────┐
        │ 渠道错误类型？       │──是──→ 禁用！
        │ ("channel:" 前缀)   │
        └──────────┬──────────┘
                   │否
        ┌──────────▼──────────┐
        │ 已标记跳过重试？     │──是──→ 不禁用
        └──────────┬──────────┘
                   │否
        ┌──────────▼──────────┐
        │ 状态码在禁用范围内？ │──是──→ 禁用！
        │ (默认仅 401)        │
        └──────────┬──────────┘
                   │否
        ┌──────────▼──────────┐
        │ 错误消息匹配禁用     │──是──→ 禁用！
        │ 关键词？(AC自动机)  │
        └──────────┬──────────┘
                   │否
                   ↓
                不禁用
```

### 8.3 DisableChannel 执行

```go
func DisableChannel(channelError types.ChannelError, reason string) {
    // 只在 channelError.AutoBan == true 时执行
    model.UpdateChannelStatus(channelId, usingKey, common.ChannelStatusAutoDisabled, reason)
    // 通知 root 用户
    NotifyRootUser(...)
}
```

渠道状态变更为 `ChannelStatusAutoDisabled = 3`（不同于手动禁用 `ChannelStatusManuallyDisabled = 2`）。

---

## 九、渠道亲和性（Channel Affinity）与重试

### 9.1 概念

渠道亲和性允许系统记住某类请求最近成功使用的渠道，并在后续同类请求中优先路由到同一渠道，提高缓存命中率。

### 9.2 与重试的互动

1. **分发阶段**：`Distribute()` 中间件先检查渠道亲和性缓存，如果命中且渠道可用，优先使用该渠道
2. **禁用渠道时**：如果亲和渠道被禁用且规则中 `SkipRetryOnFailure = true`：
   - **直接返回错误**，不进入重试循环
   - 由 `ShouldSkipRetryAfterChannelAffinityFailure()` 判断
3. **重试循环中**：`shouldRetry()` 也检查 `ShouldSkipRetryAfterChannelAffinityFailure()`，防止在亲和性规则要求不重试时仍然重试

### 9.3 跳过重试的条件

```go
func ShouldSkipRetryAfterChannelAffinityFailure(c *gin.Context) bool {
    // 1. 检查上下文标记
    if skipRetry, ok := c.Get(ginKeyChannelAffinitySkipRetry); ok {
        return skipRetry.(bool)
    }
    // 2. 检查规则配置
    meta, ok := getChannelAffinityMeta(c)
    return ok && meta.SkipRetry
}
```

---

## 十、请求体复用机制

在重试循环中，HTTP 请求体只能读取一次。系统通过 `common.GetBodyStorage(c)` 实现请求体复用：

```go
func GetBodyStorage(c *gin.Context) (*BodyStorage, error) {
    // 首次调用：读取请求体并缓存
    // 后续调用：返回缓存的副本
}
```

重试时从缓存中恢复请求体：

```go
c.Request.Body = io.NopCloser(bodyStorage)
```

如果请求体超过 `MAX_REQUEST_BODY_MB` 限制（默认 32MB），直接返回 413 错误且**不重试**。

---

## 十一、计费与退款

### 11.1 预扣费

在进入重试循环之前，系统执行预扣费：

```go
newAPIError = service.PreConsumeBilling(c, priceData.QuotaToPreConsume, relayInfo)
if newAPIError != nil { return }  // 预扣费失败，不进入重试
```

### 11.2 失败退款

重试循环的 `defer` 块处理失败后退款：

```go
defer func() {
    if newAPIError != nil {
        relayInfo.Billing.Refund(c)    // 退还预扣额度
    }
}()
```

### 11.3 违规费用

如果上游返回特定错误（如 Grok CSAM），可能触发额外扣费：

```go
service.ChargeViolationFeeIfNeeded(c, relayInfo, newAPIError)
```

---

## 十二、状态码范围配置解析

系统提供灵活的状态码范围配置语法，支持在 UI 中自定义：

**格式**：逗号分隔的范围，如 `401-407,409-499,500-503`

**解析规则** (`setting/operation_setting/status_code_ranges.go:117-169`)：
1. 支持单个状态码：`401`
2. 支持范围：`401-407`
3. 自动合并相邻/重叠范围
4. 自动排序
5. 全角逗号 `，` 自动转换为半角 `,`
6. 状态码必须在 100-599 范围内

---

## 十三、总结与建议

### 13.1 当前重试机制特点

| 特性 | 描述 |
|------|------|
| **重试策略** | 基于优先级分层的多渠道重试，每次尝试不同渠道 |
| **重试延迟** | **无延迟**，立即重试下一个渠道 |
| **退避策略** | **无**（无 exponential backoff 或 jitter） |
| **熔断机制** | **无**（通过自动禁用渠道实现类似效果） |
| **渠道切换** | 每次重试切换到下一个优先级层级的渠道 |
| **跨分组** | auto 分组支持跨分组重试 |
| **亲和性** | 支持渠道亲和性，可配置跳过重试 |

### 13.2 可改进的方向

1. **增加重试延迟**：当前无任何延迟，可能导致短时间内对多个上游同时发起请求。建议增加可配置的退避策略
2. **增加熔断器**：考虑引入 Circuit Breaker 模式，当某个渠道连续失败时暂时跳过
3. **增加重试延迟配置**：允许管理员配置重试间隔（ms），支持线性或指数退避
4. **增强重试监控**：增加重试次数、重试原因的指标导出（Prometheus/OpenTelemetry）
5. **增加渠道健康度评分**：基于历史成功率动态调整渠道优先级

### 13.3 关键调试入口

| 目的 | 文件:行号 | 函数 |
|------|----------|------|
| 重试循环开始 | `controller/relay.go:189` | `Relay()` |
| 重试判断 | `controller/relay.go:318` | `shouldRetry()` |
| 渠道选择 | `service/channel_select.go:83` | `CacheGetRandomSatisfiedChannel()` |
| 渠道禁用判断 | `service/channel.go:45` | `ShouldDisableChannel()` |
| 状态码范围判断 | `setting/operation_setting/status_code_ranges.go:80` | `ShouldRetryByStatusCode()` |
| 上游错误处理 | `service/error.go` | `RelayErrorHandler()` |
