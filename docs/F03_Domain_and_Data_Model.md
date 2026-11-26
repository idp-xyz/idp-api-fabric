# F03 — IDP API Fabric 领域模型与数据设计

> **文档编号**：F03 · Domain & Data Model  
> **项目**：IDP API Fabric (`idp-api-fabric`)  
> **文档基线**：V1.0 · 2026-11-09（沿用既定基线，不另立版本）  
> **状态**：领域与逻辑数据设计待具名评审，Product Governance 实现见 §5.5–5.6。架构 PoC、正式标准门禁和业务验收待执行。\
> **权威输入**：[F01](F01_Product_Requirements.md)、[F02](F02_System_Architecture.md)、[阶段交接约束](README.md)、《IDP Supply Chain DDD Product Engineering Standard V1.0》（IDP-SCG-STD-001）  
> **决策约束**：ADR-0001～0006 均为 `Proposed`；F03 不以此文宣称架构已批准。  
> **面向后续**：F04 API Contract Governance、F05 Security、F06 Dual Portal UX、F07 Engineering、F08 Acceptance。  
> **变更**：[CR-0001](../decisions/CR-0001.md)（2026-11-26 已决定）取消评审和批准，Release 依据检查准备。领域上的变化见 §0、§3.1、§4、§8，实现见 §5.15。[CR-0002](../decisions/CR-0002.md)（2026-11-26 已决定）内部使用从简：按编号下载看这个 Release 的阻断（§5.10、§5.12），审计如实给出行动人，平台管理员留在部署配置，暂不建 `AssetSearchIndex`（§3.3、§5.13）。

## 0. 执行摘要与设计结论

**领域设计采用“统一治理、多产品自治、单一事实权威”的联邦模式**。CargoPai、IDP Parcel、IDP Brant、IDP Prism 的运单、订单、库存等业务事实由原产品持有；Fabric 管理其 API 资产、契约修订、审查证据、不可变 Release、按访问目标生效的 Publication、外部开发者访问关系及运营证据。**七个候选限界上下文属于语义模型，不等于七个微服务、七个数据库或七个 Go Module。**

本文核心设计决定：

1. **事实权威分离**：`SourceSnapshot` / `ContractRevision` / `ContractRelease` / `PublicationSlot` / `PublicationOperation` / `PortalProjection` 均为不同业务概念。
2. **发布粒度精确**：`PublicationTarget` 指明产品、受众、环境和门户；`PublicationSlot = Target × APIAsset` 管理具体 API 的版本指针，允许一个门户同时展示多个 API。F02 与 ADR-0004 已同步统一此定义；ADR 仍 Proposed，需具名批准。
3. **检查证据绑定输入**：Release 依据修订的一次检查，检查引用修订内容摘要、策略快照和工具版本；没通过的可豁免规则在准备 Release 时逐条豁免，理由随 Release 记录。内容或策略变化后，准备新的 Release 要重新检查。CR-0001 起不再有人工批准，原先的审核证据（目标授权范围、审批人）作为只读历史保留。
4. **撤销先阻断**：公开错误发布先进行目标级禁止访问（deny）及服务端可见性拒绝，再在主库以 CAS 撤销/恢复并对账。无法将已经被第三方保存的内容“召回”。
5. **跨产品默认拒绝**：组织与用户的相同名称不构成授权；Product、Workspace、Principal、Audience、Environment 和外部 Consumer Application 的关系必须显式成立。
6. **事务只保证局部原子性**：数据库事务、对象存储、门户和外部 IAM/Gateway 之间不宣称分布式 Exactly-Once；通过持久作业、事务 Outbox、幂等与对账达到可恢复效果。

<figure class="architecture-figure"><div class="fig-label">FIGURE 01 · 七个候选领域与核心事实权威</div><div class="fig-head"><strong>IDP API Fabric · DDD Context Landscape</strong><small>各域拥有自己的业务事实 · 外部依赖通过 Ports / ACL 隔离</small></div><div class="fig-grid cols-3"><div class="fig-box tone-light"><b>Product Governance</b><small>Product / Workspace / Membership</small></div><div class="fig-box tone-light"><b>API Catalog</b><small>APIAsset / Owner / Lifecycle</small></div><div class="fig-box tone-light"><b>Contract Registry</b><small>Source / Revision / Release</small></div><div class="fig-box"><b>Policy & Review</b><small>PolicySnapshot / CheckRun / 历史评审</small></div><div class="fig-box fig-primary"><b>Publication</b><small>Target / Slot / Operation / Block</small></div><div class="fig-box"><b>Developer Access</b><small>DeveloperOrg / Application / Grant</small></div></div><div class="fig-arrow">↓ 统一审计关联、事务 Outbox、幂等消费者、可重建查询投影</div><div class="fig-grid cols-2"><div class="fig-box tone-green"><b>Platform Operations</b><small>Job / Outbox / Audit / Reconcile</small></div><div class="fig-box tone-green"><b>External Systems（非 Fabric 事实权威）</b><small>Git · IAM · Gateway · Product Runtime</small></div></div><figcaption>箭头表示业务协作，不表示共享写入或单事务跨越所有限界上下文。关系、所有权和契约见 §3。</figcaption></figure>

| 关键要求 | F03 设计对象 | 上游依据 | 后续归属 |
|---|---|---|---|
| 产品自治与隔离 | Product、Workspace、Membership、Scoped Reference | F01 FR-001–006、FR-063；F02 AI-01/03 | F05、F07 |
| 可追溯契约 | SourceSnapshot、Revision、Release、ArtifactManifest | FR-013–020；AI-02/05 | F04、F07 |
| 审查、例外及策略 | CheckRun、PolicySnapshot、Release 的豁免；ReviewDecision、WaiverRef 只读历史（CR-0001） | FR-023–028 | F04、F05 |
| 多 API 目标发布 | PublicationTarget、Slot、Operation、AccessBlock | FR-031–036；AI-04 | F04、F05、F07 |
| 双门户授权体验 | DeveloperOrg、ConsumerApp、GrantRef、PortalProjection | FR-040–055；AI-03/07 | F05、F06 |
| 可恢复运营 | Job、Outbox、Audit、Reconciliation | FR-061–066；AI-06 | F07、F08 |

## 1. 约束、适用性及精确边界

### 1.1 上位约束继承

- **IDP-SCG-STD-001 的 CORE + 触发式 Profile + 项目补充 + TECH** 为上位规范；F01 的 `F01_Project_Manifest_DRAFT.json` 仍是待批准的适用性草案，不能擅自将全部供应链业务专项规则判定为 Fabric 自身适用。
- **DDD 的战略边界先于技术部署**：领域名词、能力责任、聚合一致性决定模型；不能根据菜单、HTTP 路径、表名或 Go package 反向构造领域。
- **证据真实**：设计验证 `DESIGN_REVIEW_PENDING` 与运行测试 `NOT_RUN` 分开记录；STD-CP1…CP6 未通过具名签核不得写 `PASS`。
- **F01 FR-001–071、NFR-01–18 和 AT-01–12 编号保持不变**；本稿新增 `INV-*` 与 `DM-*` 只作为领域约束与设计验证标识。
- **F02 AI-01…AI-07 均保留为候选架构不变量**；如果精化出现歧义，记录待审设计决策，不静默修改 F02。

### 1.2 明确“不属于 Fabric”的事实

`Shipment`、`Booking`、`InventoryBalance`、`TradeOrder`、`CustomsDeclaration` 等属于各供应链产品的领域；Fabric 仅可把其 API/事件契约作为受控制品登记。外部 IAM 是凭证和身份认证权威，产品 Gateway/业务系统是生产调用权限最终执行方，Git 是提交内容与历史的外部来源权威。Fabric 不创建跨产品 `Shipment` 主数据、发行 OAuth Secret 或代理生产业务请求。

### 1.3 时间、身份、租户与不可变性的共同语义

- 所有持久身份采用稳定的不可复用 ID；人类名称、仓库地址、URL、`latest` 不充当实体身份。数据类型、UUID/ULID 的具体选型留 F07。
- 所有有业务时效的事实区分 `occurred_at`、`recorded_at`、`effective_at`、`expires_at`，以 UTC 时间戳存储，门户本地化显示；时间解释与过期判定不依赖客户端时钟。
- `product_id` 与拥有的 `workspace_id` 是控制平面隔离作用域；可记录外部 `tenant_ref`，但不自动等同 `product_id` 或 `developer_org_id`。
- 所有检查和审核证据、正式制品、已执行发布历史为**追加式不可变事实**；修订的内容快照不能原地覆盖。修订的审核**状态**原先可以受控转移，CR-0001 起不再转移，已有状态只是历史。

## 2. 统一语言及领域概念辞典

| 名词 / 推荐实体名 | 精确定义 | 身份与数据权威 | 严格不等于 |
|---|---|---|---|
| Product | Fabric 管理的独立 IDP 产品，如 CargoPai；有自己的 API 资产与外部门户 | `product_id`；Product Governance | 外部客户租户、OAuth Client |
| Workspace | Product 下内部协作及治理范围；可承载不同团队或环境操作分组 | `workspace_id`，隶属 `product_id` | 独立产品或公网发布目标 |
| TenantRef | 对应业务产品/身份系统的外部租户引用；可为多套来源 | `(authority,external_tenant_id)` | Fabric Product、数据库共享主键 |
| InternalOrganization | 内部研发/运营组织的引用；权威来自身份/组织系统 | `(identity_provider,organization_ref)` | 外部开发者公司 |
| Principal | 可被授权的用户、服务账户或工作负载身份引用 | `(issuer,subject)`，不存凭证 | 任一具体组织、角色的永久授权 |
| WorkspaceMembership | Principal 在指定 Product/Workspace 的显式成员授权关系 | `membership_id`、版本、有效期 | “同产品自动拥有全部 Workspace 权限” |
| DeveloperOrganization | 对外集成的法人/团队组织，具独立的管理范围（V1） | `developer_org_id`；Developer Access | InternalOrganization 或 Product |
| ConsumerApplication | 开发者创建的具体外部集成应用（V1） | `consumer_app_id`；Developer Access | IDP 产品、OAuth Client 的密钥 |
| OAuthClientRef | IAM 中真实 OAuth Client 的只读引用及状态快照 | `(issuer,client_id)`；IAM 实际拥有 | Fabric 自发的 Secret |
| Subscription / AccessGrant | 某开发者/应用在某 Product 的特定受众和环境的已审批接入关系（V1） | `grant_id`、有效期、审查证据；接入关系由 Fabric 管，调用授权由运行时执行 | Publication、OAuth 凭证本体 |
| APIAsset | 产品内有稳定身份的 HTTP / Event API 资产，含所有者和生命周期 | `api_asset_id`；API Catalog | 某次版本内容或门户页面 |
| ContractSource | Git 仓库受控定位或上传源的来源配置 | `source_id`；Contract Registry | 可变 Git 分支即不可变制品 |
| SourceSnapshot | 本次导入的单一来源快照和证据 | `source_snapshot_id`、`source_digest`；Contract Registry | 一个覆盖旧文件的共享 mutable path |
| ContractRevision | 绑定 SourceSnapshot 的不可变契约内容快照；审核状态只记录 CR-0001 之前的评审 | `revision_id`，`source_digest`；Contract Registry | Release、运行中的 API 部署 |
| PolicySnapshot | 本次检查适用的规则配置及摘要；准备 Release 时要求检查用的是现行策略 | `policy_snapshot_id`、`policy_digest`；Policy & Review | 下一次默认可沿用的策略 |
| CheckRun | 在固定 Revision、规则、工具版本上的一次检查执行证据；Release 依据它准备（CR-0001） | `check_run_id`；Policy & Review | 人工审批或生产运行证明 |
| ReviewDecision | 针对固定输入/受众/环境范围的审批事实；CR-0001 起不再产生，已有记录只读 | `decision_id`；Policy & Review | Release 生效或 Gateway 授权 |
| ContractRelease | 由 Revision 的一次检查（含准备时的豁免）和固定 Build Manifest 导出的不可变正式制品；CR-0001 之前的 Release 引用批准 | `release_id`、`artifact_digest`；Contract Registry | `latest` 或当前 Publication |
| PublicationTarget | 产品、受众、环境、门户组成的受控发布分发目标 | `target_id`；Publication | 单个 API 的当前版本 |
| PublicationSlot | 指定 Target 中某一个 APIAsset 的**唯一逻辑生效槽位** | `slot_id`，唯一 `(target_id,api_asset_id)`；Publication | 具体历史操作或门户索引 |
| PublicationOperation | 针对 Slot 的一次 Publish/Withdraw/Restore 命令及结果 | `operation_id`、idempotency；Publication | 实际 CDN/Edge 同步完成 |
| AccessBlock | 紧急撤销/保密事件的强制拒绝事实与传播状态 | `block_id`、scope、generation；Publication | 对已缓存的第三方文件的物理回收 |
| PortalProjection | 从 Slot/Release/授权产生的可重建只读内容与检索索引 | 版本与来源摘要；read-side | 可以直接修改的发布权威 |
| Job / OutboxEvent | 持久任务与至少一次出站消息记录 | `job_id` / `outbox_id`；Platform Operations | 生产业务订单事件 |

**命名禁则**：不要以单一 `Application` 表同时表示产品、客户端、服务账户；不要以单一 `version` 表同时表示契约修订、Release 业务版本、Slot CAS 代数和代码构建版本；不要以通用 `status` 枚举混用四类生命周期。

## 3. Context Map、事实权威与上下文协作

### 3.1 上下文边界和公开语言

<figure class="architecture-figure"><div class="fig-label">FIGURE 02 · DDD Context Map / 领域协作方向</div><div class="fig-grid cols-3"><div class="fig-box tone-light"><b>Product Governance</b><small>ProductScope & Membership</small></div><div class="fig-box tone-light"><b>API Catalog</b><small>APIAssetRef / Lifecycle</small></div><div class="fig-box tone-light"><b>Contract Registry</b><small>RevisionRef / ReleaseRef</small></div></div><div class="fig-arrow">↓ 显式引用、命令 API 与领域事件（不共享写入）</div><div class="fig-grid cols-2"><div class="fig-box"><b>Policy & Review</b><small>CheckRunRef (policy+input+tools)</small></div><div class="fig-box fig-primary"><b>Publication</b><small>Target / Slot / CAS / Block</small></div></div><div class="fig-arrow">↓ 有条件可见的只读投影；审计/投递用 Outbox</div><div class="fig-grid cols-2"><div class="fig-box tone-green"><b>Developer Access</b><small>Org / App / GrantRef (V1)</small></div><div class="fig-box tone-green"><b>Platform Operations</b><small>Job / Audit / Delivery</small></div></div><div class="fig-separator">ACL = Anti-Corruption Layer / 防腐层 · 真实身份、Gateway、Git 和供应链业务在 Fabric 之外</div><figcaption>Published Language 是带稳定 ID 与契约版本的少量跨上下文引用；不直接共享 ORM Entity、不允许其他 BC 修改所属聚合。边界仍需由领域评审确认。</figcaption></figure>

| BC | 独占的业务事实 | 上游依赖与关系 | 对外发布的语言 / 事件 | 禁止的越界操作 |
|---|---|---|---|---|
| Product Governance | Product、Workspace、Membership、ProductAudienceConfig | 外部 IAM 的身份引用经 ACL 映射 | ProductScopeRef、MembershipChanged | 为外部 IAM 签发令牌 |
| API Catalog | APIAsset、AssetOwnership、LifecycleTransition | ProductScopeRef，产品所有者声明 | APIAssetRef、AssetLifecycleChanged | 改写 Release/Publication |
| Contract Registry | SourceSnapshot、Revision、Release、ArtifactManifest | APIAssetRef、Source provider 证据 | RevisionRegistered、ReleasePrepared | 自行决定公网目标是否生效 |
| Policy & Review | PolicySnapshot、CheckRun、准备 Release 的检查门禁；ReviewDecision、WaiverRef 只读历史（CR-0001） | RevisionRef、Product policy | CheckCompleted；ReviewDecisionRecorded 不再产生 | 绕过检查门禁准备 Release：门禁取代人工批准，内容见 CR-0001 决定 1 |
| Publication | Target、Slot、Operation、AccessBlock、PublicationHistory | Product/Asset/Release/CheckRun 的只读验证 | PublicationActivated、Withdrawn、Blocked | 修改 Release 内容、修改 IAM 授权 |
| Developer Access | DeveloperOrganization、ConsumerApplication、GrantRef | 外部 IAM/Gateway 经 ACL 验证状态 | AccessGrantChanged（V1） | 存储 Client Secret 或替代网关授权 |
| Platform Operations | Job、AuditRecord、Outbox、Reconciliation | 通过稳定来源引用接收事件 | DeliveryRecorded、ReconciliationCompleted | 把任务状态当成 API 发布权威 |

### 3.2 依赖类型及事务边界

- **同一聚合内**：由一个 BC 的一个事务维护强不变量；必要时关联同事务审计索引与 Outbox。
- **不同聚合但同一个 BC**：使用应用服务编排及可验证的乐观并发；需要跨聚合事务时显式说明为何不能改成异步并保持较短事务。
- **跨 BC**：通过公开命令/查询接口、稳定引用和值对象快照协作；即使数据库在物理上共享 PostgreSQL，也不得允许直接修改其他 BC 的表。
- **跨外部服务/对象存储**：通过持久任务和对账处理，禁止把“数据库事务成功”当成对象存储/Edge 同步成功。
- **同一 Workspace 视图** 不意味着其全部数据属于同一个聚合。界面可通过 BFF/read model 组合，组合查询不改变数据归属。

### 3.3 核心查询投影的权威来源

| 查询/视图 | 权威来源 | 可重建投影 | 读时安全性 |
|---|---|---|---|
| 内部 Catalog 列表 | APIAsset + ProductScope | AssetSearchIndex | 每次有服务端范围过滤，索引无权威访问权 |
| Release 历史 | Registry Release + Digest | VersionTimeline | 权限检查 Asset/Product + Release Scope |
| 外部 API Reference | PublicationSlot 当前指针 + Release | PortalDocumentView | 必须校验当前允许发布且调用者有权；受限内容不能仅凭长效 CDN URL |
| 开发者应用状态 | Access Grant + 外部 IAM/Gateway 快照 | DeveloperDashboardView | 断连时显示 `UNKNOWN/STALE`，不假报实际运行授权 |
| 作业与审计 | Operations Audit/Job | OpsDashboardView | 运维/审核职责分离与敏感字段屏蔽 |

> CR-0002（2026-11-26 已决定）：内部 Catalog 列表暂不建 `AssetSearchIndex`。列表读 `api_asset` 的索引，资产概况在读的时候算，这是认可的做法；资产数量需要时再建索引（§5.13）。“作业与审计”一行的敏感字段屏蔽不包括审计记录里的行动人：审计员看到签发方和主体的原值（F05 §6.15）。

## 4. 核心聚合模型与不变量

设计以**最小事务一致性边界**为原则。后续逻辑表不强迫每个实体成为聚合根；一行表也不必然等于一个实体。`INV-*` 是 F03 正式设计不变量标识，待实现和测试验证。

| 聚合根 / BC | 内部实体 / 值对象 | 单事务必须保持的关键不变量 | 写入并发机制 |
|---|---|---|---|
| `Product` / Governance | ProductStatus、AudienceConfig、ProductRoleAssignment、WorkspaceRef | INV-01：产品 ID 唯一；已暂停产品拒绝新登记/发布，历史保留；撤销角色后至少保留一位有效负责人 | 版本比较、唯一约束；撤销负责人前在同一事务锁住有效分配 |
| `Workspace` / Governance | WorkspaceMembership、RoleAssignmentRef | INV-02：每个 Workspace 仅属一个 Product；成员授权按 Product×Workspace×Principal×Role×有效区间显式授予 | membership version、排他有效记录 |
| `APIAsset` / Catalog | AssetOwnerRef、LifecycleRecord、ApiType | INV-03：一个资产仅属一个 Product/Workspace；生命周期转移可审计，不与 Release 生效状态混淆 | aggregate_version |
| `ContractRevision` / Registry | SourceSnapshotRef、RefClosureManifest、RevisionState | INV-04：Revision 内容与 SHA-256 不变；修改必创建新 Revision | 唯一 ID、来源去重、状态 CAS |
| `ContractRelease` / Registry | ArtifactManifest、CheckRunRef、ReleaseWaiver、ReleaseState | INV-05：只有绑定通过门禁的检查证据（CR-0001 之前为有效审核证据）、内容可读取且摘要通过才可 READY；READY 后有效内容不能改变 | 创建幂等指纹 + 唯一制品摘要 |
| `PolicySnapshot` / Policy | PolicyRuleRef、PolicyDigest、Scope | INV-06：检查/决定始终引用固定策略快照，不能改写历史规则结果 | immutable record |
| Release 门禁 / Policy（`ReviewDecision` 只读历史） | CheckRunRef、ReleaseWaiver；SubjectScope、DecisionOutcome 只在历史评审中 | INV-07：Release 仅依据匹配内容、现行策略下的检查及其豁免；豁免不得覆盖不可豁免规则（CR-0001 修改；原文为“批准仅对匹配内容、策略、审核范围/目标和有效期有效”） | 准备 Release 的事务内判定；检查和评审记录只增不改 |
| `PublicationSlot` / Publication | ActiveReleaseRef、Generation、VisibilityState、OperationHistoryRef | INV-08：一个 Slot 同一代数只有一个当前 Release 或无；条件更新必须携带 expected generation | DB CAS + unique slot key |
| `AccessBlock` / Publication | TargetScope、Reason、BlockGeneration、PropagationState | INV-09：有效 deny 阻断内容读取及后续激活；只允许显式具权威动作解除 | 单调 block_generation |
| `DeveloperOrganization` / Access (V1) | ApplicationRef、MembershipRef | INV-10：消费者成员和应用归属不能自动穿透产品；敏感 OAuth Client Secret 不落库 | 组织成员版本、有效授权 |
| `AccessGrant` / Access (V1) | ProductRef、Audience、Environment、ApprovalRef | INV-11：只有已审批、未到期且未撤销的 Grant 才能用于受限文档访问判断；真实 API 调用权另由 IAM/Gateway 判断 | grant status CAS、有效期限 |
| `Job` / Operations | RetryPolicyRef、Lease、Attempt | INV-12：任务由单个当前有效 lease/fencing token 执行完成写入；重试不能产生重复生效结果 | lease expiry + fencing token |
| `AuditRecord/Outbox` / Operations | ActorRef、Correlation、EventPayloadRef | INV-13：成功关键写命令必须同事务记录最小审计事实及 Outbox；历史记录追加不可篡改 | append-only、唯一事件 ID |

