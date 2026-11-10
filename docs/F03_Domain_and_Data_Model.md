# F03 — IDP API Fabric 领域模型与数据设计

> **文档编号**：F03 · Domain & Data Model  
> **项目**：IDP API Fabric (`idp-api-fabric`)  
> **文档基线**：V1.0 · 2026-11-09（沿用既定基线，不另立版本）  
> **状态**：领域与逻辑数据设计完成、待具名评审；数据库实现、架构 PoC、正式标准门禁和业务验收均未执行  
> **权威输入**：[F01](F01_Product_Requirements.md)、[F02](F02_System_Architecture.md)、[阶段交接约束](README.md)、《IDP Supply Chain DDD Product Engineering Standard V1.0》（IDP-SCG-STD-001）  
> **决策约束**：ADR-0001～0006 均为 `Proposed`；F03 不以此文宣称架构已批准。  
> **面向后续**：F04 API Contract Governance、F05 Security、F06 Dual Portal UX、F07 Engineering、F08 Acceptance。

## 0. 执行摘要与设计结论

**领域设计采用“统一治理、多产品自治、单一事实权威”的联邦模式**。CargoPai、IDP Parcel、IDP Brant、IDP Prism 的运单、订单、库存等业务事实由原产品持有；Fabric 管理其 API 资产、契约修订、审查证据、不可变 Release、按访问目标生效的 Publication、外部开发者访问关系及运营证据。**七个候选限界上下文属于语义模型，不等于七个微服务、七个数据库或七个 Go Module。**

本文核心设计决定：

1. **事实权威分离**：`SourceSnapshot` / `ContractRevision` / `ContractRelease` / `PublicationSlot` / `PublicationOperation` / `PortalProjection` 均为不同业务概念。
2. **发布粒度精确**：`PublicationTarget` 指明产品、受众、环境和门户；`PublicationSlot = Target × APIAsset` 管理具体 API 的版本指针，允许一个门户同时展示多个 API。F02 与 ADR-0004 已同步统一此定义；ADR 仍 Proposed，需具名批准。
3. **审核证据绑定输入**：决策引用修订内容摘要、检查运行、策略快照、目标授权范围和角色证明；目标、策略或内容变化不能隐式沿用旧批准。
4. **撤销先阻断**：公开错误发布先进行目标级禁止访问（deny）及服务端可见性拒绝，再在主库以 CAS 撤销/恢复并对账。无法将已经被第三方保存的内容“召回”。
5. **跨产品默认拒绝**：组织与用户的相同名称不构成授权；Product、Workspace、Principal、Audience、Environment 和外部 Consumer Application 的关系必须显式成立。
6. **事务只保证局部原子性**：数据库事务、对象存储、门户和外部 IAM/Gateway 之间不宣称分布式 Exactly-Once；通过持久作业、事务 Outbox、幂等与对账达到可恢复效果。

<figure class="architecture-figure"><div class="fig-label">FIGURE 01 · 七个候选领域与核心事实权威</div><div class="fig-head"><strong>IDP API Fabric · DDD Context Landscape</strong><small>各域拥有自己的业务事实 · 外部依赖通过 Ports / ACL 隔离</small></div><div class="fig-grid cols-3"><div class="fig-box tone-light"><b>Product Governance</b><small>Product / Workspace / Membership</small></div><div class="fig-box tone-light"><b>API Catalog</b><small>APIAsset / Owner / Lifecycle</small></div><div class="fig-box tone-light"><b>Contract Registry</b><small>Source / Revision / Release</small></div><div class="fig-box"><b>Policy & Review</b><small>PolicySnapshot / CheckRun / Decision</small></div><div class="fig-box fig-primary"><b>Publication</b><small>Target / Slot / Operation / Block</small></div><div class="fig-box"><b>Developer Access</b><small>DeveloperOrg / Application / Grant</small></div></div><div class="fig-arrow">↓ 统一审计关联、事务 Outbox、幂等消费者、可重建查询投影</div><div class="fig-grid cols-2"><div class="fig-box tone-green"><b>Platform Operations</b><small>Job / Outbox / Audit / Reconcile</small></div><div class="fig-box tone-green"><b>External Systems（非 Fabric 事实权威）</b><small>Git · IAM · Gateway · Product Runtime</small></div></div><figcaption>箭头表示业务协作，不表示共享写入或单事务跨越所有限界上下文。关系、所有权和契约见 §3。</figcaption></figure>

