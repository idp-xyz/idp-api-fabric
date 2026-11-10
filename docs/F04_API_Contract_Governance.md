# F04 — IDP API Fabric API 契约与治理规范

**正式路径**：`docs/F04_API_Contract_Governance.md` 　**文档版本**：V1.0（沿用工程基线）　**日期**：2026-11-09  
**状态**：完整设计候选，**待具名领域/安全/架构批准**；本稿中规则与示例是候选实施约束，不是已运行生产系统。  
**输入**：F01（FR-001–071 / NFR-01–18 / AT-01–12）、F02（AI-01–07、ADR-0001–0006 Proposed）、F03（INV-01–13、DM-01–18、OD-01–10）；上位规范 `IDP-SCG-STD-001`。  
**范围**：HTTP OpenAPI、异步事件 AsyncAPI、契约来源/制品、兼容性、策略与审批、内外部分发与开发者体验的治理输入。  
**不在本稿**：不重新定义 F03 聚合，不实现 Gateway/IAM，不伪造 Go 工具兼容结论；F05/F06/F07/F08 各自承担安全实施、双门户高保真、代码与真实验收。

## 0. 执行摘要与设计裁决

F04 将 **`SourceSnapshot → ContractRevision → CheckRun/ReviewDecision → immutable ContractRelease → PublicationSlot`** 定义为可验证的契约治理流水线。每一份**准许对外可见**的内容必须明确对应 `(product_id, api_asset_id, target_id, release_id, artifact_digest, policy_digest, decision_id)`，并通过**当前服务端授权 + 撤销拒绝**检查；从 Git 或在线编辑器导入的草稿从不自动取得公开权限。

| 决策主题 | F04 设计提议 | 真实状态 |
|---|---|---|
| HTTP 规范 | OpenAPI **3.1.2** 为初期验证目标；最新 **3.2.1** 作为兼容候选，不在工具未验证时宣称可正式发布 | Proposed / Tool PoC `NOT_RUN` |
| 事件规范 | AsyncAPI **3.1.0** 为 V1 支持候选；MVP 可以仅登记事件资产元数据并拒绝未经支持的正式解析/发布 | Proposed / PoC `NOT_RUN` |
| 业务错误 | RFC 9457 `application/problem+json` 作为 Fabric 管理 API 的推荐错误表达 | 设计示例，非运行保证 |
| 治理优先级 | **全局不可豁免安全要求 > 产品加强规则 > 可豁免质量规则**；必须记录策略版本、工具版本和输入摘要 | Proposed |
| 对外发布 | Release 不可变；Slot `(target_id,api_asset_id)` 生效指针按 CAS 变更，deny-first；发布到文档门户 ≠ 获得真实业务 API 调用权 | Proposed / ADR-0004 待批 |
| 工具复用 | 检查器可复用成熟 CLI，但抽象为可替换的适配器；结果必须显示 `PASS/FAIL/WARN/UNKNOWN/NOT_RUN` | 设计意图 |

<figure class="architecture-figure"><div class="fig-label">FIGURE 01 · 单一贯通流程 / Controlled Contract Lifecycle</div><div class="fig-grid cols-3"><div class="fig-box tone-light"><b>01 · Source / Snapshot</b><small>Git commit / 受控上传 · source_digest</small></div><div class="fig-box tone-light"><b>02 · Revision</b><small>稳定内容快照 · 拒绝非受控引用</small></div><div class="fig-box tone-light"><b>03 · Check & Review</b><small>Policy digest · Tool version · 审批范围</small></div></div><div class="fig-arrow">↓ 仅当检查完成、审批有效、制品可核验，才能准备不可变 Release</div><div class="fig-grid cols-3"><div class="fig-box fig-primary"><b>04 · Immutable Release</b><small>artifact_digest / manifest</small></div><div class="fig-box tone-green"><b>05 · Target × API Slot</b><small>CAS generation · 授权发布</small></div><div class="fig-box tone-green"><b>06 · Authorized Read</b><small>有效指针 + grant + deny-first</small></div></div><div class="fig-separator">任何不支持规范、摘要不符、过期审批、跨产品受众或撤销未完成：拒绝发布或进入 UNKNOWN/PENDING，绝不自动放行</div><figcaption>示例为 Fabric 的设计协议，不能替代业务系统 API 的真实部署和调用授权。</figcaption></figure>

## 1. 规范效力、适用范围和依赖

### 1.1 项目约束继承

- **上位规则**：IDP-SCG-STD-001 的 CORE、TECH 及按适用性触发的 Profile 规则优先；`governance/policies/F01_Project_Manifest_DRAFT.json` 尚未获正式批准。F04 的 MUST/SHOULD/MAY 仅在项目采用后成为组织生效约束。
- **架构边界**：继承 F02 AI-01–07；Git-first/受控导入、不可变 Release、Registry 权威与读侧投影、Go 模块化单体 + Worker 均是待批准候选；不扩大为自研 API Gateway 或外部凭证签发。
- **领域边界**：遵循 F03 INV-01–13；特别是 INV-04/05/06/07/08/09/12/13 的原文不可变、审批作用域、Slot CAS、deny-first、幂等与 Outbox。
- **风险等级**：`SECURITY_MANDATORY`、`INTEGRITY_MANDATORY` 不允许被项目豁免；关键风险的 R1/R2/R3 等级、责任人和批准权限以 F05/F08 的真实组织采用记录为准，不在此编造负责人姓名。

### 1.2 规范与成熟度矩阵（截至 2026-11-09）

| 用途 | 上游规范 / 当前事实 | Fabric 候选基线 | 依赖验证义务 | 发布处置 |
|---|---|---|---|---|
| HTTP API 文档 | OAS 3.1.2 / 已存在 3.2.1 | `openapi: 3.1.2` | Schema 验证、`$ref` 解析、Diff、Go DTO 生成、渲染器 | 初始可导入校验；生产发布需 F07 PoC |
| HTTP 升级 | OAS 3.2.1（2026-09-10） | 候选，**不默认接受为可发布** | 各消费工具和扩展字段 round-trip | 未证实则 `UNSUPPORTED_SPEC_VERSION` |
| 消息/事件 | AsyncAPI 3.1.0（2026-01-31 发布） | V1 目标；MVP 元数据登记 | Parser、Channel/Operation、Bindings、负载 Schema、渲染器 | 支持范围需 Capability Matrix 批准 |
| HTTP 错误 | RFC 9457 | 推荐 `application/problem+json` | 文档与实现一致性 | 业务错误码必须稳定且不泄密 |
| 废弃通知 | RFC 9745 + RFC 8594 | V1 生命周期指导 | Header 支持、消费者迁移计划 | 退役日不得早于弃用起点 |
| 文档加工 | OpenAPI Overlay 1.2.0 | V1 可选加工机制 | 受控转换、差异可追溯、输出隔离 | Overlay **不是安全授权** |
| 幂等 | IETF Idempotency-Key 仍是 Internet-Draft | 本项目定义固定 Key 语义 | 指纹、重复/冲突、过期与租户作用域 | 不表述为已发布 RFC |

