# Per-Channel User-Agent Filter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow admins to configure per-channel User-Agent glob patterns so that a channel is only eligible for requests from matching clients.

**Architecture:** New `user_agent` column on the `Channel` model stores comma-separated glob patterns. A `MatchUserAgent` method on `Channel` evaluates patterns (case-insensitive, `*`/`?` wildcards). `GetRandomSatisfiedChannel` filters candidate channels by calling `MatchUserAgent` before weighted-random selection. The affinity system also checks UA before accepting a cached channel. Frontend exposes the field in the channel edit modal.

**Tech Stack:** Go 1.22+, Gin, GORM, React 18, Semi Design UI

**Spec:** `docs/superpowers/specs/2026-04-10-channel-user-agent-filter-design.md`

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `model/channel.go` | Add `UserAgent` field to `Channel` struct; add `MatchUserAgent` method; add `GetUserAgentPatterns` helper; add `globMatch` utility function |
| Create | `model/channel_user_agent_test.go` | Unit tests for glob matching and `MatchUserAgent` |
| Modify | `model/channel_cache.go` | Add `userAgent` param to `GetRandomSatisfiedChannel`; filter candidates |
| Modify | `model/ability.go` | Add `userAgent` param to `GetChannel` (DB fallback); filter after selection |
| Modify | `service/channel_select.go` | Extract UA from `param.Ctx` and pass to `model.GetRandomSatisfiedChannel` |
| Modify | `middleware/distributor.go` | Add UA check after affinity lookup returns a preferred channel |
| Modify | `web/src/components/table/channels/modals/EditChannelModal.jsx` | Add `user_agent` input field in advanced settings |
| Modify | `web/src/i18n/locales/en.json` | Add English translations for new UI strings |

---

### Task 1: Add `UserAgent` Field and Glob Matching to Channel Model

**Files:**
- Modify: `model/channel.go:21-57` (Channel struct and methods)
- Create: `model/channel_user_agent_test.go`

- [ ] **Step 1: Write the failing tests**

Create `model/channel_user_agent_test.go`:

```go
// ABOUTME: Unit tests for per-channel User-Agent glob matching.
// ABOUTME: Tests the MatchUserAgent method and the underlying globMatch function.
package model

import "testing"

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern string
		input   string
		want    bool
	}{
		// Basic wildcard
		{"codex*", "codex-cli/1.0.0", true},
		{"codex*", "Codex-CLI/1.0.0", true}, // case-insensitive
		{"codex*", "my-codex-tool", false},   // no match, codex not at start
		{"*codex*", "my-codex-tool", true},   // match with surrounding wildcards

		// Question mark
		{"codex-?", "codex-a", true},
		{"codex-?", "codex-ab", false},

		// Exact match
		{"codex-cli", "codex-cli", true},
		{"codex-cli", "Codex-CLI", true}, // case-insensitive
		{"codex-cli", "codex-cli/1.0", false},

		// Star matches slash (unlike path.Match)
		{"codex*", "codex-cli/1.0.0/beta", true},

		// Empty pattern and input
		{"", "", true},
		{"*", "", true},
		{"*", "anything", true},
		{"?", "", false},

		// Multiple stars
		{"*codex*claude*", "my-codex-and-claude-tool", true},
		{"*codex*claude*", "my-claude-and-codex-tool", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.input, func(t *testing.T) {
			got := globMatch(tt.pattern, tt.input)
			if got != tt.want {
				t.Errorf("globMatch(%q, %q) = %v, want %v", tt.pattern, tt.input, got, tt.want)
			}
		})
	}
}

func TestChannelMatchUserAgent(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	tests := []struct {
		name      string
		userAgent *string
		clientUA  string
		want      bool
	}{
		{"nil field accepts all", nil, "anything", true},
		{"empty string accepts all", strPtr(""), "anything", true},
		{"whitespace-only accepts all", strPtr("  "), "anything", true},
		{"single pattern match", strPtr("codex*"), "codex-cli/1.0.0", true},
		{"single pattern no match", strPtr("codex*"), "claude-code/2.0", false},
		{"multiple patterns first matches", strPtr("codex*,claude-code*"), "codex-cli/1.0", true},
		{"multiple patterns second matches", strPtr("codex*,claude-code*"), "claude-code/2.0", true},
		{"multiple patterns none match", strPtr("codex*,claude-code*"), "curl/7.0", false},
		{"patterns with whitespace", strPtr(" codex* , claude-code* "), "codex-cli/1.0", true},
		{"empty client UA with pattern", strPtr("codex*"), "", false},
		{"empty client UA without pattern", nil, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := &Channel{UserAgent: tt.userAgent}
			got := ch.MatchUserAgent(tt.clientUA)
			if got != tt.want {
				t.Errorf("MatchUserAgent(%q) = %v, want %v", tt.clientUA, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd /mnt/f/workspace/new-api && go test ./model/ -run "TestGlobMatch|TestChannelMatchUserAgent" -v`