| 关键要求 | F03 设计对象 | 上游依据 | 后续归属 |
|---|---|---|---|
| 产品自治与隔离 | Product、Workspace、Membership、Scoped Reference | F01 FR-001–006、FR-063；F02 AI-01/03 | F05、F07 |
| 可追溯契约 | SourceSnapshot、Revision、Release、ArtifactManifest | FR-013–020；AI-02/05 | F04、F07 |
| 审查、例外及策略 | CheckRun、PolicySnapshot、ReviewDecision、WaiverRef | FR-023–028 | F04、F05 |
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
- 所有审核证据、正式制品、已执行发布历史为**追加式不可变事实**；修订审核**状态**可以受控转移，但修订的内容快照不能原地覆盖。

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
| ContractRevision | 绑定 SourceSnapshot 的不可变契约内容快照，具独立审核状态 | `revision_id`，`source_digest`；Contract Registry | Release、运行中的 API 部署 |
| PolicySnapshot | 本次检查和审批适用的规则配置及摘要 | `policy_snapshot_id`、`policy_digest`；Policy & Review | 下一次默认可沿用的策略 |
| CheckRun | 在固定 Revision、规则、工具版本上的一次检查执行证据 | `check_run_id`；Policy & Review | 人工审批或生产运行证明 |
| ReviewDecision | 针对固定输入/受众/环境范围的审批事实 | `decision_id`；Policy & Review | Release 生效或 Gateway 授权 |
| ContractRelease | 由批准 Revision 和固定 Build Manifest 导出的不可变正式制品 | `release_id`、`artifact_digest`；Contract Registry | `latest` 或当前 Publication |
| PublicationTarget | 产品、受众、环境、门户组成的受控发布分发目标 | `target_id`；Publication | 单个 API 的当前版本 |
| PublicationSlot | 指定 Target 中某一个 APIAsset 的**唯一逻辑生效槽位** | `slot_id`，唯一 `(target_id,api_asset_id)`；Publication | 具体历史操作或门户索引 |
| PublicationOperation | 针对 Slot 的一次 Publish/Withdraw/Restore 命令及结果 | `operation_id`、idempotency；Publication | 实际 CDN/Edge 同步完成 |
| AccessBlock | 紧急撤销/保密事件的强制拒绝事实与传播状态 | `block_id`、scope、generation；Publication | 对已缓存的第三方文件的物理回收 |
| PortalProjection | 从 Slot/Release/授权产生的可重建只读内容与检索索引 | 版本与来源摘要；read-side | 可以直接修改的发布权威 |
| Job / OutboxEvent | 持久任务与至少一次出站消息记录 | `job_id` / `outbox_id`；Platform Operations | 生产业务订单事件 |

**命名禁则**：不要以单一 `Application` 表同时表示产品、客户端、服务账户；不要以单一 `version` 表同时表示契约修订、Release 业务版本、Slot CAS 代数和代码构建版本；不要以通用 `status` 枚举混用四类生命周期。

## 3. Context Map、事实权威与上下文协作

### 3.1 上下文边界和公开语言

<figure class="architecture-figure"><div class="fig-label">FIGURE 02 · DDD Context Map / 领域协作方向</div><div class="fig-grid cols-3"><div class="fig-box tone-light"><b>Product Governance</b><small>ProductScope & Membership</small></div><div class="fig-box tone-light"><b>API Catalog</b><small>APIAssetRef / Lifecycle</small></div><div class="fig-box tone-light"><b>Contract Registry</b><small>RevisionRef / ReleaseRef</small></div></div><div class="fig-arrow">↓ 显式引用、命令 API 与领域事件（不共享写入）</div><div class="fig-grid cols-2"><div class="fig-box"><b>Policy & Review</b><small>DecisionRef (policy+input+scope)</small></div><div class="fig-box fig-primary"><b>Publication</b><small>Target / Slot / CAS / Block</small></div></div><div class="fig-arrow">↓ 有条件可见的只读投影；审计/投递用 Outbox</div><div class="fig-grid cols-2"><div class="fig-box tone-green"><b>Developer Access</b><small>Org / App / GrantRef (V1)</small></div><div class="fig-box tone-green"><b>Platform Operations</b><small>Job / Audit / Delivery</small></div></div><div class="fig-separator">ACL = Anti-Corruption Layer / 防腐层 · 真实身份、Gateway、Git 和供应链业务在 Fabric 之外</div><figcaption>Published Language 是带稳定 ID 与契约版本的少量跨上下文引用；不直接共享 ORM Entity、不允许其他 BC 修改所属聚合。边界仍需由领域评审确认。</figcaption></figure>

| BC | 独占的业务事实 | 上游依赖与关系 | 对外发布的语言 / 事件 | 禁止的越界操作 |
|---|---|---|---|---|
| Product Governance | Product、Workspace、Membership、ProductAudienceConfig | 外部 IAM 的身份引用经 ACL 映射 | ProductScopeRef、MembershipChanged | 为外部 IAM 签发令牌 |
| API Catalog | APIAsset、AssetOwnership、LifecycleTransition | ProductScopeRef，产品所有者声明 | APIAssetRef、AssetLifecycleChanged | 改写 Release/Publication |
| Contract Registry | SourceSnapshot、Revision、Release、ArtifactManifest | APIAssetRef、Source provider 证据 | RevisionRegistered、ReleasePrepared | 自行决定公网目标是否生效 |
| Policy & Review | PolicySnapshot、CheckRun、ReviewDecision、WaiverRef | RevisionRef、TargetScope、Product policy | ReviewDecisionRecorded、CheckCompleted | 假定 PASS 检查即可部署 |
| Publication | Target、Slot、Operation、AccessBlock、PublicationHistory | Product/Asset/Release/Decision 的只读验证 | PublicationActivated、Withdrawn、Blocked | 修改 Release 内容、修改 IAM 授权 |
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

