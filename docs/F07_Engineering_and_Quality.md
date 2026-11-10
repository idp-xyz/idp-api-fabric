# F07 — IDP API Fabric 工程实现与质量设计

**正式路径**：`docs/F07_Engineering_and_Quality.md`　**文档版本**：V1.0（沿用工程基线）　**日期**：2026-11-09  
**状态**：工程设计候选，**待具名技术、安全、运维评审**。本文件不是生产代码完成报告，未执行的真实测试均为 `NOT_RUN`。  
**权威输入**：F01（FR-001–071 / NFR-01–18 / AT-01–12）、F02（AI-01–07）、F03（INV-01–13 / DM-01–18）、F04（F04-GOV-001–028）、F05（授权矩阵 / STRIDE）、F06（A/B/C 两套 LOCAL_MOCK 门户），以及上位 `IDP-SCG-STD-001`。  
**架构决策状态**：ADR-0001–0007 **均为 Proposed**；本文对设计作具体化，不代表既有 ADR 已获正式批准。  
**实施边界**：F07 是工程设计、工具链候选、验证方法和证据结构；不代替 F08 的正式验收，不在本阶段宣称 Go 业务后台已实现。

## 0. 执行摘要与工程设计裁决

F07 将前六份文档固化为可实现、可审查、可测试的工程方案。唯一贯通样例：**CargoPai Contract Source → Revision → CheckRun → ReviewDecision → 不可变 Release → `(target_id, api_asset_id)` PublicationSlot CAS → 私有/公开文档读取 → AccessBlock 紧急撤销及安全恢复**。各产品业务 API 的运行时仍在产品域，Fabric 不取代 API Gateway、IAM、CI/CD、Broker 或 Agent Runtime。

| 工程议题 | V1.0 设计候选 | 强制验证 / 不得误认 |
|---|---|---|
| 后端布局 | Go 模块化单体 + 独立 Worker 进程；领域边界由代码检查维持 | F02 ADR-0002 Proposed；不得直接拆为七个微服务 |
| 元数据与事务 | PostgreSQL 事务主库，出版指针/授权阻断/审计/Outbox 同库事务 | SQL 原子性、索引、锁、迁移和真实恢复均 `NOT_RUN` |
| 制品存储 | 受控不可变对象 Blob，Digest/Manifest 校验，制品先准备后引用 | 与数据库无分布式事务；失败对账而非“自然一致” |
| 契约工具 | OAS 3.1.2 先行 PoC，AsyncAPI 3.1.0 V1 候选，3.2.1 评估 | 各工具必须用功能样本证明，解析成功 ≠ 端到端兼容 |
| 权限 | 内外两个独立入口，服务端 ABAC/对象授权，私有内容 fail-closed | F05 矩阵及 Gate；UI 隐藏不算安全控制 |
| CI / 交付 | 规范校验、差异、生成、测试、SBOM/扫描、审批及发布证据 | CI 合格 ≠ 生产部署成功，保护机制需要真实演练 |
| 可观测性 | 结构化审计与 OpenTelemetry 指标/Trace 候选 | PII 清洗；观测可用性及具体 SLO 待基线采样批准 |
| 工程交付 | 设计图、Go 包/端口蓝图、数据/发布协议、分层测试、门禁和 Runbook | 运行态验证、授权、压测、HA、DR 和生产门禁 `NOT_RUN` |

<figure class="architecture-figure"><div class="fig-label">FIGURE 01 · F07 唯一纵向闭环 / Design Slice</div><div class="fig-stack"><div class="fig-grid cols-3"><div class="fig-box"><b>Git / 受控导入</b><small>SourceSnapshot · digest 固定</small></div><div class="fig-box"><b>Contract Registry</b><small>Revision · ref closure</small></div><div class="fig-box"><b>Policy & Review</b><small>CheckRun · PolicyDigest · Approval</small></div></div><div class="fig-arrow">↓ 准备制品可读且审批仍有效，才进入发布事务</div><div class="fig-grid cols-3"><div class="fig-box fig-primary"><b>Immutable Release</b><small>ArtifactDigest · Build Manifest</small></div><div class="fig-box tone-green"><b>Publication Slot</b><small>Target × Asset · generation CAS</small></div><div class="fig-box tone-green"><b>Authorized Read</b><small>Grant + AccessBlock + active generation</small></div></div><div class="fig-separator">撤销：先提交 AccessBlock → 读路径立即拒绝 → 再清理投影/CDN → 无传播证明显示 UNKNOWN/AT_RISK</div></div><figcaption>此图表示设计时强制验证的有序协议，不是现有生产系统的执行截图。</figcaption></figure>

## 1. 工程范围、准则和不允许发生的漂移

### 1.1 范围边界

- **实现对象**：Fabric 的管理 API、可靠 Worker、契约解析/治理、元数据事务、受控查询/发布、审计与运行质量；两套门户消费这些服务而不拥有同一身份会话。
- **不实现对象**：CargoPai Shipment 领域、Parcel Tracking 领域、网关转发、令牌颁发、消息代理、多产品共享业务数据库，以及真实的 AI Agent 任务执行。
- **严禁伪造**：不得把 Mock 原型截图记为业务 API 功能验证；不得根据某个 CLI 运行结果声称“兼容全部 OpenAPI / AsyncAPI”；不得以单一通用实体 `Application` 混淆 Product 与 ConsumerApplication。

### 1.2 约束优先级与变更处理

优先级为 `IDP-SCG-STD-001 适用规则 → F01 产品要求 → F02 架构不变量与 Proposed ADR → F03 事实/不变量 → F04 治理 → F05 安全 → F06 体验 → F07 实现细化`。如出现冲突，应在 F07 开放决策中列差异、影响及候选 ADR，具名批准后更新；**F07 不直接推翻前六份文档**。技术实现应可被替换，例如更换搜索引擎不改变 Registry、Publication 的权威事实。

### 1.3 追溯纪律

每个核心工程控制拥有稳定 `ENG-xx` 标识，关联 FR/NFR、F02 AI、F03 INV、F04 规则、F05 授权/威胁及 F06 页面、F07 测试场景。实现和测试的链接可以先为 `TBD`，但不能写虚构代码路径或执行结果。机器可读承接资产：`tests/acceptance/F07_engineering_trace.csv`、`F07_engineering_scenarios.csv`、`F07_tool_compatibility_matrix.csv`。

## 2. 质量属性与关键不变量