**兼容性不等于符合性**：上游规范支持某语法，不代表所选 Go 代码生成器、Diff 引擎、UI 组件支持所有语义。F07 需为每个受支持版本发布能力清单、限制和真实 PoC 证据；不允许“能解析 YAML”即写“完整支持 OAS”。

<figure class="architecture-figure"><div class="fig-label">FIGURE 02 · 标准采用与工具兼容关系</div><div class="fig-grid cols-3"><div class="fig-box fig-primary"><b>规范合法</b><small>OAS / AsyncAPI 官方文本与 Schema</small></div><div class="fig-box"><b>工具可处理</b><small>Parser · Ref · Diff · Generator · Portal</small></div><div class="fig-box tone-green"><b>业务可发布</b><small>策略+审批+授权+真实验证</small></div></div><div class="fig-separator">三者相互独立；任一关键步骤 UNKNOWN / UNSUPPORTED，发布都保持关闭</div><figcaption>不允许从“通过语法解析”推导“客户端代码生成安全”或“对外已授权”。</figcaption></figure>

## 2. 契约治理对象、身份与 Schema 边界

| 对象 | 必需身份/字段 | 事实权威 | 不可混淆 |
|---|---|---|---|
| APIAsset | `api_asset_id`, `product_id`, `workspace_id`, `owner_ref`, `api_kind`, `lifecycle` | API Catalog | 接口文档版本 |
| ContractSource | `source_id`, `source_type`, `locator`, `allowed_ref_policy` | Registry | 任意 Git branch 的实时内容 |
| SourceSnapshot | `snapshot_id`, `origin_commit`（适用时）、`source_digest`, `captured_at` | Registry | 可覆盖的源文件缓存 |
| ContractRevision | `revision_id`, `api_asset_id`, `source_digest`, `ref_closure_digest`, `format`, `spec_version` | Registry | 发布制品、Draft 编辑缓冲区 |
| PolicySnapshot | `policy_snapshot_id`, `policy_digest`, `rule_set_revision`, `scope` | Policy & Review | 最新默认可变规则 |
| CheckRun | `check_run_id`, `revision_id`, `input_digest`, `policy_digest`, `tool_manifest`, `findings`, `status` | Policy & Review | 人工批准 |
| ReviewDecision | `decision_id`, `reviewer`, `subject_digest`, `scope`, `expires_at`, `decision` | Policy & Review | 发布成功 |
| ContractRelease | `release_id`, `release_label`, `artifact_digest`, `manifest_digest`, `decision_id`, `source_revision_id` | Registry | 门户当前指针 |
| PublicationTarget | `target_id`, `product_id`, `audience`, `environment`, `portal_id` | Publication | 某个 Asset 当前版本 |
| PublicationSlot | `slot_id`, `(target_id,api_asset_id)`, `generation`, `active_release_id`, `visibility_state` | Publication | 仅对全站唯一的 Latest |
| PublicationOperation | `operation_id`, `idempotency_fingerprint`, `expected_generation`, `actor`, `outcome` | Publication | CDN 传播完成 |
| PortalProjection | `target_id`, `asset_id`, `source_generation`, `content_digest`, `observed_at` | 可重建只读投影 | 权限/发布事实的权威 |

### 2.1 Canonical digest 与源文件保真

- `source_digest`：对**原始提交字节**计算 SHA-256，支持保真与取证；不同换行、空白通常导致不同摘要。
- `ref_closure_digest`：将主文档与每个已允许的外部/相对引用的**字节、已解析定位及内容摘要**写入引用闭包清单，按稳定排序与确定性序列化再计算摘要；闭包清单和摘要必须同时保存，禁止在后续重新 fetch 变更内容。
- `build_manifest`：记录 `source_digest / ref_closure_digest / policy_digest / parser_version / generator_version / overlay_digest / command_options / output_digest`。若工具输出非确定性，记录非确定性字段与源内容，不得假称重建字节相同。
- `artifact_digest`：对**最终用于发布的制品原始字节**计算 SHA-256；Registry 将 Release 指向只写一次的 Blob，发布前后重新验证。`manifest_digest` 独立对确定性 Manifest 字节计算。
- `release_label` 是业务展示版本（例如 `1.2.0`），不作为主键；同一 `(api_asset_id, release_label)` 的唯一性需 F03 OD-04 决议。规范版本、业务 API 路径版本、Rev ID、Release Label、Slot generation 分别命名。

### 2.2 引用安全与供应链信任

默认**禁止解析器发起互联网抓取**。相对引用只允许在本次上传/仓库受控工作区内解析，拒绝路径穿越、符号链接逃逸和循环/深度/文件数/总字节数超出上限的闭包；远程引用必须经批准的固定域名、固定内容摘要或已镜像制品处理。上限的具体字节数由 F05/F07 基于威胁模型与容量测试确定，本稿**不捏造**。HTML/Markdown 示例经可信渲染器严格清理；`servers` 与外部链接不作为请求代理的信任依据。

## 3. OpenAPI HTTP 契约设计基线

### 3.1 每个 Operation 必需的业务语义

| 项目 | 推荐要求 | 证明方式 |
|---|---|---|
| 操作身份 | 稳定 `operationId`、明确 `summary`/`description`、Owner 与业务领域 | Lint + 归属关系审查 |
| URL 与 HTTP | 资源化路径、正确方法、符合 HTTP 语义；POST 不承诺天然幂等 | Review + 对照实现 |
| 请求与返回 | 参数、类型、必填、上下文限制、内容类型与成功状态 | Schema + 契约测试 |
| 错误 | `4xx/5xx` 适用状态、稳定业务错误类型、关联 ID；不输出敏感数据 | Schema + 负向测试 |
| 安全 | Operation 的 security requirements / scope 与平台准入策略相符 | 安全策略 + IAM/Gateway 实测（F05/F07） |
| 大型列表 | 排序稳定性、Cursor/Offset 选择依据、上限与一致性说明 | Review + 压测 |
| 业务副作用 | Idempotency-Key、重复负载不匹配、超时/异步完成语义 | API Contract Test |
| 时间金额 | UTC/RFC3339、货币 ISO 4217、金额十进制字符串与精度规则 | Schema + 业务用例 |
| 前后兼容 | 新增字段默认允许未知字段的消费者、Enum 语义的容错策略 | Diff + 消费者验证 |

