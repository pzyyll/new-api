# Per-Channel User-Agent Filtering

**Date:** 2026-04-10
**Status:** Approved

## Goal

Allow administrators to configure which clients (identified by `User-Agent` header) can use a specific channel. When a channel has a User-Agent allow-list configured, only requests from matching clients are routed to that channel. Channels without this field configured continue to accept all clients (backwards-compatible).

## Use Case

An admin wants to dedicate certain channels to specific tooling. For example:
- A channel configured with `codex*` only serves requests from Codex CLI clients.
- A channel configured with `claude-code*` only serves requests from Claude Code clients.
- Channels without a User-Agent filter serve all clients as before.

## Design

### 1. Data Model

**File:** `model/channel.go`

Add a new field to the `Channel` struct:

```go
UserAgent *string `json:"user_agent" gorm:"type:varchar(512);default:''"`
```

- **Storage format:** Comma-separated glob patterns (e.g., `codex*,claude-code*`)
- **Semantics:** If nil or empty, accept all clients. If non-empty, at least one pattern must match the client's `User-Agent` for the channel to be eligible.
- **Migration:** Automatic via GORM `AutoMigrate` (already invoked for `Channel` in `model/main.go`).

### 2. Matching Logic

**File:** `model/channel.go`

Add a method:

```go
func (channel *Channel) MatchUserAgent(clientUA string) bool
```

**Algorithm:**
1. If `channel.UserAgent` is nil or the trimmed value is empty, return `true` (accept all).
2. Split by comma and trim whitespace from each pattern.
3. For each pattern, perform a case-insensitive glob match against `clientUA`.
4. Return `true` on first match; `false` if no pattern matches.

**Glob semantics:**
- `*` matches any sequence of characters (including empty).
- `?` matches exactly one character.
- Case-insensitive: both the pattern and the UA are lowercased before matching.
- Use Go's `path.Match` with lowercased inputs. Note: `path.Match` requires `*` to not match path separators, but User-Agent strings don't contain `/` in the relevant prefix, so this is acceptable. Alternatively, implement a simple custom glob that treats `*` as matching everything.

**Decision:** Use a simple custom glob function rather than `path.Match`, because `path.Match` treats `*` as not matching `/`, which could cause unexpected behavior with User-Agent strings like `codex-cli/1.0.0`. The custom function:
- Lowercases both pattern and input.
- `*` matches any sequence of zero or more characters (including `/`).
- `?` matches exactly one character.

### 3. Channel Selection Integration

**File:** `model/channel_cache.go` — `GetRandomSatisfiedChannel`

**Change:** Add a `userAgent string` parameter. After collecting candidate channels for a priority tier, filter out channels where `MatchUserAgent(userAgent)` returns `false`.

**Detailed flow:**
1. Existing logic collects `channels` (list of channel IDs) for the group+model.
2. Existing logic iterates priority tiers.
3. **New:** When building `targetChannels` for a priority tier, skip channels where `channelsIDM[channelId].MatchUserAgent(userAgent)` is `false`.
4. If `targetChannels` is empty after filtering, fall through to the next priority tier (increment retry internally).
5. Weighted random selection proceeds over the filtered set.

**Also update:** `GetChannel` (the DB fallback when cache is disabled) to accept and apply the same `userAgent` filter.

### 4. Caller Updates

**File:** `service/channel_select.go` — `CacheGetRandomSatisfiedChannel`

Extract the User-Agent from `param.Ctx.Request.UserAgent()` and pass it to `model.GetRandomSatisfiedChannel`.

**File:** `middleware/distributor.go` — `Distribute()`

The `RetryParam` already carries `Ctx`, so no changes needed here — the User-Agent is extracted inside `CacheGetRandomSatisfiedChannel`.

### 5. Channel Affinity Check

**File:** `middleware/distributor.go` — inside the affinity block

After `GetPreferredChannelByAffinity` returns a preferred channel, verify `preferred.MatchUserAgent(c.Request.UserAgent())`. If it doesn't match, skip the affinity result and fall through to normal channel selection.

### 6. Frontend

**File:** `web/src/components/table/channels/modals/EditChannelModal.jsx`

Add a `UserAgent` text input field in the advanced settings section:
- Label: `User-Agent 过滤` (with i18n)
- Placeholder: `codex*,claude-code*`
- Help text: "Comma-separated glob patterns. If set, only clients with matching User-Agent can use this channel. Leave empty to accept all clients."
- The field binds to `channel.user_agent`.

### 7. Controller/API

**Files:** `controller/channel.go`

- `AddChannel` and `UpdateChannel`: The `UserAgent` field flows through naturally since it's on the `Channel` struct. No special handling needed.
- `SearchChannels` / `GetAllChannels`: No changes — the field is returned with the channel data.
- `EditChannelByTag`: Optionally support bulk-updating this field in a follow-up.

### 8. I18n

Add translation keys for the new UI label and help text to:
- `web/src/i18n/locales/zh-CN.json`
- `web/src/i18n/locales/en.json`
- Other locale files as needed.

## Edge Cases

1. **Empty User-Agent header:** If the client sends no User-Agent, `clientUA` will be `""`. A pattern like `codex*` won't match `""`, so channels with UA filters will be skipped. Channels without UA filters will still be available.

2. **All channels filtered out:** If all channels for a group+model have UA filters and none match, the request fails with the existing "no available channel" error. This is correct behavior.

3. **Affinity cache hit with UA mismatch:** If a cached affinity points to a channel whose UA filter doesn't match the current client, the affinity result is ignored and normal selection proceeds.

4. **Multi-key channels:** UA filtering happens at the channel level, before key selection. No interaction with multi-key logic.

## Non-Goals

- Block-list (exclude-by-UA) is not implemented. Only allow-list.
- Regex patterns are not supported. Only glob (`*`, `?`).
- This does not affect channels selected via specific token-channel binding (when a token is bound to a specific channel ID).
