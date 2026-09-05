package handler

import (
	"os"
	"strings"
	"testing"

	"cyberstrike-ai/internal/config"

	"gopkg.in/yaml.v3"
)

func TestAssetDiscoverySettingsContract(t *testing.T) {
	paths := []string{"../../web/templates/index.html", "../../web/static/js/settings.js"}
	var source strings.Builder
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source.Write(b)
	}
	for _, marker := range []string{
		`id="asset-discovery-fresh-days"`,
		`id="asset-discovery-excluded-ranges"`,
		`function renderAssetDiscoveryExcludedRanges`,
		`asset_discovery:`,
	} {
		if !strings.Contains(source.String(), marker) {
			t.Fatalf("missing %s", marker)
		}
	}
}

func TestUpdateAssetDiscoveryConfigWritesYAML(t *testing.T) {
	doc := newEmptyYAMLDocument()
	updateAssetDiscoveryConfig(doc, config.AssetDiscoveryConfig{
		ScanFreshDays: 7,
		ExcludedIPRanges: []config.AssetDiscoveryExcludedIPRange{{
			CIDR: "198.18.0.0/15", Reason: "代理映射地址", Enabled: true,
		}},
	})
	b, err := yaml.Marshal(doc.Content[0])
	if err != nil {
		t.Fatal(err)
	}
	var got config.Config
	if err := yaml.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.AssetDiscovery.ScanFreshDays != 7 {
		t.Fatalf("ScanFreshDays=%d, want 7", got.AssetDiscovery.ScanFreshDays)
	}
	if len(got.AssetDiscovery.ExcludedIPRanges) != 1 || !got.AssetDiscovery.ExcludedIPRanges[0].Enabled {
		t.Fatalf("unexpected ranges: %#v", got.AssetDiscovery.ExcludedIPRanges)
	}
}
