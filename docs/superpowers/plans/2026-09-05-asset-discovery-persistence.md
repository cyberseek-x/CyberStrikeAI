# 资产发现自动归档与排除地址段配置实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 让信息收集任务复用新鲜资产、自动绑定项目并归档已验证端点，同时让管理员在系统设置中维护扫描复用天数和排除的 IPv4/IPv6 CIDR。

**架构：** 在 `config.Config` 增加 `asset_discovery` 配置并通过现有配置接口和 YAML 持久化；后端在 `create_asset` 的唯一写入口执行项目绑定和 IP 网段过滤，防止提示词绕过。会话准备阶段注入当前项目资产的新鲜度摘要，单代理流程在发现证据存在但没有资产写入时触发一次受限归档补偿；前端以结构化列表编辑 CIDR、原因和启用状态。

**技术栈：** Go 1.24、Gin、SQLite、gopkg.in/yaml.v3、原生 JavaScript、HTML/CSS、Go testing。

---

## 文件结构

- 修改 `internal/config/config.go`：声明、规范化并提供资产发现配置默认值。
- 修改 `internal/config/config_test.go`：覆盖默认值、CIDR 校验与去重。
- 修改 `internal/handler/config.go`、`internal/handler/config_test.go`：配置 API 更新及 YAML 持久化。
- 修改 `web/templates/index.html`、`web/static/js/settings.js`、`web/static/css/style.css`、`web/static/i18n/zh-CN.json`、`web/static/i18n/en-US.json`：新增前端设置区。
- 创建 `internal/app/asset_discovery_policy.go`：项目绑定与排除网段判定。
- 修改 `internal/app/asset_tools.go`、`internal/app/app.go`、`internal/app/asset_tools_test.go`：在资产写入口应用策略并返回过滤详情。
- 创建 `internal/handler/asset_discovery_context.go`、`internal/handler/asset_discovery_context_test.go`：识别信息收集任务、生成新鲜资产复用上下文。
- 修改 `internal/handler/multi_agent_prepare.go`：注入资产发现上下文。
- 创建 `internal/handler/eino_asset_persistence.go`、`internal/handler/eino_asset_persistence_test.go`：决定是否触发一次归档补偿并统计结果。
- 修改 `internal/handler/eino_single_agent.go`：在最终报告前运行一次受限归档。
- 创建 `prompts/single-agent-report.md`，修改 `roles/信息收集.yaml`：固化资产归档和报告约束。

### 任务 1：配置模型、默认值与 YAML 持久化

**文件：**
- 修改：`internal/config/config.go`
- 修改：`internal/config/config_test.go`
- 修改：`internal/handler/config.go`
- 修改：`internal/handler/config_test.go`

- [ ] **步骤 1：编写失败的配置测试**

```go
func TestNormalizeAssetDiscoveryConfig(t *testing.T) {
	got, err := NormalizeAssetDiscoveryConfig(AssetDiscoveryConfig{
		ScanFreshDays: 7,
		ExcludedIPRanges: []AssetDiscoveryExcludedIPRange{
			{CIDR: "198.18.0.0/15", Reason: "代理映射地址", Enabled: true},
			{CIDR: "198.18.0.1/15", Reason: "重复", Enabled: true},
		},
	})
	if err != nil { t.Fatal(err) }
	if len(got.ExcludedIPRanges) != 1 { t.Fatalf("got %d rules", len(got.ExcludedIPRanges)) }
	if got.ExcludedIPRanges[0].CIDR != "198.18.0.0/15" { t.Fatalf("got %s", got.ExcludedIPRanges[0].CIDR) }
}
```

```go
func TestUpdateAssetDiscoveryConfigWritesYAML(t *testing.T) {
	doc := newEmptyYAMLDocument()
	updateAssetDiscoveryConfig(doc, config.AssetDiscoveryConfig{ScanFreshDays: 7, ExcludedIPRanges: []config.AssetDiscoveryExcludedIPRange{{CIDR: "198.18.0.0/15", Reason: "代理映射地址", Enabled: true}}})
	var got config.Config
	b, _ := yaml.Marshal(doc.Content[0])
	if err := yaml.Unmarshal(b, &got); err != nil { t.Fatal(err) }
	if got.AssetDiscovery.ScanFreshDays != 7 { t.Fatalf("got %d", got.AssetDiscovery.ScanFreshDays) }
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/config ./internal/handler -run 'TestNormalizeAssetDiscoveryConfig|TestUpdateAssetDiscoveryConfigWritesYAML' -count=1`