### 4.1 不变量不应被数据库约束替代

外键可防止孤立引用，却不能证明 Release 依据的检查匹配内容和策略、Edge deny 已传播或用户有权限。发布前必须由应用层领域政策判断，再由 DB CAS 和唯一约束兜底；安全检查归 F05/F07 验证。`AggregateVersion`（用于乐观并发）与 API 业务版本、Revision 次序、发布指针 `generation` 互相独立。

### 4.2 发布 Slot 的必要精化（待 ADR-0004 批准时确认）

F02 提出的 `PublicationTarget = Product × Audience × Environment × Portal` 是**分发目标**，不足以唯一识别每个目标内的多条 API。F03 新增 **PublicationSlot**：`UNIQUE(target_id, api_asset_id)`，其可变 `active_release_id` 与 CAS `generation` 才是**单 API 的当前版本权威**。一个 CargoPai partner/sandbox 门户可并行呈现 Tracking、Shipment、Rate API，不会在发布 Shipment 后覆盖 Tracking。此属澄清目标粒度，不改变 F02 “单一生效指针、不可变制品、原子 CAS”的不变量；已回填 ADR-0004 候选正文，尚待架构与数据负责人具名批准。

<figure class="architecture-figure"><div class="fig-label">FIGURE 03 · 一个门户目标容纳多个 API Slot / 正确的发布粒度</div><div class="fig-head"><strong>PublicationTarget · CargoPai / partner / sandbox / cargopai-developers</strong><small>唯一 target_id · audience + environment + portal</small></div><div class="fig-arrow">↓ 目标拥有 N 个独立 Slot，每个 Slot 各自 CAS generation 与 current release</div><div class="fig-grid cols-3"><div class="fig-box tone-green"><b>Slot · Shipment API</b><small>active Release R-S1 · generation 7</small></div><div class="fig-box tone-green"><b>Slot · Tracking API</b><small>active Release R-T3 · generation 12</small></div><div class="fig-box tone-light"><b>Slot · Rate API</b><small>无当前 Release · generation 0</small></div></div><div class="fig-arrow">↘ 无权限 / blocked：默认 deny，不读旧缓存；历史 Release 仍可审计但不会自动展示</div><figcaption>全部名称与版本为 DESIGN_EXAMPLE。Slot 与 Target 不等同；恢复命令只作用于目标 Slot，不能影响同目标的其他 API。</figcaption></figure>

## 5. 逻辑数据模型与关系基数

### 5.1 核心逻辑 ERD（不是已执行数据库 DDL）

<figure class="architecture-figure"><div class="fig-label">FIGURE 04 · 逻辑 ERD / 最小核心关系</div><div class="fig-grid cols-3"><div class="fig-lane"><strong>Product Governance</strong><span>Product (PK product_id)</span><div class="fig-mini">1 → N Workspace</div><div class="fig-mini">1 → N PublicationTarget（跨 BC 引用）</div><div class="fig-mini">Workspace 1 → N Membership</div></div><div class="fig-lane"><strong>Catalog & Registry</strong><span>Product 1 → N APIAsset</span><div class="fig-mini">APIAsset 1 → N ContractRevision</div><div class="fig-mini">Revision 1 → N CheckRun（Decisions 为历史）</div><div class="fig-mini">Revision 1 → N ContractRelease</div></div><div class="fig-lane"><strong>Publication</strong><span>Product 1 → N Target</span><div class="fig-mini">Target 1 → N Slot</div><div class="fig-mini">APIAsset 1 → N Slot（跨目标）</div><div class="fig-mini">Slot 1 → N Operation / History</div></div></div><div class="fig-arrow">↓ 跨域 Ref ID + 受控服务校验；ReleaseRef / CheckRunRef 不支持越界更新</div><div class="fig-grid cols-2"><div class="fig-box tone-light"><b>Slot 0..1 → Active Release</b><small>Release 必属相同 APIAsset；主库 CAS generation 为权威</small></div><div class="fig-box tone-light"><b>Target 0..N → AccessBlock</b><small>阻断范围可针对 Target / Slot / Artifact URL；有效 deny 优先</small></div></div><figcaption>本图是逻辑 ERD 关系摘要。完整键、基数、属性和唯一约束见下面数据字典及 `docs/F03_Logical_Model.csv`，不代表物理分表已锁定。</figcaption></figure>

### 5.2 主数据关系及完整性规则

| 关系 | 基数与归属 | 应用不变量与数据库候选约束 |
|---|---|---|
| Product → Workspace | 1:N；Workspace 属于唯一 Product | `UNIQUE(product_id, workspace_key)`；不得从其他产品迁移 Workspace 而保持旧 ID |
| Product → ProductRoleAssignment | 1:N；分配以 issuer×subject 标识主体 | 同 Product×Principal×Role 同时只有一条有效分配；撤销保留原行，再授予产生新 ID；应用用例加锁保证至少一位负责人 |
| Product → APIAsset | 1:N；APIAsset 属于唯一 Product | `UNIQUE(product_id, asset_key)`，`owner_workspace_id` 必属同 Product |
| APIAsset → Revision | 1:N；Revision 必属一 Asset | `UNIQUE(asset_id, revision_sequence)`；每 Revision 固定 source_snapshot_id |
| Revision → CheckRun | 1:N；每次运行绑定输入与工具 | `UNIQUE(check_run_id)`；不得因重跑覆盖已有结果 |
| Revision → Release | 1:N（允许同修订针对不同发布构建） | `UNIQUE(asset_id, release_label)`（若使用展示标签）；另以固定构建指纹做幂等；每个 Release 绑定一次检查及其豁免（CR-0001） |
| Product → PublicationTarget | 1:N；Target 属于唯一 Product | `UNIQUE(product_id,audience_key,environment_key,portal_key)` |
| PublicationTarget × APIAsset → Slot | N:1 + 1:N；同 Product 才能建立 | `UNIQUE(target_id,api_asset_id)`；必须校验 Target.Product = Asset.Product |
| Slot → ActiveRelease | 0..1；仅同 APIAsset 的 READY Release | 对 `slot_id` 加 CAS `generation`；外键不代替检查门禁与安全验证 |
| Slot → PublicationOperation | 1:N 历史命令；operation_id 稳定 | 对 `(principal,product,operation_type,idempotency_key)` 在保留期内唯一；异内容冲突 |
| Target / Slot → AccessBlock | 1:N；拒绝范围显式 | 有效阻断读侧优先；Scope 与 block_generation 独立于 Slot generation |
| DeveloperOrg → ConsumerApp | 1:N（V1）；应用属单一 DeveloperOrg | OAuth client_ref 仅外部引用；唯一由 IdP 语义决定 |
| ConsumerApp → AccessGrant | 1:N（V1）；范围可多产品 | 每个 Grant 绑定 Product×Audience×Environment×Subscription，时效与审批不可互相替代 |
| Aggregate → Audit/Outbox | 1:N 追加证据 | `event_id` 唯一；关键事实与审计 Outbox 同 DB Transaction |

### 5.3 字段设计原则与物理落地边界

- `public_id`、业务展示名、`slug` 分离；`slug` 可变、仅作导航别名，不能用于外键或审计聚合身份。
- `created_at`、`created_by_ref`、`updated_at`、`aggregate_version` 在可变聚合上按规范管理；不可变事实采用 `recorded_at`、`source_digest`、`actor_ref`、`correlation_id`、保留/删除分类。
- 每条带范围的表或索引都具备明确的 Product/Workspace 或有效引用，**不得假定每张表加一个 `tenant_id` 就完成授权**。
- 货币、重量、运费、运输节点等业务计量不属于 Fabric 本体，不为之创建共享字段和枚举；契约原文可包含这些字段，但由产品 Owner 维护业务语义。
- 历史 Release/批准/发布操作不支持普通级联删除；隐私删除请求采用去标识化、密钥生命周期与法定保留策略，具体合规由 F05 决定。
- 逻辑上可共享 PostgreSQL 实例；后续 F07 可以选择表前缀/Schema、索引及外键，但**不得让数据库内部 FK 变成跨 BC 业务写权限**。

### 5.4 数据字典的伴随机器资产

`docs/F03_Logical_Model.csv` 是本稿逻辑设计的机器可读索引：`context / aggregate / entity / key / attributes / unique / cardinality / retention / phase`。它不是可直接执行的迁移文件；任何字段精度、物理类型、约束语法、RLS 策略、分区/索引方案必须在 F07 经过验证后确定。


下面表格是领域实体的**逻辑结构索引**，同时作为 `F03_Logical_Model.csv` 的生成源。`HIST` 为历史证据不得常规覆写，`ACTIVE` 为可变业务关系但保留变更审计，`DERIVED` 为可重建数据；实际保留期限均 `TBD`。表中主键与唯一范围不代表已经执行数据库约束。

| BC | Aggregate | Entity | PrimaryKey | RequiredReferences | Uniqueness | Cardinality | DataRetention | Phase |
|---|---|---|---|---|---|---|---|---|
| ProductGovernance | Product | Product | product_id | owner_org_ref | product_key | 1 Product to N Workspace | ACTIVE | MVP |
| ProductGovernance | Product | ProductRoleAssignment | role_assignment_id | product_id+principal_issuer+principal_subject+role+role_assignment_version+granted_at+granted_by+revoked_at+revoked_by | product_id+principal_issuer+principal_subject+role where revoked_at is null | N per Product; at least one in force | HIST | MVP |
| ProductGovernance | Product | ProductAudienceConfig | audience_config_id | product_id | product_id+audience+environment where removed_at is null | N per Product over time | HIST | MVP |
| ProductGovernance | Workspace | Workspace | workspace_id | product_id | product_id+workspace_key | N per Product | ACTIVE | MVP |
| ProductGovernance | Workspace | WorkspaceMembership | membership_id | workspace_id+principal_ref | active membership scope | N per Workspace | ACTIVE | MVP |
| ProductGovernance | Product | ExternalTenantBinding | tenant_binding_id | product_id+authority | authority+external_tenant_id+product_id | N per Product | ACTIVE | V1 |
| APICatalog | APIAsset | APIAsset | api_asset_id | product_id+workspace_id | product_id+asset_key | N per Product | ACTIVE | MVP |
| APICatalog | APIAsset | AssetOwnership | ownership_id | api_asset_id+owner_ref | active ownership rule | N per Asset over time | HIST | MVP |
| APICatalog | APIAsset | AssetLifecycleTransition | lifecycle_event_id | api_asset_id+actor_ref | event_id | N per Asset | HIST | MVP |
| ContractRegistry | ContractRevision | ContractSource | source_id | api_asset_id+source_kind | source registration key | N per Asset | ACTIVE | MVP |
| ContractRegistry | ContractRevision | SourceSnapshot | source_snapshot_id | source_id+source_digest | immutable source fingerprint | N per Source | HIST | MVP |
| ContractRegistry | ContractRevision | ContractRevision | revision_id | api_asset_id+source_snapshot_id | api_asset_id+revision_sequence | N per Asset | HIST | MVP |
| ContractRegistry | ContractRevision | RefClosureManifest | ref_manifest_id | revision_id+closure_digest | revision_id+closure_digest | 1 per registered Revision | HIST | MVP |
| ContractRegistry | ContractRelease | ContractRelease | release_id | api_asset_id+revision_id+check_run_id | api_asset_id+release_label | N per Revision | HIST | MVP |
| ContractRegistry | ContractRelease | ArtifactManifest | artifact_manifest_id | release_id+artifact_digest | release_id+manifest_digest | 1 per READY Release | HIST | MVP |
| ContractRegistry | ContractRelease | IntegrityIncident | integrity_incident_id | release_id+detected_at | incident_id | N per Release | HIST | V1 |
| PolicyReview | PolicySnapshot | PolicySnapshot | policy_snapshot_id | product_id+policy_digest | policy_snapshot_id | N per Product | HIST | MVP |
| PolicyReview | CheckRun | CheckRun | check_run_id | revision_id+policy_snapshot_id | run_id | N per Revision | HIST | MVP |
| PolicyReview | ReviewDecision | ReviewDecision | decision_id | revision_id+check_run_id+reviewer_ref | decision_id | N per Revision | HIST | MVP |
| PolicyReview | ReviewDecision | WaiverRef | waiver_ref_id | decision_id+rule_id | waiver_ref_id | N per Decision | HIST | MVP |
| Publication | PublicationTarget | PublicationTarget | target_id | product_id+audience_key+environment_key+portal_key | product_id+audience_key+environment_key+portal_key | N per Product | ACTIVE | MVP |
| Publication | PublicationSlot | PublicationSlot | slot_id | target_id+api_asset_id | target_id+api_asset_id | N per Target | ACTIVE | MVP |
| Publication | PublicationSlot | PublicationOperation | operation_id | slot_id+principal_ref+idempotency_key | scoped idempotency fingerprint | N per Slot | HIST | MVP |
| Publication | PublicationSlot | PublicationHistory | publication_event_id | slot_id+generation+release_ref | slot_id+generation | N per Slot | HIST | MVP |
| Publication | AccessBlock | AccessBlock | block_id | product_id+scope_ref+reason | block_id+generation | N per Scope | ACTIVE | MVP |
| Publication | PublicationSlot | DeliveryState | delivery_state_id | slot_id+source_generation | slot_id+destination+source_generation | N per Slot+Destination | DERIVED | MVP |
| DeveloperAccess | DeveloperOrganization | DeveloperOrganization | developer_org_id | identity_tenant_ref | developer_org_id | N per Product through Grant | ACTIVE | V1 |
| DeveloperAccess | DeveloperOrganization | DeveloperOrgMembership | dev_member_id | developer_org_id+principal_ref | active membership rule | N per Organization | ACTIVE | V1 |
| DeveloperAccess | ConsumerApplication | ConsumerApplication | consumer_app_id | developer_org_id | developer_org_id+application_key | N per Organization | ACTIVE | V1 |
| DeveloperAccess | ConsumerApplication | OAuthClientRef | oauth_client_ref_id | consumer_app_id+issuer+client_id | issuer+client_id | 0..N per App | ACTIVE | V1 |
| DeveloperAccess | AccessGrant | AccessGrant | grant_id | consumer_app_id+product_id+audience+environment | grant_id | N per App | ACTIVE | V1 |
| PlatformOperations | Job | Job | job_id | product_scope+operation_type | job_id | N per Product | ACTIVE | MVP |
| PlatformOperations | Job | JobAttempt | job_attempt_id | job_id+fencing_token | job_id+attempt_number | N per Job | HIST | MVP |
| PlatformOperations | Outbox | OutboxEvent | outbox_event_id | event_id+aggregate_ref | event_id | N per Aggregate | HIST | MVP |
| PlatformOperations | Outbox | InboxDedup | inbox_dedup_id | subscriber_id+event_id | subscriber_id+event_id | N per Consumer | ACTIVE | MVP |
| PlatformOperations | Audit | AuditRecord | audit_id | actor_ref+scope_ref+correlation_id | audit_id | N per Scope | HIST | MVP |
| PlatformOperations | Reconciliation | Reconciliation | reconciliation_id | target_ref+source_generation | reconciliation_id | N per Target | HIST | MVP |
| PlatformOperations | Reconciliation | ProjectionCheckpoint | projection_checkpoint_id | projection_name+scope_ref | projection_name+scope_ref | 1 current per Scope | DERIVED | MVP |

CR-0001 起，`ContractRelease` 必须引用一次检查，豁免随 Release 记录；只有 CR-0001 之前准备的 Release 才有 `decision_ref`。`ReviewDecision` 和 `WaiverRef` 两行保留，对应只读的历史评审。物理实现见 §5.15。


### 5.5 Product Governance 已实现的物理模型（PR #6 / #7，2026-11-17 同步）

本节记录已合入 main 的实现事实，不替代其余逻辑模型的候选状态或正式评审。权威 DDL 为 `migrations/productgovernance/003_role_assignment_revocation.sql`、`004_list_order.sql`。

- `product_role_assignment` 的主键为 UUID `role_assignment_id`。主体始终是 `principal_issuer` 和 `principal_subject` 的组合；`role` 起初仅允许 `product_owner`，0.20.0 起还有 `security_admin` 和 `security_reviewer`（§5.10），0.28.0 起还有 `internal_auditor`（§5.12）。`role_assignment_version` 授予为 1、撤销为 2；授予/撤销人和时间保留，撤销三列必须同时为空或同时有值。有效记录的部分唯一索引允许撤销后以新 ID 再次授予。
- 表触发器只允许一次撤销，拒绝改主体、角色、产品及复活历史行。应用角色有 INSERT、SELECT、UPDATE，没有 DELETE；“至少一位负责人”由撤销用例在事务内锁住该 Product 的全部有效分配后计数保证，不是表触发器的保证。等待锁期间新增的负责人可能不被计入，保守返回 `LAST_OWNER` 后可重试。
- 登记时的首批负责人随 `product.registered` 产生，不各发授予事件；其后的授予和撤销发 `fabric.productgovernance.product_role.changed`。载荷只有分配编号、角色、是否有效及事件版本，不含主体身份；仅消费此事件时可能先看到首批负责人的版本 2 撤销，不能假定已经收到版本 1。
- 每次请求从当时有效的分配/成员关系重新授权。撤销立即影响下一次请求，不要求更换 JWT。旧版本若不识别撤销列仍会授予权限，第一次撤销前必须完成旧进程下线；回滚不能恢复这种旧授权逻辑。

迁移 004 的索引与列表顺序如下；成员和角色索引均只覆盖 `revoked_at IS NULL`，时间相同按 ID 排序，不使用 OFFSET。

| 索引 | 键顺序 / 用途 |
|---|---|
| `product_list_order` | `(created_at, product_id)`，平台管理员的产品列表 |
| `workspace_list_order` | `(product_id, created_at, workspace_id)`，产品内 Workspace 列表 |
| `workspace_membership_list_order` | `(product_id, workspace_id, granted_at, membership_id)`，有效成员列表 |
| `product_role_assignment_list_order` | `(product_id, granted_at, role_assignment_id)`，有效负责人列表 |
| `product_role_assignment_of_principal` | `(principal_issuer, principal_subject, product_id)`，从调用方有效角色找产品 |
| `workspace_membership_of_principal` | `(principal_issuer, principal_subject, product_id)`，从调用方有效成员关系找产品 |

收尾压测发现普通成员的 Workspace 末页可能扫描大量不可见记录。查询现先物化该主体在该产品内有效成员关系对应的去重 Workspace ID，再通过主键逐项读取，最后按固定顺序分页；LATERAL 内的 LIMIT 1 保持每次主键读取有界，不增加迁移或权限。独立 EXPLAIN 回归检查访问的 Workspace 行数，而非依赖易波动的耗时阈值。有权读取的集合很大时，此过滤路径仍有组装和排序可见集合的成本，不能宣称所有授权过滤列表的开销只与单页大小有关。

升级 #6 需要 `fabric-migrate -apply -grant-to <application_role>`，授予新需要的 UPDATE；#7 的索引迁移只需 `-apply`，不增加权限。索引创建会短暂阻塞写入。四类查询的分页约定见 F04 §3.1.1，权限与拒绝语义见 F05 §6.1；可复现的负载证据见 [列表压测记录](../tests/performance/README.md)。

### 5.6 已实现的受众配置和产品品牌（PR #10 / #11）

迁移 `005_product_audience_config.sql` 创建 `product_audience_config`。新产品从空配置开始。配置由 `audience_config_id` 标识，固定归属产品、受众（`public`、`partner`、`internal`）和环境（`sandbox`、`production`）。部分唯一索引保证每个 `(product_id, audience, environment)` 最多有一条 `removed_at IS NULL` 的配置。有效配置按 `(product_id, created_at, audience_config_id)` 索引分页。

配置从版本 1 开始，可更新或清除 `docs_url`。移除记录时间和主体并增加版本，历史行由触发器保持只读；重新配置相同组合生成新 ID。应用角色持有该表的 `SELECT`、`INSERT`、`UPDATE`，触发器限定更新为变更文档入口或结束配置。

迁移 `006_product_brand.sql` 将品牌作为 Product 的值对象，字段为 `brand_name`、`brand_logo_url`、`brand_primary_color`，共用 Product 的 `aggregate_version`。新登记的品牌名称取产品展示名。旧行的空品牌名按展示名读取，空更新主体按登记主体读取。应用角色对 `product` 的 `UPDATE` 由触发器限定为变更品牌及相应版本、更新主体和时间；表属主仍可单独调整 `status`。

四个写用例在事务内通过 `LockHeldRoles` 对调用方的当前角色分配加共享锁，使写入和负责人撤销按锁顺序生效。变更和移除配置时锁定配置行，变更品牌时锁定 Product 行。实际变更、成功审计和 Outbox 事件一起提交。权限见 F05 §6.3，条件更新见 F04 §11。升级时执行 `fabric-migrate -apply -grant-to <application_role>`，应用迁移并补授应用角色权限。