| 属性 | 不允许失守的设计约束 | 证据与责任 |
|---|---|---|
| 身份/隔离 | Product / Workspace / Principal / DeveloperOrg / ConsumerApp / Grant 彼此独立；默认拒绝 | F05 授权矩阵、跨产品负向测试；安全 Owner 待指派 |
| 完整性 | Release 是不可变制品；Revision/Policy/Decision/Release 的摘要链不能被替换 | 字节摘要、来源和工具 manifest；Registry Owner 待指派 |
| 并发 | 同 `(target_id, api_asset_id)` Slot 的世代单调，CAS 失败不得静默重试覆盖 | 并发事务测试；Publication Owner 待指派 |
| 撤销 | AccessBlock 作为源站强制门，撤销不能等待可重建搜索投影 | 撤销后直读、旧缓存、CDN 场景；Security/Operations |
| 可靠性 | Worker 至少一次；消费幂等、lease fencing、DLQ/人工处置 | 故障注入 + 事件重复执行测试；Operations |
| 可恢复性 | Metadata / Artifact / Audit 可对账；有已批准 RPO/RTO 和演练 | 备份恢复证据；Owner、目标均 TBD |
| 可理解性 | 内外门户分别记录公开/私有状态、未知传播、错误恢复与操作权限 | F06 角色场景、键盘验收；UX Owner |
| 可维护性 | 限界上下文独立依赖；生成 DTO 与领域实体分离 | 静态依赖检查 / 代码审查 |

<figure class="architecture-figure"><div class="fig-label">FIGURE 02 · 工程质量属性与硬门槛</div><div class="fig-grid cols-3"><div class="fig-box"><b>Integrity</b><small>Release Digest + CAS + 审计</small></div><div class="fig-box"><b>Security</b><small>Server ACL + Deny + Block</small></div><div class="fig-box"><b>Reliability</b><small>Job Lease + Outbox + Reconcile</small></div><div class="fig-box tone-green"><b>Evidence</b><small>真实测试记录 / 证据 URL</small></div><div class="fig-box tone-green"><b>Experience</b><small>两个独立 Portal + UX 任务</small></div><div class="fig-box tone-green"><b>Operability</b><small>SLI · Backup · Restore</small></div></div><div class="fig-separator">以可验证的架构断言组织质量，而非先设定未经测量的“99.99% 可用性”。</div><figcaption>所有 SLO、RPO/RTO、性能阈值属于拟议验收输入，需要 F08 经采样、容量成本和风险审批确定。</figcaption></figure>

## 3. Go 工程代码组织（逻辑蓝图，不是已创建的 Go 服务）

```text
idp-api-fabric/                        # 当前目录只含设计资产；以下是未来代码落点建议
├── cmd/
│   ├── fabric-api/main.go             # 控制平面 HTTP/BFF 入口
│   └── fabric-worker/main.go          # Job / Outbox / 解析治理 Worker
├── internal/
│   ├── productgovernance/{domain,application,ports,adapters}/
│   ├── apicatalog/{domain,application,ports,adapters}/
│   ├── contractregistry/{domain,application,ports,adapters}/
│   ├── policyreview/{domain,application,ports,adapters}/
│   ├── publication/{domain,application,ports,adapters}/
│   ├── developeraccess/{domain,application,ports,adapters}/
│   ├── platformoperations/{domain,application,ports,adapters}/
│   ├── transport/http/                # 生成 DTO / Handlers / 中间件
│   └── platform/{db,artifacts,identity,telemetry,workers}/
├── api/                                # Fabric 管理 API 的受控 OpenAPI 与生成配置
├── migrations/                         # 数据库迁移；SQL 经双向/恢复评估
├── tests/{unit,integration,contract,security,resilience,load}/
├── deployments/                         # 环境无关模板 + 批准的环境覆盖
└── tools/                               # 版本固定、校验可重现的构建工具
```

**注意**：本清单只是未来 Go 代码的目标布局，**不在 F07 设计文档包中假装这些目录已有可运行代码**。初期可按上述 Context 划 Go packages，但不是七个强制独立 Go module，也不是强制七个数据库 Schema。保留后续按实际事务、容量、故障与团队边界拆分服务的可能。

### 3.1 依赖允许方向

- `transport / adapters → application / ports → domain`；业务不变量在 `domain`，用例和事务协调在 `application`，HTTP/SQL/ObjectStore/IAM 仅在 Adapter。
- 领域不能依赖 HTTP 路由、ORM、云 SDK、SQL、UI、IAM SDK；跨 BC 使用显式 Ports、稳定 ID 和防腐层，而不是共享其他 BC 的数据库模型。
- 生成的 OpenAPI DTO 仅属于传输协议。转换要显式处理：nullable 与缺失、decimal 金额、时间戳、权限 Scope、错误对象。
- 每个 BC 可由自身控制接口实现同域事务；复杂跨 BC 协调在 Application 层，由事务/持久事件/对账保持约束。
- 使用 `go list -deps -json` 或静态包依赖分析生成违规路径，并在 CI 阻止 `domain → adapters/transport/platform`；仅靠 Go `internal/` 并不足以保证 BC 隔离。

<figure class="architecture-figure"><div class="fig-label">FIGURE 03 · Go DDD 分层与 Port / Adapter</div><div class="fig-grid cols-2"><div class="fig-box"><b>Internal Engineering Web</b><small>受控命令 / 审批 / 证据</small></div><div class="fig-box"><b>CargoPai Developer Web</b><small>按产品受众授权的只读投影</small></div></div><div class="fig-arrow">↓ HTTP / Identity Context / Trace → Handler Adapter</div><div class="fig-head"><strong>Application Use Cases</strong><small>ImportRevision · RunGovernance · Approve · Release · PublishCAS · Revoke</small></div><div class="fig-arrow">↓ Ports（接口）与值对象；不把 DTO 作为领域实体</div><div class="fig-head light"><strong>DDD Domains / Aggregate Invariants</strong><small>ContractRegistry · PolicyReview · Publication · DeveloperAccess · ...</small></div><div class="fig-arrow">↓ Outbound Adapters</div><div class="fig-grid cols-3"><div class="fig-box tone-green"><b>PostgreSQL</b><small>事务与乐观锁</small></div><div class="fig-box tone-green"><b>Object Blob</b><small>Release / Manifest</small></div><div class="fig-box tone-green"><b>IAM / Git / CDN</b><small>ACL / 导入 / 传播</small></div></div><figcaption>职责明确但部署可保持一个 API 进程和一个 Worker 进程；兼容 Bento-Go 的必要性及具体版本需在 PoC 决议。</figcaption></figure>

