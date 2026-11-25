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

### 3.1.1 已实现的列表与查询参数约定（PR #7，2026-11-17 同步）

`api/openapi.yaml`（OpenAPI 3.1.2）是管理 API 的契约，HTTP DTO 与处理器接口由该文件生成。Product、Workspace、WorkspaceMembership 和 ProductRoleAssignment 自 0.5.0 起采用下列分页约定。受众配置列表自 0.6.0 起采用同一约定，按 `(created_at, audience_config_id)` 正序排列。访问规则见 F05 §6.1、§6.3。

| 项目 | 当前实现 |
|---|---|
| 参数 | `page_size` 为 1–100，默认 50；`page_token` 原样使用上一页的 `next_page_token` |
| 应答 | 必有 `items`，空页为 `[]`；仅还有下一页时包含非空 `next_page_token`，刚好装满的最后一页也不包含；不返回总数；`Cache-Control: no-store` |
| 固定排序 | Product/Workspace 按 `(created_at, id)`，成员/负责人按 `(granted_at, id)`，从早到晚；授权过滤在分页前完成 |
| 选择游标的理由 | 按稳定位置继续读取；避免 OFFSET 在前面新增/撤销时重复或漏项，并使用迁移 004 的索引；吞吐和深页延迟的实测见[列表压测记录](../tests/performance/README.md) |
| 一致性 | 每页读取当时的状态，不是整次翻页的快照；翻页开始前存在且结束后仍有效的记录恰好一次，期间新增/结束的记录可能出现或不出现 |
| 令牌 | 版本 1、列表种类、UTC 微秒时间、UUID 的规范 JSON，经无填充 base64url 编码；不签名、不绑定调用方、不设到期时间，不能作为授权凭据；每页重新授权 |
| 无效令牌 | 格式不规范、其他列表种类、版本或位置无效，均为 400 `PAGE_TOKEN_INVALID`；将来改变排序/游标语义需升令牌版本，旧令牌失败后从头读取 |
| 查询参数 | 所有接口拒绝未声明、无值或不符合契约的参数，返回 400 `REQUEST_INVALID`；包括 `/healthz`、`/version`，探活请求不得附加无关参数 |
| 当前范围 | 上述五类列表没有筛选、搜索、可选排序或已撤销记录列表；成功列表不写审计，授权拒绝按 F05 留证。0.8.0 起的 API 资产列表和修订历史采用同一分页约定，资产列表另有过滤条件，见 §11.3 |

传输层先检查身份、参数和请求体，再进入用例；参数错误不代表已经查过对象或授权，也不产生业务变更/授权拒绝审计。分页令牌即使被自行构造或转交，也只定位调用方当时获准读取的集合，不能放宽产品/Workspace 范围。该分页实现和局部负载证据不等于正式 SLO、生产容量或外部 IAM 验收。

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

评审、撤回、批准、驳回和豁免见 §11.5。Release 引用批准时的有效性校验见 §11.6。

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

### 11.1 已实现的受众配置与品牌接口（PR #10 / #11）

下表路径以 `/v1/products/{product_id}` 开头。这些接口随管理 API 0.6.0 和 0.7.0 加入，访问规则见 F05 §6.3。

| 操作 | 请求与响应 |
|---|---|
| `GET …/audience-configs` | 返回有效配置，采用 §3.1.1 的分页约定 |
| `POST …/audience-configs` | 提交 `audience`、`environment` 和可选的 `docs_url`。成功返回 201、`Location` 和配置的 `ETag`。重复配置同一有效组合返回 409 `AUDIENCE_CONFIG_EXISTS` |
| `GET …/audience-configs/{audience_config_id}` | 返回配置详情和配置的 `ETag` |
| `PUT …/audience-configs/{audience_config_id}` | 以配置的 `ETag` 作 `If-Match` 更新 `docs_url`，用 `{}` 清除入口。返回配置及当前 `ETag` |
| `DELETE …/audience-configs/{audience_config_id}` | 直接移除当前有效配置，无 `If-Match` 条件。首次成功返回 204，重复移除返回 404 `AUDIENCE_CONFIG_NOT_FOUND` |
| `GET …` / `PUT …/brand` | 产品详情返回 `brand` 和 Product 的 `ETag`。更新时将该 `ETag` 作为 `If-Match`，整体替换品牌。`name` 必填，省略 `logo_url` 或 `primary_color` 清除对应值。成功返回更新后的 Product 和当前 `ETag` |

两个 `PUT` 都接受双引号包围的单个正整数版本作为强标签。缺少 `If-Match` 返回 428 `PRECONDITION_REQUIRED`，版本过期返回 412 `PRECONDITION_FAILED`，标签格式无效返回 400 `REQUEST_INVALID`。标签匹配且内容相同时返回现有内容和版本。登记产品的 201 响应也带 Product 的 `ETag`；产品列表仅随各项返回 `brand`。

请求体只接受一个 JSON 值。`docs_url` 与 `logo_url` 均为长度至多 2048 的 HTTPS URL，禁止凭证、空白字符和控制字符。Fabric 按原值存储链接，读取内容由使用方负责。`primary_color` 使用 `#RRGGBB`，保留输入的大小写。写入收到 503 `OUTCOME_UNCERTAIN` 时，先读取当前状态再决定是否重试。

### 11.2 通用治理约定

推荐管理 API 资源采用规范化命名和版本路径。设计示例中，`POST /v1/products/{product_id}/assets/{api_asset_id}/revisions` 创建固定输入 Revision，`PUT /v1/publication-slots/{slot_id}` 以 `If-Match` / `expected_generation` 的一致规则 CAS 更新。后者修改受控资源状态，幂等键及重试窗口需绑定对应的 Product、Principal、Slot 和 Action。

- `Idempotency-Key` 作用域提议：`(product_id, principal_ref, operation_type, target_id, api_asset_id, idempotency_key)`；服务端还应保存规范化请求指纹。Key 保留窗口、最大长度、请求规范化方式需 F07 配合压测与客户 SLA 定稿。
- 第一次合法请求返回结果；同 Key、同指纹的完成重试返回**同一语义结果**（状态码或异步结果链接的精确策略待实现测试）；同 Key、不同指纹返回 409；同 Key 的进行中请求返回明确处理中状态，不重复操作。
- CAS 条件若缺失，返回 `428 Precondition Required`（实现可采用其他显式模式，但契约必须一致）；错误 generation 返回 `412 Precondition Failed`。业务状态不允许但并发条件满足时使用 409。
- 对外错误使用 RFC 9457 的 `type`, `title`, `status`, `detail`（脱敏）、`instance`（可选）及 `error_code`, `trace_id` 扩展，禁止包含凭证、内部存储路径、完整私有策略内容。`status` 与实际 HTTP 状态必须一致。
- 2026 年 IETF 的 `Idempotency-Key` 仍为 Internet-Draft；它用于指导设计，不得把本项目机制表述成已符合正式 RFC。

### 11.3 已实现的 API 资产与契约修订接口

本节对应 PR #12、#14–#17 以及随后的 Git 来源和原文读取。下表路径以 `/v1/products/{product_id}` 开头。这些接口随管理 API 0.8.0–0.12.0 加入：0.8.0 加入资产登记与查询，0.9.0 加入修订导入，0.10.0 加入修订详情与历史，0.11.0 加入 Git 来源，0.12.0 加入原文读取。数据模型见 F03 §5.7，访问规则见 F05 §6.4。

| 操作 | 请求与响应 |
|---|---|
| `GET …/assets` | 列出调用方可见的资产，按 `(created_at, api_asset_id)` 正序，采用 §3.1.1 的分页约定。可用 `workspace_id`、`api_kind`、`domain_key` 过滤，`q` 是对资产 key、名称和领域 key 的不区分大小写的子串匹配，至多 120 个字符。过滤只作用在可见的资产上；翻页时保持过滤条件不变。0.31.0 起每一项带概况 `overview`，另有 `check_status` 和 `release_pending` 两个筛选，见 §11.9 |
| `POST …/assets` | 提交 `workspace_id`、`asset_key`、`display_name`、`domain_key`、`api_kind`（`HTTP` 或 `EVENT`）。成功返回 201 和 `Location`，`lifecycle` 为 `EXPERIMENTAL`。同一产品内 key 重复返回 409 `API_ASSET_KEY_TAKEN` |
| `GET …/assets/{api_asset_id}` | 返回资产详情；0.31.0 起读得了修订的人另得到概况 `overview`，见 §11.9 |
| `POST …/assets/{api_asset_id}/revisions` | 提交 `entrypoint`、`files`（每项为 `path` 和 `content_base64`），以及可选的 `git_origin`（`repository`、`commit`、可选 `ref`）。登记了新闭包返回 201 和 `Location`；同一来源下已登记的闭包返回 200 和原修订 |
| `GET …/assets/{api_asset_id}/revisions` | 修订历史，按采集时间从早到晚，采用 §3.1.1 的分页约定。只有元数据，不含原文 |
| `GET …/assets/{api_asset_id}/revisions/{revision_id}` | 修订的元数据：序号、状态、`source_kind`、`git_origin`、入口路径、`spec_version`、`source_digest`、`ref_closure_digest` 和创建时间 |
| `GET …/assets/{api_asset_id}/revisions/{revision_id}/source` | 修订的原文：入口和它引用的每个文件，按路径排序，逐字节以标准 Base64 返回，每个文件带自己的 SHA-256 |

这些资源登记后不变，所以没有 `ETag` 和 `If-Match`。全部响应带 `Cache-Control: no-store`。

