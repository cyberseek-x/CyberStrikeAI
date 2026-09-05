package handler

import (
	"os"
	"strings"
	"testing"
)

func TestAssetScanTaskOptionsContract(t *testing.T) {
	paths := []string{
		"../../web/templates/index.html",
		"../../web/static/js/assets.js",
		"../../web/static/css/style.css",
		"../../web/static/i18n/zh-CN.json",
		"../../web/static/i18n/en-US.json",
	}
	var source strings.Builder
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source.Write(contents)
	}

	for _, marker := range []string{
		`id="asset-scan-task-options"`,
		`id="asset-scan-agent-mode"`,
		`id="asset-scan-role"`,
		`id="asset-scan-concurrency"`,
		`min="1" max="8" value="2"`,
		`function prepareAssetScanTaskOptions`,
		`function assetScanConcurrencyValue`,
		`window.__csaiMultiAgentPublic`,
		`taskOptions.hidden = !taskMode`,
		`agentMode, role, concurrency`,
		`"scanAgentMode": "执行模式"`,
		`"scanRole": "专家角色"`,
		`"scanConcurrency": "并行任务数"`,
		`"scanAgentMode": "Execution mode"`,
		`"scanRole": "Expert role"`,
		`"scanConcurrency": "Parallel tasks"`,
		`.asset-scan-task-options`,
	} {
		if !strings.Contains(source.String(), marker) {
			t.Fatalf("missing %s", marker)
		}
	}
}

