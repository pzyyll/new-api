// ABOUTME: Coordinates channel selection state across relay attempts and auto groups.
// ABOUTME: Excludes attempted channels so retries exhaust each priority before falling back.
package service

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

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
		)
		return channel, selectGroup, err
	}

	if len(setting.GetAutoGroups()) == 0 {
		return nil, selectGroup, errors.New("auto groups is not enabled")
	}

	userGroup := common.GetContextKeyString(param.Ctx, constant.ContextKeyUserGroup)
	autoGroups := GetUserAutoGroup(userGroup)
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