**导入。** 文件是 UTF-8 的 YAML 或 JSON，以标准 Base64 提交；一次最多 16 个文件、解码后合计 40960 字节，HTTP 请求体不超过 65536 字节。入口必须声明 OpenAPI 3.1.2，否则返回 422 `UNSUPPORTED_SPEC_VERSION`。引用只能是包内的相对 JSON Pointer；远程引用、越出包的路径、循环等返回 422 `CONTRACT_SOURCE_INVALID`。Base64 不合法返回 400 `REQUEST_INVALID`。资产不是 `HTTP` 类型返回 409 `API_ASSET_KIND_UNSUPPORTED`，产品已暂停返回 409 `PRODUCT_NOT_ACTIVE`。导入没有 `Idempotency-Key`：幂等来自内容，相同闭包重复提交得到同一个修订，响应以 200 而不是 201 区分。

**Git 来源。** `git_origin` 的三个字段的形式都写在契约里：仓库是不含凭证、查询串和片段的 https 网址，主机名小写，至多 300 个字符；提交是 40 或 64 位小写十六进制的完整提交名，缩写、分支名和标签名都不接受；引用是 `refs/` 开头的完整引用名，至多 200 个字符。形式不符的请求在进入用例之前返回 400 `REQUEST_INVALID`，带凭证的网址不会被写入数据库或日志。带来源时，入口和文件路径就是文件在仓库里的路径。Fabric 记录声明，不读取仓库。同一仓库、提交和入口带着另一份闭包再次提交，返回 409 `GIT_ORIGIN_CONFLICT`，不留下任何记录。

**读取原文。** 响应包含 `revision_id`、`source_snapshot_id`、`entrypoint`、`source_digest`、`ref_closure_digest` 和 `files`。入口文件的摘要就是 `source_digest`。提交时没有被引用到的文件不在其中。Fabric 在返回前按存储内容重新计算摘要；与修订记录不符时不返回文件，响应为 500 `INTERNAL_ERROR`；修订编号和原因写入日志，日志的 `correlation_id` 就是响应里的 `trace_id`。此时修订的元数据仍可读取。

**兼容性。** 0.11.0 给 `ContractRevision.source_kind` 增加了 `GIT`，并给 `ContractRevision` 增加了可选的 `git_origin`。按 §5.1，响应枚举增加成员属于 RISKY：把枚举当作封闭集合、或拒绝未知字段的客户端，需要先升级才能读取从 Git 导入的修订。没有带来源导入之前，响应与 0.10.0 相同。

### 11.4 已实现的契约检查接口

本节对应 PR #22–#33。下表路径以 `/v1/products/{product_id}/assets/{api_asset_id}/revisions/{revision_id}` 开头。这些接口随管理 API 0.13.0–0.16.0 加入：0.13.0 加入发起检查，0.14.0 加入检查详情和历史，0.15.0 加入修订比较，0.16.0 让检查可以指定基线并带出比较结果。数据模型见 F03 §5.8，访问规则见 F05 §6.5。0.17.0 起评审人也能读检查和比较（§11.5）。

| 操作 | 请求与响应 |
|---|---|
| `POST …/check-runs` | 没有请求体，可选查询参数 `baseline_revision_id`。记录了新的检查返回 201、`Location` 和检查全文；同一修订、策略、工具和基线已有记录时，返回 200 和那一次检查，不带 `Location` |
| `GET …/check-runs` | 这个修订的检查历史，按完成时间从早到晚，同一时刻按编号，采用 §3.1.1 的分页约定。每项是概要，不含规则结论和发现 |
| `GET …/check-runs/{check_run_id}` | 一次检查的全文 |
| `GET …/compatibility?baseline_revision_id=…` | 比较这个修订（候选）和同一资产的另一个修订（基线）。只读，不记录检查 |

全部响应带 `Cache-Control: no-store`。检查记录后不变，所以没有 `ETag` 和 `If-Match`。检查在请求内同步完成，没有排队和进度状态，读到的检查都是已完成的。没有 `Idempotency-Key`：同一组输入只记一次，重复请求以 200 而不是 201 区分。

**检查全文。** 包含 `check_run`（概要）、`outcomes` 和 `findings`，带基线时还有 `compatibility`。概要有检查、产品、资产和修订的编号，`input_digest`（修订的闭包摘要），`policy_snapshot_id`、`policy_set_id`、`policy_digest`，`tool_manifest`，`status`，`summary`（各状态的规则数和发现总数）和起止时间；带基线时另有基线修订及其摘要、总分类和比较器。`outcomes` 每条规则一项：编号、描述、类别、级别、可否豁免、状态、发现数，以及评估它的工具（`NOT_RUN` 的规则没有工具）。`findings` 每项是规则、级别、文件路径、可选的 JSON Pointer 和行号、说明，按规则编号排列；每条规则最多列出 50 个，发现数记的是全部。

**状态。** 规则的状态是 §8 的五种。一次检查的 `status` 取各规则的风险上界，顺序是 `FAIL`、`UNKNOWN`、`WARN`、`PASS`，不会是 `NOT_RUN`。基线的 23 条规则里有 10 条还没有工具（见下表之后），所以目前 `status` 只会是 `FAIL` 或 `UNKNOWN`：没有阻断级发现的检查是 `UNKNOWN`，不是 `PASS`。这是 §8“单个 PASS 不得传播成整个流水线 PASS”的直接结果。

**规则。** 策略是 §7.1 登记表里阶段为 MVP 的 23 条规则，策略集名称 `fabric-global-contract-governance`。内置工具评估下面 13 条；除标明的以外，级别都是 `BLOCK`。

| 规则 | 评估的内容 |
|---|---|
| F04-GOV-001 | 入口文件声明的 `openapi` 是 3.1.2 |
| F04-GOV-002 | `info.title` 和 `info.version` 是非空的文本 |
| F04-GOV-003 | `paths` 下的每个操作有 `operationId` |
| F04-GOV-004 | 任何文件的任何一行都没有凭证形式的内容：Bearer 令牌，赋给 `password`、`api_key`、`client_secret` 这类名字的值，私钥头，JWT，AWS、GitHub、GitLab、Slack 的令牌，带密码的 URL。前两种以 `DESIGN_EXAMPLE` 开头或不含数字的不算 |
| F04-GOV-005 | 没有引用指向提交的文件之外：网址、绝对路径、越出包的路径、没有提交的文件 |
| F04-GOV-006 | `paths` 下的每个操作都有生效的安全要求，可以是操作自己的，也可以是文档级的；空列表不算 |
| F04-GOV-007（`WARN`） | `paths` 下各操作的错误响应（4xx、5xx、`default`）提供 `application/problem+json` |
| F04-GOV-008 | `operationId` 在整份文档内不重复，`webhooks` 下的操作也算在内 |
| F04-GOV-009 | 每个文件都是一份良构、读法唯一的文档 |
| F04-GOV-010 | 任何一行都没有内网主机的 URL：回环、私有和链路本地地址，`localhost`，`.internal`、`.local`、`.corp` 等后缀，以及 http、https、ws、wss 下不带点的主机名 |
| F04-GOV-011 | 入口文档符合官方 OpenAPI 3.1 JSON Schema。Schema 内嵌在程序里（`schema-base/2026-11-05`，方言 `dialect/2024-11-10`），校验时不访问网络。文档声明了别的 `jsonSchemaDialect`，或数值超出校验上限时，为 `UNKNOWN` |
| F04-GOV-012 | 按导入时的限制重新解析登记的文件，得到的闭包摘要与修订记录的一致 |
| F04-GOV-013 | 只在检查指定了基线时评估，见“检查里的基线” |

F04-GOV-014 到 020、025 到 027 这 10 条（Release 摘要链、发布授权、审批绑定、渲染清理、独立评审等）没有工具，每次检查都记为 `NOT_RUN`。F04-GOV-001、005、009、012 复核的是导入时已经执行过的限制。读文档内容的规则（001–003、005–008、011）在文件不是良构文档或超出读取上限时为 `UNKNOWN`；004 和 010 按行读原始字节，注释里的内容也在内。F04-GOV-004 和 010 按已知的形式匹配，`PASS` 只说明没有匹配到这些形式，不是“没有凭证”的证明。发现的说明不复述命中的内容。

登记表里 F04-GOV-011、012、013 的 `verification_mode` 仍是 `F07_TOOL_ADAPTER`。§7.1 说这一类规则不应在输出中标记 `PASS`；内置工具现在评估这三条，评估通过时给出 `PASS`。登记表没有随实现更新，这一处不一致待裁决。

**修订比较。** 比较器 `fabric-openapi-compatibility` 只比较两个修订的入口文档，并要求两边都是通过官方 Schema 的 3.1.2 文档、闭包都核对通过。它按 §5 的四类给每处差异分类，总分类取最重的一类，从重到轻是 `BREAKING`、`UNKNOWN`、`RISKY`、`COMPATIBLE`。没有列在下表里的差异一律是 `UNKNOWN`。