预期：FAIL，报告 `AssetDiscoveryConfig` 或 `updateAssetDiscoveryConfig` 未定义。

- [ ] **步骤 3：实现配置结构、规范化和保存**

```go
type AssetDiscoveryExcludedIPRange struct {
	CIDR    string `yaml:"cidr" json:"cidr"`
	Reason  string `yaml:"reason,omitempty" json:"reason,omitempty"`
	Enabled bool   `yaml:"enabled" json:"enabled"`
}

type AssetDiscoveryConfig struct {
	ScanFreshDays   int                             `yaml:"scan_fresh_days" json:"scan_fresh_days"`
	ExcludedIPRanges []AssetDiscoveryExcludedIPRange `yaml:"excluded_ip_ranges" json:"excluded_ip_ranges"`
}
```

`NormalizeAssetDiscoveryConfig` 使用 `net.ParseCIDR` 校验并规范化地址段，按规范化 CIDR 去重；`scan_fresh_days` 只接受 0–3650，空配置加载时补默认 7 天与 `198.18.0.0/15`。`UpdateConfigRequest` 增加 `AssetDiscovery *config.AssetDiscoveryConfig`，验证失败返回 400，成功后写入内存并由 `updateAssetDiscoveryConfig` 写回 YAML 节点。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/config ./internal/handler -run 'AssetDiscovery|UpdateAssetDiscovery' -count=1`

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/config/config.go internal/config/config_test.go internal/handler/config.go internal/handler/config_test.go
git commit -m "feat: add asset discovery policy configuration"
```

### 任务 2：系统设置前端维护新鲜期和排除地址段

**文件：**
- 修改：`web/templates/index.html`
- 修改：`web/static/js/settings.js`
- 修改：`web/static/css/style.css`
- 修改：`web/static/i18n/zh-CN.json`
- 修改：`web/static/i18n/en-US.json`

- [ ] **步骤 1：编写失败的静态契约测试**

在 `internal/handler/config_test.go` 增加读取静态文件的测试，断言以下标识存在：

```go
for _, marker := range []string{
	`id="asset-discovery-fresh-days"`,
	`id="asset-discovery-excluded-ranges"`,
	`function renderAssetDiscoveryExcludedRanges`,
	`asset_discovery:`,
} {
	if !strings.Contains(source, marker) { t.Fatalf("missing %s", marker) }
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/handler -run TestAssetDiscoverySettingsContract -count=1`

预期：FAIL，报告缺少 `asset-discovery-fresh-days`。

- [ ] **步骤 3：实现设置表单与序列化**

在系统设置的智能体配置区域新增“资产发现”卡片：复用天数数字输入框、排除地址段行容器和“添加地址段”按钮。每行包含 CIDR 输入框、原因输入框、启用复选框和删除按钮。`loadConfig()` 调用 `renderAssetDiscoveryExcludedRanges(currentConfig.asset_discovery.excluded_ip_ranges)`；保存请求增加：

```js
asset_discovery: {
  scan_fresh_days: Math.max(0, parseInt(document.getElementById('asset-discovery-fresh-days').value, 10) || 0),
  excluded_ip_ranges: readAssetDiscoveryExcludedRanges()
}
```

- [ ] **步骤 4：验证静态契约和 JSON 语法**

运行：`go test ./internal/handler -run TestAssetDiscoverySettingsContract -count=1 && python3 -m json.tool web/static/i18n/zh-CN.json >/dev/null && python3 -m json.tool web/static/i18n/en-US.json >/dev/null`

预期：PASS，两个 JSON 命令退出码均为 0。

- [ ] **步骤 5：提交**

```bash
git add web/templates/index.html web/static/js/settings.js web/static/css/style.css web/static/i18n/zh-CN.json web/static/i18n/en-US.json internal/handler/config_test.go
git commit -m "feat: configure asset discovery policy in settings"
```