### 5.7 已实现的 API 目录和契约修订

本节记录 PR #12、#14–#17 以及随后的 Git 来源和原文读取（管理 API 0.8.0–0.12.0）的实现事实，不替代 §5.4 逻辑模型的候选状态。权威 DDL 为 `migrations/apicatalog/001_api_asset.sql`、`migrations/contractregistry/001_uploaded_contract_revision.sql` 和 `002_git_origin.sql`。接口见 F04 §11.3，权限见 F05 §6.4。

**API 资产。** `apicatalog.api_asset` 以 UUID `api_asset_id` 为主键。`product_id` 和 `workspace_id` 是对 Product Governance 的引用，不设跨 schema 外键，登记时在同一事务内向 Product Governance 核对。资产归属的 Workspace 就是它的所有者，登记后不变。`asset_key` 在一个产品内唯一（跨 Workspace），不同产品可以重复；`asset_key` 与 `domain_key` 的格式都是 `^[a-z][a-z0-9-]{0,61}[a-z0-9]$`，`display_name` 为 1–120 个字符，`api_kind` 为 `HTTP` 或 `EVENT`。`lifecycle` 登记时为 `EXPERIMENTAL`；表约束还允许 `STABLE`、`DEPRECATED`、`RETIRED`，但目前没有改变生命周期的用例。索引 `api_asset_product_order (product_id, created_at, api_asset_id)` 和 `api_asset_workspace_order (product_id, workspace_id, created_at, api_asset_id)` 支撑列表。应用角色只有 `SELECT`、`INSERT`。

登记资产时，事务内对调用方在目标 Workspace 的成员关系加共享锁，再写入资产、成功审计和 Outbox 事件 `fabric.apicatalog.asset.registered`（版本 1），三者一起提交。事件载荷是事件编号、时间、关联编号、产品、Workspace、资产编号和聚合版本，不含 key 和名称。逻辑模型中的 `AssetOwnership`、`AssetLifecycleTransition` 尚未实现。

**来源快照和修订。** 三张表的内容都只增不改，应用角色没有 `DELETE`。评审加入之后（§5.9），应用角色对 `contract_revision` 多了 `UPDATE`，但表上的触发器只允许改修订的评审状态，其余各列和另外两张表仍然不能改。0.37.0 起程序不再改评审状态，此后的构建在授权时收回这项 `UPDATE`，修订只登记和读取（§5.15）。

| 表 | 内容 | 关键约束 |
|---|---|---|
| `contractregistry.source_snapshot` | 一次导入的证据：`source_kind`（`UPLOAD` 或 `GIT`）、入口路径、入口文件摘要 `source_digest`、闭包摘要 `ref_closure_digest`、闭包清单 `ref_closure_manifest`、采集时间和采集主体，以及 Git 来源的 `origin_repository`、`origin_commit`、`origin_ref` | `GIT` 必有仓库和提交，`UPLOAD` 的三列全为空；仓库、提交、引用各有格式和长度约束 |
| `contractregistry.source_file` | 快照的文件，按 `(source_snapshot_id, path)` 存原始字节 | 路径 1–200 个字符，内容非空 |
| `contractregistry.contract_revision` | 修订：所属产品、Workspace、资产，`revision_sequence`，`spec_version`，`status` | 一个快照只对应一个修订；序号在资产内从 1 递增且唯一；`status` 登记时为 `DRAFT`，之后随评审流转（§5.9） |

闭包清单是入口路径加上每个文件的路径和 SHA-256，文件按路径的字节顺序排列；`ref_closure_digest` 是清单的 SHA-256。逻辑模型里的 `RefClosureManifest` 在物理上是快照的一列。逻辑模型里的 `ContractSource`（来源登记）尚未实现：现在的“来源”指一个资产的全部上传，或者一个 Git 仓库网址。

采集只在提交的文件之内进行，不访问文件系统和网络。入口必须是 OpenAPI 3.1.2，文件为 UTF-8 的 YAML 或 JSON，最多 16 个、解码后合计 40960 字节。只解析包内的相对 JSON Pointer 引用；远程引用、越出包的路径、循环引用、锚点、YAML 别名、重复键、多文档文件和 `$id` 都被拒绝。没有被引用到的文件不进入闭包，也不保存。

**去重和 Git 来源。** 相同闭包在同一个来源下只登记一次：再次提交返回最先登记的那个修订。去重和“一个提交只有一份内容”由下面三个部分唯一索引保证；登记时还对资产加事务级咨询锁，使查重和序号分配按顺序进行。

| 索引 | 保证 |
|---|---|
| `source_snapshot_upload_closure_key` | 一个资产的上传中，同一闭包只有一个快照 |
| `source_snapshot_git_closure_key` | 一个资产来自同一仓库的快照中，同一闭包只有一个 |
| `source_snapshot_git_commit_key` | 同一仓库、同一提交、同一入口只有一个快照 |

Git 来源是提交者的声明：仓库的 https 网址（至多 300 个字符，不含凭证）、完整的提交名（40 或 64 位十六进制），以及可选的引用名（`refs/…`，至多 200 个字符）。Fabric 原样记录这三项和声明者，不读取仓库，所以它是“谁在何时说这些文件来自哪里”的证据，不是对提交内容的证明。仓库按写法比较，带不带 `.git` 算两个仓库。来自同一仓库的相同闭包，即使出自更晚的提交，也返回最先登记的修订和它当时的来源。同一仓库、提交和入口如果带着另一份闭包再来，会被拒绝（提交不会变，两次声明必有一次是错的）。手工上传和 Git 导入的相同内容是两个来源下的两个修订。

修订、快照、文件、成功审计和 Outbox 事件 `fabric.contractregistry.revision.registered`（版本 1）在一个事务内提交。事件载荷是事件编号、时间、关联编号、产品、资产、修订编号、序号和两个摘要，不含来源、路径和原文。

**读取原文。** 文件通过修订读取，查询条件同时包含产品、资产、Workspace 和修订编号，不接受单独的快照编号。返回之前，按存储的文件重新计算入口摘要和闭包摘要，并与修订记录的摘要比对；文件被改动、改名、增减，或摘要对不上，都不返回文件。摘要和文件存在同一个数据库里，所以这项核对能发现存储丢失或改动了文件，不能防住同时改写文件和摘要的人。

**升级。** 从 0.7.0 之后升级，执行 `fabric-migrate -apply -grant-to <application_role>`：应用上述迁移，并授予应用角色这四张表的权限。读取原文需要 `source_file` 的 `SELECT`，这项授权没有对应的迁移步骤，缺少时新进程拒绝启动并指出缺少的权限。迁移 002 把已有的快照保留为 `UPLOAD`。旧进程在迁移后的库上仍能登记上传，但它不认识 Git 来源，按内容查重时也不区分来源；过渡期内，一次与某个 Git 修订内容相同的上传可能得到那个 Git 修订。应在所有进程升级之后再开始带 Git 来源导入。

**尚未实现。** 来源登记（每个资产允许的仓库和引用策略）、CI 服务身份、由 Fabric 读取 Git（FR-021，V1）、按提交查找修订，以及记录相同内容在后续提交中的出现。检查见 §5.8，修订的状态流转和评审见 §5.9；0.37.0 起修订不再流转，见 §5.15。

### 5.8 已实现的契约检查

本节记录 PR #22–#33（管理 API 0.13.0–0.16.0）的实现事实，不替代 §5.4 逻辑模型的候选状态。权威 DDL 为 `migrations/policyreview/001_check_run.sql` 和 `002_check_compatibility.sql`。接口见 F04 §11.4，权限见 F05 §6.5。

一次检查（CheckRun）记下读了什么、按哪份策略、由哪些工具、对每条规则得出什么结论。它写入一次，之后不变；再检查一次是另一条记录。0.36.0 之前，提交评审和批准都以一次检查为依据（§5.9）：检查不是按现行策略和工具版本做的，或者其中有不可豁免的规则没有通过，提交和批准会被拒绝。0.37.0 起 Release 直接依据一次检查准备，同样的要求在准备 Release 时核对（§5.15）。Policy & Review 的表只通过 Contract Registry 和 API Catalog 签发的编号指向修订和资产，不设跨 schema 外键。

| 表 | 内容 | 关键约束 |
|---|---|---|
| `policyreview.policy_snapshot` | 检查所依据的策略的原文：`policy_set_id`、`policy_digest`、`policy_document` 和记录时间 | 一个产品对一份策略只有一个快照（`(product_id, policy_digest)` 唯一）；表约束要求 `policy_digest` 等于文档的 SHA-256 |
| `policyreview.check_run` | 一次检查：所属产品、Workspace、资产和修订，策略快照，`input_digest`（修订的 `ref_closure_digest`），`tool_manifest` 和 `tool_digest`，总状态，起止时间和发起人；带基线时还有 `baseline_revision_id` 和 `compatibility_document` | 总状态只能是 `PASS`、`WARN`、`FAIL`、`UNKNOWN`；`tool_digest` 等于工具清单的 SHA-256；结束不早于开始；基线和比较文档同有同无，基线不能是被检查的修订本身；唯一约束 `check_run_once_key` 见下 |
| `policyreview.check_rule_outcome` | 策略里的每条规则一行：状态、发现总数、评估它的工具和版本、规则在策略里的位置 | 状态是 `PASS`、`WARN`、`FAIL`、`UNKNOWN`、`NOT_RUN` 之一；`NOT_RUN` 当且仅当没有工具；`PASS` 和 `NOT_RUN` 没有发现，`FAIL` 和 `WARN` 至少有一个 |
| `policyreview.check_finding` | 发现：规则、级别（`BLOCK` 或 `WARN`）、文件路径、可选的 JSON Pointer 和行号、说明 | 说明 1–300 个字符，路径 1–200 个字符，指针至多 1024 个字符；外键保证发现属于本次检查的某条规则 |

四张表都只增不改，应用角色只有 `SELECT` 和 `INSERT`。索引 `check_run_history (product_id, api_asset_id, revision_id, completed_at, check_run_id)` 支撑检查历史。

**策略。** 目前只有一份策略：全局基线 `fabric-global-contract-governance`。它取 `governance/policies/F04_Rules_Registry.json` 里阶段为 MVP 的 23 条规则（V1 的 5 条不在内），写在代码里，由测试保证与登记表逐项一致。策略文档是规则集名称加上按编号排序的规则的规范化 JSON，`policy_digest` 是它的 SHA-256；规则有任何改动就是另一份策略，也就是另一个快照。产品还不能追加自己的规则（FR-026）。

**工具和状态。** 工具清单是按名称排序的 `[{name, version}]` 的 JSON，`tool_digest` 是它的 SHA-256。现在有两个内置工具：`fabric-openapi-rules`（版本 3）评估 F04-GOV-001 到 012；`fabric-openapi-compatibility`（版本 1）只在检查指定了基线修订时参与，评估 F04-GOV-013。其余 10 条规则（F04-GOV-014 到 020、025 到 027）没有工具评估，每次检查都如实记为 `NOT_RUN`。工具对同样的文件会得出不同结论的任何改动，都要换版本号。

一条规则的状态：有阻断级的发现是 `FAIL`；判断不了是 `UNKNOWN`；只有警告级的发现是 `WARN`；评估过且没有发现是 `PASS`；没有工具评估是 `NOT_RUN`。一次检查的总状态取风险上界，顺序是 `FAIL`、`UNKNOWN`、`WARN`、`PASS`：只要有规则没评估或判断不了，总状态就不会好于 `UNKNOWN`（F04 §8）。所以在那 10 条规则有工具之前，总状态只会是 `FAIL` 或 `UNKNOWN`，不会出现 `PASS` 或 `WARN`。

每条规则最多保留 50 个发现，规则的发现总数记全部。发现的说明由工具写成一行，说哪里不对、怎么改，不复述命中的内容：发现凭证时只说那一行有凭证形式的内容和它的种类。

**同一组输入只记一次。** 同一修订、同一策略快照、同一组工具、同一基线（或都没有基线）只有一条检查：唯一约束 `check_run_once_key (revision_id, policy_snapshot_id, tool_digest, baseline_revision_id)`，没有基线的按相同处理（`NULLS NOT DISTINCT`）。记录之前按修订加事务级咨询锁，同时到达的请求一个一个判断，后到的得到已经记录的那一次。策略、工具版本或基线有一样不同，就是另一次检查。

**基线比较。** 指定基线时，检查读取同一资产的另一个修订的原文，由比较器给两者的差异分类。结果包括基线修订和它的输入摘要、比较器和版本、总分类、至多 100 条差异以及是否截断，作为规范化 JSON 存在 `compatibility_document` 里，是规则 F04-GOV-013 的证据。分类规则见 F04 §11.4。

**读取时的核对。** 读出一次检查时重新核对：策略文档与摘要是否相符，工具清单是否是规范写法，每条规则是否都有结论，结论与规则的级别和发现数是否相符，总状态是否等于各规则结论的汇总，比较文档与规则 F04-GOV-013 的结论是否相符。此外，检查记录的 `input_digest` 必须等于修订现在的闭包摘要；带基线的，基线修订现在的摘要必须等于当时记录的。任何一项不符，都不返回这次检查。这些核对查的是各行是否自洽、是否仍对应那个修订。数据都在同一个数据库里，能把相关的行一起改写的人不会被发现，所以它不是防篡改证明。

**事务和事件。** 策略快照、检查、各规则的结论、发现、成功审计和 Outbox 事件 `fabric.policyreview.check_run.completed`（版本 1）在一个事务内提交，任何一步失败都不留下任何一行。事件载荷是事件编号、时间、关联编号、产品、资产、修订、检查编号（聚合编号，聚合版本恒为 1）、输入摘要、策略摘要和总状态，不含规则结论和发现。返回已经记录的检查时不写审计，也不发事件。

**与逻辑模型的对应。** §5.4 的 `PolicySnapshot` 和 `CheckRun` 有了物理表；`ReviewDecision` 和 `WaiverRef` 的物理表见 §5.9。§9 的 `RequestCheckRun` 设想由 Worker 异步执行并写出报告制品；现在检查在请求内同步完成，原文通过 Contract Registry 读取（读出时已核对摘要，见 §5.7），不访问文件系统和网络，没有排队、重试和报告制品。检查也不绑定受众和环境。

**升级。** 从 0.12.0 升级，执行 `fabric-migrate -apply -grant-to <application_role>`：应用上述两个迁移，并授予应用角色 `policyreview` 的 `USAGE` 和四张表的 `SELECT`、`INSERT`。库没有迁移时，新进程拒绝启动并指出缺少的表；迁移了但没有授权时，拒绝启动并列出缺少的权限。0.12.0 的进程在迁移后的库上照常运行，只是没有检查接口。迁移 002 的唯一约束用到 `NULLS NOT DISTINCT`，需要 PostgreSQL 15 或更高版本。

**尚未实现。** 产品级策略（FR-026）、其余 10 条规则的工具、导入后自动检查、由 Worker 异步执行、检查与受众和环境的绑定，以及 CI 服务身份读取检查。评审和豁免见 §5.9，0.37.0 起的豁免见 §5.15。

### 5.9 已实现的契约评审

> 0.37.0 起（CR-0001）不再提交、撤回和决定评审，`internal_reviewer` 不再授予，修订的状态不再转移。本节保留为 0.17.0–0.36.0 的记录，现状见 §5.15。

本节记录管理 API 0.17.0–0.19.0 的实现事实，不替代 §5.4 逻辑模型的候选状态。权威 DDL 为 `migrations/productgovernance/007_reviewer_role.sql`、`migrations/contractregistry/003_revision_review_state.sql`、`migrations/policyreview/003_review.sql` 和 `004_review_decision.sql`。接口见 F04 §11.5，权限见 F05 §6.6。

**三个部分。** 评审人是 Workspace 的一种成员关系（Product Governance）。修订的评审状态归 Contract Registry，和修订在同一行。评审的申请、结论和豁免归 Policy & Review，只通过编号指向修订和检查，不设跨 schema 外键。一次提交、撤回或决定同时改这两边，在同一个数据库事务里完成。

**评审人角色。** `workspace_membership.role` 增加 `internal_reviewer`（迁移 007 放宽表约束 `workspace_membership_role_known`）。一个主体可以在同一个 Workspace 同时持有作者和评审人两种成员关系，各是一条记录。评审人能读这个 Workspace 的资产的修订、原文、检查和比较，不能导入修订，也不能发起检查。

**修订的评审状态。** 迁移 `contractregistry/003` 给 `contract_revision` 增加 `status_version`（从 1 开始），并把 `status` 的取值扩大到五种。已有的修订保持 `DRAFT`、版本 1。状态怎么走由表上的两个触发器决定，对任何写入者都一样，包括表的所有者：

| 触发器 | 保证 |
|---|---|
| `contract_revision_begins_as_draft`（插入前） | 修订登记时只能是 `DRAFT`、版本 1 |
| `contract_revision_only_moves`（更新前） | 除 `status` 和 `status_version` 以外的列都不能改；每次更新版本恰好加 1；只允许下表里的转换 |

| 从 → 到 | 由谁触发 |
|---|---|
| `DRAFT` → `IN_REVIEW` | 作者提交评审 |
| `WITHDRAWN` → `IN_REVIEW` | 作者再次提交 |
| `IN_REVIEW` → `WITHDRAWN` | 申请人撤回 |
| `IN_REVIEW` → `APPROVED` | 评审人批准 |
| `IN_REVIEW` → `REJECTED` | 评审人驳回 |

`APPROVED` 和 `REJECTED` 是终点，没有离开它们的转换。违反时数据库报完整性错误（SQLSTATE 23000）。应用在改状态前锁住修订行，按读到的版本做条件更新（§4 INV-04 的状态 CAS）；同时到达的几个请求一个一个判断，后到的看到的是已经改过的状态。

修订的内容不随状态变化：读到的摘要、原文和来源在每个状态下都相同。相同内容再次导入，得到的还是这个修订，带着它现在的状态。

**与 §8.1 的差别。** §8.1 没有 `WITHDRAWN` → `IN_REVIEW`。实现加上它，是因为相同内容在一个来源下只登记一次（§5.7）：如果撤回是终点，那份内容就再也不能送审。`REJECTED` 仍是终点，与 §8.1 一致：被驳回的内容要改过之后作为新修订再提交，原样重交得到的还是被驳回的那个修订。§8.1 里“具名治理员”撤回还没有，只有申请人能撤回。这一处差别待裁决。

**评审的表。** 四张表都只增不改，应用角色只有 `SELECT` 和 `INSERT`。

| 表 | 内容 | 关键约束 |
|---|---|---|
| `policyreview.review_request` | 一次评审申请：所属产品、Workspace、资产和修订，依据的检查 `check_run_id` 和它读的内容 `input_digest`，可选的说明，申请时间和申请人，以及 `revision_status_version` | 外键 `review_request_check_fkey (revision_id, check_run_id)` 保证检查是这个修订的；`revision_status_version` 至少是 2；唯一约束 `review_request_once_key (revision_id, revision_status_version)` 见下；说明 1–500 个字符 |
| `policyreview.review_scope` | 申请批准的范围：受众和环境的组合，每个一行 | 受众是 `public`、`partner`、`internal` 之一，环境是 `sandbox`、`production` 之一；同一申请内不重复 |
| `policyreview.review_conclusion` | 评审怎么结束：`outcome`、可选的理由、结束时间、结束它的人，批准时还有 `valid_until` | 一个申请至多一个结论（主键是申请编号）；`outcome` 是 `WITHDRAWN`、`APPROVED`、`REJECTED` 之一；只有批准有 `valid_until`，且晚于结束时间；驳回必须有理由；理由 1–1000 个字符 |
| `policyreview.review_waiver` | 批准时放行的规则：规则编号、理由、可选的补偿措施、到期时间 | 外键指向结论，所以没有结论就没有豁免；同一结论内一条规则只豁免一次；理由和补偿措施各 1–1000 个字符 |

索引 `review_request_history (product_id, api_asset_id, revision_id, requested_at, review_id)` 支撑评审历史。为了让外键 `review_request_check_fkey` 成立，迁移 `policyreview/003` 给 `check_run` 加了唯一约束 `check_run_of_revision_key (revision_id, check_run_id)`。

**一个修订同时只有一个进行中的评审。** `revision_status_version` 是这次申请把修订改成 `IN_REVIEW` 时修订得到的状态版本。Contract Registry 每个版本只给一次，所以 `review_request_once_key` 保证同一次“进入评审”只对应一条申请。撤回后再提交是下一个版本，也就是这个修订的下一条评审记录。0.23.0 起，这个约束只管让修订进入评审的申请；已批准的修订再次评审（复审）不改变修订的状态，做法见 §5.11。

**提交。** 申请以修订的一次检查为依据。检查必须是这个修订的、读的就是修订现在的内容，并且是“现行”的：它的策略摘要等于现在的基线策略，它用的每个工具都是现在的工具版本（§5.8）。不是现行的检查被拒绝，要重新检查。检查里有不可豁免的规则是 `FAIL` 或 `UNKNOWN` 时也被拒绝：没有任何决定能批准这样的修订。范围是 1 到 6 个受众和环境的组合，按受众、环境的顺序保存。

**决定。** 评审人批准或驳回一条进行中的申请。批准再次要求检查是现行的；检查里每条可豁免而没通过（`FAIL` 或 `UNKNOWN`）的规则都要逐条豁免，写明理由和到期时间，可以写补偿措施。不可豁免的规则不能豁免，通过了的规则也不能豁免。`WARN` 和 `NOT_RUN` 的规则不需要豁免：批准绑定在那次检查上，检查如实记着哪些规则没有评估，Release 要不要拦下它们由 Release 决定。批准有到期时间 `valid_until`，最长 365 天；豁免的到期时间不晚于它。驳回必须写理由，没有到期时间和豁免。