| 分类 | 差异和它的代码 |
|---|---|
| `BREAKING` | 删除路径 `PATH_REMOVED`；删除操作 `OPERATION_REMOVED`；参数变为必填或新增必填参数 `PARAMETER_REQUIRED`；请求体变为必填 `REQUEST_BODY_REQUIRED`；请求 schema 收紧 `REQUEST_SCHEMA_TIGHTENED`（对象新增必填成员、字符串枚举减少成员、`maxLength` 新增或变小） |
| `RISKY` | 安全要求或安全方案改变 `SECURITY_CHANGED`；新路径可能与已有的路径模板重叠 `PATH_MAY_OVERLAP`；错误响应（4xx、5xx、`default`）改变 `ERROR_RESPONSE_CHANGED`；响应的字符串枚举增加成员 `RESPONSE_ENUM_EXPANDED` |
| `COMPATIBLE` | 新增路径 `PATH_ADDED`；新增操作 `OPERATION_ADDED`；新增可选参数或参数变为可选 `OPTIONAL_PARAMETER`；新增可选请求体或请求体变为可选 `OPTIONAL_REQUEST_BODY`。只改标题、摘要、描述、版本号和 `externalDocs` 不算差异 |
| `UNKNOWN` | 文档不受支持 `DOCUMENT_UNSUPPORTED`；闭包没有核对通过 `REFERENCE_CLOSURE_UNVERIFIED`；入口路径或被引用的文件有变 `REFERENCE_FILES_CHANGED`；安全方案之外的 `components` 有变 `COMPONENTS_CHANGED`；以及其余没有分类的改动：`DOCUMENT_CHANGED`、`PATHS_EXTENSION_CHANGED`、`PATH_REFERENCE_CHANGED`、`PATH_SETTINGS_CHANGED`、`OPERATION_CHANGED`、`PARAMETER_CHANGED`、`PARAMETER_REFERENCE_OR_DUPLICATE`、`REQUEST_BODY_CHANGED`、`REQUEST_CONTENT_CHANGED`、`RESPONSE_CHANGED` |

对照 §5.1 的矩阵：删除路径和操作、请求约束变严、响应枚举增加成员、安全和错误响应的改变、只改文字，这几行已有对应的分类；安全的改变一律记为 `RISKY`。返回字段的删除和类型变化、新增可选响应字段、幂等和分页语义的改变还没有专门的判断，落在 `UNKNOWN`。

比较的响应包含 `baseline` 和 `candidate`（各有修订编号和输入摘要）、`tool`、`classification`、`changes` 和 `truncated`。`changes` 每项是分类、代码和可选的 JSON Pointer，至多 100 条；超过时保留最重的 100 条，`truncated` 为 `true`，总分类不受截断影响。方向有意义：删掉一条路径是 `BREAKING`，反过来比是新增路径，`COMPATIBLE`。一个修订与自己比较是 `COMPATIBLE`，没有差异。

**检查里的基线。** 发起检查时带 `baseline_revision_id`，比较结果就写进这次检查，并决定规则 F04-GOV-013：`BREAKING` 为 `FAIL`，每处破坏性差异一条发现；`RISKY` 和 `UNKNOWN` 为 `UNKNOWN`，即还需要别的证据才能放行（§5 的 Fail Closed）；`COMPATIBLE` 为 `PASS`。不带基线时这条规则是 `NOT_RUN`。同一个修订带不同的基线，或不带基线，各是一次检查。基线必须是同一资产的另一个修订：与被检查的修订相同返回 400 `REQUEST_INVALID`，不属于这个资产或不存在返回 404 `REVISION_NOT_FOUND`。基线由调用方指定；Release 还没有，Fabric 不会自己选“上一个已发布的版本”。

**错误。** 修订不属于该资产或不存在，返回 404 `REVISION_NOT_FOUND`；检查不属于该修订或不存在，返回 404 `CHECK_RUN_NOT_FOUND`。编号不是 UUID、页大小超过 100、比较时没有给基线，返回 400 `REQUEST_INVALID`；分页令牌属于另一个修订的列表，返回 400 `PAGE_TOKEN_INVALID`。产品已暂停时不能发起检查，返回 409 `PRODUCT_NOT_ACTIVE`，重复请求已有的检查也是如此；读取和比较不受影响。存储的检查与记录时不符（F03 §5.8），或修订的原文核对失败时，返回 500 `INTERNAL_ERROR`，响应不说原因；日志在 `correlation_id`（就是响应里的 `trace_id`）下写明是哪一次检查和原因。这时同一修订的检查历史也返回 500，其他修订不受影响。

**与 §8 示例的差别。** §8 的 CheckRun 示例里的 `target_scope` 没有实现：检查不绑定受众和环境。`tool_manifest` 的每项只有名称和版本，没有 `support_level`。

**兼容性。** 0.13.0–0.16.0 只增加操作和 schema，没有改变此前已有的响应。0.16.0 给 0.13.0 加入的发起检查增加了可选查询参数，并给检查的全文和概要增加了可选的 `compatibility`；不带基线发起的检查，响应里没有这个成员。

### 11.5 已实现的契约评审接口

本节对应管理 API 0.17.0–0.19.0：0.17.0 加入评审人角色，0.18.0 加入提交评审、撤回和读取，0.19.0 加入批准和驳回。下表路径以 `/v1/products/{product_id}/assets/{api_asset_id}/revisions/{revision_id}` 开头。数据模型见 F03 §5.9，访问规则见 F05 §6.6。

| 操作 | 请求与响应 |
|---|---|
| `POST …/reviews` | 提交 `check_run_id`、`scope` 和可选的 `note`。成功返回 201、`Location` 和评审，修订变为 `IN_REVIEW` |
| `GET …/reviews` | 这个修订的评审历史，按申请时间从早到晚，同一时刻按编号，采用 §3.1.1 的分页约定 |
| `GET …/reviews/{review_id}` | 一条评审 |
| `POST …/reviews/{review_id}/withdrawal` | 请求体是 `{}` 或 `{"reason": …}`。成功返回 200 和评审，修订变为 `WITHDRAWN` |
| `POST …/reviews/{review_id}/decision` | 提交 `outcome`（`APPROVED` 或 `REJECTED`），以及 `reason`、`valid_until`、`waivers`。成功返回 200 和评审，修订变为 `APPROVED` 或 `REJECTED` |

全部响应带 `Cache-Control: no-store`。申请和结论记录后不变，所以没有 `ETag` 和 `If-Match`；并发由修订的状态保证，见“状态和并发”。三个写操作都没有 `Idempotency-Key`：重复提交、重复撤回和重复决定得到 409，不会产生第二条记录。

**评审。** 响应包含评审、产品、资产和修订的编号，`check_run_id` 和 `input_digest`（那次检查读的内容，即修订的闭包摘要），`status`，`scope`，可选的 `note`，`requested_by`（签发方和主体）和 `requested_at`；结束之后还有 `conclusion`。`status` 在结束之前是 `IN_REVIEW`，之后是结论的 `outcome`：`WITHDRAWN`、`APPROVED` 或 `REJECTED`。`conclusion` 有 `outcome`、可选的 `reason`、`concluded_by`、`concluded_at`；批准还有 `valid_until`，有豁免时还有 `waivers`，按规则编号排列。

**提交。** `scope` 是 1 到 6 个不重复的 `{audience, environment}`，受众是 `public`、`partner`、`internal`，环境是 `sandbox`、`production`；响应里按受众、环境排序。`note` 是 1–500 个字符的一行文字。`check_run_id` 必须是这个修订的一次检查，否则返回 404 `CHECK_RUN_NOT_FOUND`。这次检查还必须能作为决定的依据：

| 情形 | 响应 |
|---|---|
| 检查不是按现行策略和现行工具版本做的 | 409 `POLICY_STALE`，重新检查后再提交 |
| 检查里有不可豁免的规则是 `FAIL` 或 `UNKNOWN` | 409 `REVIEW_BLOCKED`，要改契约，提交新修订 |
| 修订是 `IN_REVIEW` 或 `REJECTED`（0.23.0 之前还包括 `APPROVED`；之后已批准的修订可以提交复审，见 §11.7） | 409 `REVISION_STATUS_CONFLICT` |
| 产品已暂停 | 409 `PRODUCT_NOT_ACTIVE` |

“现行”指检查的 `policy_digest` 等于现在的基线策略，`tool_manifest` 里的每个工具都是现在的版本。带不带基线都可以；带了基线而 F04-GOV-013 没通过时，这条规则可豁免，见“决定”。可豁免的规则没通过不妨碍提交。

**撤回。** 只有提交这条申请的人能撤回，并且他仍是这个 Workspace 的作者；别人撤回返回 403 `FORBIDDEN`。评审已经结束时返回 409 `REVIEW_CONCLUDED`。`reason` 可选，1–1000 个字符的一行文字。产品暂停时仍可撤回。撤回后的修订可以再次提交，那是它的下一条评审。

**决定。** 驳回必须带 `reason`，不能带 `valid_until` 和 `waivers`。批准必须带 `valid_until`：晚于现在，至多 365 天之后；`reason` 可选。批准时检查必须仍是现行的，否则返回 409 `POLICY_STALE`；这样的评审仍然可以驳回或撤回。

`waivers` 的每一项是 `rule_id`、`reason`、可选的 `compensating_control` 和 `expires_at`，至多 100 项。批准必须给检查里每条可豁免而状态是 `FAIL` 或 `UNKNOWN` 的规则各一项豁免，不多不少：

| 情形 | 响应 |
|---|---|
| 有这样的规则没有豁免 | 409 `WAIVER_REQUIRED` |
| 豁免了不可豁免的规则，不论它的状态 | 422 `WAIVER_FORBIDDEN` |
| 豁免了可豁免但不需要豁免的规则（状态是 `PASS`、`WARN` 或 `NOT_RUN`），或策略里没有的规则；同一条规则豁免两次；豁免的到期时间不晚于现在或晚于 `valid_until` | 422 `REQUEST_UNPROCESSABLE` |