## 4. 核心聚合模型与不变量

设计以**最小事务一致性边界**为原则。后续逻辑表不强迫每个实体成为聚合根；一行表也不必然等于一个实体。`INV-*` 是 F03 正式设计不变量标识，待实现和测试验证。

| 聚合根 / BC | 内部实体 / 值对象 | 单事务必须保持的关键不变量 | 写入并发机制 |
|---|---|---|---|
| `Product` / Governance | ProductStatus、AudienceConfig、WorkspaceRef | INV-01：产品 ID 唯一；已暂停产品拒绝新登记/发布，历史保留 | 版本比较与唯一约束 |
| `Workspace` / Governance | WorkspaceMembership、RoleAssignmentRef | INV-02：每个 Workspace 仅属一个 Product；成员授权按 Product×Workspace×Principal×Role×有效区间显式授予 | membership version、排他有效记录 |
| `APIAsset` / Catalog | AssetOwnerRef、LifecycleRecord、ApiType | INV-03：一个资产仅属一个 Product/Workspace；生命周期转移可审计，不与 Release 生效状态混淆 | aggregate_version |
| `ContractRevision` / Registry | SourceSnapshotRef、RefClosureManifest、RevisionState | INV-04：Revision 内容与 SHA-256 不变；修改必创建新 Revision | 唯一 ID、来源去重、状态 CAS |
| `ContractRelease` / Registry | ArtifactManifest、ApprovalEvidenceRef、ReleaseState | INV-05：只有绑定有效审核证据、内容可读取且摘要通过才可 READY；READY 后有效内容不能改变 | 创建幂等指纹 + 唯一制品摘要 |
| `PolicySnapshot` / Policy | PolicyRuleRef、PolicyDigest、Scope | INV-06：检查/决定始终引用固定策略快照，不能改写历史规则结果 | immutable record |
| `ReviewDecision` / Policy | CheckRunRef、SubjectScope、DecisionOutcome | INV-07：批准仅对匹配内容、策略、审核范围/目标和有效期有效；豁免不得覆盖不可豁免规则 | append-only decision、审批权限校验 |
| `PublicationSlot` / Publication | ActiveReleaseRef、Generation、VisibilityState、OperationHistoryRef | INV-08：一个 Slot 同一代数只有一个当前 Release 或无；条件更新必须携带 expected generation | DB CAS + unique slot key |
| `AccessBlock` / Publication | TargetScope、Reason、BlockGeneration、PropagationState | INV-09：有效 deny 阻断内容读取及后续激活；只允许显式具权威动作解除 | 单调 block_generation |
| `DeveloperOrganization` / Access (V1) | ApplicationRef、MembershipRef | INV-10：消费者成员和应用归属不能自动穿透产品；敏感 OAuth Client Secret 不落库 | 组织成员版本、有效授权 |
| `AccessGrant` / Access (V1) | ProductRef、Audience、Environment、ApprovalRef | INV-11：只有已审批、未到期且未撤销的 Grant 才能用于受限文档访问判断；真实 API 调用权另由 IAM/Gateway 判断 | grant status CAS、有效期限 |
| `Job` / Operations | RetryPolicyRef、Lease、Attempt | INV-12：任务由单个当前有效 lease/fencing token 执行完成写入；重试不能产生重复生效结果 | lease expiry + fencing token |
| `AuditRecord/Outbox` / Operations | ActorRef、Correlation、EventPayloadRef | INV-13：成功关键写命令必须同事务记录最小审计事实及 Outbox；历史记录追加不可篡改 | append-only、唯一事件 ID |

### 4.1 不变量不应被数据库约束替代

外键可防止孤立引用，却不能证明 Release 的审批匹配目标、Edge deny 已传播或用户有权限。发布前必须由应用层领域政策判断，再由 DB CAS 和唯一约束兜底；安全检查归 F05/F07 验证。`AggregateVersion`（用于乐观并发）与 API 业务版本、Revision 次序、发布指针 `generation` 互相独立。

### 4.2 发布 Slot 的必要精化（待 ADR-0004 批准时确认）

F02 提出的 `PublicationTarget = Product × Audience × Environment × Portal` 是**分发目标**，不足以唯一识别每个目标内的多条 API。F03 新增 **PublicationSlot**：`UNIQUE(target_id, api_asset_id)`，其可变 `active_release_id` 与 CAS `generation` 才是**单 API 的当前版本权威**。一个 CargoPai partner/sandbox 门户可并行呈现 Tracking、Shipment、Rate API，不会在发布 Shipment 后覆盖 Tracking。此属澄清目标粒度，不改变 F02 “单一生效指针、不可变制品、原子 CAS”的不变量；已回填 ADR-0004 候选正文，尚待架构与数据负责人具名批准。