批准的对象是那次检查读的内容（`input_digest`）、申请的范围和到期之前的这段时间，不比这更宽（§4 INV-07）。一次批准的范围不能事后追加。0.23.0 起，已批准的修订可以再经一次评审，取得范围或有效期不同的另一次批准（复审，§5.11）。

**职责分离。** 决定的人不能是登记这个修订的人、发起那次检查的人，或提交这次申请的人，即使他持有评审人角色。“登记修订的人”是最先提交这份内容的人（来源快照的采集主体）；别人后来再导入相同内容，不改变这一点。比较的是主体（签发方加主体标识）：同一个人用两个账号，这里发现不了。

**读取时的核对。** 读出评审时核对：申请写明了修订、检查和申请人，范围是合法且有序的；结论属于这条申请、不早于申请；批准有晚于结束时间的 `valid_until`，豁免按规则编号有序、有理由、到期时间在批准的有效期内；驳回有理由；驳回和撤回没有 `valid_until` 和豁免。申请记录的 `input_digest` 必须等于修订现在的闭包摘要。任何一项不符，都不返回这条评审。与检查一样，数据同库，这不是防篡改证明。

**事务和事件。** 提交时，申请、范围、修订状态的变化、成功审计和 Outbox 事件一起提交；撤回和决定时，结论、豁免、修订状态的变化、成功审计和事件一起提交。任何一步失败都不留下任何一行，修订的状态也不变。

| 事件（版本 1） | 何时 | 载荷 |
|---|---|---|
| `fabric.policyreview.review.requested` | 提交评审 | 事件编号、时间、关联编号、产品、资产、修订、评审编号（聚合编号，聚合版本 1）、检查编号、输入摘要 |
| `fabric.policyreview.review.withdrawn` | 撤回 | 同上，聚合版本 2，`outcome` 为 `WITHDRAWN` |
| `fabric.policyreview.review.decided` | 批准或驳回 | 同上，聚合版本 2，`outcome` 为 `APPROVED` 或 `REJECTED`；批准时带 `valid_until` |

事件不含说明、理由、范围和豁免，这些要通过评审接口读取。修订状态的变化没有单独的事件。§12.2 的 `ReviewDecisionRecorded` 对应 `review.decided`；它设想的载荷里有 `target_scope`，现在没有。

**与逻辑模型的对应。** §5.4 的 `ReviewDecision` 在物理上分成申请和结论两张表，`decision_id` 就是 `review_id`；`WaiverRef` 是 `review_waiver`，主键是 `(review_id, rule_id)`，没有单独的 `waiver_ref_id`。逻辑模型没有“评审申请”这个实体，也没有“撤回”这种结论。F04 §7.2 要求豁免绑定 `(rule_id, scope, subject_digest, owner, reviewer, expires_at, reason, compensating_control)`：范围、内容摘要和评审人取自豁免所属的批准，豁免没有单独的 `owner`。

**升级。** 从 0.16.0 升级，执行 `fabric-migrate -apply -grant-to <application_role>`：应用上述四个迁移，并授予应用角色 `contract_revision` 的 `UPDATE` 以及四张评审表的 `SELECT`、`INSERT`。库没有迁移时，新进程拒绝启动并指出缺少的表；迁移了但没有授权时，拒绝启动并列出缺少的权限。旧进程在迁移后的库上照常登记修订，登记的仍是 `DRAFT`、版本 1。但旧进程不认识新的取值，用真实进程走查的结果是：

| 旧进程 | 遇到 | 表现 |
|---|---|---|
| 0.16.0 | Workspace 里有评审人 | 列出这个 Workspace 的成员、读取评审人的那条成员关系，返回 500：它不发送自己的契约没有声明的角色。其他操作照常，作者照常导入和读取 |
| 0.16.0 | 评审人来读修订 | 403：它只认作者 |
| 0.16.0 | 修订不是 `DRAFT` | 照常返回 200，`status` 是它的契约没有声明的值；相同内容再导入，返回原修订和它现在的状态 |
| 0.18.0 | 评审已经批准或驳回 | 读这条评审、列出这个修订的评审，返回 500：它把不认识的结论当作损坏的记录。进行中和已撤回的评审照常读，也照常撤回 |

所以应在所有进程升级之后，再授予评审人角色、提交评审和作出决定。

指派评审人、具名治理员撤回和产品级策略仍待实现。追加审批范围和批准失效后的复审在 0.23.0 实现，见 §5.11。0.21.0 起，准备 Release 和发布时会重新核对批准：策略、评审人的授权、批准和豁免的有效期，见 §5.10。

### 5.10 已实现的 Release 与发布

> 0.37.0 起 Release 依据检查准备，激活和恢复不再核对批准，也不要求发布人独立（CR-0001），见 §5.15。下文关于批准的内容是 0.20.0–0.36.0 的记录。

本节记录管理 API 0.20.0–0.22.0 的实现事实，不替代 §5.4 逻辑模型的候选状态。0.33.0 之后的内部制品读取和槽位恢复见 §5.14。权威 DDL 为 `migrations/productgovernance/008_publication_roles.sql`、`migrations/contractregistry/004_immutable_release.sql` 和 `migrations/publication/001_targets_and_slots.sql`。接口见 F04 §11.6，权限见 F05 §6.7。

**角色。** 迁移 008 让成员关系可以取 `internal_publisher`（Workspace 的发布人），让产品角色可以取 `security_admin` 和 `security_reviewer`。三种角色都由产品负责人授予；平台管理员仍然只能任命负责人。

**Release。** Release 是一个已批准的修订连同它的证据，记录一次，之后不变。两张表都只增不改，应用角色只有 `SELECT` 和 `INSERT`。

| 表 | 内容 | 关键约束 |
|---|---|---|
| `contractregistry.release_artifact` | 制品：`artifact_digest` 和 `payload` | 表约束要求摘要等于内容的 SHA-256；内容 1–65536 字节 |
| `contractregistry.contract_release` | Release：所属产品、Workspace、资产和修订，评审、检查、策略快照及其摘要，输入摘要，制品摘要，版本号，准备时间和准备人 | `(product_id, api_asset_id, release_version)` 唯一；外键指向制品和修订，修订表为此加了唯一约束 `contract_revision_release_scope_key` |

制品是一份 JSON，格式名 `fabric-openapi-source/v1`：入口路径、规范版本、闭包摘要，以及按路径排序的每个文件的路径、SHA-256 和 Base64 内容。它从登记的原文生成，生成前按 §5.7 的方式核对摘要。字节相同的制品只存一份，可以被多个 Release 引用。登记时允许的最大源文件（16 个文件、共 40 KiB、路径各 200 个字符）生成的制品不超过 65536 字节的上限。版本号在一个资产内唯一：同一版本号、同一修订和评审再次提交，返回已有的 Release；换了修订或评审则拒绝。记录下来的 Release 都是 `READY`，§8.2 的其余状态尚未实现。索引 `contract_release_history (product_id, api_asset_id, prepared_at, release_id)` 支撑 Release 历史。

**使用批准时的重新核对。** 0.22.0 在准备 Release、激活、恢复和读取已发布制品时都重新核对批准。核对项包括修订和内容是否匹配、批准和豁免是否有效、检查是否使用现行策略和工具，以及批准人当时使用的评审人成员关系是否仍有效。重新授予评审人角色后，旧批准仍失效。发布人须独立于导入、检查发起、评审提交和批准。发布时还要核对批准范围。用另一修订的 Release 替换槽位上最近生效的 Release 时，批准依据的检查须以那个修订为基线。只有属于“在检查之外把关”的阻断级规则允许没有工具评估。这对应 INV-07。0.33.0 起内部恢复和制品读取遵循 §5.14。

**发布。** 四张表：

| 表 | 内容 | 关键约束 |
|---|---|---|
| `publication.target` | 发布目标：产品、受众配置、门户标识，登记时的受众和环境，`block_generation` | `(product_id, audience_config_id, portal_key)` 唯一；更新只能把 `block_generation` 加 1（触发器） |
| `publication.slot` | 槽位，即目标 × 资产：`active_release_id` 和 `generation` | `(target_id, api_asset_id)` 唯一；代次为 0 时没有生效的 Release；更新只能把代次加 1 并换指针（触发器） |
| `publication.operation` | 槽位上的每一次操作：代次、动作、之前和之后的 Release、理由、操作人、时间和幂等键的摘要 | `(slot_id, generation)` 和 `(slot_id, key_digest)` 唯一；`WITHDRAW` 没有 Release，另两种必有；`WITHDRAW` 和 `RESTORE` 必有理由 |
| `publication.access_block` | 访问阻断：目标，可选的资产和 Release，代次、理由、建立人和时间，以及解除的代次、理由、解除人和时间 | 同一范围同时只有一个未解除的阻断（部分唯一索引）；只能解除一次，解除人不能是建立人（触发器） |

应用角色对 `operation` 只有 `SELECT` 和 `INSERT`，对另外三张表多一项 `UPDATE`，由触发器限定为上面说的那一步。槽位在第一次激活时建立，代次从 0 开始，每次操作加 1（§4.2 的 Slot = Target × APIAsset）。

**CAS 和幂等。** 改变槽位的请求带上它依据的代次和一个幂等键。事务内先对目标加共享锁、对槽位加排他锁；代次不是当前的就拒绝。同一槽位上同一个键再次到达时，调用人、代次、动作、Release 和理由都相同就返回当时的操作，有一样不同就拒绝。`RESTORE` 只能回到这个槽位上生效过的 Release。槽位、操作记录、审计和 Outbox 事件同事务提交。

**阻断。** 阻断可以覆盖整个目标、目标上的一个资产，或其中一个 Release。它独立于槽位的指针：建立阻断不改变槽位，但被覆盖的制品经槽位读不到（0.27.0–0.37.0，按编号下载 Release 不看阻断；0.38.0 起按 CR-0002，范围是这个 Release 的阻断在任一目标上未解除时，按编号下载也被拒绝；见 §5.12），也不能激活或恢复。解除之前，被覆盖的槽位必须已经撤销，所以解除阻断本身不会让任何内容重新可见；恢复是另一次带代次的操作，会重新核对批准。建立和解除各使目标的 `block_generation` 加 1。

**读取已发布的制品。** 只通过槽位当前的指针读取（0.27.0 起，Release 的制品另可按编号下载，那条路径不做下面的核对，0.38.0 起只核对这个 Release 在任一目标上的阻断，见 §5.12）。每次读取都在事务内重新核对：调用人的成员关系、受众配置仍然有效且与目标登记时一致、指针、阻断和批准；任何一项不满足都不返回制品。读出的字节与制品摘要再核对一次。目前只有 `internal` 受众的目标可以激活和读取；对外门户的授权和边缘分发尚未接入，所以槽位的交付状态和阻断的边缘状态都如实报告为 `UNKNOWN`。

**事件。** `fabric.contractregistry.release.prepared`，以及 `fabric.publication.target.created`、`slot.activated`、`slot.withdrawn`、`slot.restored`、`access_block.created`、`access_block.cleared`，都是版本 1。载荷是事件编号、时间、关联编号、产品、聚合编号和版本，加上相关的目标、资产、Release 或修订的编号和制品摘要，不含理由和制品内容。

**升级。** 从 0.19.0 升级，执行 `fabric-migrate -apply -grant-to <application_role>`：应用上述三个迁移，并授予应用角色新表的权限。库没有迁移时，新进程拒绝启动并指出缺少的表；迁移了但没有授权时，拒绝启动并列出缺少的权限。0.19.0 的进程在迁移后的库上能继续读写原有的内容，但它不认识新角色：一个 Workspace 有了发布人，它列这个 Workspace 的成员会返回 500；产品有了安全角色，它列产品角色会返回 500。所以应在所有进程升级之后再授予新角色。

**尚未实现。** `internal` 以外受众的发布、外部授权和边缘分发及其传播证明；Release 的其余状态和撤销；对象存储（制品目前存在 PostgreSQL 里）；发布目标的修改和停用；紧急撤销的分级（F05 §9.1）；Outbox 的投递。

### 5.11 已实现的复审与批准状态的可见性

> 0.37.0 起没有复审，槽位不再有 `approval_problem`（CR-0001，§5.15）。本节保留为 0.23.0–0.36.0 的记录。

本节记录管理 API 0.23.0–0.25.0 的实现事实，不替代 §5.4 逻辑模型的候选状态。权威 DDL 为 `migrations/policyreview/005_supplementary_review.sql`。接口见 F04 §11.7，权限见 F05 §6.8。

**复审的起点。** 在 0.22.0，批准到期、批准人的评审人关系被撤销、策略或检查工具更新都会使批准失效，进而影响准备、激活、恢复和制品读取。F04 §7.2、§8.1 和 §9 规定重新评估或补充审批。0.23.0 起，作者可以直接为同一份已批准内容提交复审。0.33.0 起，批准有效性继续约束准备和激活，内部恢复和当前制品读取遵循 §5.14。

**复审。** 已批准的修订可以再次提交评审，这样的评审叫复审，申请上记着 `supplementary` 为真。它与第一次评审用同样的申请、范围、结论和豁免，规则也相同（§5.9）：作者提交，依据一次现行的检查，写明范围；申请人可以撤回；没有参与的评审人批准或驳回。不同之处只有一点：**复审不改变修订的状态。** 修订在复审进行期间、批准之后、驳回之后、撤回之后都是 `APPROVED`，状态版本也不变。所以：

- 之前的批准不受影响，各自按自己的条件成立或失效；依据它们的 Release 和已发布的制品在复审期间照常可用。
- 复审批准后，修订多一次批准，有它自己的范围、到期时间和豁免。一个修订可以同时有几次批准，这就是 §8.1 所说的“批准的最终权威是 `ReviewDecision`，Revision 上的 `APPROVED` 是关联状态”。
- 复审驳回或撤回，不收回任何已有的批准。
- `REJECTED` 的修订仍然不能再提交，要改内容。

| 表 | 变化 | 说明 |
|---|---|---|
| `policyreview.review_request` | 新增列 `supplementary boolean NOT NULL DEFAULT false` | 已有的申请都是 `false`；旧进程不写这一列，它记的申请也是 `false` |
| `policyreview.review_request` | 唯一约束 `review_request_once_key` 改为同名的部分唯一索引 `(revision_id, revision_status_version) WHERE NOT supplementary` | 让修订进入评审的申请，仍然是每个状态版本一条；复审的申请记的是已批准修订当时的状态版本，这个版本不随复审变化，所以同一个修订的各次复审共用它 |

没有新表，应用角色不需要新的权限：申请和结论仍然只增不改。

**同时只有一个评审。** 第一次评审靠修订的状态机保证这一点。复审不动状态，改由用例保证：提交、撤回和决定都先锁住修订这一行（Contract Registry 的 `HoldRevision`，对修订行加 `FOR UPDATE`，不改任何内容）。提交时，锁住之后再看这个修订有没有尚未结束的评审，有就拒绝；撤回和决定时，锁住之后重读这条评审，已经有结论就拒绝。所以同时到达的几个申请依次判断，只有一个成功；同时到达的几个决定只有一个留下结论。这一条是应用层的保证，不是表约束：表区分不了“已结束”和“未结束”的申请，直接写库可以给一个修订插入两条未结束的复审。

修订的状态机和它的触发器（§5.9）没有变化。考虑过又放弃的两种做法：给修订增加一个“复审中”的状态，旧进程不认识，复审期间会把已发布的内容当作未批准；让状态机接受 `APPROVED → APPROVED` 只加版本，状态版本的含义就不再是“状态变了一次”。

**事件。** 复审的三种事件与 §5.9 相同，载荷多一个成员 `supplementary: true`，读事件的人据此知道修订的状态没有随它变化。其他评审的事件没有这个成员，与之前逐字节相同。

**批准失效之后怎么恢复。** 不改已有的 Release，也不让它“复活”：

1. 作者对这个修订重新发起检查。检查按现行策略和工具记录，与之前的检查并存（§5.8）。
2. 作者依据新的检查提交复审，写明要批准的范围。
3. 没有参与的评审人批准。
4. 发布人依据这次批准，用一个新的版本号准备 Release。内容没变，制品的字节和摘要也不变，所以不会多存一份制品。
5. 发布人在槽位上激活这个 Release。它与槽位上最近生效的 Release 属于同一个修订，不需要兼容性比较（§5.10）。

之前那个 Release 仍然绑定在已经失效的批准上，一直不能再用，记录保留。也就是说，恢复之后槽位指向的是一个新版本号的 Release，内容逐字节相同。让旧 Release 改挂到新的批准上会改变 Release 记录的证据链，没有这样做。

**发布人能读什么（0.24.0）。** 发布人现在能读他所在 Workspace 的修订、原文、检查、比较结果和评审，与作者、评审人读到的相同。之前只持有发布人关系的主体能准备 Release，却读不到 Release 要引用的修订和评审。数据模型没有变化，规则见 F05 §6.8。

**槽位上的批准状态（0.25.0）。** 读取指向 Release 的槽位时，服务端核对它对该目标的批准是否仍然有效。失效时，`approval_problem` 对应重新激活时会得到的错误码。状态由查询计算，槽位代次保持原值。0.33.0 起，即使记录了这个原因，当前内部槽位仍可提供满足 §5.14 条件的制品。

**升级。** 从 0.22.0 升级，执行 `fabric-migrate -apply`，应用迁移 `policyreview/005`；不需要重新授权。库没有迁移时，新进程拒绝启动并指出这一步没有记录。新旧进程可以混跑：0.22.0 的进程在迁移后的库上照常提交、撤回和决定第一次评审，照常读取和提供制品；它读复审时不带 `supplementary`；它不能结束复审，决定和撤回都返回 409 `REVISION_STATUS_CONFLICT`，数据不受影响。所以复审要由新进程来结束。

**尚未实现。** 具名治理员撤回、指派评审人和产品级策略（同 §5.9）；让旧 Release 沿用新批准；列出某个产品下批准已失效的槽位（0.26.0 起可以列出一个目标上的槽位，每项带 `approval_problem`，但不能按它筛选，也没有按产品列出，见 §5.12），以及在批准将要到期时提醒；复审“同时只有一个”的表级约束。

### 5.12 已实现的查询与追溯

> 0.37.0 起槽位列表的每一项不再有 `approval_problem`；依据检查准备的 Release，来源里没有 `approval`；按编号下载不再要求修订是 `APPROVED`（§5.15）。
>
> 0.38.0 起（CR-0002）按编号下载看这个 Release 在任一目标上的阻断；审计员读到行动人的原值是认可的做法。见下文“下载读什么”和“尚未实现”两处。

本节记录管理 API 0.26.0–0.28.0 的实现事实，不替代 §5.4 逻辑模型的候选状态。这三个版本补的是读：列出发布过的槽位（0.26.0），按编号下载 Release 并读出它的来源（0.27.0），审计员按产品读审计记录（0.28.0）。权威 DDL 为 `migrations/productgovernance/009_auditor_role.sql` 和 `migrations/platformoperations/002_audit_read_order.sql`；0.26.0 和 0.27.0 没有迁移。接口见 F04 §11.8，权限见 F05 §6.9。

**槽位列表（0.26.0）。** 两个列表读的是 §5.10 的 `publication.slot`，没有新表、新列、新索引，也没有改应用角色的权限。列表项就是读单个槽位得到的那一项，含 `blocked` 和 `approval_problem`：读一个槽位和列出槽位走同一个函数（应用层 `Publication.view`，传输层 `showSlot`），所以同一个槽位在哪里读都一样。`blocked` 和 `approval_problem` 不存库，是读的时候按 §5.10 的核对算的，所以一页里的每个槽位各做一次这样的核对，耗时随页里的槽位数增加。

- 列哪些。只列做过操作的槽位（`generation > 0`），撤回的也列。没有发布过的资产没有槽位：读单个槽位时当作空槽位，列表里没有它。所以列表项的 `visibility` 只有 `ACTIVE` 和 `WITHDRAWN`。
- 顺序。槽位表没有创建时间，也没有 Workspace。列表按槽位的第一次操作（代次 1）的时间 `publication.operation.occurred_at` 排，早的在前，同一时刻按 `slot_id`。槽位和它的第一次操作在同一个事务里建立，`operation` 只增不改，`slot_id` 不变，所以一个槽位的位置从它出现起不再变：撤销、恢复、换 Release 都不换位置。翻页的位置就是上一页最后一个槽位的这两个值，语句把它当作一行来比较；没有位置时从头开始，所以一条语句读第一页，也读其余各页。存储多取一行，只用来知道后面还有没有槽位，最后一页即使装满也没有令牌。
- 可读的资产。槽位表没有 Workspace，所以目标上的槽位要先知道调用人能读哪些资产。Product Governance 给出调用人现在持有作者、评审人或发布人成员关系的 Workspace，API Catalog 给出这些 Workspace 在这个产品里的资产（新的受信查询 `AssetsInWorkspaces`，用迁移 `apicatalog/001` 已有的索引 `api_asset_workspace_order`，不给调用人任何目录读权限）。槽位的查询把这些资产的编号作为条件，写在位置和 `LIMIT` 之前，所以每一页除最后一页都是满的。一个资产也没有的集合是“什么都不列”，不是“全部”。这个集合每页取一次，开销随调用人能读的资产数增加，不只随页的大小。
- 一个资产的槽位。`listAssetPublications` 不需要这个集合：判权和读单个槽位用同一个函数。槽位表上没有以资产开头的索引，直接按资产找要读遍每个产品的每个槽位；所以先取这个产品的发布目标（只有少数几个），再逐个用唯一索引 `(target_id, api_asset_id)` 找槽位。
- 代价。一个目标上 100 个槽位、每个都指向生效的 Release，`page_size=100` 的一页整条链路（传输层、判权、PostgreSQL）约 0.31 秒，`page_size=50` 约 0.16 秒，读单个槽位约 6 到 7 毫秒；Release 平均 404 字节和 51072 字节，结果相同。这是本机的数字，量的时候别的测试也在跑，只说明量级，不是 SLO。重现用默认不跑的 `TestAPageOfSlotsWithReleasesIsReadInTheTimeItTakes`（环境变量 `IDP_FABRIC_SLOT_LIST_LOAD=1`）。

