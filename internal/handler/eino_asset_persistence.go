package handler

import (
	"fmt"
	"strings"

	"cyberstrike-ai/internal/config"
	"cyberstrike-ai/internal/database"
	"cyberstrike-ai/internal/mcp"
	"cyberstrike-ai/internal/mcp/builtin"
)

type assetPersistenceState struct {
	DiscoveryRequest        bool
	HasDiscoveryEvidence    bool
	HasSuccessfulAssetWrite bool
	Attempted               bool
}

func (s assetPersistenceState) ShouldRun() bool {
	return s.DiscoveryRequest && s.HasDiscoveryEvidence && !s.HasSuccessfulAssetWrite && !s.Attempted
}

var assetDiscoveryEvidenceTools = map[string]struct{}{
	"exec": {}, "execute": {}, "subfinder": {}, "amass": {}, "dnsenum": {}, "fierce": {}, "gau": {},
	"waybackurls": {}, "httpx": {}, "nmap": {}, "rustscan": {}, "wafw00f": {}, "naabu": {}, "masscan": {},
}

func shortToolName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if idx := strings.LastIndex(name, "::"); idx >= 0 {
		name = name[idx+2:]
	}
	return name
}

func successfulToolExecution(exec *mcp.ToolExecution) bool {
	return exec != nil && strings.TrimSpace(exec.Status) == mcp.ToolExecutionStatusCompleted && exec.Result != nil && !exec.Result.IsError
}

func inspectAssetPersistenceState(db *database.DB, executionIDs []string, discoveryRequest, attempted bool) assetPersistenceState {
	state := assetPersistenceState{DiscoveryRequest: discoveryRequest, Attempted: attempted}
	if db == nil {
		return state
	}
	for _, id := range uniqueNonEmptyStrings(executionIDs) {
		exec, err := db.GetToolExecution(id)
		if err != nil || !successfulToolExecution(exec) {
			continue
		}
		name := shortToolName(exec.ToolName)
		if name == builtin.ToolCreateAsset {
			state.HasSuccessfulAssetWrite = true
		}
		if _, ok := assetDiscoveryEvidenceTools[name]; ok {
			state.HasDiscoveryEvidence = true
		}
	}
	return state
}

func buildEinoSingleAssetPersistenceRun(source *config.Config, maxIterations int) (*config.Config, []string) {
	if source == nil {
		return nil, nil
	}
	if maxIterations < 2 {
		maxIterations = 2
	}
	finalCfg := *source
	finalCfg.Agent = source.Agent
	finalCfg.Agent.MaxIterations = maxIterations
	finalCfg.MultiAgent = source.MultiAgent
	finalCfg.MultiAgent.EinoSkills = source.MultiAgent.EinoSkills
	finalCfg.MultiAgent.EinoSkills.Disable = true
	finalCfg.MultiAgent.EinoMiddleware = source.MultiAgent.EinoMiddleware
	finalCfg.MultiAgent.EinoMiddleware.ToolSearchEnable = false
	return &finalCfg, []string{
		builtin.ToolCreateAsset,
		builtin.ToolQueryAssets,
		builtin.ToolGetProjectFact,
		builtin.ToolListProjectFacts,
		builtin.ToolSearchProjectFacts,
	}
}

func assetPersistenceInstruction(projectID string) string {
	return fmt.Sprintf(`进入资产归档阶段。当前项目 ID 为 %s。禁止继续扫描或调用命令执行工具。先用 query_assets 检查现有资产，再根据此前已经完成的工具结果，把每个已验证端点按“域名或 IP + 端口 + 协议”逐条调用 create_asset 保存；不要保存仅来自历史记录但未在本轮验证的端点。完成后立即输出完整报告，并列出新增、更新、复用、重扫、跳过、过滤和失败数量。此归档阶段只执行一次。`, strings.TrimSpace(projectID))
}