哪些规则需要豁免，读那次检查就知道：`outcomes` 里 `waivable` 为真、状态是 `FAIL` 或 `UNKNOWN` 的那些。基线的 23 条规则里可豁免的是 F04-GOV-002、003、007 和 013，其中 007 的级别是 `WARN`：有发现时状态是 `WARN`，不需要豁免。带基线的检查里，F04-GOV-013 在 `BREAKING` 时是 `FAIL`，在 `RISKY` 和 `UNKNOWN` 时是 `UNKNOWN`（§11.4），两种情况批准时都要豁免：破坏性变更和判断不了的变更不会被自动放行，评审人要写明理由。

`WARN` 和 `NOT_RUN` 的规则不需要豁免。基线里有 10 条规则没有工具，每次检查都是 `NOT_RUN`（§11.4），所以现在的批准都是在这 10 条没有评估的情况下作出的。批准绑定在那次检查上，检查里记着它们没有评估。

决定的人必须没有参与被评审的东西：登记修订的人、发起那次检查的人和提交申请的人作决定，返回 403 `REVIEWER_NOT_INDEPENDENT`，批准和驳回都一样。产品暂停时不能作决定，返回 409 `PRODUCT_NOT_ACTIVE`。

**状态和并发。** 修订的 `status` 是 `DRAFT`、`IN_REVIEW`、`APPROVED`、`REJECTED`、`WITHDRAWN` 之一（F03 §5.9）。每个写操作在事务里锁住修订再改状态，所以同时到达的几个请求只有一个成功：同时提交，其余得到 409 `REVISION_STATUS_CONFLICT`（复审是 409 `REVIEW_IN_PROGRESS`，见 §11.7）；同时决定或撤回，其余得到 409 `REVISION_STATUS_CONFLICT` 或 `REVIEW_CONCLUDED`。`REJECTED` 的修订不能再提交；`APPROVED` 的修订在 0.23.0 之前也不能，之后可以提交复审（§11.7）。相同内容再次导入，返回 200 和原来的修订，带着它现在的状态。

**错误。** 评审不属于该修订或不存在，返回 404 `REVIEW_NOT_FOUND`。请求体不符合契约（缺少必填成员、未声明的成员、取值不在枚举内、时间不是带时区的 RFC 3339、编号不是 UUID）返回 400 `REQUEST_INVALID`，三个写操作都要求有请求体。符合契约但模型不接受的（上面各表之外，还有说明或理由首尾带空白）返回 422 `REQUEST_UNPROCESSABLE`。分页令牌属于另一个修订的列表，返回 400 `PAGE_TOKEN_INVALID`。存储的评审与记录时不符（F03 §5.9）时返回 500 `INTERNAL_ERROR`，响应不说原因，日志在 `correlation_id` 下写明是哪一条评审。

批准记录作为历史证据保留，本身不会改变。0.21.0 起，准备 Release 和发布时按 §11.6 重新核对它：§9 的 `REVIEWER_AUTH_REVOKED`、`WAIVER_EXPIRED` 和 `APPROVAL_SCOPE_MISMATCH` 在那时判断。豁免的范围、内容摘要与评审人来自所属批准，受众和环境来自评审申请。

**兼容性。** 三个版本都只增加操作和 schema，但各给已有的响应枚举增加了成员，按 §5.1 属于 RISKY：0.17.0 给 `WorkspaceRole` 增加 `internal_reviewer`；0.18.0 给 `ContractRevision.status` 增加四个值；0.19.0 给 `ReviewStatus` 和结论的 `outcome` 增加 `APPROVED`、`REJECTED`，并给结论增加可选的 `valid_until` 和 `waivers`。把这些枚举当作封闭集合、或拒绝未知成员的客户端，要先升级。没有人被授予评审人、没有修订被提交之前，响应与 0.16.0 相同。

### 11.6 已实现的 Release 与发布接口

本节对应管理 API 0.20.0–0.22.0。0.20.0 加入发布人和两种安全角色，0.21.0 加入 Release，0.22.0 加入发布目标、槽位和访问阻断。数据模型见 F03 §5.10，访问规则见 F05 §6.7。自 0.33.0 起，内部恢复和制品读取遵循 §11.10。以下路径均以 `/v1/products/{product_id}` 开头。 [管理 API 契约](../api/openapi.yaml)

| 路径 | 行为 |
|---|---|
| `POST /assets/{api_asset_id}/releases` | 工作空间发布人从已批准的修订创建 `READY` Release。相同资产、版本、修订与审批的重试返回已有记录，同一版本绑定其他修订或审批返回 409 |
| `GET /assets/{api_asset_id}/releases`、`GET …/releases/{release_id}` | 授权成员读取 Release 元数据和检查、策略、审批及制品摘要 |
| `POST /publication-targets`、`GET /publication-targets`、`GET …/{publication_target_id}` | 产品负责人用当前受众配置与门户标识登记目标。产品内授权主体可读取目标 |
| `GET …/publication-targets/{publication_target_id}/assets/{api_asset_id}` | 读取 Slot 当前指针、代次和有效阻断状态。首次发布前代次为 0 |
| `POST …/assets/{api_asset_id}/operations` | `ACTIVATE`、`WITHDRAW`、`RESTORE` 使用 `expected_generation` 执行 CAS。成功回传操作记录。`Idempotency-Key` 相同且输入相同的重试回传原操作。代次冲突返回 412，复用键改写请求返回 409 |
| `GET …/assets/{api_asset_id}/operations`、`GET …/assets/{api_asset_id}/artifact` | 按代次查询操作历史，或读取当前获准的制品字节。`X-Artifact-Digest` 对应响应的 SHA-256 |
| `POST …/access-blocks`、`POST …/access-blocks/{access_block_id}/clearance` | 具有产品安全角色的主体按目标、资产或制品建立阻断。独立安全评审人可在受影响指针撤销后解除阻断。可查询阻断记录与历史 |

源文件和摘要打包为 `fabric-openapi-source/v1` JSON，文件内容采用 Base64，制品以 SHA-256 寻址。Release 和制品使用仅可追加的 PostgreSQL 表，Release 绑定修订、审批、检查和策略快照。准备、激活与恢复校验审批有效期、豁免、现行策略与工具、评审人授权和职责分离。换发其他修订时，检查的兼容性基线须匹配该 Slot 上一次生效的修订。回退仅能选择该 Slot 曾生效且当前仍有效的 Release。

Slot、操作记录、审计和 Outbox 在同一事务提交。目标阻断使用独立代次，源站读取逐次检查当前成员、受众配置、指针、审批和阻断。当前源站只提供 `internal` 受众的受控下载。外部交付与 Edge 传播维持 `UNKNOWN`，正式 F08 验收状态见 F08。

全部响应带 `Cache-Control: no-store`。Release 记录后不变，没有 `ETag`。槽位的并发控制不用 `If-Match`，而是请求体里的 `expected_generation`：读槽位得到当前代次，改变槽位时原样带上。

**准备 Release。** 请求体是 `revision_id`、`review_id` 和 `version`，版本号是 1–64 个字符的字母、数字和 `.` `_` `+` `-`，以字母或数字开头。记录了新的 Release 返回 201 和 `Location`；同一版本号、同一修订和评审已有记录时返回 200。修订不是 `APPROVED` 返回 409 `REVISION_NOT_APPROVED`；评审没有批准这个修订返回 409 `APPROVAL_REQUIRED`；版本号已经属于另一个修订或评审返回 409 `RELEASE_VERSION_CONFLICT`。使用批准时的核对不通过，返回 409 `APPROVAL_EXPIRED`、`WAIVER_EXPIRED`、`REVIEWER_AUTH_REVOKED`、`POLICY_STALE` 或 `RELEASE_CHECK_INCOMPLETE`；发布人参与过这个修订返回 403 `PUBLISHER_NOT_INDEPENDENT`。

**登记发布目标。** 请求体是 `audience_config_id` 和 `portal_key`，后者是 1–63 个字符的小写字母、数字和 `-`，以字母或数字开头。新登记返回 201 和 `Location`；同一个受众配置下同一个门户标识已经登记过，返回 200 和已有的目标，不再留审计记录和事件。受众配置不存在、已移除或属于别的产品返回 404 `AUDIENCE_CONFIG_NOT_FOUND`；产品已暂停返回 409 `PRODUCT_NOT_ACTIVE`。目标登记后不能修改，也不能删除。

**改变槽位。** `POST …/operations` 的请求体是 `action`、`expected_generation`，以及按动作需要的 `release_id` 和 `reason`；请求头 `Idempotency-Key` 必填，1–128 个字符的字母、数字和 `.` `_` `:` `-`。`ACTIVATE` 要有 `release_id`；`WITHDRAW` 要有 `reason`，不能有 `release_id`；`RESTORE` 两者都要有，而且只能回到这个槽位上生效过的 Release（否则 409 `RELEASE_NOT_IN_SLOT_HISTORY`）。成功返回 200 和操作记录。代次不是当前的返回 412 `PRECONDITION_FAILED`；同一个键用于另一个请求返回 409 `IDEMPOTENCY_CONFLICT`；要激活的已经在生效返回 409 `RELEASE_ALREADY_ACTIVE`；没有可撤销的返回 409 `NO_ACTIVE_RELEASE`。激活和恢复还会重新核对批准，除上一段的错误码外：目标的受众和环境不在批准的范围内返回 409 `APPROVAL_SCOPE_MISMATCH`；用另一个修订的 Release 替换槽位上最近生效过的那个（中间撤销过也算），而批准依据的检查不是以那个修订为基线，返回 409 `COMPATIBILITY_BASELINE_MISMATCH`，同一个修订的另一个 Release 不需要比较，恢复也不需要；目标不是 `internal` 受众返回 409 `PUBLICATION_TARGET_UNAVAILABLE`，目标的受众配置已被移除返回 404 `AUDIENCE_CONFIG_NOT_FOUND`；被阻断覆盖返回 409 `PUBLICATION_BLOCKED`；产品已暂停返回 409 `PRODUCT_NOT_ACTIVE`。撤销不核对批准，产品暂停时也可以撤销，参与过这个修订的发布人也可以撤销。