### 3.2 建议 Ports 与契约责任

| Port / Use Case | 输入与不变量 | 错误、幂等与观测 |
|---|---|---|
| `CaptureSource` | 受控 Git Commit 或上传 Blob；限制引用闭包 | 不受控 URL/Digest 漂移拒绝；审计来源 |
| `RegisterRevision` | `api_asset_id, source_digest, ref_digest`；作用域一致 | 幂等键 `(product,asset,source_digest)` 候选，需唯一性验证 |
| `EvaluatePolicy` | 固定 `revision_digest,policy_digest,tool_manifest` | UNKNOWN/UNSUPPORTED 不能按 PASS 发布 |
| `RecordDecision` | 审核人与提交人职责分离；批准绑定输入与目标 Scope | 审批过期/权限变化触发失效 |
| `PrepareRelease` | 固定 Manifest、验证 Blob 可读，Release 不可变 | Storage 未确认 → PENDING/UNKNOWN，不写 READY |
| `PublishSlot` | Target×Asset + expected_generation +有效审批 | CAS 冲突 `412/409`，不静默重放覆盖 |
| `BlockPublication` | 高优先级阻断/撤销，服务端读路径同步生效 | CDN 传播未证明为 UNKNOWN/AT_RISK |
| `ResolveDeveloperRead` | 当前 Principal、Product、Audience、Environment、Grant、Slot generation | 私有内容拒绝，不复用跨产品缓存 |

## 4. HTTP API、事件与生成代码的兼容策略

### 4.1 工具候选（正式版本锁定前必须完成兼容验证）

- OpenAPI：F04 选择 **3.1.2 初始工程兼容目标**，3.2.1 另行测评。Go 的 `oapi-codegen` 官方于 2026 年已支持部分 OpenAPI 3.1 语义，但仍存在无法忠实映射到 Go 静态类型的 Schema；工具链能生成代码不意味着全部约束被执行。
- AsyncAPI：V1 3.1.0 需要实际验证官方 Parser / Schema、Channel/Operation/Binding 和渲染支持；MVP 未验证时只允许登记元数据，不得给出正式兼容声明。
- 统一入口：内部管理 API 与外部文档读取 API 应分别标记认证、权限及受众；记录 `operationId`、错误语义、速率限制、幂等和 API 生命周期。
- 生成边界：`api/` 是 OpenAPI 契约源；生成 `transport` DTO / Handler Interface / Client 的版本、配置、Checksum 都进入 Build Manifest，禁止手改生成文件；业务验证继续在 Application / Domain。

### 4.2 不可跳过的规范 PoC 样本

| 测试族 | 必验特性 | 失败处置 |
|---|---|---|
| OAS3.1 基础 | `type:["string","null"]`，nullable 与可选区分，`required`，`readOnly/writeOnly` | 生成/验证不一致 → 不宣称支持 |
| 组合 Schema | `oneOf/allOf/anyOf`、`const`、判别规则、AdditionalProperties | 需要回退自定义处理时记录受限支持 |
| 引用安全 | 本地/外部 `$ref`、循环、深度/字节配额、闭包摘要 | 任何不受控网络访问 → 拒绝并报告 |
| HTTP 语义 | `operationId`，参数序列化，Cookie/Header，响应 `problem+json` | 类型或错误码改变 → CI 拦截 |
| 安全语义 | OAuth scope、SecurityScheme、匿名/私有读、对象授权 | Schema Lint 不能替代安全集成测试 |
| 差异检测 | Breaking/Risky/Compatible/Unknown；Enum/response/security 变化 | UNKNOWN 默认需人工审查 |
| 渲染/SDK | Go/TS/cURL 示例，错误与私有字段曝光，SDK 内容摘要 | 不支持特性必须列入发布限制 |
| AsyncAPI | 3.1 Channel/Operation/message bindings 与 Producer/Consumer 视角 | 未验证能力保持 NOT_RUN / UNSUPPORTED |

工具版本与验证样本映射在 `tests/acceptance/F07_tool_compatibility_matrix.csv`；**工具候选 ≠ 已决定依赖版本**。Go 工具链版本、Bento-Go 是否复用、数据库及浏览器版本由批准的兼容矩阵后续冻结。

## 5. PostgreSQL 物理实现候选与事务边界

### 5.1 由 F03 逻辑模型派生，不重新发明实体

- F03 `docs/F03_Logical_Model.csv` 的 37 项是逻辑数据条目；F07 只指定物理实施规则与索引/迁移验证，不擅自宣称已提交完整数据库 DDL。
- 使用不可变 stable ID 和显式 `product_id/workspace_id` 作用域（适用时）；关键关系由实际数据所有者掌控，跨 Context 的强依赖通过已批准引用或显式 FK 策略解决。
- 重点候选唯一约束：`(target_id,api_asset_id)` 唯一 Slot；`(api_asset_id,release_label)` 的唯一策略仍需 F03 OD-04 决议；`(scope,idempotency_key)` 绑定 `request_fingerprint`，防止重复键异负载。
- 每张可变聚合表要有乐观版本和审计字段，不能把历史业务事实简单软删除；保留/清理/法律保全独立治理。

### 5.2 发布 CAS 的 SQL 语义示例（**非正式 DDL / 未执行**）

```sql
-- DESIGN_EXAMPLE: 在一个数据库事务中执行；表名和字段为示意。
-- 前置：对授权、AccessBlock、Release READY、Decision/Policy 有效性进行
-- 同事务再校验或在必要的锁/隔离规则下保证读写一致，不能仅在事务外检查。
UPDATE publication_slot
   SET active_release_id = :next_release_id,
       generation = generation + 1,
       updated_at = CURRENT_TIMESTAMP
 WHERE slot_id = :slot_id
   AND generation = :expected_generation
   AND access_blocked = FALSE
RETURNING generation;
-- 必须恰好返回一行；0 行 => STALE_GENERATION/ACCESS_BLOCKED/NOT_FOUND
-- 由应用层区分原因，但不能绕过权限后重试写入。
-- 同一事务追加 publication_history、audit_record 和 outbox_event。
```

这里 `access_blocked` 仅示范数据库判定所需的同步阻断条件，**并不要求 F03 把 AccessBlock 聚合合并进 Slot**。真正实施可采取规范化表 + 同事务锁定/约束或强一致投影；必须设计并发撤销与发布的交错事务，使任何已生效 Block 都优先阻断新发布。隔离级别、锁粒度和死锁重试需真实数据库竞争测试。

### 5.3 数据迁移安全

