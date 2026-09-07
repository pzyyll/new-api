// ABOUTME: Coordinates channel selection state across relay attempts and auto groups.
// ABOUTME: Excludes attempted channels so retries exhaust each priority before falling back.
package service

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/gin-gonic/gin"
)

func GetChannelConstraints(c *gin.Context) *dto.ChannelConstraints {
	if c == nil {
		return &dto.ChannelConstraints{}
	}
	if existing, ok := common.GetContextKeyType[*dto.ChannelConstraints](c, constant.ContextKeyChannelConstraints); ok && existing != nil {
		return existing
	}
	constraints := &dto.ChannelConstraints{}
	common.SetContextKey(c, constant.ContextKeyChannelConstraints, constraints)
	return constraints
}

func AppendTaskPluginIdentityFilter(c *gin.Context, pluginKey string) {
	if c == nil {
		return
	}
	GetChannelConstraints(c).AddFilter(dto.ChannelFilter{
		Kind:                   dto.FilterTaskPluginIdentity,
		TaskPluginKey:          pluginKey,
		TaskPluginChannelTypes: pinnedTaskPluginChannelTypes(c, pluginKey),
	})
}

type RetryParam struct {
	Ctx                    *gin.Context
	TokenGroup             string
	ModelName              string
	RequestPath            string
	Retry                  *int
	ExcludedChannelIds     map[int]struct{}
	autoGroupStartIndex    int
	autoGroupStartIndexSet bool
}

func (p *RetryParam) GetRetry() int {
	if p.Retry == nil {
		return 0
	}
	return *p.Retry
}

func (p *RetryParam) IncreaseRetry() {
	if p.Retry == nil {
		p.Retry = new(int)
	}
	*p.Retry++
}

func (p *RetryParam) ExcludeChannel(channelId int) {
	if p.ExcludedChannelIds == nil {
		p.ExcludedChannelIds = make(map[int]struct{})
	}
	p.ExcludedChannelIds[channelId] = struct{}{}
}

func (p *RetryParam) HasExcludedChannels() bool {
	return len(p.ExcludedChannelIds) > 0
}

