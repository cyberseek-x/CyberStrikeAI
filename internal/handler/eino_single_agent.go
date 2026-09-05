package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"cyberstrike-ai/internal/agentfinalizer"
	"cyberstrike-ai/internal/mcp"
	"cyberstrike-ai/internal/multiagent"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// EinoSingleAgentLoopStream Eino ADK 单代理（ChatModelAgent + Runner）流式对话；不依赖 multi_agent.enabled。
func (h *AgentHandler) EinoSingleAgentLoopStream(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ev := StreamEvent{Type: "error", Message: "请求参数错误: " + err.Error()}
		b, _ := json.Marshal(ev)
		fmt.Fprintf(c.Writer, "data: %s\n\n", b)
		done := StreamEvent{Type: "done", Message: ""}
		db, _ := json.Marshal(done)
		fmt.Fprintf(c.Writer, "data: %s\n\n", db)
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return
	}

	c.Header("X-Accel-Buffering", "no")

	var baseCtx context.Context
	clientDisconnected := false
	var sseWriteMu sync.Mutex
	var ssePublishConversationID string
	sendEvent := func(eventType, message string, data interface{}) {
		if eventType == "error" && baseCtx != nil {
			cause := context.Cause(baseCtx)
			if errors.Is(cause, ErrTaskCancelled) || errors.Is(cause, multiagent.ErrInterruptContinue) {
				return
			}
		}
		ev := StreamEvent{Type: eventType, Message: message, Data: data}
		b, errMarshal := json.Marshal(ev)
		if errMarshal != nil {
			b = []byte(`{"type":"error","message":"marshal failed"}`)
		}
		sseLine := make([]byte, 0, len(b)+8)
		sseLine = append(sseLine, []byte("data: ")...)
		sseLine = append(sseLine, b...)
		sseLine = append(sseLine, '\n', '\n')
		if ssePublishConversationID != "" && h.taskEventBus != nil {
			h.taskEventBus.Publish(ssePublishConversationID, sseLine)
		}
		if clientDisconnected {
			return
		}
		select {
		case <-c.Request.Context().Done():
			clientDisconnected = true
			return
		default:
		}
		sseWriteMu.Lock()
		_, err := c.Writer.Write(sseLine)
		if err != nil {
			sseWriteMu.Unlock()
			clientDisconnected = true
			return
		}
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		} else {
			c.Writer.Flush()
		}
		sseWriteMu.Unlock()
	}

	h.logger.Info("收到 Eino ADK 单代理流式请求",
		zap.String("conversationId", req.ConversationID),
	)

	prep, err := h.prepareMultiAgentSession(&req, c, "eino_agent_stream")
	if err != nil {
		sendEvent("error", err.Error(), nil)
		sendEvent("done", "", nil)
		return
	}
	ssePublishConversationID = prep.ConversationID
	if prep.CreatedNew {
		sendEvent("conversation", "会话已创建", map[string]interface{}{
			"conversationId": prep.ConversationID,
		})
	}

	conversationID := prep.ConversationID
	assistantMessageID := prep.AssistantMessageID
	h.activateHITLForConversation(conversationID, req.Hitl)
	if h.hitlManager != nil {
		defer h.hitlManager.DeactivateConversation(conversationID)
	}

	if prep.UserMessageID != "" {
		sendEvent("message_saved", "", map[string]interface{}{
			"conversationId": conversationID,
			"userMessageId":  prep.UserMessageID,
		})
	}
	if h.runRoleWorkflowStreamIfBound(c, &req, prep, sendEvent) {
		return
	}

	var cancelWithCause context.CancelCauseFunc
	curFinalMessage := prep.FinalMessage
	curHistory := prep.History
	roleTools := prep.RoleTools

	taskStatus := "completed"
	// 仅在成功 StartTask 后再 FinishTask。若 StartTask 因 ErrTaskAlreadyRunning 失败仍 defer FinishTask，
	// 会误删其他连接上正在运行的同会话任务，导致「第一次拦截、第二次却放行」。
	taskOwned := false
	defer func() {
		if taskOwned {
			h.tasks.FinishTask(conversationID, taskStatus)
		}
	}()

	sendEvent("progress", "正在启动 Eino ADK 单代理（ChatModelAgent）...", map[string]interface{}{
		"conversationId": conversationID,
	})

	stopKeepalive := runSSEKeepalive(c, &sseWriteMu)
	defer stopKeepalive()

	if h.config == nil {
		taskStatus = "failed"
		h.tasks.UpdateTaskStatus(conversationID, taskStatus)
		sendEvent("error", "服务器配置未加载", nil)
		sendEvent("done", "", map[string]interface{}{"conversationId": conversationID})
		return
	}
	runCfg, resolvedAIChannelID, err := h.configForAIChannel(req.AIChannelID)
	if err != nil {
		taskStatus = "failed"
		h.tasks.UpdateTaskStatus(conversationID, taskStatus)
		sendEvent("error", err.Error(), nil)
		sendEvent("done", "", map[string]interface{}{"conversationId": conversationID})
		return
	}

	var result *multiagent.RunResult
	var runErr error

	budget := resolveEinoSingleBudget(runCfg.Agent)
	investigationDeadline := time.Now().Add(budget.investigation)
	baseCtx, cancelWithCause = context.WithCancelCause(detachedAgentContext(c.Request.Context()))
	taskCtx, timeoutCancel := context.WithDeadline(baseCtx, investigationDeadline)

	if _, err := h.tasks.StartTask(conversationID, req.Message, cancelWithCause); err != nil {
		var errorMsg string
		if errors.Is(err, ErrTaskAlreadyRunning) {
			errorMsg = "⚠️ 当前会话已有任务正在执行中，请等待当前任务完成或点击「停止任务」后再尝试。"
			sendEvent("error", errorMsg, map[string]interface{}{
				"conversationId": conversationID,
				"errorType":      "task_already_running",
			})
		} else {
			errorMsg = "❌ 无法启动任务: " + err.Error()
			sendEvent("error", errorMsg, nil)
		}
		if assistantMessageID != "" {
			_, _ = h.db.Exec("UPDATE messages SET content = ?, updated_at = ? WHERE id = ?", errorMsg, time.Now(), assistantMessageID)
		}
		sendEvent("done", "", map[string]interface{}{"conversationId": conversationID})
		timeoutCancel()
		return
	}
	taskOwned = true

	var cumulativeMCPExecutionIDs []string
	// 同一请求内分段续跑时，主代理 iteration 事件按偏移累计，避免 UI 出现「第3轮 → 第1轮」回跳。
	var mainIterationOffset int
	var emptyResponseContinueAttempt int
	var finalizationAutoContinueAttempt int
	var forcedReportAttempted bool
	var assetPersistenceAttempted bool
	activeRunCfg := runCfg
	activeRoleTools := roleTools
	activeDeadline := investigationDeadline
	var decision agentfinalizer.Decision
	var autoCancelledPendingExecutionIDs []string

	for {
		segmentMainIterationMax := 0
		rawProgressCallback := h.createProgressCallback(taskCtx, cancelWithCause, conversationID, assistantMessageID, sendEvent)
		progressCallback := func(eventType, message string, data interface{}) {
			if eventType == "iteration" {
				if m, ok := data.(map[string]interface{}); ok {
					if scope, _ := m["einoScope"].(string); scope == "main" {
						raw := 0
						switch v := m["iteration"].(type) {
						case int:
							raw = v
						case int32:
							raw = int(v)
						case int64:
							raw = int(v)
						case float64:
							raw = int(v)
						case float32:
							raw = int(v)
						}
						if raw > 0 {
							if raw > segmentMainIterationMax {
								segmentMainIterationMax = raw
							}
							m["iteration"] = raw + mainIterationOffset
						}
					}
				}
			}
			rawProgressCallback(eventType, message, data)
		}
		taskCtxLoop := mcp.WithMCPConversationID(taskCtx, conversationID)
		taskCtxLoop = mcp.WithToolRunRegistry(taskCtxLoop, h.tasks)
		taskCtxLoop = mcp.WithEinoExecuteRunRegistry(taskCtxLoop, h.tasks)
		taskCtxLoop = multiagent.WithAgentRuntimeCancelRegistrar(taskCtxLoop, func(cancel func(error) bool) func() {
			return h.tasks.BindAgentRuntimeCancel(conversationID, cancel)
		})
		taskCtxLoop = multiagent.WithAgentTurnLoopInterruptRegistrar(taskCtxLoop, func(push func(string) bool) func() {
			return h.tasks.BindAgentTurnLoopInterrupt(conversationID, push)
		})
		taskCtxLoop = multiagent.WithHITLToolInterceptor(taskCtxLoop, func(ctx context.Context, toolName, arguments string) (string, error) {
			return h.interceptHITLForEinoTool(ctx, cancelWithCause, conversationID, assistantMessageID, sendEvent, toolName, arguments)
		})

		result, runErr = multiagent.RunEinoSingleChatModelAgent(
			taskCtxLoop,
			activeRunCfg,
			&activeRunCfg.MultiAgent,
			h.agent,
			h.db,
			h.logger,
			conversationID,
			h.conversationProjectID(conversationID),
			curFinalMessage,
			curHistory,
			activeRoleTools,
			progressCallback,
			chatReasoningToClientIntent(req.Reasoning),
			h.agentSessionContextBlock(conversationID),
		)
		_ = resolvedAIChannelID

		if result != nil && len(result.MCPExecutionIDs) > 0 {
			cumulativeMCPExecutionIDs = mergeMCPExecutionIDLists(cumulativeMCPExecutionIDs, result.MCPExecutionIDs)
		}

		if runErr == nil && !forcedReportAttempted && errors.Is(taskCtx.Err(), context.DeadlineExceeded) {
			runErr = context.DeadlineExceeded
		}
		if runErr == nil {
			mw := &h.config.MultiAgent.EinoMiddleware
			if !forcedReportAttempted && h.tryContinueOnEinoEmptyResponse(taskCtx, mw, conversationID, result, &emptyResponseContinueAttempt, &curHistory, &curFinalMessage, progressCallback) {
				mainIterationOffset += segmentMainIterationMax
				timeoutCancel()
				baseCtx, cancelWithCause, taskCtx, timeoutCancel = h.rebindEinoRunningTaskWithTimeout(taskCtx, conversationID, timeoutCancel, time.Until(activeDeadline))
				continue
			}
			decision = h.decideAgentRunForDeliveryWithPolicy(conversationID, assistantMessageID, "eino_single", result, cumulativeMCPExecutionIDs, requestRequiresExecutionEvidence(&req))
			if cancelled := h.cleanupPendingToolExecutionsAfterIteration(taskCtx, conversationID, decision, progressCallback); len(cancelled) > 0 {
				autoCancelledPendingExecutionIDs = mergeMCPExecutionIDLists(autoCancelledPendingExecutionIDs, cancelled)
				decision = h.decideAgentRunForDeliveryWithPolicy(conversationID, assistantMessageID, "eino_single", result, cumulativeMCPExecutionIDs, requestRequiresExecutionEvidence(&req))
			}
			persistenceState := inspectAssetPersistenceState(h.db, cumulativeMCPExecutionIDs, isAssetDiscoveryRequest(req.Role, req.Message), assetPersistenceAttempted)
			if !forcedReportAttempted && persistenceState.ShouldRun() {
				assetPersistenceAttempted = true
				h.persistEinoAgentTraceForResume(conversationID, result)
				if hist, histErr := h.loadHistoryFromAgentTrace(conversationID); histErr == nil && len(hist) > 0 {
					curHistory = hist
				} else if h.logger != nil {
					h.logger.Warn("资产归档阶段恢复轨迹失败，将使用当前历史", zap.String("conversationId", conversationID), zap.Error(histErr))
				}
				curFinalMessage = assetPersistenceInstruction(h.conversationProjectID(conversationID))
				activeRunCfg, activeRoleTools = buildEinoSingleAssetPersistenceRun(runCfg, budget.finalizationMaxIterations)
				mainIterationOffset += segmentMainIterationMax
				timeoutCancel()
				remaining := time.Until(activeDeadline)
				if remaining <= 0 {
					remaining = budget.finalization
				}
				baseCtx, cancelWithCause, taskCtx, timeoutCancel = h.rebindEinoRunningTaskWithTimeout(taskCtx, conversationID, timeoutCancel, remaining)
				progressCallback("asset_persistence", "发现结果尚未归档，正在写入当前项目资产库…", map[string]interface{}{
					"conversationId": conversationID,
					"attempt":        1,
					"maxAttempts":    1,
				})
				continue
			}
			if !forcedReportAttempted && shouldForceEinoSingleReportAfterDecision(decision) {
				forcedReportAttempted = true
				h.persistEinoAgentTraceForResume(conversationID, result)
				if hist, histErr := h.loadHistoryFromAgentTrace(conversationID); histErr == nil && len(hist) > 0 {
					curHistory = hist
				} else if h.logger != nil {
					h.logger.Warn("最终回复内容不足，恢复轨迹生成报告失败，将使用当前历史",
						zap.String("conversationId", conversationID), zap.Error(histErr))
				}
				curFinalMessage = einoSingleForcedReportInstruction
				activeRunCfg, activeRoleTools = buildEinoSingleReportRun(runCfg, budget.finalizationMaxIterations)
				mainIterationOffset += segmentMainIterationMax
				timeoutCancel()
				activeDeadline = time.Now().Add(budget.finalization)
				taskCtx, timeoutCancel = context.WithDeadline(baseCtx, activeDeadline)
				progressCallback("forced_report_generation", "最终回复内容不足，正在根据已有证据重新生成完整报告…", map[string]interface{}{
					"conversationId":  conversationID,
					"reason":          "insufficient_final_response",
					"reportBudgetSec": int(budget.finalization.Seconds()),
				})
				continue
			}
			if !forcedReportAttempted && h.tryAutoContinueAfterFinalization(taskCtx, conversationID, result, decision, &finalizationAutoContinueAttempt, &curHistory, &curFinalMessage, progressCallback) {
				mainIterationOffset += segmentMainIterationMax
				timeoutCancel()
				baseCtx, cancelWithCause, taskCtx, timeoutCancel = h.rebindEinoRunningTaskWithTimeout(taskCtx, conversationID, timeoutCancel, time.Until(activeDeadline))
				continue
			}
			timeoutCancel()
			break
		}

		cause := context.Cause(baseCtx)
		if cause == nil {
			switch {
			case errors.Is(runErr, multiagent.ErrInterruptContinue):
				cause = multiagent.ErrInterruptContinue
			case errors.Is(runErr, ErrTaskCancelled):
				cause = ErrTaskCancelled
			}
		}
		if errors.Is(cause, multiagent.ErrInterruptContinue) {
			if shouldPersistEinoAgentTraceAfterRunError(baseCtx) {
				h.persistEinoAgentTraceForResume(conversationID, result)
			}
			note := h.tasks.TakeInterruptContinueNote(conversationID)
			icSummary := interruptContinueTimelineSummary(note)
			progressCallback("user_interrupt_continue", icSummary, map[string]interface{}{
				"conversationId": conversationID,
				"rawReason":      strings.TrimSpace(note),
				"emptyReason":    strings.TrimSpace(note) == "",
				"kind":           "no_active_mcp_tool",
			})
			inject := formatInterruptContinueUserMessage(note)
			// 不写入 messages 表为 user 气泡：避免主对话流出现大段模板；说明已由 user_interrupt_continue 记入助手 process_details（迭代详情）。
			if hist, err := h.loadHistoryFromAgentTrace(conversationID); err == nil && len(hist) > 0 {
				curHistory = hist
			}
			curFinalMessage = inject
			sendEvent("progress", "已合并用户补充与最新轨迹，正在继续推理…", map[string]interface{}{
				"conversationId": conversationID,
				"source":         "interrupt_continue",
			})
			mainIterationOffset += segmentMainIterationMax
			timeoutCancel()
			baseCtx, cancelWithCause = context.WithCancelCause(detachedAgentContext(baseCtx))
			h.tasks.BindTaskCancel(conversationID, cancelWithCause)
			taskCtx, timeoutCancel = context.WithTimeout(baseCtx, time.Until(activeDeadline))
			h.tasks.UpdateTaskStatus(conversationID, "running")
			continue
		}

		if shouldPersistEinoAgentTraceAfterRunError(baseCtx) {
			h.persistEinoAgentTraceForResume(conversationID, result)
		}
		if cause == nil && !forcedReportAttempted && shouldForceEinoSingleReport(runErr, taskCtx.Err()) {
			forcedReportAttempted = true
			if hist, histErr := h.loadHistoryFromAgentTrace(conversationID); histErr == nil && len(hist) > 0 {
				curHistory = hist
			} else if h.logger != nil {
				h.logger.Warn("调查预算耗尽后恢复轨迹失败，将使用当前历史生成报告",
					zap.String("conversationId", conversationID), zap.Error(histErr))
			}
			if h.agent != nil {
				h.agent.CancelRunningMCPToolsForConversation(conversationID, "调查预算已用完，停止工具并生成阶段性报告")
			}
			curFinalMessage = einoSingleForcedReportInstruction
			activeRunCfg, activeRoleTools = buildEinoSingleReportRun(runCfg, budget.finalizationMaxIterations)
			mainIterationOffset += segmentMainIterationMax
			timeoutCancel()
			activeDeadline = time.Now().Add(budget.finalization)
			taskCtx, timeoutCancel = context.WithDeadline(baseCtx, activeDeadline)
			progressCallback("forced_report_generation", "调查预算已用完，正在根据现有证据生成阶段性报告…", map[string]interface{}{
				"conversationId":  conversationID,
				"reason":          "budget_exhausted",
				"reportBudgetSec": int(budget.finalization.Seconds()),
			})
			continue
		}
		if errors.Is(cause, ErrTaskCancelled) {
			taskStatus = "cancelled"
			h.tasks.UpdateTaskStatus(conversationID, taskStatus)
			cancelMsg := "任务已被用户取消，后续操作已停止。"
			if assistantMessageID != "" {
				if result != nil {
					if err := h.mergeAssistantMessagePartialOnCancel(assistantMessageID, result.Response); err != nil {
						h.logger.Warn("合并取消前的部分回复失败", zap.Error(err))
					}
				}
				if err := h.appendAssistantMessageNotice(assistantMessageID, cancelMsg); err != nil {
					h.logger.Warn("更新取消后的助手消息失败", zap.Error(err))
				}
				_ = h.db.AddProcessDetail(assistantMessageID, conversationID, "cancelled", cancelMsg, nil)
			}
			sendEvent("cancelled", cancelMsg, map[string]interface{}{
				"conversationId": conversationID,
				"messageId":      assistantMessageID,
			})
			sendEvent("done", "", map[string]interface{}{"conversationId": conversationID})
			timeoutCancel()
			return
		}

		if errors.Is(runErr, context.DeadlineExceeded) || errors.Is(context.Cause(taskCtx), context.DeadlineExceeded) {
			taskStatus = "timeout"
			h.tasks.UpdateTaskStatus(conversationID, taskStatus)
			timeoutMsg := "任务执行超时，已自动终止。"
			if assistantMessageID != "" {
				_, _ = h.db.Exec("UPDATE messages SET content = ?, updated_at = ? WHERE id = ?", timeoutMsg, time.Now(), assistantMessageID)
				_ = h.db.AddProcessDetail(assistantMessageID, conversationID, "timeout", timeoutMsg, nil)
			}
			sendEvent("error", timeoutMsg, map[string]interface{}{
				"conversationId": conversationID,
				"messageId":      assistantMessageID,
				"errorType":      "timeout",
			})
			sendEvent("done", "", map[string]interface{}{"conversationId": conversationID})
			timeoutCancel()
			return
		}

		h.logger.Error("Eino ADK 单代理执行失败", zap.Error(runErr))
		taskStatus = "failed"
		h.tasks.UpdateTaskStatus(conversationID, taskStatus)
		errMsg := "执行失败: " + runErr.Error()
		if assistantMessageID != "" {
			_, _ = h.db.Exec("UPDATE messages SET content = ?, updated_at = ? WHERE id = ?", errMsg, time.Now(), assistantMessageID)
			_ = h.db.AddProcessDetail(assistantMessageID, conversationID, "error", errMsg, nil)
		}
		sendEvent("error", errMsg, map[string]interface{}{
			"conversationId": conversationID,
			"messageId":      assistantMessageID,
		})
		sendEvent("done", "", map[string]interface{}{"conversationId": conversationID})
		timeoutCancel()
		return
	}

	timeoutCancel()

	if decision.CompletionReason == "" {
		decision = h.decideAgentRunForDeliveryWithPolicy(conversationID, assistantMessageID, "eino_single", result, cumulativeMCPExecutionIDs, requestRequiresExecutionEvidence(&req))
		if cancelled := h.cleanupPendingToolExecutionsAfterIteration(taskCtx, conversationID, decision, nil); len(cancelled) > 0 {
			autoCancelledPendingExecutionIDs = mergeMCPExecutionIDLists(autoCancelledPendingExecutionIDs, cancelled)
			decision = h.decideAgentRunForDeliveryWithPolicy(conversationID, assistantMessageID, "eino_single", result, cumulativeMCPExecutionIDs, requestRequiresExecutionEvidence(&req))
		}
	}
	h.persistFinalizationDecision(conversationID, assistantMessageID, "eino_single", cumulativeMCPExecutionIDs, multiagent.AggregatedReasoningFromTraceJSON(result.LastAgentTraceInput), decision)

	if result.LastAgentTraceInput != "" || result.LastAgentTraceOutput != "" {
		if err := h.db.SaveAgentTrace(conversationID, result.LastAgentTraceInput, result.LastAgentTraceOutput); err != nil {
			h.logger.Warn("保存代理轨迹失败", zap.Error(err))
		}
	}

	responseText := decision.FinalText
	if !decision.Finalizable {
		responseText = finalizationBlockedMessage(decision)
		sendEvent("finalization_check", responseText, decision)
		taskStatus = decision.Status
		h.tasks.UpdateTaskStatus(conversationID, taskStatus)
	}
	sendEvent("response", responseText, finalizationResponsePayload(decision, map[string]interface{}{
		"mcpExecutionIds":                  cumulativeMCPExecutionIDs,
		"conversationId":                   conversationID,
		"messageId":                        assistantMessageID,
		"agentMode":                        "eino_single",
		"autoCancelledPendingExecutionIds": autoCancelledPendingExecutionIDs,
	}))
	sendEvent("done", "", map[string]interface{}{"conversationId": conversationID})
}