Migrations 采用 **expand → backfill → verify → switch reads/writes → contract**，禁止在与旧版进程并存时直接删必需字段。迁移每步含 owner、前后置 Schema、数据回填可重入性、索引建立策略、锁时长预算、回滚边界和备份点。**DDL 回滚不是数据恢复**，存在数据变换的升级须通过正向修复/恢复演练。迁移不可隐式从应用启动时自动抢占生产 DDL 锁；受控执行与审计。

<figure class="architecture-figure"><div class="fig-label">FIGURE 04 · 原子发布 CAS、事件与只读投影</div><div class="fig-grid cols-3"><div class="fig-box"><b>请求 / Validated Principal</b><small>Expected Generation · Idempotency</small></div><div class="fig-box fig-primary"><b>DB Transaction</b><small>Slot CAS + History + Audit + Outbox</small></div><div class="fig-box tone-green"><b>Commit → Response</b><small>返回新 Generation；不得承诺 CDN 已同步</small></div></div><div class="fig-arrow">↓ 异步可靠投递（可重复）</div><div class="fig-grid cols-2"><div class="fig-box"><b>Outbox / Worker</b><small>Lease + attempt + event_id 幂等</small></div><div class="fig-box"><b>Portal Projection</b><small>generation 单调更新；可重建</small></div></div><div class="fig-separator">绝不把索引更新或 CDN 清理混入同一 PostgreSQL ACID 事务的成功定义。</div><figcaption>非同步外部效果通过状态与 Reconciliation 观察；重试必须能检测旧事件与重复事件。</figcaption></figure>

## 6. Artifact Store、来源闭包与内容供应链

1. 对原始上传/Git 快照使用 `source_digest`；Ref Resolver 的每个定位、字节、哈希写入闭包清单，导出 `ref_closure_digest`；版本化策略和工具产生 `build_manifest_digest`；最终只读发布 Blob 对应 `artifact_digest`，四者不混淆。
2. 对对象存储先写到**不可公开的内容地址区**，验证摘要、大小、受控 MIME、内容可读性与安全扫描结果，再在数据库事务写入可引用的 READY 制品记录。文件从未直接基于用户原始 URL 加入公网索引。
3. 数据库失败但 Blob 成功时，孤儿 Blob 留待带保留窗口的对账清理；数据库成功而 Blob 读失败时，Release/Publication 必须拒绝或标记 `UNKNOWN`，不能自动曝光。
4. 制品不可覆盖。服务端授权后的下载要验证当前 AccessBlock 与 Release Scope，短时凭据也不可绕过撤销，私有 Blob 不给永久可猜 URL。
5. 构建应尽量确定性；如 PDF/HTML 组件或工具带时间戳造成不可重复字节，记录 Tool Version、配置、输入、输出摘要，不虚报可重现性。

## 7. 持久任务、Outbox 与失败恢复协议

| 对象/状态 | 候选设计 | 必须实测的异常 |
|---|---|---|
| Job | `PENDING → RUNNING → RETRY_WAIT/SUCCEEDED/FAILED/BLOCKED`；`lease_owner/lease_until/fencing` | 进程崩溃、lease 过期、钟差、任务抢占 |
| Outbox | 与业务事务同库提交；订阅 Worker 轮询或消息适配后投递 | 重复投递、进程重启、DB 故障、外部超时 |
| Idempotency | `(scope,key)` 与 request fingerprint 锁定第一次有效结果/受控未知状态 | 同键相同负载、同键不同负载、并发请求 |
| Reconciliation | 对象存储、Release、Slot、授权、CDN、索引状态按摘要与 generation 对账 | 误删、孤儿 Blob、过期投影、缓存残留 |
| Retry policy | 指数退避 + jitter、最大尝试、人工隔离/修复 | Poison item、永远失败、错误导致重试风暴 |
| Fencing | 每次获取 lease 单调 token，持久操作校验 token | 旧 Worker 在超时后完成旧任务误写 |

使用 `SKIP LOCKED` 类型的行租赁模式是候选实施方式，不应把它理解成数据库消息队列的无限吞吐保证。数据库行锁、热点 Slot、Outbox 膨胀和归档周期应压测。顺序只对明确的 Partition/Slot 约束承诺；端到端 Exactly-Once 不作为天然保证。

<figure class="architecture-figure"><div class="fig-label">FIGURE 05 · Outbox、Lease/Fencing 与故障恢复</div><div class="fig-grid cols-3"><div class="fig-box"><b>Domain Commit</b><small>Authoritative State + Outbox Event</small></div><div class="fig-box fig-primary"><b>Worker A</b><small>Lease #5 · 执行</small></div><div class="fig-box"><b>Worker B</b><small>Lease #6 · 重新认领</small></div></div><div class="fig-arrow">↓ Worker A 旧 Lease 结果被拒绝；Worker B 根据事件/目标 generation 幂等处理</div><div class="fig-grid cols-2"><div class="fig-box tone-green"><b>Projection</b><small>只接受新 Generation / 同事件去重</small></div><div class="fig-box tone-green"><b>Evidence</b><small>attempt、trace、fence、reconcile result</small></div></div><div class="fig-separator">即使先执行外部不可回滚动作，也需记录结果 UNKNOWN 并通过外部 idempotency key / 对账判定，禁止无脑重复调用。</div><figcaption>Redis/消息代理不是初始必需依赖；何时迁移由吞吐与故障指标触发，不是因为“更现代”。</figcaption></figure>

## 8. 安全实现设计与双门户鉴权责任

### 8.1 服务端授权链

- 身份来自可信 IAM/OIDC Adapter 的验证结果；令牌签发、密码、OAuth Client Secret 存在 IAM/Secret Vault，不在 Fabric 数据库存原文。
- 认证主体 `principal_id` 不自动拥有产品权限，必须结合 `product_id, workspace_id, api_asset_id, target_id, audience, environment, grant_ref, requested_action` 与时效策略构建 **AuthzContext**。
- 所有读写操作统一检查对象级授权。列表/搜索、历史 Revision、Artifact 下载、直接预签名链接、审计查询、发布、撤销和 Worker 执行均不能省略。
- 私有门户和内部工作台使用分离的登录/Session/Cookie 策略与 CSRF、CORS、SameSite 配置；跨域共享设计系统不意味着跨安全域共享 Session。
- 内网资源访问/外部 `$ref`、Webhook 目标、Explorer 出站调用需严格网络出站策略 + DNS/IP/协议检查，不把 Fabric 变成通用 SSRF 代理。

### 8.2 撤销优先的源站读取协议