**读取槽位和制品。** 槽位的响应有 `generation`、`visibility`（`EMPTY`、`ACTIVE`、`WITHDRAWN`）、可选的 `active_release_id`、`blocked` 和 `delivery_status`（恒为 `UNKNOWN`）。阻断将 `blocked` 置为真，槽位的指针和代次保持原值。制品以 `application/octet-stream` 返回，`X-Artifact-Digest` 是响应字节的 SHA-256。没有生效的 Release、被阻断或产品暂停时返回 404 `PUBLISHED_ARTIFACT_NOT_FOUND`。目标的受众配置已被移除时返回 404 `AUDIENCE_CONFIG_NOT_FOUND`。0.22.0 中，批准失效也会导致制品返回 404。0.25.0 起槽位通过 `approval_problem` 说明批准失效原因（§11.7）。0.33.0 起已生效的内部制品继续受 §11.10 的读取条件约束。

**访问阻断。** `POST …/access-blocks` 的请求体是 `reason` 和可选的 `api_asset_id`、`release_id`（有后者必有前者），分别阻断整个目标、一个资产或一个 Release。新建返回 201；同一范围已有未解除的阻断时，同一个人以同样的理由再次提交返回 200，否则 409 `ACCESS_BLOCK_EXISTS`。资产不属于这个产品返回 404 `API_ASSET_NOT_FOUND`，Release 不属于这个资产返回 404 `RELEASE_NOT_FOUND`。`POST …/clearance` 的请求体是 `reason`：解除人就是建立人返回 403 `SECURITY_REVIEWER_NOT_INDEPENDENT`；阻断覆盖的槽位还有生效的 Release 返回 409 `WITHDRAW_BEFORE_CLEARING`，只看它覆盖的那部分，按 Release 阻断时同一资产换成了别的 Release 就可以解除；已经解除的阻断，解除人以同样的理由再次提交返回 200，否则 409 `ACCESS_BLOCK_CLEARED`。阻断的响应里 `edge_status` 恒为 `UNKNOWN`。

**列表。** Release 和发布目标的列表采用 §3.1.1 的分页约定。槽位的操作历史和阻断列表按代次排序，用查询参数 `after_generation` 翻页，响应里的 `next_after_generation` 是下一页的起点。

**其他错误。** 找不到的资源返回 404 `RELEASE_NOT_FOUND`、`PUBLICATION_TARGET_NOT_FOUND`、`ACCESS_BLOCK_NOT_FOUND`。请求体不符合契约返回 400 `REQUEST_INVALID`；符合契约但模型不接受（例如动作与成员的组合不对）返回 422 `REQUEST_UNPROCESSABLE`。

**与 §10 和 §11.2 的差别。** §11.2 的设计示例用 `PUT /v1/publication-slots/{slot_id}` 加 `If-Match`；实现是目标和资产下的 `POST …/operations` 加请求体里的 `expected_generation`。幂等键的作用域是槽位，没有保留期限：操作记录不删除，键一直有效。同一个键、同一个请求的重试返回同一条操作记录。

**兼容性。** 0.20.0 给 `ProductRole` 和 `WorkspaceRole` 两个枚举增加了成员，按 §5.1 属于 RISKY：把枚举当作封闭集合的客户端要先升级。0.21.0 和 0.22.0 只增加操作和 schema。

### 11.7 已实现的复审接口与批准状态

本节对应管理 API 0.23.0–0.25.0：0.23.0 让已批准的修订可以再次评审，0.24.0 让发布人能读修订、检查和评审，0.25.0 让槽位说明批准为什么不成立。三个版本都没有增加操作，仍是 52 个。数据模型见 F03 §5.11，访问规则见 F05 §6.8。 [管理 API 契约](../api/openapi.yaml)

**复审（0.23.0）。** 用的是 §11.5 的五个操作，请求和响应的形状不变。区别在提交：

| 修订的状态 | `POST …/reviews` |
|---|---|
| `DRAFT`、`WITHDRAWN` | 与之前相同：201，修订变为 `IN_REVIEW` |
| `APPROVED` | 201，这是一次复审；修订仍是 `APPROVED` |
| `APPROVED`，并且这个修订有一条评审还没有结束 | 409 `REVIEW_IN_PROGRESS` |
| `IN_REVIEW`、`REJECTED` | 409 `REVISION_STATUS_CONFLICT` |

评审的响应多一个必有的成员 `supplementary`：复审为 `true`，其余为 `false`。对检查的要求与 §11.5 相同：必须是这个修订的、现行的检查，否则 409 `POLICY_STALE`；有不可豁免的规则没通过，409 `REVIEW_BLOCKED`。批准失效是因为策略或检查工具更新时，原来那次检查已经不是现行的，要先对修订重新发起检查（§11.4），再拿新的检查提交复审。产品暂停时不能提交。

撤回和决定与 §11.5 相同，包括谁能做、豁免怎么给、各种拒绝的响应。复审无论怎样结束，修订都是 `APPROVED`：批准给修订增加一次批准，有自己的 `scope`、`valid_until` 和 `waivers`；驳回和撤回不改变已有的批准。同时到达的几个提交只有一个成功，其余得到 409 `REVIEW_IN_PROGRESS`；同时到达的几个决定只有一个成功，其余得到 409 `REVIEW_CONCLUDED`。修订的评审历史（`GET …/reviews`）按申请时间列出全部评审，包括各次复审。

**依据复审的批准发布。** `POST …/assets/{id}/releases` 的 `review_id` 可以是这个修订的任何一次批准。依据复审的批准准备的 Release 要用一个新的版本号（版本号在资产内唯一，§11.6）；内容相同，`artifact_digest` 也相同。在槽位上用它替换同一个修订的另一个 Release 时不需要兼容性比较。批准的范围各算各的：一个 Release 只能发布到它所依据的那次批准的范围之内，否则 409 `APPROVAL_SCOPE_MISMATCH`。之前的 Release 记录不变，它依据的批准不成立时仍然不能激活或恢复。

**发布人的读取（0.24.0）。** 资产所属 Workspace 的发布人现在能调用下列操作，得到的响应与作者相同：读修订和修订历史、读修订的原文、读检查和检查历史、比较两个修订、读评审和评审历史。发布人据此找到要准备 Release 的 `revision_id` 和 `review_id`。写操作没有变化：发布人导入、发起检查、提交或撤回评审、作决定，都返回 403 `FORBIDDEN`。

**槽位的批准状态（0.25.0）。** `GET …/publication-targets/{t}/assets/{a}` 的响应多一个可选成员 `approval_problem`。槽位指向一个 Release、而它的批准对这个目标已经不成立时才出现，取值是此刻激活这个 Release 会得到的错误码：

| `approval_problem` | 含义 | 怎么办 |
|---|---|---|
| `APPROVAL_EXPIRED` | 批准过了 `valid_until` | 复审 |
| `WAIVER_EXPIRED` | 批准所依赖的某条豁免到期 | 复审，重新给出豁免，或者改契约 |
| `REVIEWER_AUTH_REVOKED` | 批准人据以批准的评审人关系已被撤销；重新授予不算 | 由现任评审人复审 |
| `POLICY_STALE` | 策略或检查工具更新，批准所依据的检查不再是现行的 | 重新检查，再复审 |
| `APPROVAL_REQUIRED` | 评审记录不是对这个修订的批准 | 复审 |
| `APPROVAL_SCOPE_MISMATCH` | 批准的范围不包含这个目标的受众和环境 | 为这个范围复审 |

后两种在正常使用中不会出现在已经生效的 Release 上，因为激活时已经核对过，而评审记录和目标的受众、环境都不会改变。`approval_problem` 出现时，`GET …/artifact` 返回 404 `PUBLISHED_ARTIFACT_NOT_FOUND`，那个响应仍然不说原因（§11.6）。`approval_problem` 与 `blocked` 互不相干，可以同时出现。槽位没有生效的 Release 时不出现。

**批准失效后的恢复步骤。** 以检查工具更新为例，之后每个受影响的资产要做四步，内容不用改：作者 `POST …/check-runs`；作者 `POST …/reviews`（复审）；评审人 `POST …/reviews/{id}/decision`；发布人 `POST …/releases` 准备新版本号的 Release，再 `POST …/operations` 激活它。激活之后槽位不再有 `approval_problem`，读到的制品与之前逐字节相同。这是 §7.2 和 §9 所说的“重新检查与复审”。

**兼容性。** 0.23.0 改变了一个已有操作的行为：对 `APPROVED` 的修订 `POST …/reviews`，之前是 409，现在是 201 并产生一条评审。依赖这个 409 的客户端要改。`Review` 新增的 `supplementary` 是响应里的必有成员；0.22.0 的进程在迁移后的库上读复审时不带它，也不能结束复审（决定和撤回返回 409 `REVISION_STATUS_CONFLICT`），见 F03 §5.11。0.24.0 只放宽了读取的授权。0.25.0 给 `PublicationSlot` 增加了一个可选的枚举成员，把响应当作封闭对象的客户端要先升级。