// EinoSingleAgentLoop Eino ADK 单代理非流式对话。
func (h *AgentHandler) EinoSingleAgentLoop(c *gin.Context) {
	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.logger.Info("收到 Eino ADK 单代理非流式请求", zap.String("conversationId", req.ConversationID))

	prep, err := h.prepareMultiAgentSession(&req, c, "eino_agent")
	if err != nil {
		status, msg := multiAgentHTTPErrorStatus(err)
		c.JSON(status, gin.H{"error": msg})
		return
	}
	h.activateHITLForConversation(prep.ConversationID, req.Hitl)
	if h.hitlManager != nil {
		defer h.hitlManager.DeactivateConversation(prep.ConversationID)
	}
	if h.runRoleWorkflowJSONIfBound(c, &req, prep) {
		return
	}

	var progressBuf strings.Builder
	progressCallbackRaw := func(eventType, message string, data interface{}) {
		progressBuf.WriteString(eventType)
		progressBuf.WriteByte('\n')
	}
	baseCtx, cancelWithCause := context.WithCancelCause(c.Request.Context())
	defer cancelWithCause(nil)

	if h.config == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器配置未加载"})
		return
	}
	runCfg, _, err := h.configForAIChannel(req.AIChannelID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	budget := resolveEinoSingleBudget(runCfg.Agent)
	taskCtx, timeoutCancel := context.WithTimeout(baseCtx, budget.investigation)
	defer func() {
		if timeoutCancel != nil {
			timeoutCancel()
		}
	}()
	progressCallback := h.createProgressCallback(taskCtx, cancelWithCause, prep.ConversationID, prep.AssistantMessageID, progressCallbackRaw)
	taskCtx = multiagent.WithHITLToolInterceptor(taskCtx, func(ctx context.Context, toolName, arguments string) (string, error) {
		return h.interceptHITLForEinoTool(ctx, cancelWithCause, prep.ConversationID, prep.AssistantMessageID, nil, toolName, arguments)
	})

	curHist := prep.History
	curMsg := prep.FinalMessage
	var result *multiagent.RunResult
	var runErr error
	var emptyResponseContinueAttempt int
	var finalizationAutoContinueAttempt int
	var forcedReportAttempted bool
	var assetPersistenceAttempted bool
	activeRunCfg := runCfg
	activeRoleTools := prep.RoleTools
	var cumulativeMCPExecutionIDs []string
	var decision agentfinalizer.Decision
	var autoCancelledPendingExecutionIDs []string
	for {
		result, runErr = multiagent.RunEinoSingleChatModelAgent(
			taskCtx,
			activeRunCfg,
			&activeRunCfg.MultiAgent,
			h.agent,
			h.db,
			h.logger,
			prep.ConversationID,
			h.conversationProjectID(prep.ConversationID),
			curMsg,
			curHist,
			activeRoleTools,
			progressCallback,
			chatReasoningToClientIntent(req.Reasoning),
			h.agentSessionContextBlock(prep.ConversationID),
		)
		if result != nil && len(result.MCPExecutionIDs) > 0 {
			cumulativeMCPExecutionIDs = mergeMCPExecutionIDLists(cumulativeMCPExecutionIDs, result.MCPExecutionIDs)
		}
		if runErr == nil && !forcedReportAttempted && errors.Is(taskCtx.Err(), context.DeadlineExceeded) {
			runErr = context.DeadlineExceeded
		}
		if runErr != nil {
			if shouldPersistEinoAgentTraceAfterRunError(baseCtx) {
				h.persistEinoAgentTraceForResume(prep.ConversationID, result)
			}
			if !forcedReportAttempted && context.Cause(baseCtx) == nil && shouldForceEinoSingleReport(runErr, taskCtx.Err()) {
				forcedReportAttempted = true
				if hist, histErr := h.loadHistoryFromAgentTrace(prep.ConversationID); histErr == nil && len(hist) > 0 {
					curHist = hist
				} else if h.logger != nil {
					h.logger.Warn("调查预算耗尽后恢复轨迹失败，将使用当前历史生成报告",
						zap.String("conversationId", prep.ConversationID), zap.Error(histErr))
				}
				if h.agent != nil {
					h.agent.CancelRunningMCPToolsForConversation(prep.ConversationID, "调查预算已用完，停止工具并生成阶段性报告")
				}
				curMsg = einoSingleForcedReportInstruction
				activeRunCfg, activeRoleTools = buildEinoSingleReportRun(runCfg, budget.finalizationMaxIterations)
				timeoutCancel()
				taskCtx, timeoutCancel = context.WithTimeout(baseCtx, budget.finalization)
				progressCallback = h.createProgressCallback(taskCtx, cancelWithCause, prep.ConversationID, prep.AssistantMessageID, progressCallbackRaw)
				taskCtx = multiagent.WithHITLToolInterceptor(taskCtx, func(ctx context.Context, toolName, arguments string) (string, error) {
					return h.interceptHITLForEinoTool(ctx, cancelWithCause, prep.ConversationID, prep.AssistantMessageID, nil, toolName, arguments)
				})
				progressCallback("forced_report_generation", "调查预算已用完，正在根据现有证据生成阶段性报告…", map[string]interface{}{
					"conversationId":  prep.ConversationID,
					"reason":          "budget_exhausted",
					"reportBudgetSec": int(budget.finalization.Seconds()),
				})
				continue
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": runErr.Error()})
			return
		}
		mw := &h.config.MultiAgent.EinoMiddleware
		if !forcedReportAttempted && h.tryContinueOnEinoEmptyResponse(taskCtx, mw, prep.ConversationID, result, &emptyResponseContinueAttempt, &curHist, &curMsg, progressCallback) {
			continue
		}
		decision = h.decideAgentRunForDeliveryWithPolicy(prep.ConversationID, prep.AssistantMessageID, "eino_single", result, cumulativeMCPExecutionIDs, requestRequiresExecutionEvidence(&req))
		if cancelled := h.cleanupPendingToolExecutionsAfterIteration(taskCtx, prep.ConversationID, decision, progressCallback); len(cancelled) > 0 {
			autoCancelledPendingExecutionIDs = mergeMCPExecutionIDLists(autoCancelledPendingExecutionIDs, cancelled)
			decision = h.decideAgentRunForDeliveryWithPolicy(prep.ConversationID, prep.AssistantMessageID, "eino_single", result, cumulativeMCPExecutionIDs, requestRequiresExecutionEvidence(&req))
		}
		persistenceState := inspectAssetPersistenceState(h.db, cumulativeMCPExecutionIDs, isAssetDiscoveryRequest(req.Role, req.Message), assetPersistenceAttempted)
		if !forcedReportAttempted && persistenceState.ShouldRun() {
			assetPersistenceAttempted = true
			h.persistEinoAgentTraceForResume(prep.ConversationID, result)
			if hist, histErr := h.loadHistoryFromAgentTrace(prep.ConversationID); histErr == nil && len(hist) > 0 {
				curHist = hist
			} else if h.logger != nil {
				h.logger.Warn("资产归档阶段恢复轨迹失败，将使用当前历史", zap.String("conversationId", prep.ConversationID), zap.Error(histErr))
			}
			curMsg = assetPersistenceInstruction(h.conversationProjectID(prep.ConversationID))
			activeRunCfg, activeRoleTools = buildEinoSingleAssetPersistenceRun(runCfg, budget.finalizationMaxIterations)
			timeoutCancel()
			taskCtx, timeoutCancel = context.WithTimeout(baseCtx, budget.finalization)
			progressCallback = h.createProgressCallback(taskCtx, cancelWithCause, prep.ConversationID, prep.AssistantMessageID, progressCallbackRaw)
			taskCtx = multiagent.WithHITLToolInterceptor(taskCtx, func(ctx context.Context, toolName, arguments string) (string, error) {
				return h.interceptHITLForEinoTool(ctx, cancelWithCause, prep.ConversationID, prep.AssistantMessageID, nil, toolName, arguments)
			})
			progressCallback("asset_persistence", "发现结果尚未归档，正在写入当前项目资产库…", map[string]interface{}{
				"conversationId": prep.ConversationID,
				"attempt":        1,
				"maxAttempts":    1,
			})
			continue
		}
		if !forcedReportAttempted && shouldForceEinoSingleReportAfterDecision(decision) {
			forcedReportAttempted = true
			h.persistEinoAgentTraceForResume(prep.ConversationID, result)
			if hist, histErr := h.loadHistoryFromAgentTrace(prep.ConversationID); histErr == nil && len(hist) > 0 {
				curHist = hist
			} else if h.logger != nil {
				h.logger.Warn("最终回复内容不足，恢复轨迹生成报告失败，将使用当前历史",
					zap.String("conversationId", prep.ConversationID), zap.Error(histErr))
			}
			curMsg = einoSingleForcedReportInstruction
			activeRunCfg, activeRoleTools = buildEinoSingleReportRun(runCfg, budget.finalizationMaxIterations)
			timeoutCancel()
			taskCtx, timeoutCancel = context.WithTimeout(baseCtx, budget.finalization)
			progressCallback = h.createProgressCallback(taskCtx, cancelWithCause, prep.ConversationID, prep.AssistantMessageID, progressCallbackRaw)
			progressCallback("forced_report_generation", "最终回复内容不足，正在根据已有证据重新生成完整报告…", map[string]interface{}{
				"conversationId":  prep.ConversationID,
				"reason":          "insufficient_final_response",
				"reportBudgetSec": int(budget.finalization.Seconds()),
			})
			continue
		}
		if !forcedReportAttempted && h.tryAutoContinueAfterFinalization(taskCtx, prep.ConversationID, result, decision, &finalizationAutoContinueAttempt, &curHist, &curMsg, progressCallback) {
			continue
		}
		break
	}

	h.persistFinalizationDecision(prep.ConversationID, prep.AssistantMessageID, "eino_single", cumulativeMCPExecutionIDs, multiagent.AggregatedReasoningFromTraceJSON(result.LastAgentTraceInput), decision)
	if result.LastAgentTraceInput != "" || result.LastAgentTraceOutput != "" {
		_ = h.db.SaveAgentTrace(prep.ConversationID, result.LastAgentTraceInput, result.LastAgentTraceOutput)
	}

	responseText := decision.FinalText
	if !decision.Finalizable {
		responseText = finalizationBlockedMessage(decision)
	}
	c.JSON(http.StatusOK, gin.H{
		"response":                         responseText,
		"conversationId":                   prep.ConversationID,
		"mcpExecutionIds":                  cumulativeMCPExecutionIDs,
		"assistantMessageId":               prep.AssistantMessageID,
		"agentMode":                        "eino_single",
		"finalized":                        decision.Finalized,
		"finalizable":                      decision.Finalizable,
		"status":                           decision.Status,
		"completionReason":                 decision.CompletionReason,
		"evidenceVerified":                 decision.EvidenceVerified,
		"evidenceRefs":                     decision.EvidenceRefs,
		"pendingExecutionIds":              decision.PendingExecutionIDs,
		"missingChecks":                    decision.MissingChecks,
		"autoCancelledPendingExecutionIds": autoCancelledPendingExecutionIDs,
	})
}