### 任务 3：资产写入口自动绑定项目并过滤排除地址段

**文件：**
- 创建：`internal/app/asset_discovery_policy.go`
- 修改：`internal/app/asset_tools.go`
- 修改：`internal/app/app.go`
- 修改：`internal/app/asset_tools_test.go`

- [ ] **步骤 1：编写失败的策略测试**

```go
func TestApplyAssetDiscoveryPolicyFiltersMappedIPAndKeepsDomain(t *testing.T) {
	asset := &database.Asset{Domain: "example.com", IP: "198.18.1.8", Port: 443, Protocol: "https"}
	match, err := applyAssetDiscoveryPolicy(asset, config.AssetDiscoveryConfig{ExcludedIPRanges: []config.AssetDiscoveryExcludedIPRange{{CIDR: "198.18.0.0/15", Reason: "代理映射地址", Enabled: true}}})
	if err != nil { t.Fatal(err) }
	if asset.IP != "" { t.Fatalf("filtered IP remains: %s", asset.IP) }
	if match == nil || match.CIDR != "198.18.0.0/15" { t.Fatalf("unexpected match: %#v", match) }
}

func TestCreateAssetBindsConversationProject(t *testing.T) {
	// 建立绑定 project-1 的对话上下文，调用 create_asset 时不传 project_id。
	// 断言保存后的 Asset.ProjectID == "project-1"。
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app -run 'TestApplyAssetDiscoveryPolicy|TestCreateAssetBindsConversationProject' -count=1`

预期：FAIL，报告 `applyAssetDiscoveryPolicy` 未定义或项目字段为空。

- [ ] **步骤 3：实现服务端强制策略**

将 `registerAssetTools` 增加 `assetDiscoveryConfig func() config.AssetDiscoveryConfig` 参数。`create_asset` 解析参数后先调用 `agentAssetProjectScope`：项目范围存在时自动填充空 `project_id`，显式不同项目返回错误。随后运行 `applyAssetDiscoveryPolicy`；命中排除地址段时清空 IP，仅域名或主机存在才继续保存，并在工具 JSON 结果中加入 `filtered_ip`、`filter_cidr`、`filter_reason`。

- [ ] **步骤 4：运行资产工具测试**

运行：`go test ./internal/app -run 'Asset|DiscoveryPolicy' -count=1`

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/app/asset_discovery_policy.go internal/app/asset_tools.go internal/app/app.go internal/app/asset_tools_test.go
git commit -m "feat: enforce asset persistence policy"
```

### 任务 4：注入资产复用上下文并限制归档补偿为一次

**文件：**
- 创建：`internal/handler/asset_discovery_context.go`
- 创建：`internal/handler/asset_discovery_context_test.go`
- 创建：`internal/handler/eino_asset_persistence.go`
- 创建：`internal/handler/eino_asset_persistence_test.go`
- 修改：`internal/handler/multi_agent_prepare.go`
- 修改：`internal/handler/eino_single_agent.go`

- [ ] **步骤 1：编写失败的新鲜度与补偿决策测试**

```go
func TestClassifyAssetFreshness(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	if got := classifyAssetFreshness(now.Add(-6*24*time.Hour), now, 7); got != assetFresh { t.Fatalf("got %s", got) }
	if got := classifyAssetFreshness(now.Add(-8*24*time.Hour), now, 7); got != assetStale { t.Fatalf("got %s", got) }
}