### 11.8 已实现的查询接口

本节对应管理 API 0.26.0–0.28.0：0.26.0 加入两个槽位列表，0.27.0 加入按编号下载 Release 和读出它的来源，0.28.0 加入审计员和审计记录的读取。三个版本一共增加 5 个操作，到 0.28.0 是 57 个。数据模型见 F03 §5.12，访问规则见 F05 §6.9。 [管理 API 契约](../api/openapi.yaml)

| 方法 | 路径 | operationId | 从哪个版本起 |
|---|---|---|---|
| `GET` | `/v1/products/{product_id}/publication-targets/{publication_target_id}/assets` | `listPublicationSlots` | 0.26.0 |
| `GET` | `/v1/products/{product_id}/assets/{api_asset_id}/publications` | `listAssetPublications` | 0.26.0 |
| `GET` | `/v1/products/{product_id}/assets/{api_asset_id}/releases/{release_id}/artifact` | `getContractReleaseArtifact` | 0.27.0 |
| `GET` | `/v1/products/{product_id}/assets/{api_asset_id}/releases/{release_id}/provenance` | `getContractReleaseProvenance` | 0.27.0 |
| `GET` | `/v1/products/{product_id}/audit-records` | `listAuditRecords` | 0.28.0 |

五个应答都带 `Cache-Control: no-store`。

**槽位列表（0.26.0）。** 应答是 `PublicationSlotList`：`items` 和下一页的 `next_page_token`。每一项就是 `getPublicationSlot` 读到的 `PublicationSlot`，含 `blocked` 和可选的 `approval_problem`（§11.7）：同一个槽位在这三处读到的完全一样。只列做过操作的槽位，撤回的也列，所以 `visibility` 只有 `ACTIVE` 和 `WITHDRAWN`；没有发布过的资产没有槽位，读单个槽位是空槽位（`EMPTY`），列表里没有它。目标的列表只含调用人读的 Workspace 里的资产的槽位，资产的列表含这个资产在本产品各个目标上的槽位。谁能读与读单个槽位相同：资产所属 Workspace 的作者、评审人和发布人。能读但没有可见的槽位，得到 200 和空的 `items`，不是错误。`approval_problem` 是读的时候算的，不存库，所以不能按它筛选。一页里的每个槽位各做一次与读单个槽位相同的核对，耗时随页里的槽位数增加，本机的量级见 F03 §5.12。

**分页和顺序。** 三个列表都采用 §3.1.1 的约定：`page_size` 为 1–100，默认 50，`page_token` 原样使用上一页的 `next_page_token`；除最后一页，每一页都是满的，最后一页没有令牌，刚好装满的也没有；不返回总数。顺序是固定的：

| 列表 | 顺序 |
|---|---|
| 两个槽位列表 | 旧的在前：槽位第一次被接受的操作的时间，同一时刻按 `slot_id`。一个槽位的位置从它出现起不变，撤销、恢复、换 Release 都不换位置 |
| 审计记录 | 新的在前：`recorded_at` 倒序，同一时刻 `audit_id` 倒序。翻页期间新写入的记录排在已发出的位置之前，不会让读过的重复，也不会让没读到的漏掉 |

令牌只记位置和它属于哪个列表（一个目标上的槽位、一个资产的槽位、一个产品的审计记录），不记调用人，也不带来任何权限：每页重新判权，权限撤销以后旧令牌读不到下一页，得到的是没有这个权限的人的答案（403 或 404）。别的列表的令牌是 400 `PAGE_TOKEN_INVALID`。令牌也不记审计记录的筛选条件：翻页时要保持筛选条件不变，换了条件不会报错，只是从那个位置起按新条件读。

**审计记录（0.28.0）。** 只有本产品的审计员能读（F05 §6.9）。每一项有 `audit_id`、`recorded_at`、`actor`（`Principal`：issuer 和 subject，如实给出，不打码）、`action`、`outcome`（`SUCCEEDED` 或 `DENIED`）、`rule_id`、`product_id`、`resource_type`、`resource_id` 和 `correlation_id`；记录没有留下资源时，没有 `resource_id` 这个成员。`correlation_id` 就是那次请求的错误应答里的 `trace_id`，它把记录和日志、事件连起来。只出本产品的记录；不属于任何产品的记录（例如无权的人登记产品被拒绝）在这里读不到。审计员能读被任命之前的记录。筛选条件都是精确匹配，互相叠加，在分页之前应用：

| 参数 | 含义 |
|---|---|
| `recorded_since` | 在这个时刻或之后记录的（含） |
| `recorded_before` | 在这个时刻之前记录的（不含） |
| `action` | 动作，例如 `product_role.grant`：小写字母、数字和下划线组成的词，用点连接，至多 64 个字符 |
| `outcome` | `SUCCEEDED` 或 `DENIED` |
| `actor_issuer`、`actor_subject` | 行动人的 issuer 和 subject。可以只给 `actor_issuer`，也可以只给 `actor_subject`（匹配任何 issuer 下的这个 subject） |
| `resource_type` | 资源类型，例如 `RoleAssignment`：字母和数字，至多 64 个字符 |
| `resource_id` | 资源编号，规范形式的 UUID |
| `correlation_id` | 一次请求的编号 |

时刻是 RFC 3339，建议用 `Z` 结尾；带偏移的 `+` 在查询里要写成 `%2B`，否则它是一个空格，请求被拒绝（400 `REQUEST_INVALID`）。时刻按微秒记，更细的截到微秒。`recorded_since` 不早于 `recorded_before` 是 422 `REQUEST_UNPROCESSABLE`：这一条只有模型能判断，所以只对审计员给出，不是审计员的人得到的是 403 或 404。其余条件的格式（上表里的长度、字符和 UUID 形式）契约已经声明，不符合的是 400 `REQUEST_INVALID`；契约没有声明的参数同样是 400（§3.1.1）。一页读多少行：不带筛选的一页读的行数和它返回的相当；按动作、结果、行动人和资源筛选没有索引，满足条件的记录很少时，一页要读很多行（F03 §5.12）。

**下载（0.27.0）。** `GET …/releases/{release_id}/artifact` 按 Release 的编号下载它的制品：`application/octet-stream`，字节就是这个 Release 准备时用的字节（§11.6 说的 `fabric-openapi-source/v1` JSON）。`X-Artifact-Digest` 是响应字节的 SHA-256，也就是 Release 的 `artifact_digest`，格式是 `sha256:` 加小写十六进制，客户端可以自己再算一遍。读取之前，服务端核对制品字节的摘要和修订的闭包摘要，与读槽位的制品时一样。与 `getPublishedArtifact` 不同，它不看槽位指向谁：同一个资产的任何 Release 都可以下载，包括已经被别的 Release 替换的和撤回的。谁能读这个 Release 谁就能下载：资产所属 Workspace 的作者、评审人和发布人，产品暂停时也可以。**它不看发布目标上的访问阻断，也不重新核对批准，这与设计正文不一致，待裁决，见 F05 §6.9。** 路径里的 `release_id` 必须是 Release 的编号：摘要和版本号不是编号，是 400 `REQUEST_INVALID`。

**来源（0.27.0）。** `GET …/releases/{release_id}/provenance` 一次给出一个 Release 准备时依据的事实，成员是：

- `release`：就是 `getContractRelease` 读到的 `ContractRelease`；
- `revision`：`revision_id` 和 `revision_sequence`，`source_kind`（`UPLOAD` 或 `GIT`），`git_origin`（只有 `GIT` 有），入口路径 `entrypoint`，`source_digest` 和 `ref_closure_digest`；
- `check`：依据的那次检查：`check_run_id`，`policy_set_id` 和 `policy_digest`，`tool_manifest`，`status`，`summary`（各状态的规则数和发现总数），`completed_at`；
- `approval`：依据的那次评审：`review_id`，`supplementary`（是不是复审），`scope`（批准的受众和环境），`concluded_by` 和 `concluded_at`（谁在什么时候批准），`valid_until`，`waivers`（豁免的规则和各自的到期时间；理由和补偿措施的文字不在这里，要读评审）。

各部分和读修订、读检查、读评审得到的同名成员逐一相同。来源说的是准备这个 Release 时依据的事实，不说批准现在成不成立：修订后来被复审、有了更新的检查，旧 Release 的来源不变；批准现在是否成立看槽位的 `approval_problem`（§11.7），来源里没有这样的成员。Git 来源是导入修订时提交人声明的，Fabric 不回仓库核对：`git_origin` 说明的是“谁说这些文件来自哪个提交”，不证明文件和那个提交一致；同一仓库的相同内容后来从别的提交再导入，得到的仍是最先登记的修订，`git_origin` 仍是最早那次的提交。上传的修订没有 `git_origin`。没有指定基线的检查，`check.summary` 里的 `not_run` 是 11：基线里有 10 条规则没有工具，再加上兼容性那一条（F04-GOV-013 只在检查指定了基线时评估），见 §11.4；指定了基线的是 10。读出的评审、检查和修订与 Release 所记的对不上，是存储的故障，答 500 `INTERNAL_ERROR`，不说哪里不对，原因在日志里（F03 §5.12）。

**失败。**

