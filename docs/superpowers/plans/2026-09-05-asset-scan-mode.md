# 资产批量扫描执行配置实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 让资产库批量创建扫描任务时可选择执行模式、专家角色和 1～8 的并行任务数。

**架构：** 复用现有 `/api/roles`、`/api/config` 与 `/api/batch-tasks` 接口，仅扩展资产扫描弹窗及其前端控制逻辑。新增静态契约测试锁定控件、默认值、可用性降级和请求字段，后端队列及数据库保持不变。

**技术栈：** 原生 HTML/CSS/JavaScript、Go `testing`、现有 Gin API。

---

## 文件结构

- 创建：`internal/handler/asset_scan_mode_ui_test.go` — 验证资产扫描弹窗与提交请求的前端契约。
- 修改：`web/templates/index.html` — 增加执行模式、专家角色和并行任务数控件。
- 修改：`web/static/js/assets.js` — 初始化配置、加载角色和多代理状态、规范化输入并提交字段。
- 修改：`web/static/css/style.css` — 为任务配置区增加响应式布局和状态样式。
- 修改：`web/static/i18n/zh-CN.json` — 增加中文产品文案。
- 修改：`web/static/i18n/en-US.json` — 增加英文产品文案。

### 任务 1：建立失败的前端契约测试

**文件：**
- 创建：`internal/handler/asset_scan_mode_ui_test.go`

- [ ] **步骤 1：编写失败的测试**

```go
func TestAssetScanTaskOptionsContract(t *testing.T) {
    // 读取 index.html、assets.js、style.css 与中英文 i18n，断言三个控件、
    // 默认并行数 2、信息收集默认角色、1～8 规范化、任务模式显隐、
    // agentMode/role/concurrency 请求字段和中英文文案均存在。
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/handler -run TestAssetScanTaskOptionsContract -count=1`

预期：FAIL，错误包含 `missing id="asset-scan-agent-mode"`。

### 任务 2：实现任务配置区和请求字段

**文件：**
- 修改：`web/templates/index.html:2036-2046`
- 修改：`web/static/js/assets.js:1070-1165`
- 修改：`web/static/css/style.css:43987-44038`

- [ ] **步骤 1：添加三个表单控件**

在 `asset-scan-task-options` 容器中添加：

```html
<select id="asset-scan-agent-mode">...</select>
<select id="asset-scan-role"><option value="信息收集">信息收集</option></select>
<input id="asset-scan-concurrency" type="number" min="1" max="8" value="2">
```

- [ ] **步骤 2：实现初始化和降级逻辑**

```javascript
async function prepareAssetScanTaskOptions() {
    // 默认 eino_single / 信息收集 / 2；加载启用角色；
    // 读取 window.__csaiMultiAgentPublic 或 /api/config，禁用不可用模式。
}
```

- [ ] **步骤 3：提交真实配置**

```javascript
const agentMode = assetScanAgentModeValue();
const role = document.getElementById('asset-scan-role')?.value || '信息收集';
const concurrency = assetScanConcurrencyValue();
// POST /api/batch-tasks: { ..., agentMode, role, concurrency }
```

- [ ] **步骤 4：补充响应式样式**

三列配置在宽屏等分排列，在窄屏改为单列；禁用项与提示使用现有主题变量。

- [ ] **步骤 5：运行测试验证通过**

运行：`go test ./internal/handler -run TestAssetScanTaskOptionsContract -count=1`

预期：PASS。

### 任务 3：补齐中英文文案并回归验证

**文件：**
- 修改：`web/static/i18n/zh-CN.json:1347-1373`
- 修改：`web/static/i18n/en-US.json:1359-1385`

- [ ] **步骤 1：添加完整文案**

中文包含“执行模式”“专家角色”“并行任务数”“同时运行的资产任务数量（1～8），扫描任务建议设置为 1～2”等；英文提供对应自然表达。

- [ ] **步骤 2：验证 JSON 语法**

运行：`python3 -m json.tool web/static/i18n/zh-CN.json >/dev/null && python3 -m json.tool web/static/i18n/en-US.json >/dev/null`

预期：两个命令均退出 0。

- [ ] **步骤 3：运行相关测试**

运行：`go test ./internal/handler -run 'TestAssetScanTaskOptionsContract|TestAssetDiscoverySettingsContract|TestBatch' -count=1`

预期：PASS。

- [ ] **步骤 4：运行完整测试**

运行：`go test ./...`

预期：所有包通过。

- [ ] **步骤 5：提交实现**

```bash
git add internal/handler/asset_scan_mode_ui_test.go web/templates/index.html web/static/js/assets.js web/static/css/style.css web/static/i18n/zh-CN.json web/static/i18n/en-US.json docs/superpowers/plans/2026-09-05-asset-scan-mode*.md
git commit -m "feat: configure asset batch scan execution"
```

## 自检

- 规格覆盖：模式、角色、默认信息收集、并行数 2、1～8 限制、多代理禁用、对话模式兼容、失败保留均有实现或由现有提交流程覆盖。
- 占位符扫描：计划未包含未定义的待办或模糊“适当处理”。
- 类型一致性：请求字段沿用后端现有 `agentMode`、`role`、`concurrency`；模式值沿用 `eino_single`、`deep`、`plan_execute`、`supervisor`。