**Release 的来源（0.27.0）。** 来源没有新的数据，是把准备 Release 时依据的记录一次读出来。Release 准备时记下了它依据的修订、评审、检查和策略快照的编号与摘要（§5.10）；来源按这些编号去读只增不改的记录：修订和它的来源快照（`contract_revision`、`source_snapshot`，§5.7），评审申请、结论、范围和豁免（`review_request`、`review_conclusion`、`review_scope`、`review_waiver`，§5.9），检查、它的策略快照和每条规则的结论与发现（`check_run`、`policy_snapshot`、`check_rule_outcome`、`check_finding`，§5.8），来源只给各状态的条数和发现总数。Contract Registry 经新的端口 `ReleaseScopes.Evidence` 要评审和检查，Policy & Review 经新的受信读取 `Reviews.ReleaseEvidence` 回答，两个上下文不读对方的表。

- 为什么复审以后不变。这些记录写入后不变，Release 记的又是编号，所以修订后来又被复审、又有了更新的检查，旧 Release 读出的仍是它准备时依据的那次评审和那次检查；复审的批准是另一条评审记录（§5.11），依据它准备的是另一个 Release。来源也不回答批准现在成不成立：不比时钟，不比评审人现在的授权，不比现行的策略和工具，这些由槽位的 `approval_problem` 说（§5.11）。豁免只带规则和到期时间，理由和补偿措施的文字不在来源里。
- 对不上按存储故障处理。读出的记录要和 Release 记的对得上：评审是一次批准，它依据的检查就是 Release 记的那次，两者读的闭包摘要等于 Release 的输入摘要，检查用的策略快照和摘要等于 Release 记的，修订的闭包摘要也等于 Release 的输入摘要。对不上不是调用人的错，是存储出了问题：答 500 `INTERNAL_ERROR`，不说哪里不对，原因写进日志，在 `correlation_id` 下。
- Git 来源。它是提交人导入时声明的，Fabric 不回仓库核对（§5.7）；同一仓库的相同内容后来从别的提交再导入，得到的仍是最先登记的修订，来源里记的仍是最早那次的提交。上传来的修订没有 `git_origin`。
- 下载读什么。按编号下载读的是制品，和读已发布的制品用同一个读取函数 `ReleaseMaterial`：修订仍是 `APPROVED`，修订的闭包摘要等于 Release 的输入摘要，制品字节的 SHA-256 等于 Release 的制品摘要。它不读评审和检查，所以评审或检查的记录坏了不影响下载，制品字节坏了也不影响来源。它也不读发布目标，所以 0.27.0–0.37.0 不看阻断（当时与设计正文不一致，见 F05 §6.9），不核对批准，产品暂停也不挡。0.38.0 起（CR-0002），读字节之前经端口 `ReleaseScopes.ReleaseBlocked` 问 Publication：Publication 先对产品的全部发布目标加共享锁，再读 `publication.access_block` 里有没有范围是这个 Release、没有解除的阻断，有就拒绝（409 `RELEASE_ACCESS_BLOCKED`）。没有新表、新列和新索引。目标和资产的阻断不算，产品暂停仍不挡，来源不看阻断。加锁的先后见 F05 §6.15。

**审计记录（0.28.0）。** 读的是 `platformoperations.audit_record`（迁移 `platformoperations/001`，表没有改）。一行是一次行动尝试的最小事实：`audit_id`、`recorded_at`、行动人（`actor_issuer`、`actor_subject`）、`action`、`outcome`（`SUCCEEDED` 或 `DENIED`）、`rule_id`、`product_id`、`resource_type`、`resource_id` 和 `correlation_id`。`product_id` 为空表示平台范围，例如无权的人登记产品被拒绝；`resource_id` 为空表示这次尝试没有留下资源。表里只存引用：没有令牌，没有请求体，没有私有对象的名字。

| 变化 | 说明 |
|---|---|
| 索引 `audit_record_list_order (product_id, recorded_at, audit_id)` | 迁移 `platformoperations/002`。列表按 `(recorded_at, audit_id)` 倒序，索引反向读 |
| 索引 `audit_record_by_correlation (product_id, correlation_id)` | 迁移 `platformoperations/002`。按请求的编号找它的几条记录，不用读完这个产品的审计 |
| 应用角色在表上的权限从 `INSERT` 变为 `SELECT`、`INSERT` | 不在 SQL 迁移里：在 `internal/platform/db/migrate/grants.go` 的 `ApplicationPrivileges()` 里声明，由 `fabric-migrate -apply -grant-to <application_role>` 授予，进程启动时核对 |

- 顺序和翻页。新的在前：`recorded_at` 倒序，同一时刻 `audit_id` 倒序。这两列都不变，所以一条记录的位置不变。翻页的位置是上一页最后一条的这两个值，按一行比较；没有位置时从“时间的尽头”开始。新写入的记录比任何已发出的位置都新，排在它们之前：不会让读过的重复，也不会让没读到的漏掉。每页多取一条，只用来知道后面还有没有。只出本产品的记录（`product_id` 等于路径里的产品）；平台范围的记录不在任何列表里。
- 筛选。`recorded_since`（含）、`recorded_before`（不含）、`action`、`outcome`、`actor_issuer`、`actor_subject`、`resource_type`、`resource_id`、`correlation_id`，都是精确匹配，互相叠加。时刻按表里存的样子比：UTC，到微秒，更细的截到微秒。
- 一页读多少行。不带筛选的一页读的行数和它返回的相当，不管表多大；带 `correlation_id`（有自己的索引）或一个时间范围的也一样。按动作、结果、行动人和资源筛选没有索引：是在按时间读索引的时候一边读一边过滤，满足条件的记录越少，一页读的行越多，最坏要读完这个产品的全部审计。测试里两个产品各 2 万条记录，十分之一是 `DENIED`，数一页读的行：不带筛选 101 行，一个时间范围 100 行，一个请求 1 行，`outcome=DENIED` 1001 行。数的是读的行数，不是耗时。
- 仍然只能追加。应用角色在表上没有 `UPDATE`、`DELETE`、`TRUNCATE`，没有任何列上的 `UPDATE`，也不能 `SELECT … FOR UPDATE` 或 `FOR SHARE`（这两个要有 `UPDATE` 权限）。这是权限层面的限制：表的所有者（迁移用的角色）仍然能改，不是防篡改的证明（F05 §14）。靠什么保证见 F05 §6.9。

**新角色。** 迁移 `productgovernance/009_auditor_role.sql` 把 `product_role_assignment_role_known` 约束换成四个取值：`product_owner`、`security_admin`、`security_reviewer`、`internal_auditor`。已有的分配不变，§5.5 的其余规则也不变；没有新表，没有新权限。迁移之前表拒绝 `internal_auditor`，迁移之后接受。0.27.0 的进程在迁移后的库上仍能授予它认识的三个角色，但它不认识 `internal_auditor`。

**升级。** 两个迁移各只做一件事：`productgovernance/009` 放宽一个约束，`platformoperations/002` 建两个索引；都不改已有的行，也不授权。授权在 `ApplicationPrivileges()` 里。从 0.25.0 升级需要重新授权：执行 `fabric-migrate -apply -grant-to <application_role>`，应用 `productgovernance/009` 和 `platformoperations/002`，并给应用角色补上审计表的 `SELECT`，然后再换进程。只迁移不授权，新进程拒绝启动：`db: the application role lacks privileges this build needs; run fabric-migrate -apply -grant-to <role>: missing SELECT on platformoperations.audit_record`；补救是再执行一次带 `-grant-to` 的命令，没有东西要迁移。既没有迁移也没有授权时，报的是同一条：启动时先核对权限，后核对迁移。旧进程在迁移和授权之后照常运行：它只往审计表里写，多出来的 `SELECT` 不影响它，它的启动检查也只查缺的迁移和权限，不查多的。索引用普通 `CREATE INDEX` 建，建的时候审计表的写入要等；成功的写操作的审计和业务写在同一个事务里（§4 INV-13），所以那段时间写操作也会等，表越大等得越久。所有进程升级以后，才授予 `internal_auditor`：旧进程的契约里没有这个取值，授予请求会被它拒绝（400 `REQUEST_INVALID`）；库里有了审计员的分配，旧进程列产品角色、读这条分配会返回 500（契约没有声明的取值不发出去），回滚到旧版本也一样。0.26.0 和 0.27.0 没有迁移，也不需要授权。

**和 §15 读模型的对应。** 现在有的和还缺的：

| §15 的读模型 | 现在有的 | 还没有的 |
|---|---|---|
| WorkspaceNavigationView | 产品列表和 Workspace 列表，按调用人持有的角色和成员关系过滤（§5.5）；“我是谁”：调用人是谁，在一个产品里持有什么（0.29.0，§5.13） | 没有把角色和待办汇在一起的视图 |
| AuthorizedCatalogView | 资产列表，授权过滤在分页之前，可按 Workspace、类型、领域和文本过滤（§5.7）；0.31.0 起每个资产带概况，可按检查结论和待发布筛选（§5.13） | — |
| ContractReviewView | 修订、原文、检查、比较、评审和它们的历史（§5.7–§5.9）；Release 的来源一次读出修订、检查和批准（0.27.0）；一个 Workspace 里的评审队列（0.30.0，§5.13） | — |
| PublicationTimelineView | 槽位（含 `blocked` 和 `approval_problem`）、槽位的操作历史、一个目标上的槽位列表、一个资产发布到了哪些目标（0.26.0）、Release 的列表和来源 | `delivery_status` 仍然只有 `UNKNOWN`；不能按批准有没有问题筛选槽位；没有按产品列出槽位 |
| AuthorizedDocumentView | 没有 | 对外读取需要的身份、授权和边缘分发；现在只有 `internal` 受众可读，读到的是字节，不是文档视图 |
| DeveloperApplicationView | 没有 | 开发者应用和订阅（V1） |
| OperationsIncidentView | 审计员按产品读审计记录（0.28.0） | Job 和对账都没有；平台范围的审计记录没有读取途径 |

**尚未实现，以及与设计不一致的地方。**

- 不属于任何产品的审计记录（`product_id` 为空，例如无权的人登记产品被拒绝）没有读取途径；审计员以外没有人读审计，也没有跨产品的读取。
- F05 §14 列的核心审计事实是 `actor_ref`、`impersonation_ref`（如有）、`product_id`、`resource_ref`、`action`、`policy_digest`、`decision_id`、`before/after generation`、`timestamp`、`result`、`correlation_id` 和最小安全标签。表里有行动人（issuer 和 subject）、`product_id`、资源（类型和编号）、`action`、`recorded_at`、`outcome`、`correlation_id`，另有 `rule_id`；没有 `impersonation_ref`、`policy_digest`、`decision_id`（`rule_id` 是授权矩阵里的规则编号，不是决定的编号）、`before/after generation` 和安全标签。
- 审计里的行动人按原值存，也按原值给出，没有脱敏：设计没有定义怎么脱敏，CR-0002 决定 2 认可按原值给出，见 F05 §6.9、§6.15。F03 OD-09（审计里的个人信息与删除请求的冲突）没有处理：应用角色不能改也不能删，没有去标识化，也没有保留期；审计导出、归档和保留策略（FR-068，V1）都没有。
- `delivery_status` 仍然只有 `UNKNOWN`（§5.10）。
- 按编号下载 Release 不看阻断，不核对批准（0.27.0–0.37.0），与设计正文不一致：已由 CR-0002 裁决，0.38.0 起看这个 Release 在任一目标上的阻断，目标和资产的阻断不看；批准 0.37.0 起已没有（§5.15）。见 F05 §6.9、§6.15。
- Release 的来源是准备时的记录，Git 提交是声明，不回仓库核对（FR-021，V1）。

### 5.13 已实现的工作台查询

> 0.37.0 起 `release_pending` 改为“最新修订有检查，而且还没有从它准备出 Release”，评审队列只读历史，不再增长（§5.15）。

本节记录管理 API 0.29.0–0.31.0 的实现事实，不替代 §5.4 逻辑模型的候选状态。这三个版本补的是内部工作台要先读的几样东西，都是读，不写业务数据：调用人是谁、在一个产品里持有什么（0.29.0），一个 Workspace 里的评审队列（0.30.0），资产列表和详情里每个资产走到了哪一步（0.31.0）。权威 DDL 只有一个：`migrations/policyreview/006_review_queue_order.sql`，只建一个索引；0.29.0 和 0.31.0 没有迁移。三个版本都没有新表、新列、新角色和新枚举取值，应用角色的权限也没有变（`ApplicationPrivileges()` 没有改）。接口见 F04 §11.9，权限见 F05 §6.10。

**调用人的身份（0.29.0）。** 没有新的数据，两个读取用的都是已有的东西。

- 登录的是谁。身份只有令牌验过之后交出的签发方和主体（`internal/platform/identity/token.go` 的 `checkClaims`）；令牌里的其他声明不往下传，所以没有姓名可给。`getCaller` 不读库：`principal` 就是这两样，`platform_administrator` 来自部署配置 `IDP_FABRIC_PLATFORM_ADMIN_SUBJECTS`，进程启动时读入，进程内固定，不在库里，改了要重启。
- 在一个产品里持有什么。`getCallerInProduct` 读的是授权时本来就读的三条语句：产品、调用人在这个产品里的产品角色（`product_role_assignment`）、调用人的成员关系（`workspace_membership`），都只取有效的行（`revoked_at IS NULL`）。每次请求现读，不加锁，不开事务，不缓存：撤销提交之后的下一次请求就没有那个角色，用的还是原来的令牌；产品暂停时照常读。
- 应答里有什么。`roles` 是持有的产品角色，每个一次，可以为空；`workspaces` 是调用人持有成员关系的 Workspace，每个一项，带它在那里的角色，每个一次，没有成员关系的 Workspace 不出现。`workspaces` 不分页，一次给全，没有上限：判权用的成员关系本来就是整读的（`Memberships.HeldBy` 没有 `LIMIT`），页面要完整才判断得了。服务端按角色名和 Workspace 编号排序，契约不承诺这个顺序。应答只说持有什么：没有授予人、授予时间和分配编号，也没有“能做哪些操作”。
- 不是“读得到什么”。产品负责人不是任何 Workspace 的成员也读得到全部 Workspace（§5.5），所以 `workspaces` 是“我持有成员关系的”，不是“我读得到的”。负责人要看自己读得到什么，仍用 `listWorkspaces`。
- 库里出现契约没有的角色时不发出去，答 500，原因写进日志，与列产品角色、列成员一致，所以以后加新角色，仍要等所有进程升级以后再授予（§5.12）。
- 页面要“我的产品和每个产品里我的身份”，得先 `listProducts` 再对每个产品问一次；没有一次给全的读取。

**评审队列（0.30.0）。** 读的是 §5.9 的 `policyreview.review_request` 和 `review_conclusion`。

- 哪个 Workspace 的评审。评审申请表自带 `workspace_id`（提交评审时随修订记下，之后不变），所以一条语句按 `(product_id, workspace_id)` 读完，不经过目录，也不跨上下文。每条评审的列和读回的方式与读单个评审（`getRevisionReview`）是同一份（`reviewColumns`、`scanReview`），所以同一条评审在哪里读都一样；某条评审的行对不上模型记下的样子，整页答 500。
- 状态不是列。`IN_REVIEW` 是还没有结论行（`LEFT JOIN review_conclusion` 之后 `review_id IS NULL`），其余三个取的是结论的结果（`outcome`）。开着的复审（§5.11）也是 `IN_REVIEW`，这时修订仍是 `APPROVED`。按状态筛选是固定的几段 SQL，不拼接用户的输入。
- 顺序键。`(requested_at, review_id)` 升序：先提交的在前，同一时刻按 `review_id`。评审申请只增不改（应用角色只有 `SELECT` 和 `INSERT`，§5.9），这两列写入后不再变，所以一条评审的位置从它出现起不再变。翻页的位置是上一页最后一条的这两个值，语句把它当作一行比较；没有位置时从头读，所以一条语句读第一页，也读其余各页。存储多取一行，只用来知道后面还有没有。翻页期间新提交的评审比任何已发出的位置都晚，排在它们之后：不会让读过的重复，也不会让没读到的漏掉。
- 新索引。迁移 `policyreview/006` 建 `review_request_queue_order (product_id, workspace_id, requested_at, review_id)`，列序就是队列的顺序，正向读。表上已有的评审索引以资产和修订开头（`review_request_history`），没有这个索引，一页要读完这个产品所有 Workspace 的评审再排序。迁移是普通的 `CREATE INDEX`：建的时候新提交评审的写入要等，读不受影响；它不改任何行，也不授权。
- 一页读多少行。判权读 4 条语句（产品、角色、成员关系、Workspace），一页的数据是 1 条。计划测试里，一个产品的两个 Workspace 各有 20,000 条评审（共 40,000 条申请、37,970 条结论），数的是一页（`page_size=100`，语句取 101 行）读的行数；每一种都走 `review_request_queue_order`，没有排序：

| 情形 | 读 `review_request` | 读 `review_conclusion` |
|---|---|---|
| 首页、中间页 | 101 | 101 |
| `APPROVED`（十分之六） | 168 | 168 |
| `WITHDRAWN`（十分之二） | 502 | 502 |
| `REJECTED`（十分之一） | 1,001 | 1,001 |
| 一个提交人（五十分之一），或 issuer 加 subject | 5,007 | 101 |
| `IN_REVIEW`，开着的占十分之一，散在各处 | 1,010 | 1,010 |
| `IN_REVIEW`，开着的只有最新 30 条（共 20,000 条） | 20,000 | 20,000 |
| 别的产品的 Workspace | 0 | 0 |

- `IN_REVIEW` 不能单靠索引。没有结论不是一列，索引读不了这一部分，所以一页要一直读到凑满，或者读完这个 Workspace 的评审，没有上界：开着的评审越靠后、越少，读得越多。这是本节唯一没有设上限的情形，测试只记录它的行数。默认不跑的负载测试 `TestAPageOfTheQueueIsReadInTheTimeItTakes`（环境变量 `IDP_FABRIC_REVIEW_QUEUE_LOAD=1`，整条 HTTP 链路，每个 Workspace 10,000 条评审，7 次读取取中位数，8 核的本机）量到：首页和各种筛选 4–5 毫秒，开着的评审只在最末尾时 10.4 毫秒，随这个 Workspace 的评审数线性增长。这是本机的数字，只说明量级，不是 SLO。
- 没有做可更新的“已结”标记：它要给只追加的 `review_request` 加一列和 `UPDATE` 权限，而旧进程不写它，新旧混跑时队列会错。
- 列表和单读只有一处不同：列表不核对评审的 `input_digest` 等于修订现在的摘要（单读核对），因为这要读修订，是 Contract Registry 的事。

**资产的概况（0.31.0）。** `listAssets` 和 `getAsset` 应答里的资产多一个可选的 `overview`：最新修订、最新修订最近的一次检查、最新的 Release，以及 `release_pending`。概况没有自己的表，也不存下来，每次读的时候从各上下文自己的表里算出来。

- 每个成员怎么定。最新修订是 `revision_sequence` 最大的修订。最近检查是**最新修订**的、`(completed_at, check_run_id)` 最大的一次检查；没有检查就没有这个成员，较早的修订有没有检查不相干。最新 Release 是这个资产 `(prepared_at, release_id)` 最大的一个，**不限于最新修订**，所以它带 `revision_id`，能看出它不是从最新修订做的。没有修订的资产，概况只有 `release_pending: false`。每个成员都是 `ContractRevision`、`CheckRunOverview`、`ContractRelease` 同名成员的同义、同值。最新修订按序号取，不是 `listRevisions` 的最后一项：那个列表按 `(captured_at, revision_id)` 排，`captured_at` 在拿资产的登记锁之前取，序号在锁里分配，同一资产的两个并发导入，两个顺序可能不同（§5.7）。
- `release_pending`。最新修订是 `APPROVED`，而且还没有从它准备出过 Release。只看状态，不重新核对批准：批准过期、豁免到期、评审人失权、策略或工具变了，它都不管，这些在准备 Release 时才核对（§5.10）。复审不改变修订的状态（§5.11），所以复审开着或结束，概况都不变。
- 在哪里拼。API Catalog 只认一个端口 `Overviews`：给一组资产（带各自的 Workspace），答每个资产的概况。端口在 `cmd/fabric-api/overview.go` 里接上另外两个上下文各自的批量读取，三个上下文互不认识（§3.2）：Contract Registry 的 `Heads`，一条语句给出一组资产各自的最新修订、最新 Release，以及最新修订有没有从它准备出的 Release；Policy & Review 的 `Latest`，一条语句给出一组修订各自最近的一次检查。两条语句都按资产逐个用已有的索引找（`contract_revision` 的 `(product_id, api_asset_id, revision_sequence)` 唯一约束、`contract_release_history`、`check_run_history`），不读别的资产的行；检查的各规则结果只在留下的检查上数（每个检查 23 行）。没有一条 SQL 点名别的上下文的 schema，`internal/architecture/schemas_test.go` 钉着这一点。
- 不带筛选的一页。原来的查询（取一页多一行的资产）加一次概况：判权 3 条语句，取资产 1 条，`Heads` 1 条，`Latest` 1 条，共 6 条，与库里有多少资产无关。调用人读不了修订的 Workspace 里的资产不去问概况，应答与以前相同。
- 带筛选的一页。`check_status` 和 `release_pending` 不是资产表的列，取资产的语句没法过滤。用例只在调用人读得了修订的 Workspace 里，按资产原来的顺序（`(created_at, api_asset_id)`）一批一批取：第一批 `page_size + 1` 个，之后每批翻倍，封顶 1000 个；每一批取资产、取概况，留下符合的，凑够 `page_size + 1` 个，或者资产取完，就停。页是留下的前 `page_size` 个，多出的一个只用来知道后面还有没有，令牌是页里最后一个资产的位置。所以除最后一页，每页都是满的。每一批 3 条语句，另加判权 3 条。
- 代价。下面是实现时在本机量的：整条 HTTP 链路，页大小 50，调用人是读修订的作者，每个资产一个修订、一半有检查、1% 已批准未发布、2% 已发布，记下应用发出的每条 SELECT 并重放 `EXPLAIN ANALYZE`。量的程序是一次性的实验，没有放进仓库。耗时是同时有别的测试在跑时量的，只说明量级，不是 SLO：