<figure class="architecture-figure"><div class="fig-label">FIGURE 03 · 一个门户目标容纳多个 API Slot / 正确的发布粒度</div><div class="fig-head"><strong>PublicationTarget · CargoPai / partner / sandbox / cargopai-developers</strong><small>唯一 target_id · audience + environment + portal</small></div><div class="fig-arrow">↓ 目标拥有 N 个独立 Slot，每个 Slot 各自 CAS generation 与 current release</div><div class="fig-grid cols-3"><div class="fig-box tone-green"><b>Slot · Shipment API</b><small>active Release R-S1 · generation 7</small></div><div class="fig-box tone-green"><b>Slot · Tracking API</b><small>active Release R-T3 · generation 12</small></div><div class="fig-box tone-light"><b>Slot · Rate API</b><small>无当前 Release · generation 0</small></div></div><div class="fig-arrow">↘ 无权限 / blocked：默认 deny，不读旧缓存；历史 Release 仍可审计但不会自动展示</div><figcaption>全部名称与版本为 DESIGN_EXAMPLE。Slot 与 Target 不等同；恢复命令只作用于目标 Slot，不能影响同目标的其他 API。</figcaption></figure>

## 5. 逻辑数据模型与关系基数

### 5.1 核心逻辑 ERD（不是已执行数据库 DDL）

<figure class="architecture-figure"><div class="fig-label">FIGURE 04 · 逻辑 ERD / 最小核心关系</div><div class="fig-grid cols-3"><div class="fig-lane"><strong>Product Governance</strong><span>Product (PK product_id)</span><div class="fig-mini">1 → N Workspace</div><div class="fig-mini">1 → N PublicationTarget（跨 BC 引用）</div><div class="fig-mini">Workspace 1 → N Membership</div></div><div class="fig-lane"><strong>Catalog & Registry</strong><span>Product 1 → N APIAsset</span><div class="fig-mini">APIAsset 1 → N ContractRevision</div><div class="fig-mini">Revision 1 → N CheckRun / Decisions</div><div class="fig-mini">Revision 1 → N ContractRelease</div></div><div class="fig-lane"><strong>Publication</strong><span>Product 1 → N Target</span><div class="fig-mini">Target 1 → N Slot</div><div class="fig-mini">APIAsset 1 → N Slot（跨目标）</div><div class="fig-mini">Slot 1 → N Operation / History</div></div></div><div class="fig-arrow">↓ 跨域 Ref ID + 受控服务校验；ReleaseRef / DecisionRef 不支持越界更新</div><div class="fig-grid cols-2"><div class="fig-box tone-light"><b>Slot 0..1 → Active Release</b><small>Release 必属相同 APIAsset；主库 CAS generation 为权威</small></div><div class="fig-box tone-light"><b>Target 0..N → AccessBlock</b><small>阻断范围可针对 Target / Slot / Artifact URL；有效 deny 优先</small></div></div><figcaption>本图是逻辑 ERD 关系摘要。完整键、基数、属性和唯一约束见下面数据字典及 `docs/F03_Logical_Model.csv`，不代表物理分表已锁定。</figcaption></figure>

### 5.2 主数据关系及完整性规则

| 关系 | 基数与归属 | 应用不变量与数据库候选约束 |
|---|---|---|
| Product → Workspace | 1:N；Workspace 属于唯一 Product | `UNIQUE(product_id, workspace_key)`；不得从其他产品迁移 Workspace 而保持旧 ID |
| Product → APIAsset | 1:N；APIAsset 属于唯一 Product | `UNIQUE(product_id, asset_key)`，`owner_workspace_id` 必属同 Product |
| APIAsset → Revision | 1:N；Revision 必属一 Asset | `UNIQUE(asset_id, revision_sequence)`；每 Revision 固定 source_snapshot_id |
| Revision → CheckRun | 1:N；每次运行绑定输入与工具 | `UNIQUE(check_run_id)`；不得因重跑覆盖已有结果 |
| Revision → Release | 1:N（允许同修订针对不同发布构建） | `UNIQUE(asset_id, release_label)`（若使用展示标签）；另以固定构建指纹做幂等 |
| Product → PublicationTarget | 1:N；Target 属于唯一 Product | `UNIQUE(product_id,audience_key,environment_key,portal_key)` |
| PublicationTarget × APIAsset → Slot | N:1 + 1:N；同 Product 才能建立 | `UNIQUE(target_id,api_asset_id)`；必须校验 Target.Product = Asset.Product |
| Slot → ActiveRelease | 0..1；仅同 APIAsset 的 READY Release | 对 `slot_id` 加 CAS `generation`；外键不代替审批与安全验证 |
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
| ProductGovernance | Product | ProductAudienceConfig | audience_config_id | product_id | product_id+audience_key+environment_key | N per Product | ACTIVE | MVP |
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
| ContractRegistry | ContractRelease | ContractRelease | release_id | api_asset_id+revision_id+decision_ref | api_asset_id+release_label | N per Revision | HIST | MVP |
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