Expected: Compilation errors — `globMatch` and `UserAgent` field don't exist yet.

- [ ] **Step 3: Add the `UserAgent` field and implement matching**

In `model/channel.go`, add the `UserAgent` field to the `Channel` struct, right after the `Remark` field (line 50):

```go
	Remark            *string `json:"remark" gorm:"type:varchar(255)" validate:"max=255"`
	UserAgent         *string `json:"user_agent" gorm:"type:varchar(512);default:''"`
	// add after v0.8.5
```

Then add the following functions at the end of the file (before the closing of the file, after the `GetHeaderOverride` method around line 919):

```go
// GetUserAgentPatterns returns the parsed list of User-Agent glob patterns.
// Returns nil if the field is empty (accept all clients).
func (channel *Channel) GetUserAgentPatterns() []string {
	if channel.UserAgent == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*channel.UserAgent)
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, ",")
	patterns := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			patterns = append(patterns, p)
		}
	}
	if len(patterns) == 0 {
		return nil
	}
	return patterns
}

// MatchUserAgent returns true if the client's User-Agent matches at least one
// of the channel's configured User-Agent glob patterns. If no patterns are
// configured, all clients are accepted.
func (channel *Channel) MatchUserAgent(clientUA string) bool {
	patterns := channel.GetUserAgentPatterns()
	if patterns == nil {
		return true
	}
	for _, pattern := range patterns {
		if globMatch(pattern, clientUA) {
			return true
		}
	}
	return false
}

// globMatch performs a case-insensitive glob match.
// Supported wildcards: * (matches any sequence of zero or more characters,
// including /), ? (matches exactly one character).
func globMatch(pattern, s string) bool {
	pattern = strings.ToLower(pattern)
	s = strings.ToLower(s)
	return globMatchInner(pattern, s)
}

func globMatchInner(pattern, s string) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			// Skip consecutive stars
			for len(pattern) > 0 && pattern[0] == '*' {
				pattern = pattern[1:]
			}
			if len(pattern) == 0 {
				return true // trailing * matches everything
			}
			// Try matching the rest of the pattern at every position
			for i := 0; i <= len(s); i++ {
				if globMatchInner(pattern, s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
			pattern = pattern[1:]
			s = s[1:]
		default:
			if len(s) == 0 || pattern[0] != s[0] {
				return false
			}
			pattern = pattern[1:]
			s = s[1:]
		}
	}
	return len(s) == 0
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd /mnt/f/workspace/new-api && go test ./model/ -run "TestGlobMatch|TestChannelMatchUserAgent" -v`
Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add model/channel.go model/channel_user_agent_test.go
git commit -m "feat(channel): add UserAgent field and glob matching logic

Add a UserAgent field to the Channel model for per-channel client
filtering. Implement case-insensitive glob matching with * and ?
wildcards."
```

---

### Task 2: Integrate UA Filtering into Channel Selection (Cache Path)

**Files:**
- Modify: `model/channel_cache.go:96-191` (`GetRandomSatisfiedChannel`)
- Modify: `service/channel_select.go:83-162` (`CacheGetRandomSatisfiedChannel`)

- [ ] **Step 1: Modify `GetRandomSatisfiedChannel` to accept and apply `userAgent` filter**

In `model/channel_cache.go`, change the function signature at line 96 from:

```go
func GetRandomSatisfiedChannel(group string, model string, retry int) (*Channel, error) {
```

to:

```go
func GetRandomSatisfiedChannel(group string, model string, retry int, userAgent string) (*Channel, error) {
```

Then update the single-channel shortcut (line 118-123). Replace:

```go
	if len(channels) == 1 {
		if channel, ok := channelsIDM[channels[0]]; ok {
			return channel, nil
		}
		return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channels[0])
	}
```

with:

```go
	if len(channels) == 1 {
		if channel, ok := channelsIDM[channels[0]]; ok {
			if channel.MatchUserAgent(userAgent) {
				return channel, nil
			}
			return nil, nil
		}
		return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channels[0])
	}
```

Then in the target channel building loop (lines 147-156), add a UA filter. Replace:

```go
	for _, channelId := range channels {
		if channel, ok := channelsIDM[channelId]; ok {
			if channel.GetPriority() == targetPriority {
				sumWeight += channel.GetWeight()
				targetChannels = append(targetChannels, channel)
			}
		} else {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelId)
		}
	}
```

with:

```go
	for _, channelId := range channels {
		if channel, ok := channelsIDM[channelId]; ok {
			if channel.GetPriority() == targetPriority && channel.MatchUserAgent(userAgent) {
				sumWeight += channel.GetWeight()
				targetChannels = append(targetChannels, channel)
			}
		} else {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelId)
		}
	}