OpenAPI `security` 既不是运行时授权凭据也不是对象级授权证明；未经授权的 API 不能因为文档中声明 OAuth scope 就自动对外开放。

### 3.2 Schema 表达规范

- OAS 3.1 Schema 与 JSON Schema 2020-12 对齐；`nullable: true` 是 OAS 3.0 的旧习惯，3.1 应使用 `type: ["string", "null"]`（在适用上下文）。
- `required` 表示对象**必须出现属性**；它不自动保证 `null` 不被允许。金额和体积重量必须有单位/币种；如果业务精度要求十进制，避免用二进制浮点近似无说明地表示货币金额。
- 通过自定义 `x-idp-*` 扩展存放 Owner/敏感分级等非标准元数据时，必须形成扩展注册表，保持符合 OAS 的扩展语法；工具不理解扩展不得据此推断它已经执行业务限制。
- 规范名称、Schema 名称和 Operation ID 统一规则应是可配置且版本化的策略，不根据名称直接判断一个业务 API 属于哪个 DDD 限界上下文。
- `format` 注解本身未必由所有校验器强制执行；强格式和行业业务字段必须用专门的校验/契约测试证明。

### 3.3 Fabric 自身的管理 HTTP 示例

`contracts/examples/openapi/cargopai-contract-control.openapi.yaml` 是**DESIGN_EXAMPLE**：说明如何表达创建 Revision 和按 CAS 发布，绝不等同已实现 URL、已签发 OAuth 客户端或真实产品路由。认证操作示例以服务账户的 `client_credentials` scope 表达；具体 IAM 与 Token Endpoint 由 F05 决策。示例使用 `application/problem+json`，附带稳定 `error_code`；区分 `409` 冲突、`412` 条件失败、`422` 语义无效与 `503` 外部依赖不确定。对于无权资源，视信息披露策略可以选择 `403` 或对外统一 `404`，不得从文档示例直接宣告产品安全实现已通过。

## 4. AsyncAPI / 事件契约（V1 详细设计，MVP 守住边界）

| 契约要素 | 设计约束 | 由谁验证 |
|---|---|---|
| `channels` / address | 明确命名空间、生产者拥有者和 Topic 变化策略 | F04 规则 / F07 Parser |
| `operations` | 区分 send / receive 与被描述**应用**的视角，不能把发布者和消费者视角倒置 | 异步集成契约测试 |
| 消息 Envelope | `event_id`, `event_type`, `event_version`, `occurred_at`, `producer`, `correlation_id` | Schema / Consumer Contract |
| 领域事件与集成事件 | 领域内部细节不直接等于外部公开事件；公开事件独立版本与分级 | Domain owner 审核 |
| 重复与顺序 | 最低按至少一次投递设计消费者去重；顺序仅在明确的分区键/协议条件下承诺 | 消费者集成测试 |
| 消息兼容 | 不删除消费者必需字段；枚举扩展、字段语义改变属于需验证变化 | Diff + Consumer Test |
| 错误与重放 | 失败重试、DLQ/隔离、重放窗口、敏感字段禁止出现在 Headers/日志 | F05/F07 |
| Broker 权限 | AsyncAPI 文档不代替消息队列 ACL | 平台运行时检查 |

`contracts/examples/asyncapi/fabric-publication-events.asyncapi.yaml` 仅为单个订阅应用的 **DESIGN_EXAMPLE**，说明其 `receive` 操作；事件生产方另行以实际实现发布契约描述。AsyncAPI 3.1.0 工具兼容、Bindings 选择及真正消息通道均为 `NOT_RUN`。

<figure class="architecture-figure"><div class="fig-label">FIGURE 03 · 事件含义与可靠性责任</div><div class="fig-grid cols-3"><div class="fig-box"><b>Domain Transaction</b><small>Publication Slot CAS + Audit + Outbox</small></div><div class="fig-box fig-primary"><b>Persistent Dispatcher</b><small>至少一次 · 可重试 · Idempotency</small></div><div class="fig-box tone-green"><b>Consumer</b><small>event_id 去重 + generation 去旧</small></div></div><div class="fig-arrow">↓ 投递成功 ≠ 门户边缘传播已验证；DeliveryVerified 需独立外部确认</div><div class="fig-separator">内部 Domain Event ≠ 可公开 Integration Event；AsyncAPI 不证明 Broker ACL 已生效</div><figcaption>状态机所有者、事务边界和 Outbox 关系继承 F03；F04 定义消息格式与兼容政策。</figcaption></figure>

## 5. 变更兼容性：四类判断而非单一 Pass/Fail

| 类别 | 定义 | 默认处理 |
|---|---|---|
| `BREAKING` | 对既有受支持消费者的契约可能失效，且有明确证据（例如删除操作、删除响应必需字段） | 阻止原兼容通道替换；要求新版、迁移与审查 |
| `RISKY` | 依赖消费方实现、协议或业务语义（例如返回枚举新增、分页顺序改变） | 人工评审 + 相关消费者测试，未具证据不可静默放行 |
| `COMPATIBLE` | 按当前已定义的消费约束通常不破坏兼容（例如新增可选响应字段） | 常规检查；仍须按安全规则审核 |
| `UNKNOWN` | Diff 无法解析或缺少消费者、运行时、规范版本的充分信息 | **Fail Closed**；记录工具限制，不转换成 PASS |

**强重要原则**：Diff 给出的“可能兼容”不是业务运行兼容的证明；安全策略、有效审批和发布目标独立于 Diff 判定。

### 5.1 HTTP 变更决策矩阵

| 变化 | 保守类别 | 依据 / 条件 |
|---|---|---|
| 删除 path/HTTP method | BREAKING | 旧客户端将无可用操作 |
| 将可选请求字段改必填 | BREAKING | 旧客户端请求可能被拒绝 |
| 请求字段收窄 enum / maxLength | BREAKING | 原合法请求可能不再有效 |
| 返回字段删除、类型变化 | BREAKING / RISKY | 视是否原先公开承诺与客户端约束 |
| 新增可选响应字段 | COMPATIBLE / RISKY | 严格解码器、签名/缓存消费者需证实 |
| 响应 enum 增加成员 | RISKY | 封闭枚举的客户端可能崩溃；不能武断判 compatible |
| 安全 scope 收紧或权限效果改变 | BREAKING / RISKY | 现有消费方可能失去访问；需要授权安全评估 |
| 错误状态码/类型改变 | RISKY | 重试、幂等与告警行为可能不同 |
| 改变 Idempotency-Key TTL / Scope | RISKY / BREAKING | 重试可见语义发生变化 |
| 修改排序、分页游标语义 | RISKY | 可能产生重复、漏项或客户端死循环 |
| 仅更新文字且语义不变 | COMPATIBLE | 不应包含业务行为隐含变更 |