func TestShouldRunAssetPersistenceOnce(t *testing.T) {
	state := assetPersistenceState{DiscoveryRequest: true, HasDiscoveryEvidence: true}
	if !state.ShouldRun() { t.Fatal("expected persistence") }
	state.Attempted = true
	if state.ShouldRun() { t.Fatal("must not run twice") }
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/handler -run 'TestClassifyAssetFreshness|TestShouldRunAssetPersistenceOnce' -count=1`

预期：FAIL，报告 `classifyAssetFreshness` 和 `assetPersistenceState` 未定义。

- [ ] **步骤 3：实现上下文和一次性归档**

`isAssetDiscoveryRequest` 根据角色“信息收集”及“暴露面、资产发现、子域名、信息收集”等关键词判定；`isForceRefreshRequest` 识别“重新扫描、强制刷新、忽略缓存”。会话准备时查询当前项目资产，按 `last_scan_at` 和 `scan_fresh_days` 生成精简摘要；强制刷新时保留资产清单但明确允许重扫。单代理在已完成发现类工具、尚无成功 `create_asset` 且未尝试归档时，使用只包含 `create_asset`、`query_assets` 和项目事实读取工具的角色工具集追加一轮，随后无论成功与否都进入最终报告。

- [ ] **步骤 4：运行处理器测试**

运行：`go test ./internal/handler -run 'AssetDiscovery|AssetPersistence|EinoSingle' -count=1`

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/handler/asset_discovery_context.go internal/handler/asset_discovery_context_test.go internal/handler/eino_asset_persistence.go internal/handler/eino_asset_persistence_test.go internal/handler/multi_agent_prepare.go internal/handler/eino_single_agent.go
git commit -m "feat: reuse fresh assets and finalize persistence"
```

### 任务 5：提示词、全量验证和运行时发布

**文件：**
- 创建：`prompts/single-agent-report.md`
- 修改：`roles/信息收集.yaml`
- 修改：`config.example.yaml`

- [ ] **步骤 1：补充提示词和示例配置**

提示词要求信息收集开始时先读取注入的新鲜资产摘要，默认复用新鲜资产，逐端点调用 `create_asset`，最后输出新增、更新、复用、重扫、跳过、失败统计；排除地址段以服务端配置为准，不把过滤地址写成真实公网 IP。示例配置加入：

```yaml
asset_discovery:
  scan_fresh_days: 7
  excluded_ip_ranges:
    - cidr: 198.18.0.0/15
      reason: VPN 或代理映射地址
      enabled: true
```

- [ ] **步骤 2：运行格式与全量测试**

运行：`gofmt -w internal/config/config.go internal/config/config_test.go internal/handler/config.go internal/handler/config_test.go internal/app/asset_discovery_policy.go internal/app/asset_tools.go internal/app/app.go internal/app/asset_tools_test.go internal/handler/asset_discovery_context.go internal/handler/asset_discovery_context_test.go internal/handler/eino_asset_persistence.go internal/handler/eino_asset_persistence_test.go internal/handler/multi_agent_prepare.go internal/handler/eino_single_agent.go && go test ./... -count=1`

预期：所有 Go 包 PASS。

- [ ] **步骤 3：构建并验证前端产物**

运行：`go build -o /tmp/cyberstrike-server-asset-discovery ./cmd/server && test -x /tmp/cyberstrike-server-asset-discovery && python3 -m json.tool web/static/i18n/zh-CN.json >/dev/null && python3 -m json.tool web/static/i18n/en-US.json >/dev/null`

预期：退出码 0，生成可执行文件 `/tmp/cyberstrike-server-asset-discovery`。

- [ ] **步骤 4：提交**

```bash
git add prompts/single-agent-report.md roles/信息收集.yaml config.example.yaml
git commit -m "docs: define asset discovery persistence workflow"
```

- [ ] **步骤 5：发布到本地运行目录并做接口验收**

备份 `/Users/vito/Projects/CyberStrikeAI/CyberStrikeAI-local/bin/cyberstrike-server`、配置和提示词，替换二进制与提示词后重启 `io.cyberstrikeai.local`。使用已登录会话读取 `/api/config`，确认 `asset_discovery.scan_fresh_days=7` 和默认排除地址段；提交一个临时配置修改再恢复，确认 YAML 持久化和服务健康检查均成功。

- [ ] **步骤 6：生成可回滚交付物**

在 `/Users/vito/Projects/AI 渗透/cyberstrikeai-asset-discovery-20260905` 创建 `MODIFIED_FILE.tar.gz`、`DIFF_FILE.patch`、`VERIFICATION.txt`、可执行 `ROLLBACK.sh`。记录原始哈希、分支字段、基线/修改/回滚命令与逐字输出；在运行目录副本执行 `ROLLBACK.sh` 并验证哈希恢复，实际运行目录保留新版本。