`AccessBlock` 必须是发布与读取路径的强制约束，不能只靠异步缓存清除。对于私有文档，源站必须在每次授权读取验证当前授权和 Block；对于公开且已被第三方缓存的内容，源站阻断无法保证外部副本已回收，因此传播进度可能持续标为 `UNKNOWN/AT_RISK`，并触发安全响应。恢复前须经授权审批并创建新 generation / 发布操作；禁止无证据自动解封。

<figure class="architecture-figure"><div class="fig-label">FIGURE 06 · 文档读取与紧急撤销的信任边界</div><div class="fig-grid cols-2"><div class="fig-box"><b>Internal Web (SSO)</b><small>Internal Principal → RBAC/ABAC</small></div><div class="fig-box"><b>Product Developer Portal</b><small>DeveloperOrg / Grant / Audience</small></div></div><div class="fig-arrow">↓ 不同会话与 Origin；服务端重新计算每次资源授权</div><div class="fig-head"><strong>Authorized Read / Publish Endpoint</strong><small>Verify Identity → Object Scope → AccessBlock → Slot Generation → Artifact Digest</small></div><div class="fig-arrow">↓ deny-first</div><div class="fig-grid cols-2"><div class="fig-box tone-green"><b>ALLOW</b><small>按当前授权返回受控 Release</small></div><div class="fig-box"><b>DENY/UNKNOWN</b><small>拒绝私有资源；记录事件并显示正确状态</small></div></div><div class="fig-separator">外部 SDK 调用业务 API 的权限仍由业务系统 / IAM / Gateway 决定；Fabric 文档可见 ≠ 可调用业务 API。</div><figcaption>前端 Mock 中隐藏按钮不构成授权证明；F05 的 35 项权限矩阵需真实 HTTP/BFF/DB 负向测试。</figcaption></figure>

## 9. F06 双门户的实现与验收对照

| 门户 / 用户任务 | MVP 交付 | V1 扩展 | 对应 Server Contract / 证据 |
|---|---|---|---|
| F06-A：Catalog、Revision 读写 | 服务端授权的筛选/详情/变更记录 | 大型索引、影响分析、审计导出 | Product scope + asset ACL / 负向测试 |
| F06-A：Check / Review / Release | 可重放 Check 证据、独立审核、不可变 Release | 扩展策略/发布运营能力 | Decision binding / 改策后审批失效 |
| F06-A：Publish / Withdraw / Restore | CAS、撤销阻断、操作追踪、UNKNOWN/AT_RISK | CDN 传播与大规模运营自动化 | Slot Generation + Block + Reconcile |
| F06-B：Quickstart / Reference | CargoPai 品牌独立站、公开/私有文档、版本提示 | SDK/Webhook 深度内容 | Source digest + audience + visibility |
| F06-B：Applications / Sandbox | **仅入口/未连接提示** | IAM/Gateway 真实应用/Grant 和首次调用 | 真实 Token/Gateway Trace；Mock 不算 |
| F06-C：组件、响应式、键盘 | 关键任务键盘可用、错误/拒绝/空态一致 | WCAG 2.2 AA 范围评估与审查 | 自动工具 + 实际键盘/屏幕阅读器测试 |

实施前端应通过 typed API client 消费 Fabric 已发布的管理 API 契约；不可从 F06 本地 JS 的 Mock 状态生成生产逻辑或权限约束。门户的 CDN/SSR/CSR 选择由敏感数据保护、运维、可访问性和 SEO 共同决定，F02 ADR-0005 未批准前不得绝对锁定某一种框架。

## 10. CI/CD 与质量门禁（设计要求，不是假设已有流水线）

<figure class="architecture-figure"><div class="fig-label">FIGURE 07 · 从 Contract PR 到发布证据的质量门禁</div><div class="fig-grid cols-3"><div class="fig-box"><b>01 · Pull Request</b><small>来源、变更、Reviewer 身份</small></div><div class="fig-box"><b>02 · Static</b><small>Format · Lint · Schema · Ref / Secrets</small></div><div class="fig-box"><b>03 · Compatibility</b><small>Diff · Generated DTO · Contract tests</small></div></div><div class="fig-arrow">↓ 控制面候选制品构建，保留不可变摘要</div><div class="fig-grid cols-3"><div class="fig-box fig-primary"><b>04 · Security</b><small>SAST · deps · SBOM · Supply Chain</small></div><div class="fig-box"><b>05 · System</b><small>DB / IAM / CAS / Outbox / Security</small></div><div class="fig-box tone-green"><b>06 · Approval</b><small>Independent Review + evidence</small></div></div><div class="fig-arrow">↓ 发布到受控环境并保留验证证据，不默认等于生产业务上线</div><div class="fig-head light"><strong>Versioned Artifacts · Deploy · Verify · Monitor · Revert/Block</strong><small>保留 toolchain versions、build digest、部署 ID、执行结果与审计追踪</small></div></div><figcaption>治理门禁与上位 STD-CP1…CP6 要作具名关联；图中的 CI 作业还没有真实部署，执行结果 NOT_RUN。</figcaption></figure>

### 10.1 必要阶段与失败处置

| 阶段 | 典型作业 | 不通过时的处置 |
|---|---|---|
| L0 输入 | 文件清单、受控引用与 provenance、License / Secret Scan | 无来源/越权来源立即拒绝 |
| L1 规范 | OAS/AsyncAPI 官方校验器、F04 内置/外部 Lint | UNSUPPORTED/UNKNOWN 不允许直接发布 |
| L2 变更 | Breaking Diff、语义审查、生成 DTO、SDK Golden Sample | 不兼容/生成损失进入专门审查 |
| L3 代码 | `go fmt` / `go vet` / `go test` / race/fuzz、边界依赖扫描 | 测试失败必须阻止交付 |
| L4 安全 | SAST、依赖与漏洞检查、SBOM、制品签名、Secret 保护 | 高风险按已批准策略阻断，不以忽略警告替代裁决 |
| L5 系统 | PostgreSQL 迁移、对象存储、IAM/授权、CAS 竞态、Worker | 关键安全/完整性场景失败禁止发布 |
| L6 发布 | Review bind + Release Digest + Slot CAS + audit | 过期审批或 generation 不匹配立即拒绝 |
| L7 发布后 | 健康/SLI/文档验证/撤销、回滚与对账 | 自动/人工回滚与阻断 Runbook 触发 |