### 5.2 事件兼容矩阵

| 变化 | 默认等级 | 判定依赖 |
|---|---|---|
| 删除 Topic / Operation | BREAKING | 订阅与路由直接失效 |
| 移除消费者需要字段 | BREAKING | 反序列化与业务计算失效 |
| 新增非必需字段 | RISKY/COMPATIBLE | 消费者是否允许额外字段 |
| 新增枚举值 | RISKY | 枚举开放策略与消费者测试 |
| 改变 Partition/Ordering Key | BREAKING / RISKY | 顺序与去重语义改变 |
| 事件 ID 及重放语义改变 | BREAKING | 幂等和恢复正确性风险 |
| 个人信息字段新增 | SECURITY_MANDATORY REVIEW | 即便结构兼容也不得绕过隐私审批 |

<figure class="architecture-figure"><div class="fig-label">FIGURE 04 · Diff Classifier / 风险分流</div><div class="fig-grid cols-4"><div class="fig-box tone-red"><b>BREAKING</b><small>版本迁移 + 阻断原通道</small></div><div class="fig-box"><b>RISKY</b><small>Owner 评审 + Consumer Test</small></div><div class="fig-box tone-green"><b>COMPATIBLE</b><small>仍进行治理与审批</small></div><div class="fig-box tone-red"><b>UNKNOWN</b><small>工具不支持 / 缺证据 → 阻断</small></div></div><div class="fig-separator">规则可版本化；不得用“自动忽略未知差异”提高通过率</div><figcaption>最终合并结论采用风险上界，独立安全阻断可覆盖 COMPATIBLE。</figcaption></figure>

## 6. 版本、废弃、迁移及政策发布

| 不同版本类型 | 正式字段 | 不得用于 |
|---|---|---|
| 规范版本 | `openapi` / `asyncapi` | API 产品业务版本 |
| 面向消费者的业务版本 | URL `/v1/...` 或协商策略 | Source Revision ID |
| Revision 身份 | `revision_id` | 生效 Slot 状态 |
| 制品业务标签 | `release_label` | Slot 的 CAS generation |
| 发布指针代数 | `slot_generation` | 人类可见 API 版本 |
| 策略版本 | `policy_digest` / `rule_set_revision` | 业务接口版本 |
| 工具版本 | `tool_manifest` | 放弃审核的理由 |

V1 按 `Experimental → Stable → Deprecated → Retired` 表达资产生命周期，与 Revision/Release/Publication 不合并。废弃通知必须定义**通知对象、开始时间、计划停用时间、替代接口、迁移路径、支持窗口**；HTTP 端点可用 RFC 9745 `Deprecation` 与 RFC 8594 `Sunset`，但不能保证所有消费者会读取它们。`Sunset` 时间不能早于 `Deprecation` 时间。正式弃用窗口和通知提前期由产品 SLA 与客户合同确定，当前标记 `TBD`。

## 7. 分层治理规则与策略评估顺序

### 7.1 Rule Registry 模型

`governance/policies/F04_Rules_Registry.json` 是本稿的**机器可读规则提案**，每条含 `rule_id`, `description`, `category`, `severity`, `waivable`, `owner_role`, `scope`, `stage`, `evaluator`, `normative_source`, `verification_mode`。其中 `verification_mode=AUTOMATED_SUBSET` 表示本次提供了小型**示范 lint**，不是已经覆盖完整 OpenAPI 官方 Schema；`MANUAL_REVIEW` / `F07_TOOL_ADAPTER` 不应在输出中标记 PASS。

| 规则类别 | 处理原则 | 典型规则 |
|---|---|---|
| SECURITY_MANDATORY | 全局不可豁免、拒绝优先 | 公开内部 URL、凭证示例、无权外部发布 |
| INTEGRITY_MANDATORY | 不可豁免 | 摘要不匹配、来源引用不受控、策略过期、Release 被改写 |
| CONTRACT_COMPATIBILITY | 阶段可要求迁移及批准 | 删除 Operation、必填变化、枚举风险 |
| QUALITY | 满足正当用途和记录可限定豁免 | operationId 规范、文案、示例丰富度 |
| PRODUCT_EXTENSION | 仅可收紧全球策略 | CargoPai 的特殊业务字段单位/时效 |

### 7.2 优先级和冲突裁决

- 有效规则集 = 全局强制规则 + 可用的全局质量规则 + 产品/受众/环境的追加规则。**产品覆盖规则不得删除全局强制规则，也不得把 BLOCK 改成 WARN。**
- 以固定 `policy_digest` 评价 CheckRun；Policy Change、Input Change、Target/Audience Change、Reviewer Permission Change 或 Decision Expiry 要求重新评估或重新审批，不可直接复用旧批准。
- `UNKNOWN` 和工具故障不等于失败的业务契约已证实有错，但对正式发布为 **block until resolved**，可以继续保存草稿并输出可读的失败证据。
- 一条可豁免规则的 Waiver 必须绑定 `(rule_id, scope, subject_digest, owner, reviewer, expires_at, reason, compensating_control)`；超出原对象范围或过期不得继承；不可豁免规则不允许创建 `ACTIVE` Waiver。
- 安全例外责任和分工在 F05/F08 做具名授权；仅 `reviewed_by ≠ created_by` 字符串判断不足以证明职责分离，还需服务端检查主体独立、授权角色、产品/工作区作用域与有效期。

<figure class="architecture-figure"><div class="fig-label">FIGURE 05 · 策略层次与审核失效条件</div><div class="fig-head"><strong>Global Mandatory / 无豁免底线</strong><small>安全泄露 · 权限边界 · 摘要完整性 · 不受控外部引用</small></div><div class="fig-arrow">↓ 产品只允许追加约束，不得稀释</div><div class="fig-grid cols-2"><div class="fig-box"><b>Global Quality</b><small>命名 / 示例 / 文档 / 兼容评估</small></div><div class="fig-box tone-green"><b>Product Extensions</b><small>CargoPai / Parcel 各自业务规范</small></div></div><div class="fig-arrow">↓ 固定 PolicySnapshot + ToolManifest + Revision Digest</div><div class="fig-head light"><strong>Independent Review Decision</strong><small>目标、受众、环境或输入变化 → 审批作废/重审</small></div><figcaption>威胁模型中不可豁免的范围由 F05 定稿；本图是策略依赖方向，不表示任何规则已经通过真实安全验收。</figcaption></figure>