## 6. 数据权威、制品摘要与可追溯发布链

<figure class="architecture-figure"><div class="fig-label">FIGURE 05 · 单一事实权威与来源追踪 / Import → Release → Portal</div><div class="fig-grid cols-5"><div class="fig-box"><b>SourceSnapshot</b><small>Git Commit/Upload · 原文 digest</small></div><div class="fig-box"><b>Revision</b><small>Immutable content · ref closure</small></div><div class="fig-box"><b>Check + Decision</b><small>Policy digest · scope · review evidence</small></div><div class="fig-box"><b>Release</b><small>READY · immutable artifact manifest</small></div><div class="fig-box fig-primary"><b>PublicationSlot</b><small>CAS pointer · target audience/env</small></div></div><div class="fig-arrow">↓ 主库目标版本权威 → Outbox/Worker → PortalProjection；读时再次按当前策略判定</div><div class="fig-grid cols-3"><div class="fig-box tone-green"><b>Git / Upload</b><small>来源事实</small></div><div class="fig-box tone-green"><b>Registry / Object Store</b><small>正式内容/摘要事实</small></div><div class="fig-box tone-green"><b>Publication DB</b><small>当前生效和阻断事实</small></div></div><figcaption>不允许把门户索引、CDN 或 `latest` 当成权威；完整 digest 链可核验来源、引用闭包、构建工具及最终制品。</figcaption></figure>

| 内容对象 | 必须固化的证据 | 何时不可变 | 失败语义 |
|---|---|---|---|
| Git SourceSnapshot | repository id、commit SHA、path、采集主体、原文 digest、采集时间 | 导入快照建成后 | Git 指针移动不改写快照；不可读取原提交则 `SOURCE_UNAVAILABLE` |
| Upload SourceSnapshot | 上传操作者、输入 digest、受控文件集合、采集时间 | 导入确认后 | 文件格式/安全检查失败不得成为可发布 Revision |
| Revision | asset_id、source_snapshot_id、原文 digest、引用闭包 digest、规范声明、sequence | 成功登记时内容不可变 | 校验失败保留诊断，不伪造 APPROVED |
| CheckRun | revision_digest、policy_digest、ruleset/tool versions、结果快照 | CheckRun 完成后 | worker 崩溃可重试新 attempt，不改写旧完成事实 |
| ReviewDecision | decision actor、角色证据、范围、target class、有效期、CheckRunRef | 决策记录提交后 | 策略/范围变化可能使旧证据不适用 |
| Release | release_id、revision_id、decision refs、build_manifest_digest、artifact_digest、signing ref（可选） | READY/FAILED 终态按不变量冻结 | 可读性/摘要失败不得切 READY |
| PublicationSlot | target_id、asset_id、active_release_id、generation、visibility、block ref | 历史 Operation 不可变；current pointer 可 CAS 修改 | CAS 失败拒绝，不强行覆盖并发赢家 |
| Projection | slot generation、source release digest、projection built_at | 只读派生快照，可替换 | 索引失败可重建，不改变 Slot 事实 |

**重要细节**：`source_digest`（原始字节）≠ `reference_closure_digest`（固定引用集合）≠ `artifact_digest`（发布文件）≠ `policy_digest`（审核规则）。即使内容规范化后语义相同，也不可用一个摘要掩盖来源差异。算法使用具名标识（候选 SHA-256），算法替换要保留历史可核验性。

## 7. 多维访问范围与关系模型

<figure class="architecture-figure"><div class="fig-label">FIGURE 06 · 多维授权所需的实体关系（并非完整 ABAC 策略）</div><div class="fig-grid cols-3"><div class="fig-box tone-light"><b>Principal / InternalOrg</b><small>来自 IdP · 由 Membership 建立 Workspace 范围</small></div><div class="fig-box fig-primary"><b>Product → Workspace → APIAsset</b><small>管理域 · 不跨产品隐式继承</small></div><div class="fig-box tone-light"><b>DeveloperOrg → App</b><small>V1 · 身份与 ClientRef 为外部权威</small></div></div><div class="fig-arrow">↓ Product + Audience + Environment + Portal 决定 Target；Target + Asset 决定 Slot</div><div class="fig-grid cols-2"><div class="fig-box"><b>PublicationSlot</b><small>当前 Release、generation、access block</small></div><div class="fig-box"><b>AccessGrant / Membership</b><small>显式范围、状态、有效期、issuer</small></div></div><div class="fig-arrow">↓ 所有搜索、详情、下载、缓存回源必须做统一 deny-first 权限判断</div><div class="fig-head light"><strong>Authorized Portal Read = valid identity + scoped grant/public policy + ACTIVE slot + no block</strong><small>最后是否可执行生产 API 请求，仍由该业务产品的 IAM/Gateway 单独决定</small></div><figcaption>这是授权数据模型的表达能力，而非 F05 的最终策略实现。匿名访问走单独的 public 发布配置；没有 Grant 的内部/外部同名组织不能相互代替。</figcaption></figure>