治理规则检查本身不得允许 `SKIPPED` 被映射成 `PASS`；策略豁免必须记录 rule ID、范围、风险接受者、到期与例外证据。CI 缓存产物要与文件摘要/工具版本绑定，禁止复用其他产品/受众的未受控构建缓存。

### 10.2 Go 工具链安全与复现

- 使用固定 `go.mod/go.sum`、工具二进制版本与容器摘要；官方 Go toolchain 选择受 `go`/`toolchain` 与 `GOTOOLCHAIN` 配置影响，CI 要记录 `go version` 和工具链来源。
- `go test -race`、Fuzz、`govulncheck`、依赖许可扫描、SBOM、生成器稳定性和 `git diff --exit-code` 属于独立检查项；不因为项目尚无 Go 代码就伪报它们通过。
- Artifact provenance、签名与构建环境隔离应使用可验证的 CI 身份、短时令牌及最小权限，禁止持久写入仓库的生产 Secret。

## 11. 测试策略与证据等级

| 测试层 | 最小关注对象 | 当前状态 |
|---|---|---|
| Domain unit/property | F03 13 条 INV、非法状态转换、幂等指纹、generation 单调 | `NOT_RUN` |
| Contract / Golden | OAS / AsyncAPI、生成类型、错误体、版本 Diff、敏感字段 | `NOT_RUN`（F04 自定义样例校验不能替代） |
| SQL integration | 真 PostgreSQL CAS 事务、迁移、Outbox、死锁/约束、隔离级别 | `NOT_RUN` |
| Adapter integration | 不可变 ObjectStore、IAM/Git/Gateway/CDN 正常与超时 | `NOT_RUN` |
| Security negative | F05 35 授权规则、29 STRIDE 风险、跨产品直连下载、SSRF/缓存 | `NOT_RUN` |
| Portal user journey | F06 21 页面视图、32 UX 场景，真 BFF/授权/角色 | `NOT_RUN`（Local Mock 已有独立静态检查） |
| Concurrency/resilience | CAS 双写、Worker crash、lease fencing、对象存储部分失败、撤销竞态 | `NOT_RUN` |
| Load/capacity | 真实多产品目录、合同尺寸、并发用户、索引积压与 SLI | `NOT_RUN` |
| DR / Deployment | 备份、PITR、恢复对账、升级回退与日志安全 | `NOT_RUN` |

**证据分级**：E0 仅设计草案；E1 静态/解析及离线原型；E2 本地隔离集成（真实依赖）；E3 类生产真实链路与故障注入；E4 经批准的运行观察及生产演练。不得从 E1 推导 E3/E4 结论。F08 针对每条 AT、ENG 场景记录 `environment, commit, artifact_digest, tool_version, executor, timestamp, expected, observed, outcome, evidence_uri, approver`；缺失关键字段视 `NOT_RUN/INCOMPLETE`。

## 12. 关键工程验证场景（用于 F08 执行）

本节只定义测试意图；完整列表在 `tests/acceptance/F07_engineering_scenarios.csv` 中，所有真实运行状态均初始化为 `NOT_RUN`。关键类别包括：

- **完整性**：受控 Source Digest 改变、Ref 闭包被调包、被修改的 Release Blob、过期审批、规则集变化、重复 Release。
- **并发**：同 Slot 两发布者 CAS、撤销与发布竞争、同幂等 Key 异负载、Outbox 消费顺序、旧 Worker fencing。
- **安全**：跨产品搜索/历史/直链拒绝、AccessBlock 生效、Edge 缓存残留显示风险、模拟恶意 Ref/SSRF、Explorer 任意 URL 禁止。
- **可靠性**：Blob 成功 DB 失败、DB 成功 Blob 读失败、Worker 重启、任务 Poison、投影重建、CDN 超时 UNKNOWN。
- **门户**：Internal 审批职责分离与可解释错误、CargoPai 私有版本失权、V1 Sandbox 无法伪造真实首调、移动键盘/屏幕阅读器任务。
- **运维**：Schema Expand/Contract、PITR 恢复、制品对账、灾备指标、SLO 报表、故障和审计证据留存。

## 13. 可观测性、SLI/SLO 与审计设计

### 13.1 Telemetry 方案

OpenTelemetry Go 是适合的候选，核心 Trace 与 Metrics 目前为稳定信号；Logs 相关成熟度需跟随实际所选版本验证。应保证 `trace_id, span_id, request_id, correlation_id, product_id (低基数或脱敏标签), api_asset_id (查询日志而非高基数指标), target_id, slot_generation, release_digest, operation_id, job_id, result_class` 可追溯，并避免把 Access Token、密钥、客户身份原文、完整 Schema/请求体写入指标或普通日志。审计事件记录真实主体、决策与前后事实，由受控写入路径生成，不以“打印日志”替代防篡改审计。

### 13.2 SLI 定义与正式阈值

| SLI | 精确定义的候选分子/分母或分布 | 需要的保护 |
|---|---|---|
| Control API availability | 符合服务目标的授权合法请求 / 全部合法请求（排除明确客户端错） | 健康检查不能只看进程存活 |
| Authorized read p95/p99 | 鉴权成功且资源存在时，HTTP 到首字节/整体完成时延 | 分公开/私有与受众；不得平均掩盖尾部 |
| Contract check latency | 固定契约大小区间的提交至确定结果时延 | 大文件分桶 / 资源上限 |
| Publication freshness | 主库有效 generation 到门户观察到同 generation 的延迟 | 区分源站/Edge、公开/私有 |
| Revocation enforcement | AccessBlock 提交至源站所有受控读路径拒绝的延迟 | CDN 传播是单独指标，不虚报立即清除第三方缓存 |
| Outbox backlog | 最老未确认事件年龄、积压量、重试率 | 吞吐、租户级公平性及告警 |
| Audit completeness | 应记录审计的高风险事务数 vs 实际持久审计条数 | 不能用随机采样替代强制审计 |
| DR recovery | 恢复后满足一致性检查的时间与数据丢失范围 | 目标由风险负责人批准，不在本稿编造 |

**SLO/告警阈值保持 `TBD`**，F08 在负载模型、成本约束、基线采样、业务恢复目标确定后批准；必须给出误差预算与业务应对策略。Public/Private 门户应分组观察，不能以一个总体平均值掩盖私有内容拒绝或撤销失败。