## 8. 检查流水线及可核验输出

<figure class="architecture-figure"><div class="fig-label">FIGURE 06 · 8 个不可跳过的检查阶段（MVP 推荐）</div><div class="fig-grid cols-4"><div class="fig-box"><b>① Ingest</b><small>保存 bytes、来源、限制</small></div><div class="fig-box"><b>② Parse/Refs</b><small>版本、闭包、安全限制</small></div><div class="fig-box"><b>③ Lint</b><small>命名、Schema、必需文档</small></div><div class="fig-box"><b>④ Diff</b><small>历史兼容性与基线</small></div><div class="fig-box"><b>⑤ Sensitive Scan</b><small>内部地址 / 凭证 / PII</small></div><div class="fig-box"><b>⑥ Policy Evaluate</b><small>规则/例外/目标完整</small></div><div class="fig-box"><b>⑦ Independent Review</b><small>具权威的人工决策</small></div><div class="fig-box tone-green"><b>⑧ Release Verify</b><small>不变制品摘要和 Manifest</small></div></div><div class="fig-separator">任一关键环节 UNKNOWN / FAIL → 不得推进至公开发布；所有结果必须保留证据</div><figcaption>可并发运行独立检查，但通过状态必须与同一组输入摘要和策略版本绑定。图示顺序是逻辑依赖顺序。</figcaption></figure>

建议的 CheckRun JSON 结果（字段名为设计契约）：

```json
{
  "check_run_id": "DESIGN_EXAMPLE-check-001",
  "revision_id": "DESIGN_EXAMPLE-rev-001",
  "input_digest": "sha256:...",
  "policy_digest": "sha256:...",
  "target_scope": {"product_id": "cargopai", "audience": "partner", "environment": "sandbox"},
  "tool_manifest": [{"name": "openapi-parser", "version": "TBD", "support_level": "NOT_VERIFIED"}],
  "status": "UNKNOWN",
  "findings": [{"rule_id": "F04-GOV-020", "level": "BLOCK", "message": "Parser compatibility evidence is missing"}],
  "started_at": "DESIGN_EXAMPLE-time", "completed_at": null
}
```

**状态含义**：`PASS` = 该指定校验项真实执行并得到通过证据；`FAIL` = 指定检查明确拒绝；`WARN` = 检查执行发现风险，需处理；`UNKNOWN` = 检查无法作出判断；`NOT_RUN` = 没有执行。**单个 PASS 不得传播成整个流水线 PASS；静态示例 lint PASS 不得传播成生产 OpenAPI 整链 PASS。**

## 9. ReviewDecision、批准、Waiver 与失效

| 条件 | 批准/发布策略 | 结果 |
|---|---|---|
| 输入摘要、策略快照、ToolManifest、目标范围均匹配；没有不可豁免失败 | 分离职责审核，且 Reviewer 权限实时有效 | 允许准备 READY Release（非自动公开） |
| 产品或受众变更 | 目标范围不同，旧决定无效 | `APPROVAL_SCOPE_MISMATCH` |
| 生效策略版本变更 | 如强制规则新增或严重级别提高 | `POLICY_STALE`，重新检查与复审 |
| Reviewer 权限已撤销 | 不得继续借用历史批准作为新发布授权 | `REVIEWER_AUTH_REVOKED` |
| Waiver 已过期或尝试豁免强制项 | 拒绝 | `WAIVER_EXPIRED` / `WAIVER_FORBIDDEN` |
| Diff 的 UNKNOWN / 契约版本不支持 | 不可自动批准 | `COMPATIBILITY_UNKNOWN` / `UNSUPPORTED_SPEC_VERSION` |

审批历史为追加式，状态迁移与事实不可变约束继承 F03。所有后台定期审查的 TTL、复核频次与应急权限仍待批准。

## 10. 多产品多受众的发布与恢复语义

`PublicationTarget = (product_id, audience, environment, portal_id)`；`PublicationSlot` 唯一 `(target_id,api_asset_id)`。同一 CargoPai Portal 可展示 Shipment 与 Tracking 两个 API，并独立更新。**此为 F03 OD-03 对 ADR-0004 的细化候选，尚需正式架构批准**。

| 发布动作 | 必须验证 | 同一事务中的权威变化 | 外部传播/可见性 |
|---|---|---|---|
| Publish | 当前授权、Asset/Release 属同 Product；Decision 在目标范围有效；制品 digest 正确；expected_generation 匹配；没有 deny | CAS 更新 Slot generation/active_release、Operation、Audit、Outbox | `PENDING` 直到投影和边缘传播确认 |
| Withdraw | 授权/应急权限、预期 Slot、原因 | 清空或拒绝生效指针 + generation++、Audit/Outbox，deny 状态优先 | 立即拒绝所有**受控读路径**；无法控制的缓存残留报告 UNKNOWN |
| Restore | 目标历史 Release 完整、策略和权限重新核实、CAS 匹配、无有效阻断 | 将 Slot 指向已核验历史制品，新 Operation、新 generation | 不改写历史 Release，不把同步中称为完成 |
| Emergency Block | 高优先级应急权限、范围/事件依据 | 新 `AccessBlock` 单调 generation、审计；传播异步 | 读时强制 deny；存量离线副本不可保证回收 |

**拒绝优先**：所有私有内容的查询、下载、搜索结果点击、直连 Release、`/latest` 解析与 CDN/BFF 受控访问路径均必须在可信边界实施当前授权与 deny。Portal Projection 只用于发现和加速，不能决定发布权限。公开内容一旦被第三方抓取无法保证完全撤回，敏感内容误发布应按安全事件响应处理（F05）。

<figure class="architecture-figure"><div class="fig-label">FIGURE 07 · Target × APIAsset Slot / 可控发布粒度</div><div class="fig-head"><strong>CargoPai · partner · sandbox · Developer Portal</strong><small>PublicationTarget target-CP-PARTNER-SBX</small></div><div class="fig-arrow">↓ 独立 Slot · 各自代数，不存在全站唯一的“current release”</div><div class="fig-grid cols-2"><div class="fig-box tone-green"><b>Slot A · Shipment API</b><small>asset-shipment → rel-003 · gen 8</small></div><div class="fig-box tone-light"><b>Slot B · Tracking API</b><small>asset-tracking → rel-005 · gen 3</small></div></div><div class="fig-arrow">↓ 撤销 Shipment 仅改变 Slot A；Tracking 仍指向自己的 Release</div><div class="fig-separator">每次 Read：检查当前生效 Slot / AccessBlock / caller grant；真实业务 API Gateway 并不由该发布动作自动授予调用权</div><figcaption>全部名称、Release ID 和 generation 为 DESIGN_EXAMPLE，不代表已存在对应 API。</figcaption></figure>