### 7.1 授权关系及拒绝优先决策

外部文档读取必须满足以下设计期谓词：

`allow_read(principal,target,asset,release) = target.is_enabled AND slot.active_release_id == release.id AND slot.visibility == ACTIVE AND NOT effective_deny(target,slot,release) AND valid_scope(principal,product,audience,environment) AND policy_allows(principal,resource)`。

公开匿名访问的 `valid_scope` 通过显式 `public` 策略处理；受限文档依赖组织/应用/Grant 的可信有效状态。任何身份、来源、策略或外部授权状态无法确认时执行 `DENY` 或返回明确的 `UNAVAILABLE`，而不是乐观放行。F05 负责完整 RBAC/ABAC、风险评估与 HTTP 行为约定。

### 7.2 跨产品负面样例

**DESIGN_EXAMPLE**：`CargoPai/product_cp` 与 `Parcel/product_px` 各注册了名为 `Shipment API` 的 Asset，ID 分别是 `api_cp_ship` 与 `api_px_ship`。即使开发者在两个产品使用相同邮箱、公司名称或 OAuth issuer，若仅获 Parcel Grant，访问 `CargoPai+partner+sandbox` 的私有目录、版本下载、内部搜索及历史 Release 必须失败；错误信息不得泄露 CargoPai 私有 API 名称或摘要。回传 `404/403` 的具体信息披露策略归 F05，不能在此冒充已经实测通过。

## 8. 四类状态机及事务性状态边界

<figure class="architecture-figure"><div class="fig-label">FIGURE 07 · 四条独立生命周期 / 不共用单一 status</div><div class="fig-grid cols-2"><div class="fig-lane"><strong>Revision</strong><span>DRAFT → IN_REVIEW → APPROVED / REJECTED</span><div class="fig-mini">撤回在审：WITHDRAWN（显式受控状态）</div><div class="fig-mini">变更内容：创建新 Revision，不修改原文</div></div><div class="fig-lane"><strong>Release</strong><span>PREPARING → READY / FAILED</span><div class="fig-mini">READY 内容不可变</div><div class="fig-mini">公开撤销只影响 Slot/Block，不“删除 Release”</div></div><div class="fig-lane"><strong>Publication（Slot 当前状态）</strong><span>EMPTY / REQUESTED / PREPARING / ACTIVE / WITHDRAWN / FAILED</span><div class="fig-mini">Operation 与传播 DeliveryState 分离</div><div class="fig-mini">ACTIVE 指主库逻辑生效，不假称 Edge 已验证</div></div><div class="fig-lane"><strong>APIAsset Lifecycle</strong><span>EXPERIMENTAL → STABLE → DEPRECATED → RETIRED</span><div class="fig-mini">废弃不自动解除所有历史发布</div><div class="fig-mini">Retired 阻止新激活，历史依策略保留</div></div></div><figcaption>F03 增加修订 WITHDRAWN 分支和 Publication EMPTY（无有效版本）用于表达业务空态；不修改 F02 的主状态语义。所有转换见 §8.1～§8.4。</figcaption></figure>

### 8.1 Revision 状态转换（基于内容不可变）

| 从 → 到 | 触发者 / 命令 | 守卫与拒绝条件 | 幂等与证据 |
|---|---|---|---|
| 创建 → DRAFT | 授权 Engineer/CI 注册修订 | 来源可信、引用闭包固定、Asset/Product 匹配；失败不得产生半成品正式修订 | 同导入指纹可返回相同结果；来源/摘要记录 |
| DRAFT → IN_REVIEW | Engineer `SubmitForReview` | 草稿校验可完成、内容 digest 固定、状态版本匹配 | 记录操作者、规则版本和关联请求 |
| IN_REVIEW → APPROVED | 有权且满足职责分离的 Reviewer | CheckRun 匹配输入和策略、重大失败不可豁免、批准作用域明确 | 追加 ReviewDecision；Revision state CAS |
| IN_REVIEW → REJECTED | Reviewer | 有拒绝权限，拒绝理由存在 | 审批事实不可变，重交创建新 Revision |
| IN_REVIEW → WITHDRAWN | 原提交人/具名治理员 | 尚未有效批准；没有后续 Release 依赖 | 保留历史，不能就地修改原文 |

注意：批准的最终权威是 `ReviewDecision`，Revision 上的 `APPROVED` 是关联状态，不足以单独授权任意发布；同 Revision 若需扩展外部受众，应补充目标范围审批。

### 8.2 Release 状态转换