<figure class="architecture-figure"><div class="fig-label">FIGURE 08 · 观测数据、审计事实与运行运营</div><div class="fig-grid cols-3"><div class="fig-box"><b>API / Worker</b><small>Trace / Metrics / Sanitized Logs</small></div><div class="fig-box"><b>Business Audit</b><small>Who / What / Policy / Generation / Evidence</small></div><div class="fig-box"><b>External Evidence</b><small>Gateway / CDN / Storage receipts</small></div></div><div class="fig-arrow">↓ 分离存储、授权和保留；不能互相替代</div><div class="fig-grid cols-2"><div class="fig-box tone-green"><b>SLI / Alerting</b><small>可靠性 / 撤销 / 延迟 / 任务积压</small></div><div class="fig-box tone-green"><b>Operations Runbook</b><small>诊断 → 阻断 → 对账 → 恢复 → 审核</small></div></div><figcaption>指标监控是运行质量证据，不构成法律审计记录；实际可观测性部署尚未实现。</figcaption></figure>

## 14. 部署、配置、容量与弹性

### 14.1 环境分层

`local / CI / sandbox / staging / production` 使用一致镜像/制品摘要和显式环境配置。不同环境必须隔离凭证、对象存储、数据库、产品授权和受众；staging 的公开域名/缓存也不能无意访问生产私有制品。Feature Flag 只控制体验或逐步启用，**不允许关闭服务端 Object Authorization 或 AccessBlock**。

### 14.2 初期拓扑与演进触发

- 单区域生产候选：至少 API/Worker 分进程，受控 PostgreSQL + Blob + IAM/Gateway 适配；高可用、备份方式和多副本由业务风险与 PoC 决定，不默认生产可用。
- 搜索 MVP 可由 PostgreSQL FTS + ACL 查询承担；仅当测得数据量、查询相关性、p95/性能或隔离需求超标时引入专用索引，不造成写权威分裂。
- Job/Outbox 初期可在 PostgreSQL 运行；若锁争用、可用性、延迟、维护成本超出已批准阈值，再评估 NATS/Kafka，而不是先加入中间件制造复杂度。
- 容量估计必须输入 `产品数、APIAsset 数、版本/年、平均/最大 Schema 与引用闭包大小、外部订阅组织数、峰值查询QPS、审批并发、扫描 CPU、保留年限、CDN egress`。缺少实测输入时先保留公式和测量计划，不填“预计百万 QPS”。

## 15. 备份、灾难恢复和安全撤销 Runbook

### 15.1 数据恢复一致性

| 资产 | 恢复策略候选 | 不能忽视的验证 |
|---|---|---|
| PostgreSQL | 加密备份 + WAL/PITR 方案（按部署环境批准） | 与 Release/Outbox/Slot generation 一致性 |
| 对象存储 | 内容不可变 + 版本/保留策略 + 异地副本评估 | Manifest 到 Blob 可读及摘要一致 |
| 索引与门户投影 | 可从权威 Slot/Release/Grant 重建 | 重建期间不泄露被 Block 内容 |
| 审计与证据 | 单独保留策略及受限查阅 | 恢复不能篡改已批准 Decision/历史 |
| CI/配置 | 可复建 IaC、工具/镜像摘要、签名证明 | 恢复后配置及部署来源可证 |

### 15.2 四类操作 Runbook（候选）

1. **错误公开/敏感泄漏**：立即基于最小 Scope 写入 AccessBlock 并由权威库提交 → 源站读取拒绝 → 检查所有门户/缓存/预签名 URL/CDN → 未证明传播时标记 `UNKNOWN/AT_RISK` 并升级安全响应 → 完成必要通知及取证 → 具名授权恢复。
2. **CAS 冲突**：不自动覆盖 → 返回当前 Slot generation 和安全可见的冲突原因 → 操作者重新获取审批/版本对比 → 独立重新申请操作。
3. **Worker/Outbox 积压**：暂停危险重放 → 定位 lease/fence/Poison 事件 → 保留消息状态及 trace → 重试受控幂等任务 / 手工隔离 → 对账读侧 generation。
4. **元数据或制品恢复**：宣告恢复范围 → 恢复一致性快照 → 逐条检查引用 Blob Digest、Slot/Block、Outbox、Audit → 先私有读验证，再按批准恢复公开分发。

<figure class="architecture-figure"><div class="fig-label">FIGURE 09 · 故障处理与恢复分支（撤销优先）</div><div class="fig-grid cols-2"><div class="fig-box"><b>发现事件</b><small>安全泄漏 / CAS / Worker / Blob / DB</small></div><div class="fig-box fig-primary"><b>判断受影响 Scope</b><small>Product · Target · Asset · Release · Audience</small></div></div><div class="fig-arrow">↓ 信息泄漏或访问控制不确定：首先阻断</div><div class="fig-grid cols-3"><div class="fig-box"><b>AccessBlock</b><small>同步 deny-first</small></div><div class="fig-box"><b>Reconcile</b><small>Blob / Slot / Projection / Audit</small></div><div class="fig-box"><b>Owner Decision</b><small>恢复/回退 / 通知 / 风险接受</small></div></div><div class="fig-separator">任何外部传播未证明清除时保持 UNKNOWN/AT_RISK；不以“重新部署成功”当成安全撤销完成。</div><figcaption>待 F08 在真实目标环境执行故障注入、PITR、CDN 清理和受限读路径验证。</figcaption></figure>

## 16. 软件供应链、安全开发与依赖治理

- 对照 NIST SSDF（SP 800-218）建立安全开发措施，包括环境保护、依赖来源、漏洞响应、配置最小权限、保留发布证据。
- Go module、NPM/前端、CI Actions、外部 Schema 解析器、CLI 均应锁定来源、校验和、许可证及安全支持状态；SBOM 与待发布制品关联；不可使用 `latest` 作为审计意义的唯一版本身份。
- 安全漏洞按真实影响、可利用性、受影响服务与补丁时效分级。CVE 扫描“无报告”不是不存在漏洞的证明；紧急风险由具名 Owner 记录豁免/阻断和过期处置。
- 生成器输入为潜在不可信 Schema，代码生成和文档渲染应在受限进程/容器内运行，限制 CPU/内存/文件/网络和工作目录，不执行未知用户脚本。
- 机密不得出现在构建日志、报表、测试 Fixture、追溯 CSV 或 HTML 示例；凭证在 Vault/受控 Secret Store 使用短时授权进行注入，日志脱敏可被负向测试验证。

## 17. 可维护性：代码评审、分支、迁移与技术债

提交应同时带来契约变化、领域/适配层变更、迁移策略（需要时）、测试与证据；Reviewer 包含领域 Owner、安全 Owner（高风险时）、契约 Owner。为 Go 代码设置依赖边界与变化基线、覆盖率趋势、lint/静态分析及 CI 可复现性，不以单一覆盖率百分比证明质量。把所有临时架构例外绑定 `owner, reason, scope, expiry, mitigations, successor ADR`，定期处理到期豁免。