### 10.1 典型失败模式

| 故障/竞态 | API 语义与业务处置 | 证据 |
|---|---|---|
| 双发布者同 generation | 一个 CAS 成功，另一个 `412 PRECONDITION_FAILED`；从服务器读取新 generation 再人工处理 | 冲突 Operation + winner ref |
| 同 Idempotency-Key 不同请求指纹 | `409 IDEMPOTENCY_CONFLICT`；不得产生两个发布效果 | 原始指纹和冲突指纹摘要 |
| CheckRun 对旧 Policy/Revision | `409 POLICY_STALE`；重新检查 | 快照版本差异 |
| Object Store 写成，数据库尚未提交 | 无 Release 生效；孤儿文件通过 Job/GC 对账清理 | 作业/暂存状态，非公开 |
| DB 指针提交但 Edge/Projection 更新失败 | 主库记录真实发布；DeliveryState `PENDING/UNKNOWN`；重试并告警 | Outbox/Job/Observation |
| 失效/撤销后 Edge 尚存缓存 | 受控私有路径立即 deny；未确认的第三方缓存不可宣称清除 | Block generation + 传播状态 |
| Git/IAM/Gateway 不可用 | 当前受影响检查 `UNKNOWN`；不得把旧快照当实时授权或部署证明 | 外部依赖错误与 last_observed_at |
| 支持矩阵中不存在的版本 | `UNSUPPORTED_SPEC_VERSION`；拒绝正式处理，不自动降级或静默转换 | Parser capability record |

## 11. Fabric 自身 API 的 HTTP/幂等/错误边界

推荐管理 API 资源采用规范化命名和版本路径；示例仅定义设计形态。`POST /v1/products/{product_id}/assets/{api_asset_id}/revisions` 创建固定输入 Revision；`PUT /v1/publication-slots/{slot_id}` 以 `If-Match` / `expected_generation` 的一致规则 CAS 更新。后者是**修改受控资源状态**，幂等键的重试窗口不得跨 Product、Principal、Slot、Action 误用。

- `Idempotency-Key` 作用域提议：`(product_id, principal_ref, operation_type, target_id, api_asset_id, idempotency_key)`；服务端还应保存规范化请求指纹。Key 保留窗口、最大长度、请求规范化方式需 F07 配合压测与客户 SLA 定稿。
- 第一次合法请求返回结果；同 Key、同指纹的完成重试返回**同一语义结果**（状态码或异步结果链接的精确策略待实现测试）；同 Key、不同指纹返回 409；同 Key 的进行中请求返回明确处理中状态，不重复操作。
- CAS 条件若缺失，返回 `428 Precondition Required`（实现可采用其他显式模式，但契约必须一致）；错误 generation 返回 `412 Precondition Failed`。业务状态不允许但并发条件满足时使用 409。
- 对外错误使用 RFC 9457 的 `type`, `title`, `status`, `detail`（脱敏）、`instance`（可选）及 `error_code`, `trace_id` 扩展，禁止包含凭证、内部存储路径、完整私有策略内容。`status` 与实际 HTTP 状态必须一致。
- 2026 年 IETF 的 `Idempotency-Key` 仍为 Internet-Draft；它用于指导设计，不得把本项目机制表述成已符合正式 RFC。

## 12. API Catalog 元数据、文档产物和发布加工

- `contracts/examples/catalog/cargopai-api-asset.catalog.json` 仅为**设计样本**：由 API Asset 稳定 ID、产品、Owner、受众、标签、源修订关联等构成；它不是 Gateway 路由配置，也不能授予消费者调用权。
- API Reference 生成必须从授权的 Release 派生，标注 `source_release_id`、`artifact_digest` 和发布目标；文档与代码示例要区分生产 / 沙箱基础 URL，不能嵌入真实密钥。
- Overlay 或其他文档加工输出是单独 `transformation_digest` 与 `output_digest`，由新的审查/批准范围覆盖。**不能依靠从私有 OpenAPI 删除敏感字段来实现访问控制**；外部契约可经专门设计审查形成独立发布 artifact。
- 多品牌门户仅共享排版引擎，不共享可访问权限：CargoPai 私有内容不得出现在 Parcel 的全文搜索索引、下载链接、静态构建包或公共 sitemap。
- Catalog 依赖分析与运行时 drift 在 F01 阶段分别为 V1.x / V1；不得将其缺失作为 MVP 已完成的虚假声明。

## 13. CI/CD、Go 工程集成与制品清单

推荐契约在产品的 Go 代码旁维护，经 Pull Request 执行：`parse → schema/lint → diff → sensitive scan → policy → review → release`；Fabric 管理 CheckRun/Approval/制品，业务 CI 平台真正执行各仓库构建，**Fabric 不替代 CI/CD 编排**。

- **机器输出**：标准化 Finding 包含 `rule_id`, `path`, `location`, `severity`, `message`, `evidence_ref`, `tool_version`, `input_digest`。
- **可重建**：必须存原始输入/引用闭包、依赖清单、命令参数、规则版本和生成结果摘要；日志不应输出 Token/Secret。
- **Go DTO 生成**：API Transport DTO/handler 不得成为 DDD Entity；映射由 Adapter/Application 负责。`oapi-codegen` 等工具的规范支持不能凭宣传假定，F07 必须在实际锁定版本与 3.1.2/3.2.1 样例上测试。
- **准入**：MVP 强制可执行规则之外，若 Parser/安全检测/兼容工具缺失或不确定，**不得放行生产公开**；CI 构建的 PASS 与 Fabric 发布审批的 APPROVED 分开。

## 14. 最小机器可执行资产与负向样例

