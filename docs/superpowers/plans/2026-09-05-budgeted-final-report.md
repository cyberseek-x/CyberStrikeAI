# 有时限的单代理收尾报告实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 让 Eino 单代理在调查时间或轮次预算耗尽后，停止工具调用并在独立预算内生成阶段性报告。

**架构：** 在 handler 层集中解析预算并控制“调查→报告”两阶段；在 multiagent 运行错误层为可恢复的迭代上限增加显式开关。报告阶段通过关闭 Skills、工具搜索并过滤角色工具来确保不再调用工具。

**技术栈：** Go、CloudWeGo Eino ADK、Gin、YAML、Go testing

---

## 文件结构

- 创建 `internal/handler/eino_single_budget.go`：预算解析、阶段切换判定和报告配置构造。
- 创建 `internal/handler/eino_single_budget_test.go`：预算及阶段切换测试。
- 修改 `internal/config/config.go`：新增预算配置字段和默认值。
- 修改 `internal/handler/config.go`：支持 API 局部更新和 YAML 持久化。
- 修改 `internal/handler/openapi.go`：公开新配置字段。
- 修改 `internal/multiagent/eino_adk_run_loop.go`：公开迭代上限识别并携带可恢复标记。
- 修改 `internal/multiagent/eino_run_error_handler.go`：可恢复上限不提前发错误。
- 修改 `internal/multiagent/eino_run_error_handler_test.go`：验证两种上限行为。
- 修改 `internal/multiagent/eino_single_runner.go`：把调查阶段可恢复标记传入运行循环。
- 修改 `internal/handler/eino_single_agent.go`：实现流式和非流式两阶段运行。
- 修改 `config.example.yaml`：记录新字段。

### 任务 1：预算模型

- [ ] 编写失败测试，覆盖默认 15/3/2、总预算减预留、非法配置归一化和轮次/时间触发条件。
- [ ] 运行 `go test ./internal/handler -run 'TestResolveEinoSingleBudget|TestShouldForceEinoSingleReport' -count=1`，确认因符号未定义而失败。
- [ ] 创建预算模块并加入 `AgentConfig` 字段，使测试通过。
- [ ] 运行同一测试命令，确认通过。

### 任务 2：可恢复迭代上限

- [ ] 先增加测试：恢复模式应发送 `iteration_limit_reached` 而不发送 `error`，普通模式仍发送两者。
- [ ] 运行 `go test ./internal/multiagent -run TestEinoRunErrorHandlerIterationLimit -count=1`，确认恢复模式失败。
- [ ] 给运行参数和错误处理器加入 `RecoverIterationLimit`，由单代理调查阶段开启。
- [ ] 运行同一测试命令，确认通过。

### 任务 3：强制报告阶段

- [ ] 编写处理器级测试，验证报告配置把最大轮次设为 2、关闭 Skills/工具搜索并使用无匹配角色工具。
- [ ] 运行 `go test ./internal/handler -run TestBuildEinoSingleReportRun -count=1`，确认失败。
- [ ] 在流式和非流式处理器中使用调查上下文；捕获调查超时或迭代上限后恢复轨迹，切换无工具报告上下文并继续。
- [ ] 运行 handler 与 multiagent 包测试，确认通过。

### 任务 4：配置与文档

- [ ] 为配置局部更新增加测试，验证新字段不会覆盖未提交字段。
- [ ] 在配置结构、默认配置、YAML 持久化和 OpenAPI 中增加四个预算字段。
- [ ] 更新 `config.example.yaml` 和本机部署配置为 20/15/3/2。
- [ ] 运行 `go test ./internal/config ./internal/handler -count=1`。

### 任务 5：构建、部署与回滚

- [ ] 运行 `gofmt`、`go test ./...` 和 `go build -o dist/cyberstrike-ai ./cmd/server`。
- [ ] 备份当前二进制和配置，复制新二进制，重启本机服务。
- [ ] 请求 `http://127.0.0.1:8080/` 并检查服务日志中的启动状态。
- [ ] 在另一个副本上执行回滚脚本，核对原始哈希恢复；保留已部署版本不回滚。