## 18. 工程控制目录与上位规范门禁

正式候选工程控制集保存在 `governance/policies/F07_Engineering_Controls_DRAFT.yaml`。编号 `ENG-01…ENG-30`，每项含执行责任、验证方式、适用阶段和状态。所有“强制”措辞基于拟议设计基线，最终纳入组织规范需经具名批准。与上位 `STD-CP1…CP6` 的对应关系是**设计建议**，不得声称已有 STD-CP6 生产签收。

| 项目门禁 | 设计中所需的最小证据 | 最终判定方 |
|---|---|---|
| STD-CP1 | 业务目标、适用 Profile、非目标与 F01 签收 | 产品/治理责任人 TBD |
| STD-CP2 | F02–F03 领域、事实权威、ADR、INV 与冲突裁决 | 架构/领域责任人 TBD |
| STD-CP3 | F04 契约、工具 PoC、规则与兼容性报告 | API 治理 Owner TBD |
| STD-CP4 专业体验 | F06-A/B/C 双门户任务、错误反馈、真实用户任务验证、键盘/读屏与 WCAG 适用性证据 | Product/UX Owner TBD |
| STD-CP5 工程与质量 | F07 编译、依赖/迁移/并发/负载/恢复、F05 安全控制实证与供应链扫描 | Engineering/QA/Security Owner TBD |
| STD-CP6 | F08 真实系统、恢复、安全、SLO、发布/回滚批准 | 独立发布责任人 TBD |

## 19. 关键待决与验证障碍（不擅自锁定）

| ID | 待决定内容 | 决策输入 | 责任/状态 |
|---|---|---|---|
| ENG-OD-01 | Go / PostgreSQL / 前端 / CI 精确版本与组合 | 兼容性 PoC、支持期与供应链报告 | Tech Owner TBD / Proposed |
| ENG-OD-02 | Bento-Go 的明确接入范围及升级策略 | 接口边界、生成器、依赖树及真实消费者测试 | Architecture TBD / Proposed |
| ENG-OD-03 | OAS 3.1 / AsyncAPI 3.1 全链路工具兼容范围 | F07 Tool Matrix / Golden Sample | Contract Owner TBD / NOT_RUN |
| ENG-OD-04 | Blob 服务、签名/复制/保留、公开 CDN 模型 | 故障、保密撤销和成本 | Security + Operations TBD |
| ENG-OD-05 | DB FTS 与专用搜索索引的演进阈值 | 生产相似数据/查询负载 | Platform TBD |
| ENG-OD-06 | SLO、RPO/RTO、备份周期与 DR 拓扑 | 业务要求、实际容量和成本 | Operations + Product TBD |
| ENG-OD-07 | AccessBlock 一致性与 CDN 传播证据 | F05 风险、真实边缘缓存测试 | Security TBD |
| ENG-OD-08 | GitOps / Repo 授权和发布审批的组织责任 | Git / IAM / CI 实际权限设计 | Governance TBD |
| ENG-OD-09 | V1 Sandbox/ConsumerApplication 的真实 IAM/Gateway 接口 | F06 V1 范围与集成 PoC | Integration TBD |
| ENG-OD-10 | 正式 STD-CP1–CP6 的具名采纳与风险级别 | 上位规则、F01 Manifest 草案、F08 | Governance TBD |

## 20. F08 正式交接任务：Delivery & Acceptance

**唯一任务**：F08 把 F01–F07 的全部候选需求和可执行验收资产转换为有真实负责人、真实环境与真实结果的产品交付路线图和验收门禁；不能在 F08 重新设计领域或将未执行测试判为通过。

**必须读取**：F01 的 MVP/V1 切片和 AT-01–12，F02 AI-01–07/ADR，F03 INV-01–13，F04 28 治理规则，F05 35 访问矩阵/29 威胁和 36 负向场景，F06 21 页面与 32 UX 场景，F07 30 ENG 控制、工具兼容矩阵、工程场景、运维 Runbook。

**必须交付**：实施批次/WBS、依赖及最小纵切顺序、具名审批待分配位、里程碑验收、风险与资源/成本估计方法、测试矩阵和真实证据格式、六道门禁与收尾条件、降级/回滚/灾备，正式路径 `docs/F08_Delivery_and_Acceptance.md/.html`；**F08 开始时不可改变 F01–F07 基线正文**。

**必须特别防止**：把 F06 Local Mock 误认已真实接入；把 F04 样本 lint 当完整 OAS 认证；把 F05 静态授权矩阵当越权测试；把 F07 工程蓝图或离线文档检查当生产 PoC。所有场景真实执行状态须以事实填报，缺证据保持 `NOT_RUN`。

## 21. 参考依据与采用限制

- [IDP Supply Chain DDD Product Engineering Standard V1.0](../README.md) ：本工程的上位规范由项目采用记录指向具体版本；链接返回本项目入口，不等同随包附带上位标准全文。
- [OpenAPI Specification 3.1.2](https://spec.openapis.org/oas/v3.1.2.html)、[OpenAPI Specification 3.2.1](https://spec.openapis.org/oas/v3.2.1.html)：标准版本不自动决定 Go 工具兼容范围。
- [AsyncAPI Specification 3.1.0](https://www.asyncapi.com/docs/reference/specification/v3.1.0)：异步契约候选基线，PoC 未运行。
- [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen)：Go OpenAPI 生成器候选；其 2026 年版本增加初始 OpenAPI 3.1 支持，需按真实版本验证生成能力、依赖和代码安全。
- [Go Toolchains](https://go.dev/doc/toolchain)、[Go Testing](https://go.dev/doc/tutorial/add-a-test)：用于设计工具版本冻结、race/fuzz 和测试复现，不代表已安装或验证目标版本。
- [OpenTelemetry Go](https://opentelemetry.io/docs/languages/go/)：Trace/Metric 候选；实施前核对具体组件稳定性。
- [NIST SP 800-218 SSDF](https://csrc.nist.gov/pubs/sp/800/218/final)：安全研发实践参考；合规义务和具体控制适用范围需另由责任人批准。

**文档状态结语**：F07 的 Markdown/HTML、机器可读工程资产、文档静态校验与压缩包完整性可以单独验证；**没有 Go 服务/数据库/IAM/Gateway/CDN 的运行证据，因此不能宣布工程实现、业务测试或正式验收完成**。