| 操作 | 状态码和错误码 |
|---|---|
| 两个槽位列表 | 400 `REQUEST_INVALID`（参数）和 `PAGE_TOKEN_INVALID`（不是这个列表的令牌）；401；403 `FORBIDDEN`（知道产品，但读不了任何槽位）；404 `PRODUCT_NOT_FOUND`；目标的列表还有 404 `PUBLICATION_TARGET_NOT_FOUND`，资产的列表还有 404 `API_ASSET_NOT_FOUND` |
| 下载和来源 | 400 `REQUEST_INVALID`（`release_id` 不是编号）；401；403 `FORBIDDEN`；404 `PRODUCT_NOT_FOUND`、`API_ASSET_NOT_FOUND` 和 `RELEASE_NOT_FOUND`；500 `INTERNAL_ERROR`（存储与 Release 所记的对不上） |
| 审计记录 | 400 `REQUEST_INVALID` 和 `PAGE_TOKEN_INVALID`；401；403 `FORBIDDEN`（知道产品，但不是审计员）；404 `PRODUCT_NOT_FOUND`；422 `REQUEST_UNPROCESSABLE`（时间范围颠倒，只对审计员） |

401 是 `CREDENTIALS_MISSING` 或 `TOKEN_INVALID`。404 的用法和读单个槽位、读 Release 一样：不认识产品的人得到 `PRODUCT_NOT_FOUND`；看不到资产的人（包括平台管理员和别的 Workspace 的读者）得到 `API_ASSET_NOT_FOUND`；别的资产和别的产品的 Release 编号，对读这个资产的人，与不存在的编号答案相同，都是 `RELEASE_NOT_FOUND`；别的产品的目标和资产，也与不存在的答案相同。

**兼容性。** 0.26.0 只增加两个操作和一个 schema（`PublicationSlotList`），0.27.0 增加两个操作和五个 schema（`ContractReleaseProvenance`、`ReleaseProvenanceRevision`、`ReleaseProvenanceCheck`、`ReleaseProvenanceApproval`、`ReleaseProvenanceWaiver`）。0.28.0 增加一个操作和五个 schema（`AuditRecord`、`AuditRecordList`、`AuditAction`、`AuditOutcome`、`AuditResourceType`），并给 `ProductRole` 增加了成员 `internal_auditor`：它出现在授予角色的请求里，也出现在列出、读取和授予角色的响应里。按 §5.1，响应的枚举增加成员属于 RISKY：把枚举当作封闭集合的客户端要先升级。几处已有操作的描述补了文字，行为没有变：`grantProductRole` 的描述原来写“A product has one role, product_owner”，现在写明四个角色和谁能授予；`getContractRelease`、`getPublishedArtifact`、`getPublicationSlot` 和 `blockPublicationAccess` 说明了按编号下载与槽位、阻断的关系（阻断和批准管经槽位读，按编号下载不看它们）；`info.description` 里“其他列表旧的在前”补上了审计记录新的在前。没有新的错误码。

### 11.9 已实现的工作台查询接口

本节对应管理 API 0.29.0–0.31.0，当前为 0.31.0：0.29.0 加入两个“我是谁”，0.30.0 加入一个 Workspace 里的评审队列，0.31.0 给资产的读取加上概况，并给资产列表加两个筛选。三个版本一共增加 3 个操作，现在是 60 个。数据模型见 F03 §5.13，访问规则见 F05 §6.10。 [管理 API 契约](../api/openapi.yaml)

| 方法 | 路径 | operationId | 从哪个版本起 |
|---|---|---|---|
| `GET` | `/v1/caller` | `getCaller` | 0.29.0 |
| `GET` | `/v1/products/{product_id}/caller` | `getCallerInProduct` | 0.29.0 |
| `GET` | `/v1/products/{product_id}/workspaces/{workspace_id}/reviews` | `listWorkspaceReviews` | 0.30.0 |

已有的两个操作在 0.31.0 有变化，操作本身没有增减：

| 操作 | 变化 |
|---|---|
| `listAssets` | 多两个可选参数 `check_status` 和 `release_pending`；每一项多一个可选成员 `overview` |
| `getAsset` | 应答多一个可选成员 `overview` |

三个新操作的应答都带 `Cache-Control: no-store`，没有 `ETag`。

**“我是谁”（0.29.0）。** `getCaller` 答 `Caller`：`principal`（`Principal`：`issuer` 和 `subject`）和 `platform_administrator`（布尔，是否由部署配置指定为平台管理员）。它不带任何参数；带了查询参数或请求体是 400 `REQUEST_INVALID`，所以没有办法问别人：身份只来自令牌，请求头里写什么都不参与。先认证后检查参数，没有令牌的请求得到的是 401。任何已认证的人都能调，不读库，从不写审计。令牌里只有签发方和主体会往下传，所以没有姓名。`getCallerInProduct` 答 `CallerInProduct`：`product_id`；`roles`，调用人现在持有的产品角色（`ProductRole`，每个一次，可以为空）；`workspaces`，调用人是成员的 Workspace，每一项是 `workspace_id` 和它在那里的角色（`WorkspaceRole`，每个一次，至少一个）。`workspaces` 不是一页列表，没有 `next_page_token`：每个有成员关系的 Workspace 一项，一次给全。它只说持有什么，不说“能做哪些操作”：是不是负责人看 `roles` 里有没有 `product_owner`；负责人不是成员也读得到全部 Workspace，所以 `workspaces` 是“我持有成员关系的”，不是“我读得到的”。每次请求读当时有效的角色和成员关系，不缓存：撤销提交之后的下一次请求就没有，用的还是原来的令牌；产品暂停时照常答。谁能读与 `getProduct` 相同：平台管理员，和在这个产品里现在持有任一产品角色或 Workspace 成员关系的人；什么都不持有的平台管理员得到 200 和两个空数组。其他人，包括曾经持有、已被撤销的人和别的产品的负责人，得到 404 `PRODUCT_NOT_FOUND`，与不存在的产品逐成员相同（`trace_id` 除外）。

**评审队列（0.30.0）。** `GET …/workspaces/{workspace_id}/reviews` 列出一个 Workspace 里每个资产的每个修订的评审，先提交的在前，一页一页读。应答是已有的 `ReviewList`，每一项就是 `getRevisionReview` 读到的 `Review`，由同一个函数输出：没有新的 schema，也不带 `workspace_id` 和资产名，页面要显示资产名得再读资产。没有评审是 200 和空的 `items`；不给总数。参数：

| 参数 | 含义 |
|---|---|
| `status` | `IN_REVIEW`、`WITHDRAWN`、`APPROVED` 或 `REJECTED`。`IN_REVIEW` 是还没有结论的评审，开着的复审也是，这时修订仍是 `APPROVED`；其余是评审结束的方式 |
| `requested_by_issuer`、`requested_by_subject` | 提交评审申请的人的签发方和主体，写法与审计记录的 `actor_issuer`、`actor_subject` 相同，都是 1 到 255 个字符的引用。可以只给 `requested_by_issuer`，也可以只给 `requested_by_subject`（匹配任何签发方下的这个主体） |
| `page_size`、`page_token` | 同 §3.1.1 |

筛选条件都是精确匹配，互相叠加，在分页之前应用，所以除最后一页，每页都是满的；没有符合的是空列表，不是错误。谁能读：这个 Workspace 里现在持有 `internal_author`、`internal_reviewer` 或 `internal_publisher` 的人，读到的是整个 Workspace 的全部评审（别人提交的、已撤回的、已驳回的都在），名单与读单个评审相同；产品暂停时照常读。列表与单读只有一处不同：单读核对评审依据的内容仍是修订现在的内容（`input_digest` 等于修订现在的摘要，需要读修订），列表不做这一项核对。某条评审的记录对不上模型记下的样子，整页答 500 `INTERNAL_ERROR`，不说原因，原因写进日志。列表项不带“这条该不该我批”：评审人批的时候才知道自己批不了（403 `REVIEWER_NOT_INDEPENDENT`，§11.5）。

**资产的概况（0.31.0）。** `listAssets` 和 `getAsset` 应答里的每个资产多一个可选成员 `overview`（`AssetOverview`）；`registerAsset` 的 201 不带它。它必有 `release_pending`，其余三个成员可选，每个都是对应读取里同名成员的同义、同值：

| 成员 | 内容 | 与谁同义 |
|---|---|---|
| `latest_revision` | `revision_id`、`revision_sequence`、`status`、`created_at`：`revision_sequence` 最大的修订 | `getRevision` 的 `ContractRevision` |
| `latest_check` | `check_run_id`、`status`、`completed_at`：**最新修订**最近完成的一次检查；没有检查就没有这个成员 | `listRevisionChecks` 的 `CheckRunOverview` |
| `latest_release` | `release_id`、`revision_id`、`version`、`prepared_at`：这个资产最后准备的 Release，**不管它从哪个修订来**；没有 Release 就没有这个成员 | `listContractReleases` 的 `ContractRelease` |
| `release_pending` | 布尔：最新修订是 `APPROVED`，而且还没有从它准备出 Release | — |

没有修订的资产，`overview` 是 `{"release_pending": false}`。复审不改变修订的状态，所以复审开着或结束，概况都不变。谁看得到 `overview`：现在在资产所属 Workspace 持有 `internal_author`、`internal_reviewer` 或 `internal_publisher` 的人，也就是读那个 Workspace 的修订的人。产品负责人和 Workspace 维护者读得到资产，读不到概况：应答里没有 `overview` 这个成员，不是空对象；兼任某个 Workspace 作者的负责人，只在那个 Workspace 的资产上看到。概况每次读的时候现算，不存，所以它说的就是读修订、检查和 Release 此刻说的。`listAssets` 多两个筛选，和已有的 `workspace_id`、`api_kind`、`domain_key`、`q` 叠加，都在分页之前应用；顺序和令牌不变，仍是 `(created_at, api_asset_id)` 升序：