| 资产数 | 请求 | 轮 | 语句 | 耗时 |
|---:|---|---:|---:|---:|
| 20,000 | 不带筛选 | 1 | 6 | 约 9 毫秒 |
| 20,000 | 符合的占四分之一 | 3 | 12 | 约 13 毫秒 |
| 20,000 | 符合的占 1%（读了 5,581 个资产才凑够一页） | 9 | 30 | 约 55 毫秒 |
| 20,000 | 一个都不符合 | 24 | 75 | 约 0.24 秒 |
| 2,000 | 一个都不符合 | 6 | 21 | 约 35 毫秒 |

读的行数受规划器的选择影响：这份数据里 `contract_release` 只有几十到几百行（2,000 个资产时 40 行，20,000 个资产时 400 行），规划器把它整张读，所以量到的行数里它占了很大一部分（20,000 个资产、不带筛选的一页读 1,129 行，其中 400 行是它）。提交里的计划测试用两万行的大表，数的是读的行数：一组资产在 `contract_revision`、`source_snapshot`、`contract_release`、`check_run` 里读的行数与这一组的大小成正比，不做全表扫描，规则结果只读留下的检查。

- 批量读取和单独读在校验上的差别。批量读取和单独读一样，要求检查的行对得上模型记下的样子（状态等于各规则结果之和），而且检查读的闭包摘要就是修订现在的；行被改坏时，概况和单独读一样答 500，而且对读得了概况的人，这让这个资产的读（`getAsset` 和列表）整个答 500，不只是少了概况。两处不同：批量读取不去 Contract Registry 查对比检查的基线修订；带 `check_status` 时，被排除的检查不读规则结果，用的是库里存的状态，不核对它等于规则结果之和。
- 没有汇总表。设计阶段比较过两种做法，都没有选。一是触发器维护的汇总表：读的行数不随资产总数涨，但触发器要进到导入、检查、评审、准备 Release 四条核心写路径，它出错就是这些写全部失败；这是仓库里第一次让数据库替应用维护数据，也是第一次在库里跨上下文。二是 Outbox 加 worker 的事件投影（§3.3、§12 设计的做法）：不碰写路径，但概况会落后一小段时间，要新增 worker 和回填，而事件现在只写进库，没有任何消费者。现在的做法与它们的契约和端口相同，以后可以换，换的只是 `cmd/fabric-api/overview.go` 背后的读取。§3.3 里目录列表的可重建投影 `AssetSearchIndex` 仍然没有：列表读 `api_asset` 的索引，概况在读的时候算。资产多到一个产品几万个，或者页面轮询这两个筛选时，要重新看这一点。CR-0002 决定 4：读的时候算是认可的做法，`AssetSearchIndex` 等资产数量需要时再建。

**升级。** 从 0.28.0 升级只需要 `fabric-migrate -apply`，不需要重新授权：应用 `policyreview/006`，只建 `review_request_queue_order` 一个索引，不改行，应用角色的权限没有变。库没有迁移时，0.30.0 和 0.31.0 的新进程拒绝启动并指出这一步没有记录：`db: the database is not migrated for this build; run fabric-migrate -apply first: migration policyreview/006_review_queue_order is not recorded`。旧进程在迁移后的库上照常运行：它读评审和写评审用的语句没有变，它的启动检查只查自己计划里的步骤，计划以外的步骤接受（`internal/platform/db/open.go` 的 `checkHistory`）。旧进程没有新的路径，对 `GET /v1/caller`、`GET /v1/products/{product_id}/caller` 和 `GET …/workspaces/{workspace_id}/reviews` 答 404 `ROUTE_NOT_FOUND`；它的 `listAssets` 不认识 `check_status` 和 `release_pending`，答 400 `REQUEST_INVALID`，资产的应答里没有 `overview`。建索引用普通 `CREATE INDEX`，建的时候新提交评审的写入要等，评审越多等得越久，读不受影响。概况没有状态，0.31.0 不需要任何准备，要用新筛选的页面等所有进程升级完再上。

**和 §15 读模型的对应。** 三个读模型现在有的和还缺的（§5.12 的表里的其余几行没有变）：

| §15 的读模型 | 现在有的 | 还没有的 |
|---|---|---|
| WorkspaceNavigationView | 产品列表和 Workspace 列表，按调用人持有的角色和成员关系过滤（§5.5）；“我是谁”：调用人是谁，在一个产品里持有哪些角色和成员关系（0.29.0） | 没有把角色和待办汇在一起的一次读取：首页要先问“我是谁”，再分别读评审队列、资产列表和槽位；没有“我在所有产品里的身份”的一次读取，要逐个产品问；没有“能做哪些操作”的清单；没有姓名 |
| AuthorizedCatalogView | 资产列表和详情带概况（最新修订、最近一次检查、最新 Release、`release_pending`），可按检查结论和待发布筛选，授权过滤和筛选都在分页之前（0.31.0） | 概况只给读得了修订的人，负责人和维护者看不到；概况里没有“当前发布”，要读 `listAssetPublications`；没有 `AssetSearchIndex` 投影（CR-0002：资产数量需要时再建） |
| ContractReviewView | 一个 Workspace 里的评审队列，先提交的在前，可按状态和提交人筛选（0.30.0），加上 §5.12 列的各项 | 队列不给总数，不标“这条该不该我批”，不能只列我能决定的，没有评审的指派；`IN_REVIEW` 比别的状态读得多 |

F06 的 IN-01（首页的待审核、检查失败、可发布、撤销风险）没有单独的汇总接口，要由页面拼：待审核是 `listWorkspaceReviews` 加 `status=IN_REVIEW`，检查失败是 `listAssets` 加 `check_status=FAIL`，可发布是 `listAssets` 加 `release_pending=true`，撤销风险是 `listPublicationSlots` 和 `listAssetPublications`（每项带 `approval_problem` 和 `blocked`，不能按它们筛选）。页面先用 `getCallerInProduct` 看自己的身份，再决定调哪些：没有权限的调用每次都被拒绝，也每次都留下一条审计记录（F05 §6.10）。

**尚未实现，以及与设计不一致的地方。**

- “我是谁”只有签发方和主体。显示名从哪里来没有定，另存会碰到个人信息的保留和删除，与 OD-09 同类。平台管理员只来自部署配置：页面能知道调用人是不是管理员，不能增减管理员，几个副本的配置不一致时，同一个人的答案会不同；没有放在库里的、可撤销且有记录的平台管理员角色。CR-0002 决定 3 认可部署配置：fabric-api 的每个副本带同一份设置，改动走部署自身的变更控制，Fabric 不审计这项改动（F05 §6.15）。
- 没有评审的指派、“只列我能决定的”和待审核的条数：队列不给总数，要精确的数得另开一个操作。评审人点进去批的时候才知道自己批不了（403 `REVIEWER_NOT_INDEPENDENT`，§5.9）。`IN_REVIEW` 在评审特别多时读得更多，没有设上界，等量大了再决定要不要加“已结”标记。
- 概况里没有“当前发布”（资产现在在哪些目标上生效）：逐个槽位核对批准太重，只看指针又可能与实际不符。要看用 `listAssetPublications`，撤销风险用 `listPublicationSlots`。`release_pending` 不重新核对批准；另给一个把批准是否还成立算进去的“可以发布”筛选，没有做。
- `check_status` 收 `PASS`、`WARN`、`FAIL`、`UNKNOWN` 四个取值。基线里有规则没有工具，一次检查里就有 `NOT_RUN` 的规则，检查的总状态目前只会是 `FAIL` 或 `UNKNOWN`（§5.8），所以 `PASS` 和 `WARN` 今天没有结果。“检查失败”是最新修订最近一次检查是 `FAIL`，也包括评审时豁免了失败规则并批准的修订，这样的修订同时是 `release_pending`。
- 没有汇总表，没有 `AssetSearchIndex`，没有事件投影，见上；这原是与 §3.3 的设计不一致的地方，CR-0002 决定 4 认可读的时候算，资产数量需要时再建索引。
- 设计里首页的 Worklist（F06 IN-01）和目录的 Asset Index（F06 IN-03）都没有做成读模型：首页要由页面拼，目录列表读 `api_asset`。
- 批量读取带回完整的修订行，一个都不符合的 20,000 个资产要收 40,002 行；只选概况用到的列，等有人觉得慢再做。
- 0.31 阶段的后续工作包括浏览器登录、会话、评审通知及扩充治理读取权限。当前实现见 §5.14 和 F05 §6.12。

### 5.14 内部发布事实、审计员和读取范围

- `PublicationSlot` 保留已经生效的 Release 指针。内部源站根据当前读取权限、槽位指针、有效受众配置、制品摘要和 AccessBlock 决定是否提供制品。0.33.0–0.36.0 中，批准失效时 `approval_problem` 说明无法再次激活的原因，内部恢复使用该槽位的历史和原批准证据，并要求独立发布人与 CAS；0.37.0 起恢复核对槽位历史、Release 记录的检查、阻断和 CAS（§5.15）。
- 审计员仍保存在 `productgovernance.product_role_assignment`。0.35.0 由满足职责分离的平台管理员任命和撤销，已有行与授权历史保留。角色筛选在排序和分页之前应用。
- 0.36.0 在 `productgovernance.workspace_membership` 增加 `internal_reader` 枚举值。迁移 010 只替换角色约束，沿用现有记录、索引和应用角色权限。主体、Product、Workspace 与当前成员关系仍决定单读和分页范围。
- 产品级安全角色读取本产品的资产和发布元数据，Workspace 维护者读取所属 Workspace 的治理资料。`internal_reader` 读取所属 Workspace 的 Release 元数据，通过当前槽位读取受阻断校验的已发布制品。制品仍保存于 Release，发布操作和阻断仍由 Publication 负责。

### 5.15 Release 依据检查（CR-0001，0.37.0）

本节记录管理 API 0.37.0 的实现事实（PR #74），不替代 §5.4 逻辑模型的候选状态。决定见 [CR-0001](../decisions/CR-0001.md)。权威 DDL 为 `migrations/contractregistry/005_release_from_check.sql`、`006_open_reviews_withdrawn.sql` 和 `migrations/policyreview/007_open_reviews_withdrawn.sql`。接口见 F04 §11.5、§11.6，权限见 F05 §6.14。

**准备 Release 的门禁。** 准备 Release 时指定修订的一次检查，并给出豁免。Policy & Review 按 `domain.GateRelease` 判定，不再有人批准：

- 检查须是这个修订的、读的就是修订现在的内容，并且在现行策略和工具下完成，否则 `POLICY_STALE`。
- 结果为 `FAIL` 或 `UNKNOWN` 的规则，不分阻断级和警告级：有一条不可豁免，就不能准备（`RELEASE_BLOCKED`）；可豁免的逐条豁免，各写一行理由。豁免恰好覆盖这些规则，不多不少。
- 阻断级规则为 `NOT_RUN` 时，只接受 `outsideContractChecks` 列出的规则，即在检查之外执行的 10 条，以及没有选对比基线时的 F04-GOV-013；其余拒绝（`RELEASE_CHECK_INCOMPLETE`）。

修订的状态不再参与判断。这就是 §4 的 INV-07。原先由批准范围限定的受众和环境不再绑定在 Release 上：能发布到哪个目标，由发布人的角色和目标本身决定。

**表的变化。** 迁移 `contractregistry/005` 把 `contract_release.review_id` 改为可空，增加 `waivers`：JSON 数组，每项是 `rule_id` 和 `reason`，按规则编号排列，默认空数组。表约束要求 `waivers` 是数组，并且 `review_id` 不为空时 `waivers` 为空：依据批准准备的旧 Release 保留它的 `review_id`，豁免在那次批准里；依据检查准备的 Release 不引用评审。表仍只增不改，应用角色的权限没有变。同一版本号再次提交，修订、检查和豁免（不计顺序）都相同时返回已有的 Release，否则拒绝；依据批准准备的旧 Release 不会被新的请求复用。

**激活和恢复。** 只核对 Release 依据的检查就是它记录的那次，检查读的内容与 Release 的输入摘要相同。不再核对现行策略和工具、批准、豁免的期限和发布人是否独立，所以策略更新后，已有 Release 仍可激活和恢复（CR-0001 决定 5）。`ACTIVATE` 用另一个修订的 Release 替换槽位上最近生效的 Release 时，检查仍须以那个修订为对比基线。合作方源站按槽位指针提供制品，不再核对批准。槽位不再计算 `approval_problem`。按编号下载和来源读取不再要求修订是 `APPROVED`；来源只对依据批准准备的旧 Release 给出 `approval`。

**评审成为历史。** 不再产生评审，也不再产生三种评审事件。迁移 `contractregistry/006` 把仍为 `IN_REVIEW` 的修订改为 `WITHDRAWN`，状态版本加 1。迁移 `policyreview/007` 给每条还没有结论的评审补一条 `WITHDRAWN` 结论，复审也一样（复审的修订仍是 `APPROVED`）。结论的理由引用 CR-0001，结束人是系统主体 `urn:idp-api-fabric:system` / `migration/policyreview/007_open_reviews_withdrawn`，不是任何人；结束时间不早于申请时间。已有的结论不变。此后修订登记为 `DRAFT` 后不再转移，`APPROVED`、`REJECTED`、`WITHDRAWN` 是此前评审留下的状态。评审的读取保留，记录不变。

**角色。** `internal_reviewer` 不再授予（422 `REQUEST_UNPROCESSABLE`）。已有的成员关系保留，持有人仍能读原来读得到的修订、检查和评审，没有写权限。

**概况。** `release_pending` 改为：最新修订有检查，而且还没有从它准备出 Release。它不说明这次检查能否通过门禁：检查可能失败，策略和工具也可能已经更新。

**升级。** 从 0.36.0 升级，执行 `fabric-migrate -apply`，应用上述三个迁移；新进程不需要新的权限。两个撤回迁移只处理执行时还开着的评审，旧进程在迁移之后仍提供评审的写接口，所以应尽快换完进程。调用方要改用 `check_run_id` 和 `waivers` 准备 Release（F04 §11.6）。

**收回评审的写权限。** PR #74 的构建仍授予应用角色四张评审表的 `INSERT` 和 `contract_revision` 的 `UPDATE`，供滚动升级期间的旧进程使用。此后的构建（契约仍是 0.37.0）不再授予：`ApplicationPrivileges()` 里四张评审表只有 `SELECT`，`contract_revision` 只有 `SELECT`、`INSERT`，因为程序不再写评审，也不再改修订，此前的评审和修订状态只读。`GRANT` 只会添加，所以 `fabric-migrate -apply -grant-to <application_role>` 在授权的同一事务里按 `RetiredPrivileges()` 收回这五项，重复执行结果不变；列表以外的权限，包括手工多授的，不收回。执行带 `-grant-to` 的命令之前，先停掉所有 0.36.x 及更早的进程：收回之后，它们提交、撤回和决定评审会被数据库拒绝（42501），重启时也因缺少权限拒绝启动。0.37.0 的进程不用这五项，收回前后都能启动；启动检查不查多出来的权限，所以只执行 `-apply`、暂不带 `-grant-to` 时，旧进程照常写评审，新进程也照常启动。

**与逻辑模型的对应。** §5.4 的 `ContractRelease` 引用 `check_run_id`；`ReleaseWaiver` 没有单独的表，是 `contract_release.waivers` 的数组元素。`ReviewDecision` 和 `WaiverRef` 的物理表（§5.9）只读。

## 6. 数据权威、制品摘要与可追溯发布链

<figure class="architecture-figure"><div class="fig-label">FIGURE 05 · 单一事实权威与来源追踪 / Import → Release → Portal</div><div class="fig-grid cols-5"><div class="fig-box"><b>SourceSnapshot</b><small>Git Commit/Upload · 原文 digest</small></div><div class="fig-box"><b>Revision</b><small>Immutable content · ref closure</small></div><div class="fig-box"><b>Check + Waivers</b><small>Policy digest · tools · 准备时的豁免</small></div><div class="fig-box"><b>Release</b><small>READY · immutable artifact manifest</small></div><div class="fig-box fig-primary"><b>PublicationSlot</b><small>CAS pointer · target audience/env</small></div></div><div class="fig-arrow">↓ 主库目标版本权威 → Outbox/Worker → PortalProjection；读时再次按当前策略判定</div><div class="fig-grid cols-3"><div class="fig-box tone-green"><b>Git / Upload</b><small>来源事实</small></div><div class="fig-box tone-green"><b>Registry / Object Store</b><small>正式内容/摘要事实</small></div><div class="fig-box tone-green"><b>Publication DB</b><small>当前生效和阻断事实</small></div></div><figcaption>不允许把门户索引、CDN 或 `latest` 当成权威；完整 digest 链可核验来源、引用闭包、构建工具及最终制品。</figcaption></figure>

| 内容对象 | 必须固化的证据 | 何时不可变 | 失败语义 |
|---|---|---|---|
| Git SourceSnapshot | repository id、commit SHA、path、采集主体、原文 digest、采集时间 | 导入快照建成后 | Git 指针移动不改写快照；不可读取原提交则 `SOURCE_UNAVAILABLE` |
| Upload SourceSnapshot | 上传操作者、输入 digest、受控文件集合、采集时间 | 导入确认后 | 文件格式/安全检查失败不得成为可发布 Revision |
| Revision | asset_id、source_snapshot_id、原文 digest、引用闭包 digest、规范声明、sequence | 成功登记时内容不可变 | 校验失败保留诊断，不伪造 APPROVED |
| CheckRun | revision_digest、policy_digest、ruleset/tool versions、结果快照 | CheckRun 完成后 | worker 崩溃可重试新 attempt，不改写旧完成事实 |
| ReviewDecision（CR-0001 之前的历史） | decision actor、角色证据、范围、target class、有效期、CheckRunRef | 决策记录提交后 | 只读保留；CR-0001 起不再产生 |
| Release | release_id、revision_id、check_run_id 和豁免（CR-0001 之前为 decision refs）、build_manifest_digest、artifact_digest、signing ref（可选） | READY/FAILED 终态按不变量冻结 | 可读性/摘要失败不得切 READY |
| PublicationSlot | target_id、asset_id、active_release_id、generation、visibility、block ref | 历史 Operation 不可变；current pointer 可 CAS 修改 | CAS 失败拒绝，不强行覆盖并发赢家 |
| Projection | slot generation、source release digest、projection built_at | 只读派生快照，可替换 | 索引失败可重建，不改变 Slot 事实 |

**重要细节**：`source_digest`（原始字节）≠ `reference_closure_digest`（固定引用集合）≠ `artifact_digest`（发布文件）≠ `policy_digest`（检查规则）。即使内容规范化后语义相同，也不可用一个摘要掩盖来源差异。算法使用具名标识（候选 SHA-256），算法替换要保留历史可核验性。

## 7. 多维访问范围与关系模型

<figure class="architecture-figure"><div class="fig-label">FIGURE 06 · 多维授权所需的实体关系（并非完整 ABAC 策略）</div><div class="fig-grid cols-3"><div class="fig-box tone-light"><b>Principal / InternalOrg</b><small>来自 IdP · 由 Membership 建立 Workspace 范围</small></div><div class="fig-box fig-primary"><b>Product → Workspace → APIAsset</b><small>管理域 · 不跨产品隐式继承</small></div><div class="fig-box tone-light"><b>DeveloperOrg → App</b><small>V1 · 身份与 ClientRef 为外部权威</small></div></div><div class="fig-arrow">↓ Product + Audience + Environment + Portal 决定 Target；Target + Asset 决定 Slot</div><div class="fig-grid cols-2"><div class="fig-box"><b>PublicationSlot</b><small>当前 Release、generation、access block</small></div><div class="fig-box"><b>AccessGrant / Membership</b><small>显式范围、状态、有效期、issuer</small></div></div><div class="fig-arrow">↓ 所有搜索、详情、下载、缓存回源必须做统一 deny-first 权限判断</div><div class="fig-head light"><strong>Authorized Portal Read = valid identity + scoped grant/public policy + ACTIVE slot + no block</strong><small>最后是否可执行生产 API 请求，仍由该业务产品的 IAM/Gateway 单独决定</small></div><figcaption>这是授权数据模型的表达能力，而非 F05 的最终策略实现。匿名访问走单独的 public 发布配置；没有 Grant 的内部/外部同名组织不能相互代替。</figcaption></figure>

### 7.1 授权关系及拒绝优先决策

外部文档读取必须满足以下设计期谓词：