| 从 → 到 | 命令和前置条件 | 失败行为与边界 |
|---|---|---|
| 不存在 → PREPARING | `PrepareRelease`；绑定已批准 revision、有效策略/范围、唯一 build fingerprint | 重复同指纹返回现有工作；同 release label 不同内容冲突 |
| PREPARING → READY | 固定构建输入、制品摘要与对象存储读取复核、可核验批准证据齐备 | 只有一次成功定稿；历史 metadata/manifest 不可覆盖 |
| PREPARING → FAILED | 校验/构建/存储不可恢复或超限 | 保留诊断；重试需新 Attempt 或新 Release 操作，不改写 READY |
| READY →（无内容变更） | 无允许内容状态写入 | 撤销使用 Publication/AccessBlock；遗失对象进入独立 IntegrityIncident 而非篡改 READY 记录 |

### 8.3 Publication 状态、指针与撤销

| 条件与迁移 | 业务命令 / 主事务 | 并发与可见性要求 |
|---|---|---|
| EMPTY/WITHDRAWN → REQUESTED | `RequestPublish(slot,release,expected_generation)` | 检查 Product、Asset、受众/环境、审批/策略和 deny；登记操作 |
| REQUESTED → PREPARING | `StagePublication` | 先准备受控内容与交付依赖，不使未批准内容可见 |
| PREPARING → ACTIVE | `ActivateSlot` | 一次 DB CAS 设置 `active_release_id`、递增 generation，并写 Audit+Outbox；外部交付另记状态 |
| ACTIVE → ACTIVE（替换新版本） | `ReplaceRelease(slot,new_release,expected_generation)` | 只一个条件写赢；失败者读取最新 generation 后重新确认，不能自动覆盖 |
| ACTIVE → WITHDRAWN/EMPTY | `WithdrawPublication` | 先启用 deny / 服务端拒绝，CAS 清空/移除生效指针；旧 Release 保留 |
| WITHDRAWN → ACTIVE（恢复） | `RestoreApprovedRelease` | 重新校验当前策略、目标、授权、摘要、deny 清除的审批条件；不是恢复旧未批准的访问 |
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
| `RegisterAPIAsset` | API Catalog | Product Engineer；APIAsset | asset + owner + audit/outbox | 搜索索引更新 | MVP |
| `ImportContractRevision` | Registry | CI/Engineer；Revision | 通过暂存和 digest 验证后提交 revision + event | 异步 CheckRun（失败可诊断） | MVP |
| `RequestCheckRun` | Policy & Review | Engineer/CI；CheckRun | 注册固定 input/policy 指纹 | Worker 执行、报告制品写入 | MVP |
| `RecordReviewDecision` | Policy & Review | 独立 Reviewer；ReviewDecision | 权限/职责分离 + immutable decision + audit | Release 可引用 approved decision | MVP |
| `PrepareRelease` | Registry | Release Manager；Release | 保存 PREPARING + job/outbox | 私有构建、摘要复核后 READY | MVP |
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
| ReviewDecision | `decision_089` | 独立审核员对 `partner/sandbox` 的批准证据 |
| ContractRelease | `rel_cp_ship_1_4_0` | 已验证 manifest/digest 的 READY 制品 |
| PublicationTarget | `target_cp_partner_sandbox` | `product_cp × partner × sandbox × cargopai-developers` |
| PublicationSlot | `slot_cp_ship_partner_sb` | Target × Shipment APIAsset；generation=6（假设） |
| PortalProjection | `projection_cp_ship_7` | 已从第 7 代生效指针构建的只读投影 |

### 10.2 正常路径，明确哪些环节不在同一事务

<figure class="architecture-figure"><div class="fig-label">FIGURE 08 · CargoPai 正常发布（跨域时序，设计示例）</div><div class="fig-grid cols-4"><div class="fig-lane"><strong>1 · CI / Registry</strong><span>Import commit + digest</span><div class="fig-mini">SourceSnapshot → Revision DRAFT</div><div class="fig-mini">审计与任务记录</div></div><div class="fig-lane"><strong>2 · Review</strong><span>CheckRun + PolicySnapshot</span><div class="fig-mini">结果保存 / 角色分离</div><div class="fig-mini">Decision APPROVED (scope bound)</div></div><div class="fig-lane"><strong>3 · Registry</strong><span>Prepare → Verify → READY</span><div class="fig-mini">Artifact Digest + Build Manifest</div><div class="fig-mini">发布制品内容不可变</div></div><div class="fig-lane"><strong>4 · Publication</strong><span>Target + Slot + CAS</span><div class="fig-mini">generation 6 → 7</div><div class="fig-mini">Audit + Outbox 同事务</div></div></div><div class="fig-arrow">↓ Worker 投影、Edge 分发、读时鉴权和外部状态对账；交付成功需独立证据</div><div class="fig-head light"><strong>External Developer Portal · CargoPai / partner / sandbox</strong><small>仅授权开发者看到新 Release；Parcel-only 授权在搜索/详情/下载都被拒绝</small></div><figcaption>外部 Git/对象存储/Edge 不进入 Fabric PostgreSQL 的同一 ACID 事务。每个阶段留下可恢复、可追踪的证据。</figcaption></figure>

按步骤说明：