| 路径 | 用途 | 当前验证范围 |
|---|---|---|
| `governance/policies/F04_Rules_Registry.json` | 候选规则清单、严重级别、可豁免性和检查责任 | JSON 结构可验证；规则不等于已全实现 |
| `contracts/examples/openapi/cargopai-contract-control.openapi.yaml` | HTTP 管理 API 设计样本 | PyYAML + JSON Schema 子集 + 自定义 lint |
| `contracts/examples/asyncapi/fabric-publication-events.asyncapi.yaml` | AsyncAPI 3.1 事件样本 | YAML 基本解析 + 自定义结构约束 |
| `contracts/examples/catalog/cargopai-api-asset.catalog.json` | Catalog 元数据样本 | JSON 基础约束 |
| `tests/acceptance/fixtures/F04_negative_*.yaml` | 负样本（重复 operationId、暴露密钥、外部引用、版本不支持） | 应被本地示例 lint 识别 |
| `scripts/validate_f04.py` | 可重复本地**子集**检查 | **不是**官方 OAS/AsyncAPI 全量符合性认证 |
| `tests/acceptance/F04_governance_scenarios.csv` | 真实功能测试计划（24 项） | **全部 `NOT_RUN`** |
| `tests/acceptance/F04_contract_trace.csv` | 所有 71 FR + 18 NFR 的承接/排除分类 | 设计追溯，不代表运行测试 PASS |

`python scripts/validate_f04.py` 只运行本包自带的**示范规则子集**；完整工具链、Webhook/AsyncAPI Bindings、Gateway/IAM、SSRF 检测、审核权限、容量、缓存撤销和真实消息系统测试留给 F07/F08 并保持 `NOT_RUN`。工具提供的错误定位和分类要被 F06 门户如实展示。

## 15. 契约治理场景与拒绝证明

| ID | 场景 | 必须出现的结果 | 阶段 | 证明状态 |
|---|---|---|---|---|
| CG-01 | CargoPai 导入有效 OAS 3.1.2 | 按相同快照建立 Revision；记录 refs/digest | MVP | NOT_RUN |
| CG-02 | 在受控工作区之外解析 `$ref` | 拒绝并保留安全发现 | MVP | NOT_RUN |
| CG-03 | 明文 Token 出现在描述或例子 | SECURITY_MANDATORY 拒绝公开 | MVP | NOT_RUN |
| CG-04 | 解析 OAS 3.2.1 且工具尚未验明支持 | UNSUPPORTED/UNKNOWN 阻止发布 | MVP | NOT_RUN |
| CG-05 | 删除现有 GET Operation | BREAKING，禁止覆盖兼容发布通道 | MVP | NOT_RUN |
| CG-06 | 返回 enum 新增 | RISKY，要求消费者证明 | MVP | NOT_RUN |
| CG-07 | 当前 Policy Digest 不匹配历史批准 | POLICY_STALE，重新检查 | MVP | NOT_RUN |
| CG-08 | 可豁免规则申请有限期例外 | 仅允许具权限审批并限制目标 | MVP | NOT_RUN |
| CG-09 | 尝试豁免安全必选规则 | WAIVER_FORBIDDEN | MVP | NOT_RUN |
| CG-10 | 同一 Slot 并发提交不同 Release | 恰一 CAS 成功，冲突 412 | MVP | NOT_RUN |
| CG-11 | 两个 API 共用 Target 发布 | 指针独立，互不覆盖 | MVP | NOT_RUN |
| CG-12 | 公网错误发布后紧急撤销 | 受控路径 deny-first，传播不确定可观察 | MVP | NOT_RUN |
| CG-13 | 再次准备同一个已批准 Release | 幂等且不覆盖旧制品 | MVP | NOT_RUN |
| CG-14 | 私有下载链接绕过门户导航 | 服务端授权拒绝 | MVP | NOT_RUN |
| CG-15 | 伪造 Git commit / 原文被修改 | Digest 不匹配，禁止 READY | MVP | NOT_RUN |
| CG-16 | 外部 IAM 不可达 | AUTH_UNKNOWN / Fail Closed | MVP | NOT_RUN |
| CG-17 | 文档加工导致敏感字段重新出现 | 输出物重新扫描与审查 | V1 | NOT_RUN |
| CG-18 | AsyncAPI send/receive 方向错误 | 消费者契约校验拒绝 | V1 | NOT_RUN |
| CG-19 | 事件重投/乱序且 generation 较旧 | 幂等忽略旧数据，不回滚当前投影 | V1 | NOT_RUN |
| CG-20 | 业务 API Gateway 与文档版本漂移 | 展示来源、检测时间及风险，不虚报通过 | V1 | NOT_RUN |
| CG-21 | `Deprecation` 早于 `Sunset` | 合理且可公告；颠倒则拒绝计划 | V1 | NOT_RUN |
| CG-22 | 用户跨 Product 搜索私有 Operation | 不泄露元数据和全文结果 | MVP | NOT_RUN |
| CG-23 | SDK 对未知字段及新增枚举的行为 | 在真实消费端回归验证兼容判断 | V1 | NOT_RUN |
| CG-24 | REST/AsyncAPI 工具不支持特定 Schema 语法 | UNKNOWN，提供限制说明并不发布 | MVP/V1 | NOT_RUN |

## 16. 上位规范符合性与风险分工

| 上位门禁 | F04 负责的可审查资产 | 不在此声称完成的证据 |
|---|---|---|
| STD-CP1 | 规则范围、适用 Profile、项目需求承接 | 具名 Project Manifest 及风险授权 |
| STD-CP2 | 契约与 F03 领域身份/发布模型对应表 | 领域专家批准与跨产品评估 |
| STD-CP3 | 规则注册表、兼容判断、样例与检查语义 | 真实系统契约/安全回归通过 |
| STD-CP4 | 双门户应呈现的 CheckRun/Release/拒绝信息 | F06 可用性/可访问性测试 |
| STD-CP5 | 失败恢复策略、幂等和受控发布约束 | 故障注入、压测、恢复演练 |
| STD-CP6 | 发布、审计、废弃与责任分工 | 生产验收、运营 KPI 与具名签核 |

主要符合性关联：`SCG-GOV-002/004/007/009/012`、API/事件与跨域失败恢复相关适用规则、`SCG-UX-008` 及真实证据门禁。适用 Profile、详细规则 ID 与风险等级仍以现有上位标准及项目正式批准的采用表为准。**此处为设计期映射，不是已通过六道门禁。**

## 17. 与 F01/F02/F03 的追溯关系

| 本文设计议题 | F01 需求 | F02 候选架构 | F03 不变量/决策 | 下一份责任 |
|---|---|---|---|---|
| HTTP/AsyncAPI 支持矩阵 | FR-013–022、NFR-05 | AI-05、ADR-0003 | INV-04/05、OD-04 | F07 工具 PoC |
| Lint/Diff/Sensitive Scan | FR-023–030、NFR-07 | AI-02、ADR-0003 | INV-06/07 | F05 安全 / F07 验证 |
| PublicationTarget/Slot CAS | FR-031–036、NFR-06 | AI-04、ADR-0004 | INV-08/09、OD-03 | F05/F07/F08 |
| 两个门户的版本与文档 | FR-040–055 | AI-03/07、ADR-0005 | INV-10/11 | F06 信息架构 |
| CI/CD 与可靠执行 | FR-056–066 | AI-06、ADR-0006 | INV-12/13 | F07/F08 |
| AI/Agent 工具发布 | FR-069–071（V2） | AI-07 | `NOT_IN_F04_MVP` | 未来独立设计 |