```

Also, the priority collection loop (lines 125-132) must only consider channels that match the UA, so the priority tiers are correct. Replace:

```go
	uniquePriorities := make(map[int]bool)
	for _, channelId := range channels {
		if channel, ok := channelsIDM[channelId]; ok {
			uniquePriorities[int(channel.GetPriority())] = true
		} else {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelId)
		}
	}
```

with:

```go
	uniquePriorities := make(map[int]bool)
	for _, channelId := range channels {
		if channel, ok := channelsIDM[channelId]; ok {
			if channel.MatchUserAgent(userAgent) {
				uniquePriorities[int(channel.GetPriority())] = true
			}
		} else {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelId)
		}
	}
	if len(uniquePriorities) == 0 {
		return nil, nil
	}
```

- [ ] **Step 2: Update `CacheGetRandomSatisfiedChannel` to pass the User-Agent**

In `service/channel_select.go`, at line 118 (inside the auto-groups loop), change:

```go
			channel, _ = model.GetRandomSatisfiedChannel(autoGroup, param.ModelName, priorityRetry)
```

to:

```go
			userAgent := ""
			if param.Ctx != nil && param.Ctx.Request != nil {
				userAgent = param.Ctx.Request.UserAgent()
			}
			channel, _ = model.GetRandomSatisfiedChannel(autoGroup, param.ModelName, priorityRetry, userAgent)
```

At line 156 (the non-auto path), change:

```go
		channel, err = model.GetRandomSatisfiedChannel(param.TokenGroup, param.ModelName, param.GetRetry())
```

to:

```go
		userAgent := ""
		if param.Ctx != nil && param.Ctx.Request != nil {
			userAgent = param.Ctx.Request.UserAgent()
		}
		channel, err = model.GetRandomSatisfiedChannel(param.TokenGroup, param.ModelName, param.GetRetry(), userAgent)
```

- [ ] **Step 3: Verify compilation**

Run: `cd /mnt/f/workspace/new-api && go build ./...`
Expected: Compiles successfully with no errors.

- [ ] **Step 4: Run existing tests to verify no regressions**

Run: `cd /mnt/f/workspace/new-api && go test ./model/ ./service/ ./middleware/ -v -count=1 2>&1 | tail -30`
Expected: All existing tests PASS.

- [ ] **Step 5: Commit**

```bash
git add model/channel_cache.go service/channel_select.go
git commit -m "feat(channel): filter channels by User-Agent during selection