1. CI 以服务身份导入固定 commit 与路径；Registry 复制并校验原文及 `$ref` 闭包，生成不可变 SourceSnapshot 和 Revision。CI 重试相同指纹返回同一登记结果；同 Key 异内容报冲突。
2. Worker 对固定 `revision_digest + policy_digest + target_scope` 执行检查；审核员须满足职责分离，形成不可变 ReviewDecision。`PASSED_CHECKS` 不等于 `APPROVED`。
3. Registry 构建 Release 制品包，读取回验证 Artifact Digest，成功才将 Release 标记 `READY`；对象存储异常的制品不能转为 READY。
4. Publication 将 `target_cp_partner_sandbox × api_cp_ship` 解析到既有 Slot；校验同产品、目标策略、审核范围和主体权限，通过 `expected_generation=6` 的 CAS 原子切换为 Release 并递增到 7，写审计与 Outbox。
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
| 已批准后 Source 变化 | 用旧审核批准新内容 | Snapshot 和 digest 固定，任何变更新 Revision/CheckRun | DM-03 / F04 |
| PolicySnapshot 更新或受众变更 | 旧批准被带到不允许的新目标 | INV-07 重新比对 scope/digest/expiry；重新检查/批准 | DM-04 / F04/F05 |
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
| `APIAssetRegistered` | Catalog / Asset 创建成功 | product_id、asset_id、owner_ref | Registry 允许登记契约；事件重复要去重 |
| `ContractRevisionRegistered` | Registry / Revision 成功登记 | revision_id、asset_id、source_digest | Check Workflow 建立任务；不等于批准 |
| `GovernanceCheckCompleted` | Policy / 检查终态 | check_run_id、policy_digest、revision_digest | Review UI；失败证据不得默认隐藏 |
| `ReviewDecisionRecorded` | Policy / 审核决定写入 | decision_id、target_scope、expires_at | Registry Release 前验收；目标变更要重审 |
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
| DM-03 | 修改 Git commit 内容不会改写既有 Revision；新输入须重审 | FR-013/016、AR-03 | NOT_RUN |
| DM-04 | 策略和受众变化时旧批准不可被滥用 | FR-026/027、AI-02 | NOT_RUN |
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
| 契约版本与审核 | ContractReviewView | Registry Revision + Policy Decisions | Diff 呈现、审核流程、可理解错误 |
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
| OD-05 | 审核有效期、作用域和豁免优先级 | 输入、策略、范围、时效绑定，不允许安全强制项豁免 | F04 / F05 |
| OD-06 | Edge deny 的生效协议与传播证明 | deny-first；网络不可达不得假报已完成 | F05 / F07 |
| OD-07 | 外部 developer Org、IAM/网关真实主权关系 | Fabric 存接入申请/关系，真实密钥/调用权归外部 | F05 / F06 |
| OD-08 | PostgreSQL、对象存储、锁/Job 策略、保留和容量目标 | 仅逻辑候选；技术与数值保持 TBD | F07 / F08 |
| OD-09 | 历史审计中的个人信息与删除请求冲突 | 追加证据 + 个人信息最小化、分离去标识化 | F05 / 法务 |
| OD-10 | 风险等级、具名责任人与 Profile 采用 | 继承 F01 Manifest Draft，不宣称批准 | F08 / 项目治理 |

**设计细化声明**：F03 在原始架构候选阶段显式引入 `PublicationSlot`、`SourceSnapshot`、`AccessBlock`、Approval Evidence Scope 和 Portal DeliveryState 的细化语义；这些是为满足现有 FR 和 AI 不变量提出的领域设计。F02 和 ADR-0004 的候选正文已统一 Slot 粒度；若批准人否决该方案，必须重新启动 F02–F08 的影响分析并更新相应 ADR。当前仅完成文档一致性修订，未取得正式架构批准。

## 17. F04–F08 责任边界与顺序交接

| 下阶段 | 必须承接本文 | 此处刻意没有做的工作 |
|---|---|---|
| **F04 — API Contract Governance** | 修订/审查/Release/Publication 契约语义、事件字段/策略快照、Digest 规则和幂等键 | 精确 OpenAPI/AsyncAPI Schema、HTTP 错误码和兼容性算法尚未定稿 |
| **F05 — Security & Multitenancy** | 多维身份/租户关系、deny-first、敏感文档与 Edge 阻断事实、审计 | 正式 RBAC/ABAC、威胁模型、缓存与密钥安全策略待设计 |
| **F06 — Dual Portal Product & UX** | 两个门户的独立 read models、审核/发布和接入旅程所需状态 | 高保真页面、组件、交互、WCAG 全量验证待完成 |
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
- **真实性**：当前只证明**文档构建和静态结构验证**。没有执行生产数据库、服务端 API、权限渗透、并发压测、Edge 全路径撤销、灾备和上位标准正式门禁；不得宣称系统实现或验收通过。
- **结束条件**：设计可进入具名领域/架构评审；OD-01…OD-10 需由相应责任人处理后再冻结涉及实现的关键决策。F04 可基于该候选领域模型开展契约规范设计，但对未批准的边界必须保留约束与追踪。
