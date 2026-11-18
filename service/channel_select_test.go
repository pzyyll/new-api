// ABOUTME: Verifies retry-aware channel selection across auto groups.
// ABOUTME: Covers untried-channel ordering and multi-key fallback after cross-group failures.
package service

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCacheGetRandomSatisfiedChannelReturnsToEarlierMultiKeyAutoGroup(t *testing.T) {
	originalDB := model.DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalAutoGroups := setting.AutoGroups2JsonString()
	originalUserUsableGroups := setting.UserUsableGroups2JSONString()
	originalGroupRatios := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		model.DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUserUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroupRatios))
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["group-a","group-b"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","group-a":"A","group-b":"B"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"group-a":1,"group-b":1}`))

	common.MemoryCacheEnabled = false
	highPriority := int64(200)
	lowPriority := int64(100)
	weight := uint(1)
	multiKey := &model.Channel{
		Status:   common.ChannelStatusEnabled,
		Name:     "group-a-multi-key",
		Key:      "key-a\nkey-b",
		Models:   "gpt-4o",
		Group:    "group-a",
		Priority: &highPriority,
		Weight:   &weight,
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 2,
		},
	}
	require.NoError(t, multiKey.Insert())
	groupB := &model.Channel{
		Status:   common.ChannelStatusEnabled,
		Name:     "group-b-single-key",
		Key:      "key-c",
		Models:   "gpt-4o",
		Group:    "group-b",
		Priority: &lowPriority,
		Weight:   &weight,
	}
	require.NoError(t, groupB.Insert())
	common.MemoryCacheEnabled = true
	model.InitChannelCache()

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyAutoGroup, "group-a")
	common.SetContextKey(ctx, constant.ContextKeyAutoGroupIndex, 0)
	common.SetContextKey(ctx, constant.ContextKeyTokenCrossGroupRetry, true)

	retryParam := &RetryParam{
		Ctx:                ctx,
		TokenGroup:         "auto",
		ModelName:          "gpt-4o",
		RequestPath:        ctx.Request.URL.Path,
		Retry:              common.GetPointer(1),
		ExcludedChannelIds: map[int]struct{}{multiKey.Id: {}},
	}

	selected, selectedGroup, err := CacheGetRandomSatisfiedChannel(retryParam)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, groupB.Id, selected.Id)
	assert.Equal(t, "group-b", selectedGroup)

	retryParam.ExcludeChannel(groupB.Id)
	retryParam.IncreaseRetry()
	selected, selectedGroup, err = CacheGetRandomSatisfiedChannel(retryParam)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, multiKey.Id, selected.Id)
	assert.Equal(t, "group-a", selectedGroup)
}

func TestSharedNativeRouteIdentityFilterContainsAllCandidateKeys(t *testing.T) {
	registry := jsplugin.NewRegistry()
	for _, key := range []string{"route-alpha", "route-beta"} {
		_, err := registry.Register(fmt.Sprintf(`
export const meta = {apiVersion: 1, key: %q, name: %q, version: "1.0.0", author: {name: "Test"}, models: ["task-model"], fetchMode: "per_task",
	routes: [{method: "POST", path: "/probe/v1/jobs", type: "submit", decode: "decode", render: "render"}]};
export const native = {decode() { return {}; }, render() { return {}; }};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {}; }
`, key, key), jsplugin.Options{})
		require.NoError(t, err)
	}
	generation := registry.Generation()
	candidates := generation.LookupDeclaredRoutes("POST", "/probe/v1/jobs")
	require.Len(t, candidates, 2)
	c, _ := gin.CreateTestContext(nil)
	c.Set(jsplugin.ContextKeyPinnedRoute, jsplugin.PinnedRoute{Generation: generation, Plugin: candidates[0].Plugin, Route: candidates[0].Route, Candidates: candidates})
	AppendTaskPluginIdentityFilter(c, "route-alpha")
	filters := GetChannelConstraints(c).Filters
	require.Len(t, filters, 1)
	assert.Equal(t, "route-alpha", filters[0].TaskPluginKey)
	assert.Equal(t, []string{"route-alpha", "route-beta"}, filters[0].TaskPluginKeys)
}