| 参数 | 含义 |
|---|---|
| `check_status` | `PASS`、`WARN`、`FAIL` 或 `UNKNOWN`（`CheckRunStatus`）：最新修订最近一次检查的结论。没有检查的资产不符合任何一个 |
| `release_pending` | 只收 `true`：留下 `release_pending` 为真的资产。没有“不是”的筛选 |

两个筛选只在调用人读得了修订的 Workspace 里找：别的 Workspace 的资产不被问概况，也不会被留下。带筛选的一页要找到够数的资产，可能一批一批地读，符合的越少越慢（F03 §5.13 有量级）；除最后一页，每页仍是满的，令牌记的是页里最后一个资产的位置，翻页时筛选要保持不变。验证器按 Go 的 `strconv.ParseBool` 读布尔参数，`release_pending=1`、`t`、`T`、`TRUE`、`True` 也被当作 `true`，契约里的 `const: true` 拦不住它们；`false`、其他词、没有值和写两次都是 400。

“检查失败”和“可发布”的确切含义：

- 检查失败，就是 `check_status=FAIL`：最新修订最近一次检查的结论是 `FAIL`。较早的修订的检查不算，最新修订还没有检查也不算；评审时豁免了失败的规则并批准的修订，检查仍是 `FAIL`，这样的修订同时是 `release_pending`。基线里有 10 条规则没有工具，一次检查的总状态目前只会是 `FAIL` 或 `UNKNOWN`（§11.4），所以 `check_status=PASS` 和 `WARN` 今天没有结果，接口仍收这四个取值。
- 可发布，就是 `release_pending=true`：最新修订已批准、还没有从它准备出 Release。它只看状态，不重新核对批准：批准过期、豁免到期、评审人失权、策略或工具变了，它都不管，这些在准备 Release 时才核对（§11.6），所以“可发布”不保证现在一定能准备出 Release。

概况里没有“当前发布”（资产现在在哪些目标上生效）：要看用 `listAssetPublications`，撤销风险用 `listPublicationSlots`（§11.8）。

**分页和顺序。** 队列采用 §3.1.1 的约定：`page_size` 为 1–100，默认 50，`page_token` 原样使用上一页的 `next_page_token`；除最后一页，每一页都是满的，最后一页没有令牌，刚好装满的也没有；不返回总数。`listAssets` 的分页和顺序没有变。

| 列表 | 顺序 |
|---|---|
| 评审队列 | 先提交的在前：提交评审的时刻升序，同一时刻 `review_id` 升序。两个值写入后不变，所以一条评审的位置不变；翻页期间新提交的评审排在已发出的位置之后，不会让读过的重复，也不会让没读到的漏掉 |
| 资产列表 | 与 §11.3 相同：`(created_at, api_asset_id)` 升序，带不带新筛选都一样 |

令牌只记位置和它属于哪个列表（一个产品的一个 Workspace 的队列），不记调用人，也不带来任何权限：每页重新判权，权限撤销以后旧令牌读不到下一页，得到的是没有这个权限的人的答案（403 或 404）。别的 Workspace、别的产品和别的列表的令牌是 400 `PAGE_TOKEN_INVALID`。令牌也不记筛选条件：翻页时要保持筛选条件不变（队列的 `status`、`requested_by_issuer`、`requested_by_subject`，资产列表的 `check_status`、`release_pending` 和已有的几个），换了条件不会报错，只是从那个位置起按新条件读。

**工作台的待办怎么拼。** F06 的 IN-01 要首页显示待审核、检查失败、可发布和撤销风险。没有单独的汇总接口，由页面拼：待审核是 `listWorkspaceReviews` 加 `status=IN_REVIEW`；检查失败是 `listAssets` 加 `check_status=FAIL`；可发布是 `listAssets` 加 `release_pending=true`；撤销风险是 `listPublicationSlots` 和 `listAssetPublications`（每项带 `approval_problem` 和 `blocked`，不能按它们筛选）。页面先用 `getCallerInProduct` 看自己在这个产品里是什么身份，再决定调哪些：没有读评审或读修订的身份的人，调队列和带筛选的列表每次都被拒绝，也每次都留下一条审计记录（F05 §6.10）。要“我的产品和每个产品里我的身份”，得先 `listProducts` 再对每个产品问一次 `getCallerInProduct`，没有一次给全的接口。

**失败。**

| 操作 | 状态码和错误码 |
|---|---|
| `getCaller` | 400 `REQUEST_INVALID`（带了查询参数或请求体）；401。没有 403 和 404：没有人被拒绝 |
| `getCallerInProduct` | 400 `REQUEST_INVALID`（`product_id` 不是标识，或带了别的参数、请求体）；401；404 `PRODUCT_NOT_FOUND`。没有 403 |
| `listWorkspaceReviews` | 400 `REQUEST_INVALID`（参数）和 `PAGE_TOKEN_INVALID`（不是这个队列的令牌）；401；403 `FORBIDDEN`（是负责人或这个 Workspace 的成员，但没有作者、评审人、发布人的身份）；404 `PRODUCT_NOT_FOUND` 和 `WORKSPACE_NOT_FOUND`；500 `INTERNAL_ERROR`（某条评审的记录对不上） |
| `listAssets` 带新筛选 | 400 `REQUEST_INVALID`（`check_status` 不是四个取值，`release_pending` 不是 `true`、写了两次或没有值）；401；403 `FORBIDDEN`（能列资产，但在哪个 Workspace 都读不了修订）；404 `PRODUCT_NOT_FOUND`；500 `INTERNAL_ERROR`（检查的记录对不上） |
| `getAsset` | 与 §11.3 相同；读得了概况的人遇到检查的记录对不上，得到 500，而不是没有概况 |

401 是 `CREDENTIALS_MISSING` 或 `TOKEN_INVALID`。队列的 404 分两种：不认识产品的人和不存在的产品，是 `PRODUCT_NOT_FOUND`；认识产品、却既不是负责人也不是这个 Workspace 成员的人（别的 Workspace 的成员、平台管理员、只有产品角色的人），是 `WORKSPACE_NOT_FOUND`，与不存在的 Workspace 和别的产品的 Workspace 相同；负责人问产品没有的 Workspace，也是 `WORKSPACE_NOT_FOUND`。队列里没有读评审的身份的人得到的是 403，不是空列表，空列表会说这个 Workspace 没有评审。`listAssets` 带新筛选的 403：本来就不能列资产的人（陌生人、平台管理员、审计员、安全角色）的答案与不带筛选时相同；能列资产却在哪个 Workspace 都读不了修订的人（负责人、维护者）带任一筛选得 403，不答空页，因为空页会说出筛选留下了什么。

**兼容性。** 0.29.0 增加两个操作和三个 schema（`Caller`、`CallerInProduct`、`CallerInWorkspace`）；0.30.0 增加一个操作，没有新的 schema；0.31.0 增加四个 schema（`AssetOverview`、`OverviewRevision`、`OverviewCheck`、`OverviewRelease`），给 `APIAsset` 增加可选成员 `overview`，给 `listAssets` 增加两个可选参数。七个新 schema 都是封闭的（`additionalProperties: false`）：以后加成员，例如显示名，是新增成员，按契约严格校验应答的客户端要先升级。`APIAsset` 本来就不封闭，多一个可选成员不影响读它的客户端；按 §5.1，新增可选响应字段是 COMPATIBLE / RISKY，严格解码、按签名或缓存消费应答的客户端要先确认。没有新的错误码，没有改已有操作对读不了修订的人的行为。滚动升级时，旧进程对三个新路径答 404 `ROUTE_NOT_FOUND`，对带 `check_status` 或 `release_pending` 的 `listAssets` 答 400 `REQUEST_INVALID`（未声明的参数），资产的应答里没有 `overview`；要用新接口和新筛选的页面，等所有进程都升级以后再上（F03 §5.13、F05 §6.10）。

### 11.10 内部发布与读取权限的契约更新

| 管理契约 | 变更 | 调用方需要核对 |
|---|---|---|
| 0.33.0 | 已生效的内部制品沿用当前槽位。批准失效通过 `approval_problem` 告知。内部 `RESTORE` 验证原批准证据，`ACTIVATE` 验证当前批准 | 读取成功仍须满足现行权限、配置和 AccessBlock。恢复沿用槽位历史、独立发布人和 CAS |
| 0.35.0 | 角色分配列表支持 `role` 筛选。平台管理员在有权的产品内使用 `role=internal_auditor` 查看审计员 | 分页请求保持相同角色筛选。产品负责人保留查询权限，任命和撤销由独立的平台管理员操作 |
| 0.36.0 | Workspace 角色枚举增加 `internal_reader`。该角色可查询所属 Workspace 的 Release 列表和元数据。目录、治理材料和发布读取体现维护者及安全角色的作用域 | 先应用迁移 010 并升级所有副本，再授予新角色。只读角色通过槽位读取内部制品并处理阻断后的 404 |

上述变更沿用现有 60 个管理操作。服务端按请求重新判权，列表在分页之前按授予的产品和 Workspace 范围过滤。写操作按各自角色检查。详细作用域与拒绝语义见 [F05 §6.12](F05_Security_and_Multitenancy.md#612-内部发布审计与读取权限033003500360)。

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