详细逐项追溯 CSV 位于 `tests/acceptance/F04_contract_trace.csv`，保留 **全部 71 项 FR 和 18 项 NFR**，每项注明 `PRIMARY_GOVERNANCE / GOVERNANCE_SUPPORT / NEXT_STAGE / OUT_OF_SCOPE_V2`，避免不合理地将所有界面和运维需求声明为“F04 已完成”。

## 18. 双门户必需的治理可解释性输入

| 用户任务 | 内部门户需要展示 | 外部门户需要展示 | 安全要求 |
|---|---|---|---|
| 查看 API 列表 | owner、lifecycle、Revision、策略结论、风险 | 仅授权的 API Reference、版本与迁移说明 | 搜索和详情不得泄露私有内容 |
| 处理检查失败 | 规则 ID、定位、修复建议、权威结果、Waiver | 可公开的标准错误说明；不展示内部扫描细节 | 发现项的敏感证据需脱敏 |
| 发布/撤销 | Scope、Target/Slot、旧新 generation、审批、传播状态 | 当前授权生效版本、变更公告 | 仅内部具权威角色可发布 |
| 阅读代码示例 | 原始/渲染制品摘要、示例可信来源 | 隐去任何真实 Token、沙箱与生产明确区分 | 不把 Try It 请求发向未授权地址 |
| 接入事件 | AsyncAPI Message/Operation/Version | 仅已支持的 Channel/协议和消费指南 | Broker ACL 独立于文档 ACL |

F06 应用这张需求表完成 F06-A/B/C；不应把本稿静态图直接冒充高保真 UI，也不应把 F04 实例资源路由当成已部署页面后台。

## 19. 决策未决清单及明确责任移交

| 编号 | 未决决策 | 当前提议 | 决策归属 |
|---|---|---|---|
| CG-OD-01 | OAS 3.1.2 → 3.2.1 工具支持 | 初期仅 3.1.2 目标 | F07 PoC / 架构 Owner |
| CG-OD-02 | AsyncAPI 3.1.0 解析/Bindings 支持深度 | V1 分协议能力表 | F07 / 事件集成 Owner |
| CG-OD-03 | 产品/全局规则的风险批准角色 | 不可豁免强制控制 | F05/F08 安全与治理 Owner |
| CG-OD-04 | PublicationTarget × APIAsset 细化 | Slot 单独 CAS | F03 OD-03 / ADR-0004 |
| CG-OD-05 | Release Label 唯一与构建再现策略 | 内容和版本分离 | F03 OD-04 / F07 |
| CG-OD-06 | 缓存阻断与公开泄露响应 SLO | deny-first，观测未知不可称成功 | F05/F07/F08 |
| CG-OD-07 | 幂等窗口、存储阈值和业务版本废弃通知 | 值 TBD，合同 / 压测决定 | 产品 Owner / F07/F08 |
| CG-OD-08 | 第三方验证工具与 license/版本 | 选用可替换适配器 | F07 安全/法务复核 |

<figure class="architecture-figure"><div class="fig-label">FIGURE 08 · 设计依赖与下一阶段的责任边界</div><div class="fig-grid cols-3"><div class="fig-box"><b>F01 / F02 / F03</b><small>需求 · 架构 · 领域事实与不变量</small></div><div class="fig-box fig-primary"><b>F04 · Governance</b><small>规范 / 规则 / Diff / 审批 / 发行输入</small></div><div class="fig-box tone-green"><b>F05 / F06 / F07 / F08</b><small>安全、双门户、工程验证、真实验收</small></div></div><div class="fig-separator">F04 中的设计提议不替代 ADR 审批；F07 验证失败必须回馈 F04 规则和支持矩阵</div><figcaption>每份后续文档使用同一 V1.0 工程目录，不新建重复版本或与上游含义冲突的平行标准。</figcaption></figure>

## 20. 交付完成定义与证据状态

- **已交付的设计资产**：F04 Markdown（唯一维护源）、同名离线 HTML、8 幅关联业务的可视化图、机器可读 Rule Registry、OpenAPI/AsyncAPI/Catalog 示例、负样本、初级本地校验脚本、71 FR + 18 NFR 追溯 CSV、24 个真实业务验证场景计划。
- **可以校验的事实**：文件完整性、结构、编号、示例 YAML/JSON 解析、初级 Lint 与预期负样本诊断、HTML 导航和构建可复现性。仅这些项目在真实执行后才能记录 PASS。
- **尚未被证明的能力**：完整 OAS/AsyncAPI 规范符合性、Go 代码生成兼容性、真实多租户授权、Git/对象存储/数据库/网关集成、性能、Edge 缓存撤销、生产 SDK 与发布恢复；全部保持 `NOT_RUN`。
- **正式签收条件**：具名标准采用与风险裁决、F02/F03/ADR 决策审查、F05 安全授权边界、F07 工具链 PoC、F08 端到端验证按真实证据通过。本文设计稿完成不代表这些条件已满足。

## 21. 规范参考与来源

- [OpenAPI Specification v3.1.2](https://spec.openapis.org/oas/v3.1.2.html)；[v3.2.1](https://spec.openapis.org/oas/v3.2.1.html)：HTTP 契约规范、引用与安全实现注意事项。
- [AsyncAPI Specification v3.1.0](https://www.asyncapi.com/docs/reference/specification/v3.1.0)：消息驱动 API 描述，发送/接收视角。
- [OpenAPI Overlay v1.2.0](https://spec.openapis.org/overlay/v1.2.0.html)：规范化、可重复文档加工，不代表权限控制。
- [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457)：HTTP Problem Details；[RFC 9745](https://www.rfc-editor.org/rfc/rfc9745)：Deprecation；[RFC 8594](https://www.rfc-editor.org/rfc/rfc8594)：Sunset。
- [IETF Idempotency-Key Internet-Draft](https://datatracker.ietf.org/doc/draft-ietf-httpapi-idempotency-key-header/)：截至文档日期仍为草案，不能误写成 RFC。
- `docs/F01_Product_Requirements.md` / `docs/F02_System_Architecture.md` / `docs/F03_Domain_and_Data_Model.md` 和上位 `IDP-SCG-STD-001` 为本项目直接继承依据。
