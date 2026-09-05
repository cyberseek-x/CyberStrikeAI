package app

import (
	"fmt"
	"net"
	"strings"

	"cyberstrike-ai/internal/config"
	"cyberstrike-ai/internal/database"
)

func applyAssetDiscoveryPolicy(asset *database.Asset, cfg config.AssetDiscoveryConfig) (*config.AssetDiscoveryExcludedIPRange, error) {
	if asset == nil {
		return nil, fmt.Errorf("资产不能为空")
	}
	for _, rule := range cfg.ExcludedIPRanges {
		if !rule.Enabled {
			continue
		}
		_, network, err := net.ParseCIDR(strings.TrimSpace(rule.CIDR))
		if err != nil {
			continue
		}
		matched := false
		if ip := net.ParseIP(strings.TrimSpace(asset.IP)); ip != nil && network.Contains(ip) {
			asset.IP = ""
			matched = true
		}
		if ip := net.ParseIP(strings.TrimSpace(asset.Host)); ip != nil && network.Contains(ip) {
			asset.Host = ""
			matched = true
		}
		if matched {
			if strings.TrimSpace(asset.Domain) == "" && strings.TrimSpace(asset.Host) == "" && strings.TrimSpace(asset.IP) == "" {
				return &rule, fmt.Errorf("IP 命中排除地址段 %s，且没有可保存的域名或主机", rule.CIDR)
			}
			return &rule, nil
		}
	}
	return nil, nil
}
