package handler

import (
	"strings"
	"testing"
	"time"

	"cyberstrike-ai/internal/config"
	"cyberstrike-ai/internal/database"
)

func TestClassifyAssetFreshness(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-6 * 24 * time.Hour)
	stale := now.Add(-8 * 24 * time.Hour)
	if got := classifyAssetFreshness(&fresh, now, 7); got != assetFresh {
		t.Fatalf("fresh got %s", got)
	}
	if got := classifyAssetFreshness(&stale, now, 7); got != assetStale {
		t.Fatalf("stale got %s", got)
	}
	if got := classifyAssetFreshness(nil, now, 7); got != assetNeverScanned {
		t.Fatalf("never got %s", got)
	}
}

func TestAssetDiscoveryRequestAndForceRefreshDetection(t *testing.T) {
	if !isAssetDiscoveryRequest("信息收集", "请整理目标暴露面") {
		t.Fatal("information collection role must be recognized")
	}
	if !isForceRefreshRequest("忽略缓存，强制刷新全部资产") {
		t.Fatal("force refresh must be recognized")
	}
}

func TestBuildAssetDiscoveryContextSeparatesFreshAndStale(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	freshTime := now.Add(-2 * 24 * time.Hour)
	staleTime := now.Add(-9 * 24 * time.Hour)
	text := buildAssetDiscoveryContext([]*database.Asset{
		{Domain: "fresh.example", Port: 443, Protocol: "https", LastScanAt: &freshTime},
		{Domain: "stale.example", Port: 80, Protocol: "http", LastScanAt: &staleTime},
	}, config.AssetDiscoveryConfig{ScanFreshDays: 7}, now, false)
	for _, want := range []string{"fresh.example:443/https", "复用", "stale.example:80/http", "允许扫描"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
}