`allow_read(principal,target,asset,release) = target.is_enabled AND slot.active_release_id == release.id AND slot.visibility == ACTIVE AND NOT effective_deny(target,slot,release) AND valid_scope(principal,product,audience,environment) AND policy_allows(principal,resource)`。

公开匿名访问的 `valid_scope` 通过显式 `public` 策略处理；受限文档依赖组织/应用/Grant 的可信有效状态。任何身份、来源、策略或外部授权状态无法确认时执行 `DENY` 或返回明确的 `UNAVAILABLE`，而不是乐观放行。F05 负责完整 RBAC/ABAC、风险评估与 HTTP 行为约定。

### 7.2 跨产品负面样例

**DESIGN_EXAMPLE**：`CargoPai/product_cp` 与 `Parcel/product_px` 各注册了名为 `Shipment API` 的 Asset，ID 分别是 `api_cp_ship` 与 `api_px_ship`。即使开发者在两个产品使用相同邮箱、公司名称或 OAuth issuer，若仅获 Parcel Grant，访问 `CargoPai+partner+sandbox` 的私有目录、版本下载、内部搜索及历史 Release 必须失败；错误信息不得泄露 CargoPai 私有 API 名称或摘要。回传 `404/403` 的具体信息披露策略归 F05，不能在此冒充已经实测通过。

## 8. 四类状态机及事务性状态边界

<figure class="architecture-figure"><div class="fig-label">FIGURE 07 · 四条独立生命周期 / 不共用单一 status</div><div class="fig-grid cols-2"><div class="fig-lane"><strong>Revision</strong><span>DRAFT → IN_REVIEW → APPROVED / REJECTED</span><div class="fig-mini">撤回在审：WITHDRAWN（显式受控状态）</div><div class="fig-mini">CR-0001 起停在 DRAFT，不再转移</div><div class="fig-mini">变更内容：创建新 Revision，不修改原文</div></div><div class="fig-lane"><strong>Release</strong><span>PREPARING → READY / FAILED</span><div class="fig-mini">READY 内容不可变</div><div class="fig-mini">公开撤销只影响 Slot/Block，不“删除 Release”</div></div><div class="fig-lane"><strong>Publication（Slot 当前状态）</strong><span>EMPTY / REQUESTED / PREPARING / ACTIVE / WITHDRAWN / FAILED</span><div class="fig-mini">Operation 与传播 DeliveryState 分离</div><div class="fig-mini">ACTIVE 指主库逻辑生效，不假称 Edge 已验证</div></div><div class="fig-lane"><strong>APIAsset Lifecycle</strong><span>EXPERIMENTAL → STABLE → DEPRECATED → RETIRED</span><div class="fig-mini">废弃不自动解除所有历史发布</div><div class="fig-mini">Retired 阻止新激活，历史依策略保留</div></div></div><figcaption>F03 增加修订 WITHDRAWN 分支和 Publication EMPTY（无有效版本）用于表达业务空态；不修改 F02 的主状态语义。所有转换见 §8.1～§8.4。</figcaption></figure>

### 8.1 Revision 状态转换（基于内容不可变）

| 从 → 到 | 触发者 / 命令 | 守卫与拒绝条件 | 幂等与证据 |
|---|---|---|---|
| 创建 → DRAFT | 授权 Engineer/CI 注册修订 | 来源可信、引用闭包固定、Asset/Product 匹配；失败不得产生半成品正式修订 | 同导入指纹可返回相同结果；来源/摘要记录 |
| DRAFT → IN_REVIEW | Engineer `SubmitForReview` | 草稿校验可完成、内容 digest 固定、状态版本匹配 | 记录操作者、规则版本和关联请求 |
| IN_REVIEW → APPROVED | 有权且满足职责分离的 Reviewer | CheckRun 匹配输入和策略、重大失败不可豁免、批准作用域明确 | 追加 ReviewDecision；Revision state CAS |
| IN_REVIEW → REJECTED | Reviewer | 有拒绝权限，拒绝理由存在 | 审批事实不可变，重交创建新 Revision |
| IN_REVIEW → WITHDRAWN | 原提交人/具名治理员 | 尚未有效批准；没有后续 Release 依赖 | 保留历史，不能就地修改原文 |

注意：批准的最终权威是 `ReviewDecision`，Revision 上的 `APPROVED` 是关联状态，不足以单独授权任意发布；同 Revision 若需扩展外部受众，应补充目标范围审批。

已实现的转换见 §5.9：比上表多一条 `WITHDRAWN → IN_REVIEW`（撤回后再次提交），撤回只有申请人能做。已批准修订的补充审批由复审实现（§5.11）：它给修订增加一条 `ReviewDecision`，修订的状态不因此变化，所以上表没有从 `APPROVED` 出发的转换。

CR-0001 起上表的转换都不再发生：修订登记为 `DRAFT` 后不再转移，Release 依据检查准备，与修订状态无关。迁移把仍为 `IN_REVIEW` 的修订改为 `WITHDRAWN`（§5.15）。`APPROVED`、`REJECTED`、`WITHDRAWN` 是此前评审留下的状态。

### 8.2 Release 状态转换

| 从 → 到 | 命令和前置条件 | 失败行为与边界 |
|---|---|---|
| 不存在 → PREPARING | `PrepareRelease`；绑定 revision 的一次现行策略下的检查及其豁免（CR-0001 之前为已批准 revision 和有效范围）、唯一 build fingerprint | 重复同指纹返回现有工作；同 release label 不同内容冲突 |
| PREPARING → READY | 固定构建输入、制品摘要与对象存储读取复核、可核验的检查证据齐备 | 只有一次成功定稿；历史 metadata/manifest 不可覆盖 |
| PREPARING → FAILED | 校验/构建/存储不可恢复或超限 | 保留诊断；重试需新 Attempt 或新 Release 操作，不改写 READY |
| READY →（无内容变更） | 无允许内容状态写入 | 撤销使用 Publication/AccessBlock；遗失对象进入独立 IntegrityIncident 而非篡改 READY 记录 |

### 8.3 Publication 状态、指针与撤销

| 条件与迁移 | 业务命令 / 主事务 | 并发与可见性要求 |
|---|---|---|
| EMPTY/WITHDRAWN → REQUESTED | `RequestPublish(slot,release,expected_generation)` | 检查 Product、Asset、受众/环境、Release 依据的检查（替换另一修订时须以其为对比基线）和 deny；登记操作 |
| REQUESTED → PREPARING | `StagePublication` | 先准备受控内容与交付依赖，不使未准备的内容可见 |
| PREPARING → ACTIVE | `ActivateSlot` | 一次 DB CAS 设置 `active_release_id`、递增 generation，并写 Audit+Outbox；外部交付另记状态 |
| ACTIVE → ACTIVE（替换新版本） | `ReplaceRelease(slot,new_release,expected_generation)` | 只一个条件写赢；失败者读取最新 generation 后重新确认，不能自动覆盖 |
| ACTIVE → WITHDRAWN/EMPTY | `WithdrawPublication` | 先启用 deny / 服务端拒绝，CAS 清空/移除生效指针；旧 Release 保留 |
| WITHDRAWN → ACTIVE（恢复） | `RestoreApprovedRelease`（CR-0001 起不再要求批准） | 校验槽位历史、目标、授权、摘要、deny 已清除，以及 Release 依据的检查就是它记录的那次；不按现行策略重新检查（CR-0001 决定 5） |
| REQUESTED/PREPARING → FAILED | 预检、对象存储、发布传输或条件写失败 | 操作记失败/重试；Slot 当前版本不能错误切换 |

**分离三种状态**：`slot.visibility_state`（主库逻辑生效）与 `operation.status`（命令处理）及 `delivery.status`（Edge/投影传播）互不相同。`ACTIVE` 不意味着所有 Edge 已传播，也不能作为“网络访问全部安全”的证据。

### 8.4 API Asset 生命周期

| 从 → 到 | 规则与动作 |
|---|---|
| EXPERIMENTAL → STABLE | Owner 提交稳定性证明，经过目标批准；不自动把历史实验性 Release 公开 |
| STABLE → DEPRECATED | 记录替代资产、通知/迁移计划及生效时间；已发布版本仍按策略服务 |
| DEPRECATED → RETIRED | 满足停用门禁，停止新公开发布；历史 Release、审计与法定保留事实留存 |
| 退回/例外 | 不允许通过直接覆写状态规避既有消费者影响；如需恢复，提出独立例外审批和迁移流程（F04） |

## 9. 命令、查询、事件与数据流映射

| 用例 / 命令 | 责任 BC | 授权与聚合 | 同步本地事务 | 异步副作用 / 查询模型 | 阶段 |
|---|---|---|---|---|---|
| `RegisterProduct` / `CreateWorkspace` | Product Governance | PlatformAdmin / ProductAdmin；Product/Workspace | unique + membership validation + audit/outbox | Catalog scope 投影更新 | MVP |
| `GrantProductRole` / `GetProductRole` / `RevokeProductRole` | Product Governance | 产品负责人；平台管理员仅可授予 | 授予/撤销同事务写分配、审计和 Outbox；撤销前锁住有效负责人 | `product_role.changed`；初始授予见 §5.5 | MVP |
| `ListProducts` / `ListWorkspaces` / `ListMemberships` / `ListProductRoles` | Product Governance | 每页重新按当前角色过滤；见 F05 §6.1 | 只读游标分页，不缓存授权、不返回总数；拒绝独立审计 | 无成功读取事件或审计 | MVP |
| `ConfigureAudience` / `ChangeAudienceConfig` / `RemoveAudienceConfig` | Product Governance | 该产品负责人，配置归属该产品 | 校验当前负责人分配；配置、审计和 Outbox 同事务 | `audience_config.changed` | MVP |
| `GetAudienceConfig` / `ListAudienceConfigs` | Product Governance | 按产品可见性读取有效配置，见 F05 §6.3 | 详情或只读游标分页 | 无成功读取事件或审计 | MVP |
| `ChangeProductBrand` | Product Governance | 该产品负责人，按 Product 版本更新 | 锁定当前负责人分配和 Product；品牌、审计和 Outbox 同事务 | `product.brand_changed` | MVP |
| `RegisterAPIAsset` | API Catalog | Product Engineer；APIAsset | asset + owner + audit/outbox | 搜索索引更新 | MVP |
| `ImportContractRevision` | Registry | CI/Engineer；Revision | 通过暂存和 digest 验证后提交 revision + event | 异步 CheckRun（失败可诊断） | MVP |
| `RequestCheckRun` | Policy & Review | Engineer/CI；CheckRun | 注册固定 input/policy 指纹 | Worker 执行、报告制品写入 | MVP |
| `RecordReviewDecision`（CR-0001 起删除） | Policy & Review | 原为独立 Reviewer；ReviewDecision | 已有决定只读保留 | Release 改为依据检查，见 `PrepareRelease` | MVP |
| `PrepareRelease` | Registry + Policy & Review | Release Manager；Release | 检查门禁与豁免（CR-0001）+ 保存 PREPARING + job/outbox | 私有构建、摘要复核后 READY | MVP |
| `ActivatePublication` | Publication | Publisher；PublicationSlot | CAS pointer + operation + audit/outbox | 投影/Edge 异步传播 | MVP |
| `WithdrawPublication` / `Restore` | Publication | EmergencyPublisher；Block + Slot | deny 事实与 CAS/审计（因外部边界分步） | Edge block、缓存失效、回源负向验证 | MVP |
| `GetAuthorizedDocument` | Publication Read + Developer Access policy | Public/Developer；Slot/Release | 只读、读时验证 ACL 和 deny | 可读取已授权 Release / projection | MVP |
| `CreateDeveloperApp` / `RequestGrant` | Developer Access | DeveloperOrg Admin；App/Grant | 组织/产品范围 + workflow status | 外部 IAM/Gateway 适配确认 | V1 |
| `ReconcilePublicationDelivery` | Operations | Worker/Operator；Job | 结果记录 + audit/outbox | 重试/修复投影但不变更未经授权的 Slot | MVP |
| `InspectDrift` | Integration adapter / Operations | Authorized Operator；snapshot | 存证来源与 captured_at | 比对运行时快照、未知状态正确呈现 | V1 |

查询不得产生暗含写入，出站事件必须包含 `event_id`, `aggregate_id`, `aggregate_version`, `occurred_at`, `product_id`, `correlation_id`, `schema_version`；其字段字典由 F04 定义。跨 BC 的引用在失败/降级时返回显式状态，不能把不可达解释成“无权限”或“已完成”。

## 10. 统一贯通案例：CargoPai 契约发布

> **以下所有实体 ID、环境、组织、状态和调用均为 `DESIGN_EXAMPLE`，用于验证领域模型，不是现有生产事实或运行测试。** 例子刻意不定义 CargoPai 的 Shipment 业务实体，只管理 `Shipment API` 契约。

### 10.1 角色与样本边界

| 概念 | 示例稳定 ID | 说明 |
|---|---|---|
| Product | `product_cp` | CargoPai；与 `product_parcel` 互不授予权限 |
| Workspace | `ws_cp_api` | CargoPai API 工程 Workspace |
| APIAsset | `api_cp_ship` | Shipment API 资产，归 CargoPai 所属 |
| SourceSnapshot | `srcsnap_0007` | `repo_cp` 的固定 Git commit 和文件快照 |
| Revision | `rev_cp_ship_0042` | 内容与来源 digest 不变 |
| CheckRun | `check_214` | 绑定 Revision Digest 和 PolicySnapshot |
| ReviewDecision | `decision_089` | CR-0001 之前的样例：独立审核员对 `partner/sandbox` 的批准证据。CR-0001 起不再产生，Release 依据 `check_214` 及准备时的豁免 |
| ContractRelease | `rel_cp_ship_1_4_0` | 已验证 manifest/digest 的 READY 制品 |
| PublicationTarget | `target_cp_partner_sandbox` | `product_cp × partner × sandbox × cargopai-developers` |
| PublicationSlot | `slot_cp_ship_partner_sb` | Target × Shipment APIAsset；generation=6（假设） |
| PortalProjection | `projection_cp_ship_7` | 已从第 7 代生效指针构建的只读投影 |

### 10.2 正常路径，明确哪些环节不在同一事务

<figure class="architecture-figure"><div class="fig-label">FIGURE 08 · CargoPai 正常发布（跨域时序，设计示例）</div><div class="fig-grid cols-4"><div class="fig-lane"><strong>1 · CI / Registry</strong><span>Import commit + digest</span><div class="fig-mini">SourceSnapshot → Revision DRAFT</div><div class="fig-mini">审计与任务记录</div></div><div class="fig-lane"><strong>2 · Check</strong><span>CheckRun + PolicySnapshot</span><div class="fig-mini">结果保存 / 门禁判定</div><div class="fig-mini">豁免在准备 Release 时填写</div></div><div class="fig-lane"><strong>3 · Registry</strong><span>Prepare → Verify → READY</span><div class="fig-mini">Artifact Digest + Build Manifest</div><div class="fig-mini">发布制品内容不可变</div></div><div class="fig-lane"><strong>4 · Publication</strong><span>Target + Slot + CAS</span><div class="fig-mini">generation 6 → 7</div><div class="fig-mini">Audit + Outbox 同事务</div></div></div><div class="fig-arrow">↓ Worker 投影、Edge 分发、读时鉴权和外部状态对账；交付成功需独立证据</div><div class="fig-head light"><strong>External Developer Portal · CargoPai / partner / sandbox</strong><small>仅授权开发者看到新 Release；Parcel-only 授权在搜索/详情/下载都被拒绝</small></div><figcaption>外部 Git/对象存储/Edge 不进入 Fabric PostgreSQL 的同一 ACID 事务。每个阶段留下可恢复、可追踪的证据。</figcaption></figure>

按步骤说明：

1. CI 以服务身份导入固定 commit 与路径；Registry 复制并校验原文及 `$ref` 闭包，生成不可变 SourceSnapshot 和 Revision。CI 重试相同指纹返回同一登记结果；同 Key 异内容报冲突。
2. Worker 对固定 `revision_digest + policy_digest` 执行检查，结果不可变。CR-0001 起不设审核员：准备 Release 时按检查门禁判定，没通过的可豁免规则逐条豁免，不可豁免规则没通过则不能准备。
3. Registry 构建 Release 制品包，读取回验证 Artifact Digest，成功才将 Release 标记 `READY`；对象存储异常的制品不能转为 READY。
4. Publication 将 `target_cp_partner_sandbox × api_cp_ship` 解析到既有 Slot；校验同产品、目标策略、Release 依据的检查和主体权限，通过 `expected_generation=6` 的 CAS 原子切换为 Release 并递增到 7，写审计与 Outbox。
5. Worker 创建 portal projection；门户读取时再次根据 Slot 当前代数、deny 和访问范围校验。投影延迟不允许将旧未授权制品公开为新版本。
6. 同门户的 Tracking API 所属 Slot 与 generation 不变；Parcel 组织只具有 Parcel 文档权限，读取 CargoPai 私有制品须拒绝。

### 10.3 发布结果的三级可观察语义

| 层次 | 判定事实 | 不能推断的内容 |
|---|---|---|
| `COMMAND_ACCEPTED` | PublicationOperation 已可靠受理 | 不能声称新版本已生效 |
| `LOGICALLY_ACTIVE` | DB CAS 提交成功，Slot generation 更新 | 不能声称 Edge/CDN 全部传播 |
| `DELIVERY_VERIFIED` | 授权路径、目标内容与禁止访问探测已验证，记录时刻/来源 | 不能声称外部真实 API 的业务行为符合契约 |

## 11. 竞态、异常、失败恢复和证明义务

### 11.1 并发 CAS 与幂等边界

假设两位发布者都读取 Slot generation=6：Publisher A 意图 Release R7，Publisher B 意图 Release R8。A 的条件更新成功，则 Slot generation=7、active=R7；B 更新影响 0 行，收到 `CONCURRENT_MODIFICATION`，必须重新读取状态并再次审核其发布意图。B 不得自动重试时覆盖 R7。

**CAS（表达语义，不是可执行 SQL 或迁移）**：

```sql
UPDATE publication_slot
SET active_release_id = :approved_release,
    generation = generation + 1,
    visibility_state = 'ACTIVE'
WHERE slot_id = :slot_id
  AND generation = :expected_generation
  AND product_id = :authorized_product_id
  AND effective_block = FALSE;
-- affected_rows == 1 才可提交对应 operation、audit、outbox 的同一事务。
```

生产实现可将 `effective_block` 由受控子查询、锁定阻断记录或事务锁策略保证；它**不是**宣称现有表里已经存在该列。业务层必须同时验证 Release 的 Asset/Product/PolicyScope 一致性与当前审核时效。

**幂等作用域**：`(caller/principal, product, operation_kind, idempotency_key)`；绑定规范化请求指纹、作用域目标、保留期限和完成结果。相同 Key + 相同指纹返回原结果，相同 Key + 不同指纹必须拒绝；保留期限、HTTP 返回码由 F04 定义。Worker 的 `event_id`、CAS generation 和 Job fencing 分别防护不同的重复问题，不能合并为单一 “exactly once” 标识。

### 11.2 失败模式矩阵

| 风险/故障 | 最危险错误结果 | 领域保障与恢复路径 | 后续验证 |
|---|---|---|---|
| 并发向同一个 Slot 发布 | 两个版本同时被视为当前 | INV-08；同一 slot/generation 原子 CAS；失败者重新确认 | DM-06 / F07 |
| 检查或批准后 Source 变化 | 用旧检查或旧审核放行新内容 | Snapshot 和 digest 固定，任何变更新 Revision/CheckRun | DM-03 / F04 |
| PolicySnapshot 更新或受众变更 | 旧检查被用来准备新的 Release | INV-07：准备 Release 时要求现行策略下的检查；已有 Release 依其准备时的检查仍可发布和恢复（CR-0001 决定 5）。Release 不再绑定受众，能发布到哪些目标由发布人角色和目标配置决定 | DM-04 / F04/F05 |
| 对象存储成功、DB 提交失败 | 孤儿对象被外部可见 | 私有临时对象、不可猜测/不可公开 URL、对账垃圾回收 | DM-07 / F07 |
| DB 指向 READY，但对象读失败 | 用户获取不可用文档 | READY 前可读性复核；激活前再次验证；运行时标记 IntegrityIncident/受控降级 | DM-08 / F07 |
| Worker 重复事件或 lease 过期 | 重复发布、越权重试 | Outbox at-least-once、消费者 inbox 去重、fencing 和 CAS | DM-09 / F07 |
| 目录索引落后或损坏 | 已撤销 API 被搜索暴露 | 权威读时校验 deny/slot；投影可重建并监控 lag | DM-10 / F05/F07 |
| 紧急公开撤销 CDN 未传播 | 已撤销文档继续可读取 | deny-first、Edge/CDN/Origin 路径封堵与负向探测，状态可显示未验证 | DM-11 / F05/F07 |
| DB 与制品库联合恢复 | 错误版本被恢复到公网 | DB 指针/制品 digest/deny generation 对账；恢复演练 | DM-12 / F07/F08 |
| 开发者 Grant 到期/撤销 | 私有文档仍靠缓存/旧会话可见 | 读时查当前授权，缩短缓存安全边界，撤销事件失效 | DM-13 / F05 |
| 产品停用、Workspace 变更 | 旧成员仍可提交或发布 | Governance 权威同步、写时审查有效范围、deny 优先 | DM-14 / F05 |
| Git 来源不可达 / 外部 IAM 超时 | 旧状态被显示为新事实 | `STALE/UNKNOWN`；新导入失败可恢复；敏感读失败关闭 | DM-15 / F04/F05 |
| 同产品同名 API/重命名 slug | 不同 Asset 被误认为同一 | 稳定 APIAsset ID；product 内唯一 key；名称仅展示 | DM-02 / F03 |
| 历史记录删除/归档 | 审核证明和事故追踪丢失 | Release/Decision/Operation/Audit 保留策略和去标识化分离 | DM-16 / F05/F07 |

