package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"cyberstrike-ai/internal/config"
)

func TestResolveEinoSingleBudgetDefaults(t *testing.T) {
	got := resolveEinoSingleBudget(config.AgentConfig{})
	if got.total != 15*time.Minute || got.investigation != 12*time.Minute || got.finalization != 3*time.Minute {
		t.Fatalf("durations = total:%s investigation:%s finalization:%s", got.total, got.investigation, got.finalization)
	}
	if got.finalizationMaxIterations != 2 {
		t.Fatalf("finalizationMaxIterations = %d, want 2", got.finalizationMaxIterations)
	}
}

func TestResolveEinoSingleBudgetConfiguredAndClamped(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cfg         config.AgentConfig
		total       time.Duration
		investigate time.Duration
		finalize    time.Duration
		iterations  int
	}{
		{
			name: "configured",
			cfg: config.AgentConfig{
				TaskTimeoutMinutes:          20,
				FinalizationReservedMinutes: 4,
				FinalizationMaxIterations:   3,
			},
			total: 20 * time.Minute, investigate: 16 * time.Minute, finalize: 4 * time.Minute, iterations: 3,
		},
		{
			name: "reserve cannot consume whole task",
			cfg: config.AgentConfig{
				TaskTimeoutMinutes:          2,
				FinalizationReservedMinutes: 5,
			},
			total: 2 * time.Minute, investigate: time.Minute, finalize: time.Minute, iterations: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveEinoSingleBudget(tc.cfg)
			if got.total != tc.total || got.investigation != tc.investigate || got.finalization != tc.finalize || got.finalizationMaxIterations != tc.iterations {
				t.Fatalf("budget = %#v", got)
			}
		})
	}
}

func TestShouldForceEinoSingleReport(t *testing.T) {
	if !shouldForceEinoSingleReport(errors.New("exceeds max iterations"), nil) {
		t.Fatal("iteration limit must force report")
	}
	if !shouldForceEinoSingleReport(context.DeadlineExceeded, context.DeadlineExceeded) {
		t.Fatal("deadline must force report")
	}
	if shouldForceEinoSingleReport(errors.New("HTTP 500"), nil) {
		t.Fatal("general model error must not force report")
	}
}

func TestBuildEinoSingleReportRun(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.MaxIterations = 20
	cfg.MultiAgent.EinoSkills.Disable = false
	cfg.MultiAgent.EinoMiddleware.ToolSearchEnable = true

	got, roleTools := buildEinoSingleReportRun(cfg, 2)
	if got == cfg {
		t.Fatal("report config must be a copy")
	}
	if got.Agent.MaxIterations != 2 {
		t.Fatalf("max iterations = %d, want 2", got.Agent.MaxIterations)
	}
	if !got.MultiAgent.EinoSkills.Disable || got.MultiAgent.EinoMiddleware.ToolSearchEnable {
		t.Fatalf("tools still enabled: %#v", got.MultiAgent)
	}
	if len(roleTools) != 1 || roleTools[0] != "__final_report_no_tools__" {
		t.Fatalf("roleTools = %#v", roleTools)
	}
	if cfg.Agent.MaxIterations != 20 || cfg.MultiAgent.EinoSkills.Disable || !cfg.MultiAgent.EinoMiddleware.ToolSearchEnable {
		t.Fatal("source config was mutated")
	}
}

func TestApplyAgentConfigUpdateBudgetFields(t *testing.T) {
	total, reserve, iterations := 20, 4, 3
	dst := config.AgentConfig{MaxIterations: 30, ToolTimeoutMinutes: 10}
	applyAgentConfigUpdate(&dst, &AgentConfigUpdate{
		TaskTimeoutMinutes:          &total,
		FinalizationReservedMinutes: &reserve,
		FinalizationMaxIterations:   &iterations,
	})
	if dst.TaskTimeoutMinutes != 20 || dst.FinalizationReservedMinutes != 4 || dst.FinalizationMaxIterations != 3 {
		t.Fatalf("budget fields = %#v", dst)
	}
	if dst.MaxIterations != 30 || dst.ToolTimeoutMinutes != 10 {
		t.Fatalf("unrelated fields changed = %#v", dst)
	}
}