GetRandomSatisfiedChannel now accepts a userAgent parameter and
excludes channels whose UA patterns don't match the client."
```

---

### Task 3: Integrate UA Filtering into DB Fallback Path

**Files:**
- Modify: `model/ability.go:106-144` (`GetChannel`)

- [ ] **Step 1: Add `userAgent` parameter to `GetChannel`**

In `model/ability.go`, change the function signature at line 106 from:

```go
func GetChannel(group string, model string, retry int) (*Channel, error) {
```

to:

```go
func GetChannel(group string, model string, retry int, userAgent string) (*Channel, error) {
```

After the channel is fetched from DB (line 142), add a UA check before returning. Replace:

```go
	err = DB.First(&channel, "id = ?", channel.Id).Error
	return &channel, err
```

with:

```go
	err = DB.First(&channel, "id = ?", channel.Id).Error
	if err != nil {
		return nil, err
	}
	if !channel.MatchUserAgent(userAgent) {
		return nil, nil
	}
	return &channel, nil
```

- [ ] **Step 2: Update the call site in `GetRandomSatisfiedChannel`**

In `model/channel_cache.go`, line 99, change:

```go
		return GetChannel(group, model, retry)
```

to:

```go
		return GetChannel(group, model, retry, userAgent)
```

- [ ] **Step 3: Verify compilation**

Run: `cd /mnt/f/workspace/new-api && go build ./...`
Expected: Compiles successfully.

- [ ] **Step 4: Commit**

```bash
git add model/ability.go model/channel_cache.go
git commit -m "feat(channel): add UA filtering to DB fallback channel selection

GetChannel (used when memory cache is disabled) now also filters by
User-Agent, consistent with the cache path."
```

---

### Task 4: Add UA Check to Channel Affinity Path

**Files:**
- Modify: `middleware/distributor.go:102-128` (affinity block in `Distribute`)

- [ ] **Step 1: Add UA check after affinity lookup**

In `middleware/distributor.go`, inside the affinity block starting at line 102, after the status check and before the group matching. Find the block (around line 104):

```go
					if err == nil && preferred != nil {
						if preferred.Status != common.ChannelStatusEnabled {
```

Add a UA check right after the status-enabled checks succeed. The entire affinity block (lines 102-128) should be updated so that the three branches that assign `channel = preferred` also verify UA match. The simplest approach is to add a single guard after the `preferred != nil` check. Replace the block:

```go
				if preferredChannelID, found := service.GetPreferredChannelByAffinity(c, modelRequest.Model, usingGroup); found {
					preferred, err := model.CacheGetChannel(preferredChannelID)
					if err == nil && preferred != nil {
						if preferred.Status != common.ChannelStatusEnabled {
							if service.ShouldSkipRetryAfterChannelAffinityFailure(c) {
								abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorChannelDisabled))
								return
							}
						} else if usingGroup == "auto" {
```

with:

```go
				if preferredChannelID, found := service.GetPreferredChannelByAffinity(c, modelRequest.Model, usingGroup); found {
					preferred, err := model.CacheGetChannel(preferredChannelID)
					if err == nil && preferred != nil && preferred.MatchUserAgent(c.Request.UserAgent()) {
						if preferred.Status != common.ChannelStatusEnabled {
							if service.ShouldSkipRetryAfterChannelAffinityFailure(c) {
								abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorChannelDisabled))
								return
							}
						} else if usingGroup == "auto" {
```

This way, if the preferred channel's UA filter doesn't match the client, the entire affinity block is skipped and normal channel selection proceeds.

- [ ] **Step 2: Verify compilation**

Run: `cd /mnt/f/workspace/new-api && go build ./...`
Expected: Compiles successfully.

- [ ] **Step 3: Commit**

```bash
git add middleware/distributor.go
git commit -m "feat(channel): check UA filter on affinity-cached channels

Skip affinity-cached channel if its User-Agent filter doesn't match
the current client, falling through to normal selection."
```

---

### Task 5: Add Frontend User-Agent Field

**Files:**
- Modify: `web/src/components/table/channels/modals/EditChannelModal.jsx`
- Modify: `web/src/i18n/locales/en.json`

- [ ] **Step 1: Add default value in `originInputs`**

In `EditChannelModal.jsx`, in the `originInputs` object (around line 172), add after the `settings` line (line 198):

```javascript
    user_agent: '',
```

So it reads:

```javascript
    settings: '',
    user_agent: '',
    // 仅 Vertex: 密钥格式（存入 settings.vertex_key_type）
```

- [ ] **Step 2: Add `user_agent` to the advanced settings auto-expand check**

In `EditChannelModal.jsx`, in the `hasAdvancedValues` calculation (around line 1003), add a new condition. After the line:

```javascript
        (data.remark && data.remark.trim()) ||
```

add:

```javascript
        (data.user_agent && data.user_agent.trim()) ||
```

- [ ] **Step 3: Add the input field in the advanced settings form**

In `EditChannelModal.jsx`, after the `remark` TextArea field (around line 2439), add the `user_agent` input:

```jsx
                  <Form.Input
                    field='user_agent'
                    label={t('User-Agent 过滤')}
                    placeholder='codex*,claude-code*'
                    showClear
                    onChange={(value) => handleInputChange('user_agent', value)}
                    extraText={t('逗号分隔的通配符模式。设置后，仅匹配的客户端 User-Agent 可使用此渠道。留空则允许所有客户端。')}
                  />
```

- [ ] **Step 4: Add English translations**

In `web/src/i18n/locales/en.json`, add these entries (in alphabetical order among existing keys):

```json
    "User-Agent 过滤": "User-Agent Filter",
    "逗号分隔的通配符模式。设置后，仅匹配的客户端 User-Agent 可使用此渠道。留空则允许所有客户端。": "Comma-separated glob patterns. If set, only clients with matching User-Agent can use this channel. Leave empty to accept all clients.",
```

- [ ] **Step 5: Verify the frontend builds**

Run: `cd /mnt/f/workspace/new-api/web && bun run build 2>&1 | tail -10`
Expected: Build succeeds.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/table/channels/modals/EditChannelModal.jsx web/src/i18n/locales/en.json
git commit -m "feat(frontend): add User-Agent filter field to channel edit modal

Adds a text input for comma-separated glob patterns in the advanced
settings section. Includes English translations."
```

---

### Task 6: Final Verification

**Files:** None (verification only)

- [ ] **Step 1: Run all Go tests**

Run: `cd /mnt/f/workspace/new-api && go test ./... 2>&1 | tail -30`
Expected: All tests PASS.

- [ ] **Step 2: Run the full frontend build**

Run: `cd /mnt/f/workspace/new-api/web && bun run build 2>&1 | tail -10`
Expected: Build succeeds.

- [ ] **Step 3: Verify Go compilation**

Run: `cd /mnt/f/workspace/new-api && go build ./...`
Expected: Clean build, no errors.