### 11.3 紧急撤销的安全优先序

<figure class="architecture-figure"><div class="fig-label">FIGURE 09 · 撤销、恢复和 Edge 传播边界</div><div class="fig-grid cols-5"><div class="fig-box tone-red"><b>1 · Incident</b><small>发现错误公开 / 风险事件</small></div><div class="fig-box tone-red"><b>2 · DENY</b><small>阻断路径/受众/Slot · 确认当前 deny</small></div><div class="fig-box"><b>3 · CAS Withdraw</b><small>清空或切换指针 · 审计 + Outbox</small></div><div class="fig-box"><b>4 · Reconcile</b><small>Edge + Origin + Projection</small></div><div class="fig-box tone-green"><b>5 · Verify</b><small>未授权负向探测 / 事故闭环</small></div></div><div class="fig-arrow">↘ 若 Edge 不可达，保持 FAIL-CLOSED / `BLOCK_PENDING`，不声明撤销已经全域完成</div><div class="fig-head light"><strong>历史 Release 保留供审计；历史公开拷贝不能从第三方设备撤回</strong><small>重新发布必须重新满足有效审核、当前权限及 Block 解除条件</small></div><figcaption>受控读取系统应优先查询高优先级 deny 状态。不能把“发出 CDN invalidate 请求”当成已经证明所有路径不再可读。</figcaption></figure>

安全模型承认客观边界：内容曾合法公开并被用户下载后，平台无法从外部存储设备彻底删除；敏感信息误公开需要按安全事件管理处理，不能将“撤销成功”混同“已经消除所有信息泄露影响”。F05 要定义缓存拓扑、边缘拒绝策略、私有 URL、不可信引用和匿名访问路径。

## 12. Outbox、Job、事件、查询一致性

### 12.1 可靠消息边界

- `DomainEvent`：聚合发生状态变化的内部领域事实；不一定直接公开给其他产品。
- `OutboxEvent`：在同一主库事务中登记的可靠异步交付记录，具有固定 `event_id` / `payload_schema_version` / `correlation_id`，可至少一次投递。
- `IntegrationEvent`：经稳定契约和权限治理后对其他系统发布的事件；V1 的订阅边界由 F04 冻结，不能把表事件直接对外公开。
- `Inbox/DedupRecord`：消费者按 `(subscriber,event_id)` 去重；同一生产者的 `aggregate_version` 可用于检测乱序，但跨不同聚合不保证全局有序。
- `Job`：`PENDING → RUNNING → RETRY_WAIT → SUCCEEDED / FAILED / BLOCKED`，lease 与 fencing token 决定合法执行者；超时不是自动确认外部调用失败，须查询外部结果或人工介入。

<figure class="architecture-figure"><div class="fig-label">FIGURE 10 · 局部 ACID + Outbox + 最终可验证交付</div><div class="fig-grid cols-3"><div class="fig-box tone-green"><b>Transaction / PostgreSQL</b><small>Slot CAS + Operation + Audit + Outbox</small></div><div class="fig-box"><b>Worker / Lease</b><small>at-least-once · fencing · retries</small></div><div class="fig-box tone-light"><b>Portal / Edge / Index</b><small>投影、传输、回源探测、DeliveryState</small></div></div><div class="fig-arrow">↓ 每一步有状态和证据；消费者幂等；失败进入对账/人工介入；不宣称全域 Exactly-Once</div><div class="fig-grid cols-2"><div class="fig-box tone-red"><b>不可接受</b><small>DB 已切换但外部门户错显示“所有节点已同步”</small></div><div class="fig-box tone-green"><b>可恢复</b><small>DB 生效 + 投影延迟 / DELIVERY_PENDING + 重试与告警</small></div></div><figcaption>Slot 指针是业务生效权威；交付证据只是“谁在什么时候看到什么”的运行状态，不反向篡改业务发布历史。</figcaption></figure>

### 12.2 设计事件目录及所有者

| 事件名（候选） | Owner / 触发条件 | 主体引用 / 关键字段 | 事件接受者及限制 |
|---|---|---|---|
| `ProductRegistered` | Governance / Product 创建成功 | product_id、owner_ref、version | Catalog 建立可见范围；不可把 Product 视为业务 Tenant |
| `ProductRoleChanged`（已实现为 `fabric.productgovernance.product_role.changed`） | Governance / 后续负责人授予、撤销 | product_id、role_assignment_id、role、in_force、aggregate_version | 同事务 Outbox；不含主体；登记时首批负责人不单独发授予事件，见 §5.5 |
| `AudienceConfigChanged`（`fabric.productgovernance.audience_config.changed`） | Governance / 新增、变更文档入口、移除配置 | product_id、audience_config_id、audience、environment、in_force、aggregate_version | `aggregate_id` 为配置 ID；文档入口通过授权后的配置详情读取，移除通过 `in_force=false` 表示 |
| `ProductBrandChanged`（`fabric.productgovernance.product.brand_changed`） | Governance / 品牌实际变更 | 通用事件字段，`aggregate_id` 为产品 ID，`aggregate_version` 为产品版本 | 品牌内容通过授权后的产品详情读取 |
| `APIAssetRegistered` | Catalog / Asset 创建成功 | product_id、asset_id、owner_ref | Registry 允许登记契约；事件重复要去重 |
| `ContractRevisionRegistered` | Registry / Revision 成功登记 | revision_id、asset_id、source_digest | Check Workflow 建立任务；不等于批准 |
| `GovernanceCheckCompleted` | Policy / 检查终态 | check_run_id、policy_digest、revision_digest | 检查详情与准备 Release；失败证据不得默认隐藏 |
| `ReviewDecisionRecorded` | Policy / 审核决定写入（CR-0001 起不再产生） | decision_id、target_scope、expires_at | 原为 Registry Release 前验收；已发出的事件保留 |
| `ContractReleasePrepared` | Registry / Release 转 READY | release_id、artifact_digest、revision_id | Publication 可作为候选；无自动公网权 |
| `PublicationActivated` | Publication / Slot CAS 成功 | slot_id、generation、release_id、target_id | Portal projections；投递失败不撤销主库事实 |
| `PublicationWithdrawn` | Publication / Slot 清空/切换 | slot_id、generation、reason_code | Edge/投影清理；无保证全部缓存立即生效 |
| `AccessBlocked` | Publication / 生效 deny | block_id、scope、block_generation | Edge/读侧必须高优先级反映 |
| `DeliveryVerified` | Operations / 传播验证完成 | target_id、slot_id、source_generation、checked_at | 运营视图；不是新主发布事实 |

F04 负责将这些事件从候选业务含义转化成 AsyncAPI 或其他机器契约，并明确兼容性、事件载荷禁止字段、重放时效和渠道；F03 只锁定事实、责任和顺序约束。

### 12.3 Outbox 与业务事务是否同库

推荐的 Go 模块化单体候选可让 Publication 的 Slot、Operation、Audit 和 Outbox 在 PostgreSQL 中具备局部原子性；如果未来 BC 迁移到独立数据库，需重新评估该协议的可靠传输、事务边界与恢复机制，并发布 ADR。**不得仅因为不同领域共享一个数据库，就把跨 BC 的每一条业务流程全部塞进一个超大事务**。

## 13. 数据完整性、删除、历史、保留与重建

| 数据分类 | 原则 | 绝不能执行的行为 | 待 F05/F07 确认 |
|---|---|---|---|
| Revision 原文及引用闭包 | 原字节与摘要固定；存储路径只作为定位 | 用新上传覆盖旧 Revision blob | 生命周期、存储加密、归档 |
| READY Release/Manifest | 不可变、可内容核验 | 通过改写对象恢复某个历史版本 | WORM/签名/长期存储成本 |
| ReviewDecision、PublicationOperation、Audit | 追加式记录、关联 Trace/主体 | 常规更新历史审核结论或删除安全证据 | 最短保留期、个人信息最小化 |
| Product/Workspace/Membership | 可停用、关闭授权，保留必要历史 | 停用后仍允许新发布或跨产品授权 | 删除/迁移机制、RLS |
| ConsumerApp/Grant（V1） | 有效期限与撤销记录显式 | 仅删前端应用按钮却不撤销 IAM/Gateway 权限 | 双系统对账与责任 |
| PortalProjection / Index | 可重建，带源版本和新鲜度 | 以投影代替主库授权事实 | lag、重建耗时、缓存失效 |
| Job / Temp Artifacts | 以幂等与保留边界进行清理 | 清理仍被 READY Release 引用的对象 | GC、安全扫描、存储监测 |

### 13.1 归档和一致性恢复检查点

恢复至少比较 `(product, target, slot, generation, active_release_digest, effective_deny)` 与经验证的 ArtifactManifest，确保不可变数据可读、引用闭包可解释、拒绝状态优先于恢复后的旧发布。需要从 DB/对象存储备份一致的恢复点出发，不应只恢复某一侧就对外开放。`RPO/RTO` 数字、恢复责任、演练样本与可用性证明由 F07/F08 批准，当前 `TBD`。

## 14. F01/F02 需求追溯及关联证明

### 14.1 可执行追溯资产

| 资产 | 完整性约束 | 证据当前状态 |
|---|---|---|
| `tests/acceptance/F03_domain_trace.csv` | 包含 FR-001…071、NFR-01…18，每项指向 F03 子域/不变量或后续归属；不强行生成对应表 | 设计映射完成；实际验收 `NOT_RUN` |
| `docs/F03_Logical_Model.csv` | 列出核心逻辑实体、主键、唯一性与归属、关联关系和阶段 | 设计字典；不代表执行迁移 |
| `tests/acceptance/F03_domain_scenarios.csv` | DM-01…DM-18，注明 F01 / F02 关联、预期结果与状态 | 全部 `NOT_RUN` |
| `decisions/ADR-0001…0006` | 保留 `Proposed`，新增澄清登记见 §16 | 非批准证据 |

### 14.2 F03 设计验证场景（Definition of Verifiability）

| 场景 ID | 可检验的业务结果 | F01 / F02 关联 | 实际状态 |
|---|---|---|---|
| DM-01 | CargoPai/Parcel 可以登记同名 Asset，身份、归属分开 | FR-001/007、AR-01 | NOT_RUN |
| DM-02 | 同 Product 重复 asset_key 被拒绝，改名不影响稳定 ID | FR-007、AI-01 | NOT_RUN |
| DM-03 | 修改 Git commit 内容不会改写既有 Revision；新输入须重新检查（CR-0001 之前为重审） | FR-013/016、AR-03 | NOT_RUN |
| DM-04 | 策略和受众变化时旧批准不可被滥用；CR-0001 起改为：策略变化后，准备新 Release 须用现行策略下的检查 | FR-026/027、AI-02 | NOT_RUN |
| DM-05 | 不可读/摘要不匹配的 Artifact 不可成为 READY Release | FR-017、AR-05 | NOT_RUN |
| DM-06 | 同 Slot 并发 CAS 只允许一个生效版本 | FR-033/036、AR-04 | NOT_RUN |
| DM-07 | 对象写成但 DB 失败不发生公开，可回收孤儿对象 | FR-017/064、AR-05 | NOT_RUN |
| DM-08 | READY 制品运行时缺失时拒绝正常发布并告警 | FR-017/035、AI-05 | NOT_RUN |
| DM-09 | Worker 重投不引起重复生效，lease 旧主不得提交 | FR-061/064、AR-07 | NOT_RUN |
| DM-10 | 索引滞后/损坏不能使已撤销私有契约外泄 | FR-008/063、AR-08 | NOT_RUN |
| DM-11 | 紧急撤销在所有受控读路径执行 deny，并报告未验证传播 | FR-034/036、AR-06 | NOT_RUN |
| DM-12 | 从备份恢复后 Pointer/Digest/Deny 可对账 | FR-066、AR-10 | NOT_RUN |
| DM-13 | Grant 撤销/到期后私有文档不可继续访问 | FR-049/053、AI-03 | NOT_RUN |
| DM-14 | 暂停 Product/Workspace 后无新的不当创建或发布 | FR-006/063、AI-01 | NOT_RUN |
| DM-15 | 外部 IAM/Git 不可达时不报告虚假 PASS/授权 | FR-059/060、AI-06 | NOT_RUN |
| DM-16 | 合规删除与审计保留保持可追溯且按策略最小化个人信息 | FR-062/068、NFR-17 | NOT_RUN |
| DM-17 | 同一门户同时展示 Shipment 和 Tracking API，不互相覆盖版本 | FR-009/035/047、AI-04 | NOT_RUN |
| DM-18 | MVP 文档访问与 V1 实际沙箱调用严格区分，不能假报真实调用成功 | FR-048/051–053、AI-07 | NOT_RUN |

### 14.3 与上位六道门禁的关系

- **STD-CP1**：项目 Profile 与责任人采用记录（F01 Manifest 仍草案）。
- **STD-CP2**：本 F03 的 Context Map、事实权威、聚合/状态机/反例；待领域专家具名复核。
- **STD-CP3**：F04 契约规则、本稿并发与恢复语义、F07 实测；待执行。
- **STD-CP4**：F06 双门户用户任务和无障碍真实测试；待执行。
- **STD-CP5**：F07 容量、并发、故障和安全证据；待执行。
- **STD-CP6**：F08 生产上线、运营监测、签收及真实反馈；待执行。

F03 不将“有规则映射”误当成“通过门禁”。任何发现与上位规范的冲突必须登记责任人和裁决，而不是在 F03 静默放宽。

## 15. 双门户最小读模型和协作输入（仅领域视角）

| 门户功能 | 需要的读模型 | 数据权威与拒绝条件 | F06 继续完成的工作 |
|---|---|---|---|
| 内部产品切换 | WorkspaceNavigationView | Product Governance Membership、状态 | 导航、角色任务、错误/空态 |
| API Catalog 筛选 | AuthorizedCatalogView | Catalog Asset + scopes；无权资产不能进入响应 | 表格、筛选器、批量操作 |
| 契约版本与检查 | ContractReviewView | Registry Revision + CheckRun；历史评审只读 | Diff 呈现、检查与准备 Release、可理解错误（CR-0001 起没有审核流程） |
| 发布工作台 | PublicationTimelineView | Publication Slot + Operation + DeliveryState | 版本比较、并发冲突、回滚 UX |
| 外部 API Reference | AuthorizedDocumentView | Active Slot + Release + AccessPolicy + Deny | 多品牌导航、参考文档、代码示例 |
| 开发者应用/订阅（V1） | DeveloperApplicationView | Developer Access Grant + IAM 快照 | OAuth 说明、沙箱/生产操作旅程 |
| 运营告警 | OperationsIncidentView | Job、Audit、Reconcile | 值守信息架构、恢复提示 |

F06 不得把两个门户合并为一套相同的功能菜单，但可共享设计组件和文档渲染引擎。门户查询模型不具有直接修改任何 BC 业务事实的权力。

## 16. 设计差异、未决问题与 ADR 接续

| 决策 ID | 需要具名确认的事实或取舍 | 此次默认设计提案 | 验证归属 |
|---|---|---|---|
| OD-01 | Product / TenantRef / DeveloperOrganization 实际身份映射 | 单独身份 + 显式绑定；不以名称合并 | F03 领域 Owner + F05 |
| OD-02 | 七个限界上下文最终是否需要拆合 | 暂保持 F02 七候选 BC，不做物理服务拆分 | 领域评审 / ADR-0002 |
| OD-03 | PublicationTarget 的多 API 生效粒度 | 已统一 `Slot = Target × APIAsset` 的候选定义；ADR-0004 仍 Proposed | 架构 Owner / F04 |
| OD-04 | 多 Release 基于同 Revision 的发布版本策略 | Release 可独立 build，但同 Asset release_label 唯一 | F04 / F07 |
| OD-05 | 审核有效期、作用域和豁免优先级 | 输入、策略、范围、时效绑定，不允许安全强制项豁免。CR-0001 起不再有审核：豁免在准备 Release 时逐条填写，随 Release 记录，没有有效期和作用域 | F04 / F05 |
| OD-06 | Edge deny 的生效协议与传播证明 | deny-first；网络不可达不得假报已完成 | F05 / F07 |
| OD-07 | 外部 developer Org、IAM/网关真实主权关系 | Fabric 存接入申请/关系，真实密钥/调用权归外部 | F05 / F06 |
| OD-08 | PostgreSQL、对象存储、锁/Job 策略、保留和容量目标 | 仅逻辑候选；技术与数值保持 TBD | F07 / F08 |
| OD-09 | 历史审计中的个人信息与删除请求冲突 | 追加证据 + 个人信息最小化、分离去标识化 | F05 / 法务 |
| OD-10 | 风险等级、具名责任人与 Profile 采用 | 继承 F01 Manifest Draft，不宣称批准 | F08 / 项目治理 |

CR-0002（2026-11-26 已决定）裁决了实现与设计不一致的四处，不新增也不关闭上表的条目：按编号下载看这个 Release 的阻断（§5.12）；审计员看到行动人的原值，OD-09 的删除请求冲突仍然开着；平台管理员留在部署配置（§5.13）；`AssetSearchIndex` 资产数量需要时再建（§3.3、§5.13）。

**设计细化声明**：F03 在原始架构候选阶段显式引入 `PublicationSlot`、`SourceSnapshot`、`AccessBlock`、Approval Evidence Scope 和 Portal DeliveryState 的细化语义；这些是为满足现有 FR 和 AI 不变量提出的领域设计。F02 和 ADR-0004 的候选正文已统一 Slot 粒度；若批准人否决该方案，必须重新启动 F02–F08 的影响分析并更新相应 ADR。当前仅完成文档一致性修订，未取得正式架构批准。

## 17. F04–F08 责任边界与顺序交接

| 下阶段 | 必须承接本文 | 此处刻意没有做的工作 |
|---|---|---|
| **F04 — API Contract Governance** | 修订/审查/Release/Publication 契约语义、事件字段/策略快照、Digest 规则和幂等键 | 精确 OpenAPI/AsyncAPI Schema、HTTP 错误码和兼容性算法尚未定稿 |
| **F05 — Security & Multitenancy** | 多维身份/租户关系、deny-first、敏感文档与 Edge 阻断事实、审计 | 正式 RBAC/ABAC、威胁模型、缓存与密钥安全策略待设计 |
| **F06 — Dual Portal Product & UX** | 两个门户的独立 read models、检查/发布和接入旅程所需状态（CR-0001 起没有审核） | 高保真页面、组件、交互、WCAG 全量验证待完成 |
| **F07 — Engineering & Quality** | 逻辑 ERD、CAS、Outbox/Job、Index、对象存储恢复义务 | 实际 Go 包、DDL/Alembic 之外的迁移实现、PoC/压测/灾备演练未执行 |
| **F08 — Delivery & Acceptance** | DM-01…18、上位门禁映射与签收界面 | 正式测试、风险裁决、SLO/RPO/RTO 签署与生产上线未执行 |

不得将 F03 的物理实现假设反向强加于 F01 产品需求或 F02 推荐架构。每一需要更改上游边界的决策，先形成可审计的架构/需求差异记录。

## 18. 参考来源与采用边界

- [F01 Product Requirements](F01_Product_Requirements.md)：范围、FR/NFR/AT、双门户和 MVP 阶段的正式输入（内容定稿，待签收）。
- [F02 System Architecture](F02_System_Architecture.md)：AI-01…07、七个候选领域、单一事实权威、不可变发布及技术候选（Proposed）。
- [F03 阶段执行交接](README.md)：输入、禁止漂移、文件路径及验收准则。
- 《IDP Supply Chain DDD Product Engineering Standard V1.0》（IDP-SCG-STD-001）：上位 CORE/TECH/触发 Profile、DDD 不变量、事实权威与 STD-CP1…6。采用仍待具名批准。
- [Domain-Driven Design Reference](https://www.domainlanguage.com/ddd/reference/)：统一语言、聚合与限界上下文的概念参考；项目模型以本稿的可检验规则为准。
- [OpenAPI 3.1.2](https://spec.openapis.org/oas/v3.1.2.html)：F02 建议的工程候选协议，不宣称已通过工具链验证。
- [AsyncAPI 3.1.0](https://www.asyncapi.com/docs/reference/specification/v3.1.0)：V1 异步契约候选，仍需 F04/F07 验证。
- [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457.html)：F04 将定义具体 Problem Details 格式；本文不将领域异常直接视作 HTTP 响应码。
- [OWASP API Security Top 10](https://owasp.org/API-Security/editions/2023/en/0x00-header/)：F05 的威胁建模输入。

## 19. F03 交付完成定义与证据状态

- **设计正文**：本 Markdown 为权威维护源；离线 HTML 由构建脚本生成，不得独立人工编辑。
- **逻辑模型**：已明确定义事实权威、Owner、聚合、不变量、Scope、ERD、状态机与恢复语义；`F03_Logical_Model.csv` 为同一设计的索引而非替代规格。
- **可读性**：至少 10 幅实际服务于设计判定的图表；图、表、命令、关系与不变量引用需自洽；移动端仅局部表格允许横向滚动。
- **追溯**：保留 F01 71 FR、18 NFR；使用 `F03_domain_trace.csv` 显式区分领域落点和后续承接；F03 18 个设计场景均为 `NOT_RUN`。
- **验收状态**：原设计场景与正式门禁保持 `NOT_RUN`。Product Governance 的实现记录见 §5.5–5.6，涵盖负责人、列表、受众配置和品牌。
- **结束条件**：设计可进入具名领域/架构评审；OD-01…OD-10 需由相应责任人处理后再冻结涉及实现的关键决策。F04 可基于该候选领域模型开展契约规范设计，但对未批准的边界必须保留约束与追踪。
