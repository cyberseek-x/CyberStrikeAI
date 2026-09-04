package handler

import (
	"context"
	"errors"
	"time"

	"cyberstrike-ai/internal/config"
	"cyberstrike-ai/internal/multiagent"
)

const (
	defaultEinoSingleTaskTimeoutMinutes          = 15
	defaultEinoSingleFinalizationReservedMinutes = 3
	defaultEinoSingleFinalizationMaxIterations   = 2
)

type einoSingleBudget struct {
	total                     time.Duration
	investigation             time.Duration
	finalization              time.Duration
	finalizationMaxIterations int
}

func resolveEinoSingleBudget(cfg config.AgentConfig) einoSingleBudget {
	totalMinutes := cfg.TaskTimeoutMinutes
	if totalMinutes <= 0 {
		totalMinutes = defaultEinoSingleTaskTimeoutMinutes
	}
	reserveMinutes := cfg.FinalizationReservedMinutes
	if reserveMinutes <= 0 {
		reserveMinutes = defaultEinoSingleFinalizationReservedMinutes
	}
	if totalMinutes < 2 {
		totalMinutes = 2
	}
	if reserveMinutes >= totalMinutes {
		reserveMinutes = totalMinutes - 1
	}
	finalIterations := cfg.FinalizationMaxIterations
	if finalIterations <= 0 {
		finalIterations = defaultEinoSingleFinalizationMaxIterations
	}

	return einoSingleBudget{
		total:                     time.Duration(totalMinutes) * time.Minute,
		investigation:             time.Duration(totalMinutes-reserveMinutes) * time.Minute,
		finalization:              time.Duration(reserveMinutes) * time.Minute,
		finalizationMaxIterations: finalIterations,
	}
}

func shouldForceEinoSingleReport(runErr, taskCtxErr error) bool {
	return multiagent.IsEinoIterationLimitError(runErr) ||
		errors.Is(runErr, context.DeadlineExceeded) ||
		errors.Is(taskCtxErr, context.DeadlineExceeded)
}

func buildEinoSingleReportRun(source *config.Config, maxIterations int) (*config.Config, []string) {
	if source == nil {
		return nil, []string{"__final_report_no_tools__"}
	}
	finalCfg := *source
	finalCfg.Agent = source.Agent
	finalCfg.Agent.MaxIterations = maxIterations
	finalCfg.MultiAgent = source.MultiAgent
	finalCfg.MultiAgent.EinoSkills = source.MultiAgent.EinoSkills
	finalCfg.MultiAgent.EinoSkills.Disable = true
	finalCfg.MultiAgent.EinoMiddleware = source.MultiAgent.EinoMiddleware
	finalCfg.MultiAgent.EinoMiddleware.ToolSearchEnable = false
	return &finalCfg, []string{"__final_report_no_tools__"}
}

const einoSingleForcedReportInstruction = `调查阶段预算已经结束。立即停止所有工具调用，仅根据已有对话、工具结果和证据生成阶段性安全评估报告。报告必须明确区分已验证、疑似、未发现和未覆盖项，并包含评估范围、执行摘要、资产与服务、发现汇总、详细证据、修复优先级和后续复测建议。不得继续调查，不得调用任何工具。`