// CacheGetRandomSatisfiedChannel returns the highest-priority eligible channel
// that has not already been attempted by this request. The outer relay loop
// still enforces RetryTimes as the total retry limit.
func CacheGetRandomSatisfiedChannel(param *RetryParam) (*model.Channel, string, error) {
	selectGroup := param.TokenGroup
	userAgent := ""
	if param.Ctx != nil && param.Ctx.Request != nil {
		userAgent = param.Ctx.Request.UserAgent()
	}

	if param.TokenGroup != "auto" {
		channel, err := model.GetRandomSatisfiedChannel(
			param.TokenGroup,
			param.ModelName,
			param.ExcludedChannelIds,
			true,
			userAgent,
			param.RequestPath,
			GetChannelConstraints(param.Ctx).Filters...,
		)
		return channel, selectGroup, err
	}

	userGroup := common.GetContextKeyString(param.Ctx, constant.ContextKeyUserGroup)
	autoGroups := GetRequestAutoGroups(param.Ctx, userGroup)
	if len(autoGroups) == 0 {
		return nil, selectGroup, errors.New("auto groups is not enabled")
	}

	startGroupIndex := 0
	if lastGroupIndex, exists := common.GetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex); exists {
		if index, ok := lastGroupIndex.(int); ok {
			startGroupIndex = index
		}
	} else {
		currentGroup := common.GetContextKeyString(param.Ctx, constant.ContextKeyAutoGroup)
		for index, autoGroup := range autoGroups {
			if autoGroup == currentGroup {
				startGroupIndex = index
				break
			}
		}
	}

	if !param.autoGroupStartIndexSet {
		param.autoGroupStartIndex = startGroupIndex
		param.autoGroupStartIndexSet = true
	}

	endGroupIndex := len(autoGroups)
	crossGroupRetry := common.GetContextKeyBool(param.Ctx, constant.ContextKeyTokenCrossGroupRetry)
	if param.HasExcludedChannels() && !crossGroupRetry && startGroupIndex < endGroupIndex {
		endGroupIndex = startGroupIndex + 1
	}

	for groupIndex := startGroupIndex; groupIndex < endGroupIndex; groupIndex++ {
		autoGroup := autoGroups[groupIndex]
		logger.LogDebug(param.Ctx, "Auto selecting untried channel in group: %s, retry: %d", autoGroup, param.GetRetry())
		channel, err := model.GetRandomSatisfiedChannel(
			autoGroup,
			param.ModelName,
			param.ExcludedChannelIds,
			false,
			userAgent,
			param.RequestPath,
			GetChannelConstraints(param.Ctx).Filters...,
		)
		if err != nil {
			return nil, autoGroup, err
		}
		if channel == nil {
			continue
		}

		common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroup, autoGroup)
		common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, groupIndex)
		logger.LogDebug(param.Ctx, "Auto selected group: %s", autoGroup)
		return channel, autoGroup, nil
	}

	fallbackStartGroupIndex := param.autoGroupStartIndex
	fallbackEndGroupIndex := len(autoGroups)
	if param.HasExcludedChannels() && !crossGroupRetry && fallbackStartGroupIndex < fallbackEndGroupIndex {
		fallbackEndGroupIndex = fallbackStartGroupIndex + 1
	}
	for groupIndex := fallbackStartGroupIndex; groupIndex < fallbackEndGroupIndex; groupIndex++ {
		autoGroup := autoGroups[groupIndex]
		logger.LogDebug(param.Ctx, "Auto selecting multi-key fallback in group: %s, retry: %d", autoGroup, param.GetRetry())
		channel, err := model.GetRandomSatisfiedChannel(
			autoGroup,
			param.ModelName,
			param.ExcludedChannelIds,
			true,
			userAgent,
			param.RequestPath,
			GetChannelConstraints(param.Ctx).Filters...,
		)
		if err != nil {
			return nil, autoGroup, err
		}
		if channel == nil {
			continue
		}

		common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroup, autoGroup)
		common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, groupIndex)
		logger.LogDebug(param.Ctx, "Auto selected multi-key fallback group: %s", autoGroup)
		return channel, autoGroup, nil
	}

	return nil, selectGroup, nil
}

func pinnedTaskPluginChannelTypes(c *gin.Context, expected string) []int {
	if c == nil || expected == "" {
		return nil
	}
	if value, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint); exists {
		pinned, ok := value.(jsplugin.PinnedEndpoint)
		if ok && pinned.Generation != nil && len(pinned.Candidates) > 1 {
			expectedFound := false
			channelTypes := make([]int, 0, len(pinned.Candidates))
			seen := make(map[int]struct{}, len(pinned.Candidates))
			for _, candidate := range pinned.Candidates {
				if candidate.Plugin == nil {
					continue
				}
				if candidate.Plugin.Meta.Key == expected {
					expectedFound = true
				}
				for _, channelType := range candidate.Plugin.Meta.ChannelTypes {
					if channelType == 0 || channelType == constant.ChannelTypeTaskPlugin {
						continue
					}
					if _, duplicate := seen[channelType]; duplicate {
						continue
					}
					if plugin, indexed := pinned.Generation.GetByChannelType(channelType); indexed && plugin == candidate.Plugin {
						seen[channelType] = struct{}{}
						channelTypes = append(channelTypes, channelType)
					}
				}
			}
			if expectedFound {
				return channelTypes
			}
		}
	}
	value, exists := c.Get(jsplugin.ContextKeyPinnedPlugin)
	pinned, ok := value.(jsplugin.PinnedPlugin)
	if !exists || !ok || pinned.Generation == nil || pinned.Plugin == nil || pinned.Plugin.Meta.Key != expected {
		return nil
	}
	channelTypes := make([]int, 0, len(pinned.Plugin.Meta.ChannelTypes))
	for _, channelType := range pinned.Plugin.Meta.ChannelTypes {
		if channelType == 0 || channelType == constant.ChannelTypeTaskPlugin {
			continue
		}
		channelTypes = append(channelTypes, channelType)
	}
	if len(channelTypes) == 0 {
		return nil
	}
	return channelTypes
}
