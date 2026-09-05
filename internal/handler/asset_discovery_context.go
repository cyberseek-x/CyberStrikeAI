package handler

import (
	"fmt"
	"strings"
	"time"

	"cyberstrike-ai/internal/config"
	"cyberstrike-ai/internal/database"

	"go.uber.org/zap"
)

type assetFreshness string

const (
	assetFresh        assetFreshness = "fresh"
	assetStale        assetFreshness = "stale"
	assetNeverScanned assetFreshness = "never"
)

func classifyAssetFreshness(lastScanAt *time.Time, now time.Time, freshDays int) assetFreshness {
	if lastScanAt == nil || lastScanAt.IsZero() {
		return assetNeverScanned
	}
	if freshDays <= 0 {
		freshDays = config.DefaultAssetScanFreshDays
	}
	if now.Sub(*lastScanAt) < time.Duration(freshDays)*24*time.Hour {
		return assetFresh
	}
	return assetStale
}

func isAssetDiscoveryRequest(role, message string) bool {
	role = strings.ToLower(strings.TrimSpace(role))
	message = strings.ToLower(strings.TrimSpace(message))
	if strings.Contains(role, "信息收集") || strings.Contains(role, "资产发现") {
		return true
	}
	for _, keyword := range []string{"暴露面", "攻击面", "资产发现", "资产测绘", "子域名", "信息收集", "域名枚举"} {
		if strings.Contains(message, keyword) {
			return true
		}
	}
	return false
}

func isForceRefreshRequest(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	for _, keyword := range []string{"重新扫描", "重新探测", "强制刷新", "强制重扫", "忽略缓存", "不使用缓存", "force refresh", "rescan"} {
		if strings.Contains(message, keyword) {
			return true
		}
	}
	return false
}

func assetEndpointLabel(asset *database.Asset) string {
	if asset == nil {
		return ""
	}
	target := strings.TrimSpace(asset.Domain)
	if target == "" {
		target = strings.TrimSpace(asset.Host)
	}
	if target == "" {
		target = strings.TrimSpace(asset.IP)
	}
	target = strings.NewReplacer("\n", "", "\r", "", "\t", " ").Replace(target)
	protocol := strings.TrimSpace(asset.Protocol)
	if protocol == "" {
		protocol = "unknown"
	}
	return fmt.Sprintf("%s:%d/%s", target, asset.Port, protocol)
}

func buildAssetDiscoveryContext(assets []*database.Asset, cfg config.AssetDiscoveryConfig, now time.Time, forceRefresh bool) string {
	freshDays := cfg.ScanFreshDays
	if freshDays <= 0 {
		freshDays = config.DefaultAssetScanFreshDays
	}
	var b strings.Builder
	b.WriteString("[资产发现执行策略]\n")
	b.WriteString(fmt.Sprintf("扫描结果复用期：%d 天。", freshDays))
	if forceRefresh {
		b.WriteString("用户已明确要求重新扫描，本轮允许绕过复用期。\n")
	} else {
		b.WriteString("复用期内的现有端点不得重复主动探测。\n")
	}
	for _, asset := range assets {
		label := assetEndpointLabel(asset)
		if label == "" {
			continue
		}
		switch classifyAssetFreshness(asset.LastScanAt, now, freshDays) {
		case assetFresh:
			if forceRefresh {
				b.WriteString("- " + label + "：已有新鲜结果，但本轮允许重新扫描\n")
			} else {
				b.WriteString("- " + label + "：复用现有结果\n")
			}
		case assetStale:
			b.WriteString("- " + label + "：结果已过期，允许扫描\n")
		default:
			b.WriteString("- " + label + "：从未扫描，允许扫描\n")
		}
	}
	b.WriteString("已验证的新端点必须逐条调用 create_asset；服务端会自动绑定当前项目、去重并过滤排除地址段。最终报告必须列出新增、更新、复用、重扫、跳过和失败数量。")
	return b.String()
}

func (h *AgentHandler) assetDiscoveryContext(projectID, role, message string) string {
	if h == nil || h.db == nil || h.config == nil || strings.TrimSpace(projectID) == "" || !isAssetDiscoveryRequest(role, message) {
		return ""
	}
	assets, _, err := h.db.ListAssetsForOperation(500, database.AssetListFilter{ProjectID: strings.TrimSpace(projectID)}, database.RBACListAccess{Scope: database.RBACScopeAll})
	if err != nil {
		if h.logger != nil {
			h.logger.Warn("加载项目资产复用上下文失败", zap.Error(err))
		}
		return ""
	}
	return buildAssetDiscoveryContext(assets, h.config.AssetDiscovery, time.Now(), isForceRefreshRequest(message))
}
