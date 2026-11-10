# F02 — IDP API Fabric 总体架构设计

> **文档编号**：F02 · System Architecture  
> **项目**：IDP API Fabric (`idp-api-fabric`)  
> **文档基线**：V1.0 · 2026-11-09  
> **状态**：设计候选基线，已形成可评审的总体架构；具名架构批准、工程 PoC、性能/恢复实测及标准门禁尚未完成  
> **直接上游**：`docs/F01_Product_Requirements.md`；《IDP Supply Chain DDD Product Engineering Standard V1.0》（IDP-SCG-STD-001）  
> **后续承接**：F03 领域与数据模型、F04 契约治理、F05 安全与多租户、F06 双门户、F07 工程质量、F08 交付验收  
> **文档边界**：描述应如何构建，不声明系统已实现、上线、满足 SLO 或通过六道门禁。

## 0. 执行摘要与架构裁决

**建议的 V1.0 目标架构**：一个独立于 CargoPai、IDP Parcel、IDP Brant 和 IDP Prism 的 **联邦式 API 治理控制平面**；业务产品保有独立的领域服务和运行时 API。控制平面优先使用 **Go 模块化单体 + 持久任务 Worker**，共享 PostgreSQL 权威元数据存储、不可变制品存储及可重建的查询索引。双门户以独立授权面访问同一受控能力集合；外部按产品分别发布及展示。

<figure class="architecture-figure"><div class="fig-label">FIGURE 01 · 架构总览 / 控制平面与各产品数据平面</div><div class="fig-stack"><div class="fig-head"><strong>IDP API Fabric — Control Plane</strong><small>Contract Registry · API Catalog · Governance · Publication · Developer Experience</small></div><div class="fig-grid cols-4"><div class="fig-box"><b>内部工程门户</b><small>审批、目录、变更、运营</small></div><div class="fig-box"><b>产品外部门户</b><small>独立品牌、授权文档</small></div><div class="fig-box"><b>CI / Git 接入</b><small>来源校验、契约提交</small></div><div class="fig-box"><b>IAM / Gateway 接入</b><small>身份、订阅与状态同步</small></div></div><div class="fig-arrow">↓ 经由显式权限、版本与发布门禁</div><div class="fig-layer"><b>Fabric 核心</b><span>Registry / Catalog / Governance / Release & Publication / Access / Audit</span></div><div class="fig-grid cols-3"><div class="fig-box tone-green"><b>PostgreSQL</b><small>权威业务元数据 / Outbox</small></div><div class="fig-box tone-green"><b>不可变对象存储</b><small>契约与发布制品</small></div><div class="fig-box tone-green"><b>Search / Read Models</b><small>授权约束的可重建投影</small></div></div><div class="fig-separator">严格边界：Fabric 不执行物流业务命令或替代 Gateway</div><div class="fig-grid cols-4"><div class="fig-box tone-light"><b>CargoPai</b><small>货代领域 API</small></div><div class="fig-box tone-light"><b>IDP Parcel</b><small>跨境小包 API</small></div><div class="fig-box tone-light"><b>IDP Brant</b><small>贸易 API</small></div><div class="fig-box tone-light"><b>IDP Prism</b><small>仓储 / WMS API</small></div></div></div><figcaption>上下两层是责任边界而不是同一网络安全域；各业务产品 API 运行时由其所有者治理。图中的 Gateway、IAM 与 CI 是外部依赖，不表示 Fabric 拥有其执行权。</figcaption></figure>

| 决策主题 | 推荐结论 | 依据 / 约束 | 批准状态 |
|---|---|---|---|
| 平台与产品关系 | 一套共享治理能力、多产品独立契约及独立发布权限 | F01 BG-01/04、FR-001–005、FR-032/047 | ADR-0001 Proposed |
| 服务组织 | 先行 Go 模块化单体；Worker 独立进程，按容量与职责适时拆分 | 领域边界 ≠ 微服务边界；减少初期分布式复杂度 | ADR-0002 Proposed |
| 契约来源 | Git-first；同时允许受控上传，**每一修订仅一种可证明来源** | 版本来源可验、避免双主写入 | ADR-0003 Proposed |
| 发布与检索 | Release 不可变；Publication 可变指针；目录/检索为可重建投影 | 安全撤销、恢复与追溯 | ADR-0004 Proposed |
| 双门户 | 独立安全策略和发布面，复用标准引擎与设计组件 | F01 FR-040–055、NFR-01/15 | ADR-0005 Proposed |
| 外部基础设施 | 通过适配器接入 IAM / Gateway / Git / CI；无自建数据平面 | 最小可行范围、可替换性 | ADR-0006 Proposed |

**严格区分**：这六项是 F02 提出的推荐架构，不等于已取得具名批准。尚未执行 PoC、容量实测、渗透测试或恢复演练的结论必须标记为 `NOT_RUN`，详见第 17 章。

## 1. 设计输入、范围与适用标准

### 1.1 与 F01 的需求对齐

| 需求簇 | 架构责任 | 本文覆盖位置 |
|---|---|---|
| 产品自治、Workspace、身份范围（FR-001–006、NFR-01/02） | 多维隔离、统一 Policy Enforcement Point | §3、§6、§11 |
| Catalog 与 Registry（FR-007–022、NFR-03/04/13） | 资产模型、制品权威、索引分离、版本溯源 | §4、§5、§8 |
| 治理与发布（FR-023–039、NFR-05/12） | 规则执行、风险评估、不可变 Release、Publication、回退 | §7–§9 |
| 双门户（FR-040–055、NFR-14/15） | 独立 BFF/访问面、多品牌发布、UX 责任 | §10 |
| 集成与可靠性（FR-056–068、NFR-06–11/16–18） | 非交互 API、可靠任务、审计、安全、备份恢复 | §12–§15 |
| AI-Native 预留（FR-069–071） | 只声明可扩展的契约引用，不默认开放执行 | §16 |

### 1.2 上位标准的优先级

上位工程规范采用 **CORE / 能力触发 Profile / 项目补充 / 正交 TECH** 的适用模型。Fabric 作为平台，在 F01 的 Project Manifest **草案**基础上预评估 CORE、TECH、GO；BENTO_GO 仅在正式选用且版本被验证时启用。业务专项 Profile（例如 TRACE、TRANSPORT、REGULATORY）由 Fabric **自身实际能力**触发，不因为它管理其他产品的这些 API 就自动适用全部业务执行规则。未来实际提供 Agent 工具发布/受控执行时，重新判断 AI Profile。

本设计引用上位标准的 `SCG-DDD-001/003/005/007/009/010`、`SCG-DATA-002–010`、`SCG-SEC-001–010`、`SCG-UX-001–008`、`SCG-ENG-001–012` 和 `SCG-TECH-001–004` 的适用原则。**具体条件是否触发、规则版本、责任人及例外只能由正式 Manifest 和治理评估确定，不能用本章文字代替**。

### 1.3 工程设计不变量（Architectural Invariants）

- **AI-01 产品自治**：任何写操作均有确定 Product/Workspace 范围；不得通过共享数据库把某产品 API 的内容自动暴露给另一产品。
- **AI-02 契约溯源**：每个正式 Release 必须能校验内容摘要，追到来源修订、签核结果、策略版本和构建摘要。
- **AI-03 私有默认**：无显式授权时，API 详情、搜索、下载、外部门户和索引均不暴露内容；隐藏菜单不能替代服务端拒绝。
- **AI-04 发布原子性**：`PublicationTarget` 仅定义分发范围（Product × Audience × Environment × Portal）；同一 Target 的每个 `APIAsset` 独立拥有 `PublicationSlot`。仅 Slot 持有当前 `active_release_id` 与 `generation`，以 `UNIQUE(target_id, api_asset_id)` 和 Slot 级 CAS 实现原子生效、可审计及幂等重试。不同 API 的 Slot 不应相互竞争；部分成功不得显示为正式生效。
- **AI-05 权威单一**：Git 或受控上传原始内容属于修订来源；Registry 管理正式不可变制品；PublicationSlot 的主库指针管理每 API 生效事实，PublicationTarget 仅定义分发范围；索引不得升级为业务事实权威。
- **AI-06 可恢复**：任何外部依赖短暂故障都应具备可追踪的失败、重试、对账或人工修复路径；不可宣称跨组件 Exactly-Once。
- **AI-07 无越界执行**：API 文档读取权限不蕴含运行时 API 的调用权；Fabric 不在 MVP 内代理生产业务流量。

## 2. 质量属性、架构驱动与假设

质量属性按“业务场景 → 可观察指标 → 验证条件”定义；本稿不捏造已批准的数值阈值。

| 驱动 | 要求 / 设计响应 | 验证责任及状态 |
|---|---|---|
| 隔离 | 搜索、详情、导出、缓存、后台任务统一判定 Product × Workspace × Audience × Environment × Principal | F05；负向矩阵 NOT_RUN |
| 正确性 | 内容寻址的不可变 Release、条件更新 Publication、乐观版本与幂等唯一约束 | F03/F04；并发/故障测试 NOT_RUN |
| 延迟 | 公开读路径与治理写路径解耦；缓存只保存可安全共享的公开制品 | F07；p95、规模与命中率 TBD |
| 可恢复 | DB、对象存储、签名密钥、发布快照联合备份；索引可重建 | F07/F08；RPO/RTO TBD |
| 扩展 | 新产品通过元数据、授权及发布策略接入，不复制整套平台 | F03/F06；第二产品试点 NOT_RUN |
| 安全 | 受限 `$ref`、沙箱网络隔离、敏感内容扫描、发布撤销阻断 | F05；攻击面审查 NOT_RUN |
| 可维护 | DDD 依赖方向、Ports/Adapters、分层测试、ADR 与制品来源 | F07；模块检查 NOT_RUN |
| DX | 门户按任务组织、真实/Mock 明示、错误反馈、双门户键盘可用 | F06；任务测试 NOT_RUN |

**待测规模假设**：产品数量、每产品 API 数量、历史 Release 增长、文件尺寸、搜索 QPS、并发检查任务、公开文档访问峰值和历史保留期限；不得在未完成基线盘点前写入固定容量承诺。

## 3. C4 视图：系统上下文及信任边界

### 3.1 外部参与者与系统关系

<figure class="architecture-figure"><div class="fig-label">FIGURE 02 · C4 Level 1 / 系统上下文</div><div class="fig-grid cols-3"><div class="fig-box"><b>内部研发 / 架构 / 审批人</b><small>SSO · Engineering Portal</small></div><div class="fig-box fig-primary"><b>IDP API Fabric</b><small>发布权威、审查、门户内容</small></div><div class="fig-box"><b>客户 / 集成商 / 合作伙伴</b><small>Product Developer Portal</small></div></div><div class="fig-connect">↕ 身份与审计　 ↕ 契约及发布　 ↕ 受控文档发现</div><div class="fig-grid cols-4"><div class="fig-box tone-light"><b>Git / CI</b><small>提交、凭证证明</small></div><div class="fig-box tone-light"><b>Identity Provider</b><small>认证和令牌</small></div><div class="fig-box tone-light"><b>Gateway / Product APIs</b><small>运行时授权执行</small></div><div class="fig-box tone-light"><b>Object Store / Observability</b><small>制品、日志、监测</small></div></div><figcaption>文档与管理请求进入 Fabric；真实物流业务请求仍指向对应产品的运行时 API，不能绕过该产品独立授权。</figcaption></figure>

### 3.2 数据与网络信任区域

| 区域 | 典型访问者 | 可接触数据 | 强制边界 |
|---|---|---|---|
| Z0 Public | 匿名浏览器 | 标记为 public 的已发布内容 | CDN/Edge + 公开目标 allow 判定；禁止私有资产混入 |
| Z1 Developer Authenticated | 外部开发者组织 | 对应产品与订阅允许的受限文档和应用元数据 | 外部 IAM、对象级授权、产品与环境隔离 |
| Z2 Engineering | 内部成员与 CI 服务账户 | 工作空间、修订、审批与已授权制品 | 内部 SSO、MFA/策略、最小权限、操作审计 |
| Z3 Trusted Control | Go 服务、Worker | 权威元数据、不可变制品、私密发布策略 | 私有网络、工作负载身份、DB/存储最小权限 |
| Z4 Product Runtime | 产品 API、Gateway、沙箱 | 真实业务与运行时状态 | 外部系统独立授权，Fabric 仅有限适配权限 |

跨域的每次操作都显式声明 **caller、delegated actor（如有）、subject、scope、policy version、target**。不能把内部管理员身份默认转换为客户生产 API 权限。网络分区、租户标识、RLS 和应用级授权属于不同防护层，应组合使用并开展绕过验证。

## 4. C4 Level 2：容器与部署职责

<figure class="architecture-figure"><div class="fig-label">FIGURE 03 · C4 Level 2 / 逻辑容器与交互</div><div class="fig-grid cols-2"><div class="fig-box"><b>Engineering Web</b><small>内部 SSO / 授权工作台</small></div><div class="fig-box"><b>Developer Web</b><small>按产品站点/品牌的公开与私有入口</small></div></div><div class="fig-arrow">↓ API + 访问策略（同一逻辑域，独立认证与 BFF/路由策略）</div><div class="fig-head"><strong>Fabric API / Control Plane · Go</strong><small>Catalog · Registry · Governance · Release · Access · Audit · Integrations</small></div><div class="fig-arrow">↓ 事务 / 持久任务 / 出站适配</div><div class="fig-grid cols-3"><div class="fig-box"><b>PostgreSQL</b><small>事务主库、发布指针、Outbox、Job</small></div><div class="fig-box"><b>Artifact Store</b><small>不可变内容、摘要、签名证据</small></div><div class="fig-box"><b>Index / Portal Projection</b><small>可重建、受控查询</small></div></div><div class="fig-arrow">↔ 持久任务调度（至少一次投递）</div><div class="fig-head light"><strong>Validation / Publishing Worker · Go</strong><small>Parse · Lint · Diff · Scan · Build · Index · Reconcile · Audit Export</small></div><figcaption>这里的“容器”指 C4 软件部署单元，不强制表示 Docker 镜像数。MVP 可以在同仓库中部署 API 和 Worker 两种进程，未来视负载和风险独立伸缩。</figcaption></figure>

### 4.1 可部署组件

| 组件 | 职责 | 是否独立伸缩 | 运行域 |
|---|---|---|---|
| `fabric-api` | 登录上下文、命令受理、领域编排、授权、只读查询、发布状态 API | 可以 | 私有控制平面 |
| `fabric-worker` | 解析校验、差异、安全扫描、构建、索引、修复与对账 | 可以 | 受限执行域，不直接受公网任意输入控制 |
| `engineering-web` | 内部工程治理 UI | 可独立部署 | 仅内部可信身份访问 |
| `developer-web` | 多产品模板渲染、公开与受限文档、开发者接入（V1） | 可分产品部署 | 公开/受限 Edge，受独立策略保护 |
| `postgresql` | 事务性事实与审计元数据 | 运维策略决定 | 受控数据域 |
| `artifact-store` | 内容寻址、不可变发布包 | 独立容量扩展 | 私有对象存储 |
| `read-index` | 可重建的检索及门户投影，MVP 可由 PostgreSQL 承担 | 按规模决策 | 不作为授权与发布权威 |
| 外部 `IAM / Gateway / Git / CI` | 认证、业务调用、版本与流水线 | 不归 Fabric 实现 | 各自的治理域 |

**不强制的组件**：独立 API Gateway、Kafka/NATS、Elasticsearch/OpenSearch、服务网格、Kubernetes。引入条件是测量后的性能、可用性、隔离及组织需要，而非架构图的视觉完整性。

### 4.2 初期部署拓扑建议

建议 MVP 采用单区域、多个受控网络区域、API/Worker 分离运行、PostgreSQL 可恢复部署和受控对象存储。生产前必须明确高可用、备份及恢复；**单区域候选方案不等于已批准生产部署**。对私有文档优先经过鉴权请求按版本读取，不依赖可长期公开的静态 CDN URL。公开内容允许 CDN，但必须具备紧急拒绝与缓存失效策略。

## 5. DDD 子域、限界上下文与依赖方向

### 5.1 先定义领域边界，不按表或 UI 拆服务

| 建议 BC（待 F03 细化） | 主要业务责任 | 事实/不变量的拥有者 | 不应该拥有的内容 |
|---|---|---|---|
| Product Governance | 产品身份、Workspace、成员与产品角色 | Product、Workspace 与管理边界 | 外部 OAuth 令牌签发 |
| API Catalog | API 资产注册、归属、领域、生命周期索引 | Asset 身份和负责人声明 | Release 原始制品 |
| Contract Registry | 导入来源、Revision、Release、内容摘要 | 修订溯源和 Release 不可变性 | Portal 当前生效指针 |
| Policy & Review | 策略版本、检查结果、审批证据与豁免 | 治理结论及审批权限约束 | 运行时 Gateway 的最终授权 |
| Publication | 目标、受众、环境、生效、撤销、恢复 | Publication 指针和生效状态 | 变更不可变 Release 内容 |
| Developer Access | 开发者组织、应用、订阅与授权申请（V1） | 接入关系、申请流转 | IAM 凭证签发、产品业务 API 权限本体 |
| Platform Operations | 任务、集成状态、审计、恢复与对账 | Job / Reconciliation 状态及运营证据 | 业务领域最终执行事实 |

> **BC 是逻辑语义边界，不是初始 Go Module、数据库 Schema、微服务或团队的必然一一对应关系**。F03 应通过用例、事务不变量、聚合边界和反例检查确认是否拆合。

<figure class="architecture-figure"><div class="fig-label">FIGURE 04 · DDD Context Map / 模块协作</div><div class="fig-grid cols-3"><div class="fig-box tone-light"><b>Product Governance</b><small>身份归属、范围、成员</small></div><div class="fig-box"><b>API Catalog</b><small>API 资产与发现</small></div><div class="fig-box"><b>Contract Registry</b><small>Revision / Release</small></div></div><div class="fig-arrow">↓ 受保护的应用服务接口 + 明确消费者契约</div><div class="fig-grid cols-3"><div class="fig-box"><b>Policy & Review</b><small>规则、审核与证据</small></div><div class="fig-box fig-primary"><b>Publication</b><small>版本指针与发布授权</small></div><div class="fig-box"><b>Developer Access</b><small>外部组织与访问申请</small></div></div><div class="fig-arrow">↓ 只通过明确应用接口，外部入站使用 ACL</div><div class="fig-head light"><strong>Platform Operations & Integration ACLs</strong><small>Git · CI · IAM · Gateway · Event Bus · Object Storage</small></div><figcaption>各上下文之间使用显式命令/查询契约、稳定标识、必要领域事件及 ACL。共享 DTO 或事件格式不意味着共享数据库写入和业务决策权。</figcaption></figure>

### 5.2 代码依赖方向（Go First）

建议仓库主模块参考 `github.com/idp-xyz/idp-api-fabric`（仓库地址为建议，非已验证存在）。内部目录可以从下述形态起步：

```text
idp-api-fabric/                     # 同一产品工程仓库
├── cmd/fabric-api/                 # 控制平面进程入口
├── cmd/fabric-worker/              # 持久任务 Worker 入口
├── internal/contexts/
│   ├── product/
│   ├── catalog/
│   ├── registry/
│   ├── governance/
│   ├── publication/
│   ├── developeraccess/
│   └── operations/
│       # 每个上下文可包含 domain / application / ports / adapters
├── internal/platform/              # DB / Auth adapter / telemetry / transport
├── api/                             # Fabric 自身受控对外契约；非所有产品 API 合并文件
├── migrations/                      # 追加式迁移
└── web/
    ├── engineering/
    ├── developer/
    └── shared-ui/
```

**依赖规则**：`transport → application → domain`；领域层不引用 HTTP、ORM、Redis、IAM SDK、外部门户或其他上下文的数据库模型。跨上下文编排由应用层/明确契约完成；Domain Event 不直接暴露为对外 Integration Event。`internal/` 的 Go 可见性语义不足以自动保证跨 BC 隔离，需要静态依赖检查。Bento-Go 如拟复用，应先通过精确版本与工程消费者验证，不能预先将其列为强制依赖。

### 5.3 跨上下文协作示例

发布申请的 Application Use Case 验证调用者、Product/Target 范围、Release 存在且不可变、Governance Decision 与策略版本匹配，再提交 Publication 状态转换和 Outbox/任务状态。Catalog、搜索和外部文档均通过异步投影反映生效结果，**不能由各上下文分别决定同一 Publication 是否有效**。

## 6. 身份、租户、产品与权限隔离

### 6.1 多维关系不是一棵“tenant_id”树

<figure class="architecture-figure"><div class="fig-label">FIGURE 05 · 隔离维度与访问判定</div><div class="fig-grid cols-3"><div class="fig-box"><b>Principal</b><small>内部成员 / 外部开发者 / CI Service</small></div><div class="fig-box"><b>Product + Workspace</b><small>CargoPai / Parcel / Brant / Prism</small></div><div class="fig-box"><b>Audience + Environment</b><small>internal / partner / public · dev / sandbox / prod</small></div></div><div class="fig-arrow">↓ Policy Decision / 必须在服务端复核</div><div class="fig-head"><strong>Allow = 有效身份 ∧ 有效关系 ∧ 允许操作 ∧ 对象范围 ∧ 发布范围</strong><small>缺省拒绝 · 附加角色/属性策略 · 审计 · 有效期 · deny 优先</small></div><div class="fig-arrow">↓ 校验资源身份与版本，禁止只凭搜索结果/站点路由推定授权</div><div class="fig-grid cols-2"><div class="fig-box tone-green"><b>Authorized</b><small>返回最小必要的内容及版本</small></div><div class="fig-box tone-red"><b>Denied</b><small>拒绝并写入受控审计，不泄露私有元数据</small></div></div><figcaption>此判定表达最小必备的维度；最终 ABAC/RBAC 条件及授权实体细节由 F05 和 F03 完成。对外身份的认证成功不能自动授予 API 执行权限。</figcaption></figure>

### 6.2 授权执行原则

- 统一 AuthN 适配层验证 issuer、audience、签名、expiry、授权主体及委托关系；但内部与外部使用独立 client / policy 配置。**不能把同一个 SSO Token 用于跨安全域通行**。
- 服务端 `authorize(principal, action, resource, context)` 应覆盖命令、查询、搜索、导出、后台任务、批量处理、对象存储获取和门户发布；对每次下载重判资格。
- 查询可先进行范围过滤，再进行结果级校验；不能仅凭 Search Index ACL 片段作为最终允许依据。索引/缓存失效的默认处理为拒绝敏感请求。
- Worker 接收到的是经过验证的目标/操作身份快照，执行前重新确认权限、状态与策略有效性。撤销/到期后的积压任务不得继续以旧权限执行高风险发布。
- 开发者组织、产品订阅、Consumer Application 与 OAuth Client 不同；凭证原值保存在授权的 Secret/IAM 服务而非业务库或普通日志中。
- 产品品牌域名/会话/CORS/CSP/CSRF/重定向应隔离；共享渲染层不会继承共享 Cookie。公网访问私有文档要经过 Edge 与服务端双重控制。

## 7. 契约与不可变制品：权威、版本、来源

### 7.1 数据权威矩阵

| 数据事实 | 权威记录 | 是否允许覆盖 | 派生/消费 |
|---|---|---|---|
| Git 来源的契约正文及修订历史 | 绑定仓库 + Commit + 文件路径 + 内容摘要 | Git 按其受控流程演进，已导入记录不可改写 | Fabric Revision |
| 受控上传来源的契约正文 | Fabric 保存的原始上传快照 + 上传人 + 摘要 | 不可就地覆盖；更新创建新 Revision | Fabric Revision |
| 审批、扫描与规则结果 | Policy & Review 记录（绑定策略版本、输入摘要） | 新决定产生新记录，不能篡改旧决定 | Release Gate |
| 正式 Contract Release | Registry 元数据 + 对象存储内容寻址制品 | **不可变**；需要撤回时改变可见性与发布事实 | 文档渲染、SDK/集成 |
| 生效的 Publication | Publication 主库的 `PublicationSlot`（唯一 `target_id + api_asset_id`）中的 `active_release_id / generation` | Slot 级 CAS 条件原子切换；保留操作历史；Target 只定义分发范围 | 公开入口、内部版本查阅 |
| 搜索目录、门户菜单和缓存 | 基于权威来源的投影/缓存 | 可重建、可删除 | 只读发现 |
| 真实 API 部署/运行状态 | 产品部署平台 / Gateway / 产品 Owner | Fabric 只复制带来源与检测时间的快照 | V1 Drift 分析 |

**显式拒绝的模式**：Git/数据库/门户三个可写主本；同一 Release 修改原文件；把 `latest` 当成永久 ID；把外部 API 的实时执行状态写成 Fabric 发布事实；用客户端导航隐藏替代授权。

### 7.2 完整可追溯的契约链

<figure class="architecture-figure"><div class="fig-label">FIGURE 06 · 不可变链路 / Source → Revision → Release → Publication</div><div class="fig-grid cols-4"><div class="fig-box"><b>Source</b><small>Git commit 或受控上传 / Provenance</small></div><div class="fig-box"><b>Revision</b><small>规范原文 SHA-256 / schema version</small></div><div class="fig-box"><b>Release</b><small>审核证据、制品 Manifest、不可变 Digest</small></div><div class="fig-box fig-primary"><b>PublicationTarget → Slot</b><small>Target 定范围；每 APIAsset 独立 Slot CAS 指向 Release</small></div></div><div class="fig-arrow">↓ 准确绑定来源摘要与策略版本</div><div class="fig-grid cols-3"><div class="fig-box tone-green"><b>内部 Catalog</b><small>管理与追溯</small></div><div class="fig-box tone-green"><b>外部文档</b><small>只有授权范围内的 Release</small></div><div class="fig-box tone-green"><b>版本对比</b><small>不影响历史资产与证据</small></div></div><figcaption>Release 的身份是内容摘要与发布 ID 的组合，不是可以漂移的 latest 标签。一个 Target 允许多个 API 的 Slot 并存；仅 Slot 指针可变更，必须与操作记录、审计和 Outbox 在主库事务内协调。</figcaption></figure>

### 7.3 唯一键和制品完整性

- 最小标识集：`product_id`、`workspace_id`、`api_asset_id`、`revision_id`、`release_id`、`publication_target_id`、`publication_slot_id`、`publication_operation_id`、`digest`、`source_ref`、`policy_version`、`audit_correlation_id`。
- `revision_id` 与 `release_id` 稳定不可复用；一个 API Asset 可关联多个 Revisions/Releases；Release 可发布到多个 Target，但权限必须逐目标和 API 评审。`PublicationTarget` 由 `product_id,audience_key,environment_key,portal_key` 唯一确定；`PublicationSlot` 由 `UNIQUE(target_id, api_asset_id)` 唯一确定，且 `APIAsset.product_id = PublicationTarget.product_id`。Target 没有全局当前 Release 指针。
- 原始源制品与规范化制品摘要不同：必须保存**原文摘要**、**转换流水线版本**及**最终发布制品摘要**，禁止仅凭规范化后的 JSON 断言源文件完全相同。
- 包内多文件 `$ref` 必须固定引用闭包，生成统一 Build Manifest（路径、摘要、引用版本、工具版本）；不得允许任意网络解析器在发布时从未批准地址拉取变动内容。
- 下载与发布校验摘要/签名（如配置），Release Manifest 记录算法、签署身份、构建来源和安全扫描结果；签名方案及关键材料托管由 F07/F05 落实。

### 7.4 标准兼容策略

以 F01 确认的规范族为输入；推荐先使用 **OpenAPI 3.1.2** 作为受控工程基线候选，OpenAPI 3.2.1 作为兼容性评估目标，AsyncAPI 3.1.0 对应 V1 候选。**任何版本支持声明必须基于解析、差异、文档渲染、Go/TS 生成和安全测试的实际验证**，F02 不宣布工具链已完成适配。具体支持矩阵、例外和升级门禁归 F04/F07。

## 8. 治理检查与可靠任务执行

### 8.1 Check Pipeline

建议每次可发布变更生成不可变 `check_run_id` 与输入摘要，执行：**格式/引用 → 规范校验 → 项目/全局 Lint → 相容性 Diff → 敏感信息/授权扫描 → 策略判定 → 审核证据固化**。检查规则具有 `rule_id + version + severity + waiver eligibility + policy_snapshot_digest`。

**检查结论不是发布许可**：`PASSED_CHECKS` 只说明在特定输入、规则和工具版本下检查成功；真正发布还需符合角色、审批和目标范围。策略变化、来源改动、目标变化必须判定已有证据是否失效。

### 8.2 作业与外部依赖可靠性

| 失败或竞争场景 | 必须实现的架构处理 |
|---|---|
| CI 重投相同提交 | `idempotency_scope = principal + product + operation + key`，同键异内容拒绝；保留期限由 F04 确定 |
| Worker 执行中崩溃 | 持久 Job `PENDING/RUNNING/RETRY_WAIT/SUCCEEDED/FAILED/BLOCKED`，带 lease、fencing token 和重试上限 |
| 数据库提交后通知失败 | 本地事务 Outbox 与状态同行提交；外部投递至少一次，消费者通过事件 ID/版本去重 |
| 对象存储写入成功、DB 未登记 | 先临时对象/验证摘要，再提交元数据；孤儿制品清理/对账，不提前公开 |
| DB 已批准但索引未更新 | 以权威 Publication 读验证；索引重试/重建；显示同步延迟，不假报已完整同步 |
| 审批后策略发生变化 | 启动发布前检查授权与策略版本是否仍有效，失效时返回新审批需求 |
| 消费方无幂等或查询接口 | 暂停盲目重试，转为查询结果/人工 HOLD；不推断成功 |

**初期不强制消息中间件**：PostgreSQL 持久 Job + Outbox 已能覆盖较多 MVP 场景，但必须按吞吐、锁争用、积压和故障恢复实测决定是否需要 NATS/Kafka 等。任何“Exactly-Once”措辞仅可描述被证明的局部效果，不宣称端到端业务天然恰好一次。

## 9. 发布、撤销、回退与一致性

### 9.1 必须分离的状态机

- Revision：`DRAFT → IN_REVIEW → APPROVED | REJECTED`；撤回草稿可由独立事件记录，修改生成新 Revision。
- Release：`PREPARING → READY | FAILED`；`READY` 不可修改内容，撤回权限不能修改其历史摘要。
- Publication：`REQUESTED → PREPARING → ACTIVE | FAILED | WITHDRAWN`；失败可重试，ACTIVE 的原子指针可被新已批准 Release 替换。
- API Asset 生命周期：`EXPERIMENTAL → STABLE → DEPRECATED → RETIRED`；单独管理，不与 Release、Publication 状态混淆。

状态仅为架构建议；合法迁移、不变量、拒绝及并发行为必须由 F03/F04 具体冻结。

<figure class="architecture-figure"><div class="fig-label">FIGURE 07 · 安全发布与紧急撤销</div><div class="fig-grid cols-5"><div class="fig-box"><b>1 · 预检</b><small>来源/摘要/规则/批准</small></div><div class="fig-box"><b>2 · Stage</b><small>构建静态/私有制品</small></div><div class="fig-box"><b>3 · Validate</b><small>目标授权、可读性与路径</small></div><div class="fig-box fig-primary"><b>4 · Commit</b><small>原子生效指针 + Outbox</small></div><div class="fig-box"><b>5 · Reconcile</b><small>索引/CDN/消费方差异</small></div></div><div class="fig-arrow">↘ 错误公开：先执行 Edge deny/目标关闭，再进行受控指针撤销/回退与缓存失效</div><div class="fig-grid cols-3"><div class="fig-box tone-red"><b>Emergency Deny</b><small>立即阻断对应目标及资源路径</small></div><div class="fig-box tone-light"><b>Withdraw / Restore</b><small>条件切换、审计、恢复到可用历史版</small></div><div class="fig-box tone-green"><b>Verify</b><small>匿名/授权负向访问与边缘缓存核验</small></div></div><figcaption>数据库事务不能直接保证远端 CDN 的原子可见。正式发布“成功”的定义需明确区分 DB 指针生效、Edge 可见性和最终对账；涉及敏感内容时撤销必须具备独立的立即拒绝控制。</figcaption></figure>

### 9.2 发布可见性协议（MVP）

1. **Staging**：先生成带 Digest 的不可变文档制品，完成病毒/敏感信息/外部引用及渲染安全检查；对外不可见。
2. **Preflight**：验证调用者、审批、策略快照、目标、预设版本、内容可读取、可回退目标和必要的紧急 deny 控制；不满足则拒绝。
3. **Commit**：数据库事务按 `(target_id, api_asset_id)` 定位唯一 PublicationSlot，以 `expected_generation` / CAS 验证**该 Slot** 当前代数并写入 `active_release_id`、递增 `generation`、操作审计和 Outbox；同 Slot 竞争只有一个条件提交成功，不同 Slot 可独立成功。必须验证 Release 属于该 APIAsset、产品一致且审批对本次目标有效。
4. **Delivery**：Portal / Edge 根据 Slot generation、Target 的受众/环境及当前授权读取；检索与缓存异步更新。**不会声称 DB 事务对全球 CDN 是分布式原子操作**。
5. **Confirmation**：外部门户人工/自动探测证明目标内容与 Digest 对应；未完成时标记 `SYNCING/DEGRADED` 之类的独立交付状态，不把它混入业务指针状态。
6. **Withdraw**：遇错误或敏感暴露，优先执行可同步验证的 Edge/Origin deny（`AccessBlock` 可覆盖 Target、Slot 或 Artifact，Target-wide Block **不替代 Slot 指针**）；随后仅对受影响的 Slot 使用 `expected_generation` CAS 更新为安全不可见或先前已批准的 Release，保留其它 API 的 Slot 不变，并驱动缓存清理、审计与可见性验证。

**禁止**在对象存储桶中把受控私有内容直接赋予长期公开 URL；敏感文档不使用无法即时撤销的长效预签名链接。公开静态 CDN 内容也必须给出最坏缓存残留策略和可验证应急阻断能力。若选择的发布介质无法实现这些控制，应调整部署而不是将风险标记“已解决”。

## 10. 双门户架构与多品牌发布

### 10.1 两个不同的授权体验

<figure class="architecture-figure"><div class="fig-label">FIGURE 08 · 两种体验、同一受控内核</div><div class="fig-grid cols-2"><div class="fig-lane"><strong>Engineering Portal · 内部</strong><span>SSO / 管理授权 / 组织范围</span><div class="fig-mini">Product & Workspace / API Catalog</div><div class="fig-mini">Contract Diff / Governance / Review</div><div class="fig-mini">Release / Withdraw / Audit / Operations</div><small>高密度操作工作台 · 全操作结果可解释</small></div><div class="fig-lane"><strong>Product Developer Portal · 外部</strong><span>公开访问或按产品授权</span><div class="fig-mini">Quick Start / API Reference / Guides</div><div class="fig-mini">版本、示例、错误、Webhook / SDK（分期）</div><div class="fig-mini">Application / Subscription / Sandbox（V1）</div><small>CargoPai 等独立品牌 · 无跨产品默认继承</small></div></div><div class="fig-arrow">↓ 双门户共享 API Contract/Publication 真源，不共享无差别会话或执行权限</div><div class="fig-head light"><strong>Fabric Services + Versioned Rendering Components</strong><small>Public vs private content policy · Product theme · locale · availability · source digest</small></div><figcaption>两个门户可复用前端组件，但依然具备独立安全域、访问策略与部署选择。是否采用不同代码仓库不由产品边界直接推出。</figcaption></figure>

### 10.2 门户渲染与内容发布

- 外部文档主题配置属于 Product 的门户设置；域名、品牌、默认语言、导航和可见范围通过独立 Publication Target 决定。
- 每个文档页面必须能追溯 `product / target / release / digest / locale`；翻译是 Release 的派生内容，需有源语言版本与欠缺翻译回退声明。
- 文档内容和 Markdown/HTML 渲染必须清洗，CSP、链接、安全头与脚本执行策略由 F05/F06 冻结。交互式调试只能通过受控 Explorer 执行，不能直接用浏览器任意抓取开放内网地址。
- 内部门户偏高密度表格、批量操作、变更对比、审核和恢复；外部门户偏文档导航、示例、任务旅程和自助支持，具体页面与无障碍验收由 F06-A/B/C 详细设计。
- **MVP 外部门户仅承诺经授权的文档发现、阅读、版本选择**；真实沙箱首调作为 V1 功能，不能在 MVP 成功指标中混淆。

### 10.3 BFF 与部署选项

可以采用同一个 API 核心、分别配置内外路由/BFF/Edge 的实现，也可以采用独立 Web 服务。**安全正确性必须由服务器端资源授权和网络策略保证**，不能由前端源代码拆分替代。外部门户只暴露面向消费者的最小接口，不暴露审核操作。推荐外部查询遵循显式 allowlist 的视图契约。

## 11. 安全架构与威胁面前置要求

| 威胁 / 风险 | 主要防护层 | 需由 F05 验证 |
|---|---|---|
| 越权跨产品读取 / 下载 / 发布 | 统一鉴权拦截 + 领域资源校验 + 存储访问隔离 | 横向/纵向 IDOR，索引与缓存越权 |
| 恶意 `$ref` / OpenAPI 内容 | 解析器沙箱、协议白名单、私网拒绝、深度/大小/超时限制 | SSRF、循环引用、压缩炸弹、资源耗尽 |
| 渲染富文本注入 | Markdown / HTML Sanitizer、CSP、链接处理 | XSS、危险 URI、脚本注入 |
| Explorer / Webhook 请求 | 目标允许名单、网络 egress 隔离、身份代理 | 通过浏览器/服务端访问内部地址 |
| 供应链攻击 | 锁定工具版本、SBOM、制品摘要/签名、构建来源 | 依赖替换、制品污染、许可证审查 |
| CI/Worker 凭证泄漏 | Workload Identity / Secret 管理、最小权限、轮换 | 日志、错误堆栈、构建输出泄漏 |
| 错误对外公开 | 目标级审批 + 敏感扫描 + Edge deny + 访问探测 | 撤销后残存访问路径与缓存回源 |
| 审计伪造/篡改 | 事务审计、访问控制、可校验导出与保留策略 | 谁在何时以何身份批准和执行 |

安全控制遵循**风险基础上的多层防御**，不把“已经采用 OAuth”当作对象级授权或租户隔离已通过的证据。凭证密钥材料不得写入普通 DB/日志；外部生产 OAuth 调用的权限最终由业务产品 IAM/Gateway 实施。

## 12. 关键 API、事件与集成边界

### 12.1 Fabric 自身 API（设计意图，不是已实现接口）

| 操作族（示例） | 消费者 | 主要命令/查询 | 安全要求 |
|---|---|---|---|
| Product/Workspace | 内部管理员 | 登记产品、设置成员、查询范围 | Product 管理权限，审计 |
| Contract Import | CI / 工程人员 | 创建 Revision、提交来源及摘要 | 非交互身份、内容检查、幂等 |
| Governance | 工程/审核 | 请求 Check Run、查询 Diff、记录批准 | 检查/审批职责分离 |
| Release | 注册中心使用者 | 生成不可变 Release、读取制品 | Owner/策略快照与 Digest |
| Publication | 授权发布人 | Publish、Withdraw、Restore、查询状态 | Target 级授权、CAS、最高级别审计 |
| Developer Document | 外部开发者 | 查询已发布目录、版本与文档 | 产品/受众/环境逐项判定 |
| Operations | 管理/值守 | 查询作业、重试、修复、审计 | 运维权限、危险操作限权 |

具体 URI、参数、错误、Idempotency-Key 和并发前置条件由 F04 的契约定义；不能在 F02 的示意操作表中假定已有稳定生产 API。

### 12.2 建议的内部集成事件

- `ContractRevisionRegistered`：修订被登记，不代表审查通过或正式发布。
- `GovernanceCheckCompleted`：保存 Check Run 结果、策略摘要和来源。
- `ContractReleasePrepared`：不可变制品准备完成，不代表外部可见。
- `PublicationActivated` / `PublicationWithdrawn`：主库的目标状态转移完成，门户最终传播另报状态。
- `PortalProjectionUpdated` / `PortalDeliveryFailed`：投影/交付结果，不改变 Publication 事实权威。

事件必须有 `event_id / occurred_at / aggregate_id / aggregate_version / product_id / correlation_id / schema_version` 的明确语义；版本/分区顺序、消费去重、迟到和重放由 F03/F04 冻结。不得将普通日志事件自动视作可供外部客户订阅的正式 AsyncAPI 契约。

### 12.3 外部适配器契约

Git 适配器负责认证提交/仓库/文件来源；CI 适配器提供非交互提交与检查结果；Identity 适配器负责身份验证和已批准的成员同步；Gateway 适配器只提供部署快照或经批准的配置意图，**不由 Fabric 执行商业业务请求**。连接超时/不可用必须显示为 `UNKNOWN/STALE/FAILED`，不因上次检测通过而假报实时一致。

## 13. 数据存储、事务、一致性与搜索

### 13.1 最小储存布局

- **PostgreSQL**：Product/Workspace 关系、资产 ID、Revision/Release 元数据、审批结果、Publication Target（分发范围）与 PublicationSlot（每 API 生效指针及 CAS generation）、审计索引、Job 和 Outbox。版本以项目批准配置为准，不能假定特定版本已实际测试。
- **对象存储**：原始上传、固定 `$ref` 引用闭包、不可变 Release 包、渲染输出和校验报告；通过 Digest 引用及校验访问。
- **索引与缓存**：MVP 可使用 PostgreSQL 全文检索/专用投影表，达到规模阈值后再评估 OpenSearch/其他索引。任何索引均可从主库+制品重建。
- **Secret Store**：运行身份/连接机密存放于授权 Secret 系统，不保存在文档仓库或 Release 示例中。

### 13.2 一致性模型

| 行为 | 需要的保证 | 允许的滞后 | 不允许的结果 |
|---|---|---|---|
| 同一目标 Publication 生效切换 | 主库单事务 + CAS/版本约束 | 交付缓存/索引可短暂同步 | 两个相斥版本同时被主库标记同目标当前版本 |
| 正式 Release 创建 | 内容摘要匹配 + 只写一次 + 完成状态与制品可读 | 构建阶段可异步 | 引用缺失对象却标记 READY |
| 搜索与目录投影 | 由受授权源数据驱动、可重建 | 必须可检测新鲜度 | 将私有结果当成公开内容泄露 |
| Check Run 与审批 | 绑定输入摘要、规则和策略版本 | 长任务可异步 | 用旧检查结果批准已修改的内容 |
| Audit 与发布命令 | 本地关键审计元数据和命令事实同事务 | 外部归档可异步 | 成功发布无可追溯操作者/范围 |
| 外部 Gateway 运行态 | 带时间戳的快照/检查结果 | 允许有限滞后但必须显示 | 断连当成一致 / 100% 在线 |

跨 PostgreSQL 与对象存储的操作没有天然分布式事务。采用“先私有暂存制品 → 验证 Digest/可读性 → 主库提交 READY/生效指针 → 可靠 Outbox 通知 → 对账/孤儿清理”，所有失败都应留下可恢复的观测状态。

### 13.3 并发与故障试验重点

同一个 Target 的并发发布、旧审批被替换、同幂等键异内容、Worker lease 过期、上传后网络未知、对象存储短暂读失败、索引落后/异常重建、紧急公开撤销后 CDN 残留、带权限快照任务的过期执行等都应进入 F08 测试矩阵。业务语义明确时采用串行化或条件更新，**不可把“最终一致”当作硬约束可以不受保护的理由**。

## 14. 可观测性、运营与灾难恢复

### 14.1 监控与审计维度

按责任区分：控制平面可用性、契约校验成功率、Check Run 排队时延、Publication 端到端生效延迟、公开撤销生效时延、对象存储读校验失败、索引投影 Lag、外部依赖新鲜度、拒绝访问率、审计写入失败率。

Trace 关联 `trace_id / command_id / correlation_id / product_id / workspace_id / release_id / publication_target_id / job_id`；需要限制个人信息与机密进入日志。Metrics/Traces/Logs 可遵循 OpenTelemetry 语义，但不将采用某一观察组件等价为可运维。

### 14.2 恢复分类与演练

| 故障 | 恢复来源 | 必须核验 |
|---|---|---|
| 搜索索引丢失 | 权威元数据 + Release 制品 | ACL、版本、新鲜度一致性 |
| Worker 重启/队列积压 | Job + lease + Outbox | 不重复有效发布、失败可见 |
| 对象存储部分损坏 | 有摘要的备份/副本 | Digest、来源、历史制品完整性 |
| PostgreSQL 恢复 | 一致性备份/日志恢复 | Publication 指针、审计/Outbox 与制品能对账 |
| 外部门户泄露事件 | Edge deny + 撤销/回退 + 访问核验 | 各 CDN/Origin 路径不可读、完整事件记录 |
| IAM/Gateway 临时不可达 | Fail closed / 告警 / 恢复后对账 | 不能因依赖失败绕过权限或假报状态 |

恢复目标必须明确范围（控制台/公网门户/Registry）、RPO、RTO、样本数据规模、负责人、观察窗口以及失败处置，**在首次生产上线前真实演练**。最终目标值由 F07/F08 与运营方批准，F02 不虚构 99.99% 或 15 分钟等承诺。

## 15. 技术选型、演进门槛与成本

### 15.1 推荐起步技术决策（待 PoC）

| 层次 | 优先候选 | 选择理由 | 必须证明的边界 |
|---|---|---|---|
| 业务核心 | Go + DDD 模块化单体 | 与 IDP Go-first 技术方向一致、低初期分布式复杂度 | 依赖规则、并发、真实用户任务 |
| HTTP 契约 | OpenAPI 3.1.2 候选基线 | 工具链成熟度相对可控 | 解析/代码生成/差异和渲染兼容 |
| 异步契约 | AsyncAPI 3.1.0 候选（V1） | 描述事件与消息协议 | Binding/工具支持/安全 |
| 事务存储 | PostgreSQL | 一致性、关系与条件写入 | 容量、索引、迁移与 HA |
| 制品 | 支持 Digest、版本、WORM/不可变策略的对象存储 | 可核验、分离容量与历史 | 完整性、权限、恢复与成本 |
| 任务 | PostgreSQL Job/Outbox + Go Worker | 最小依赖，具可恢复性 | 背压、并发、重试与队列容量 |
| 文档渲染 | 受控 OpenAPI Reference 组件 | 重用成熟展示能力 | 多品牌、访问控制、XSS 防御 |
| 外部集成 | Ports & Adapters | IAM / Gateway / Git 可替换 | 失败语义与消费方契约 |

**不宜过早引入**：全域微服务拆分、数据库每 BC 物理拆库、集中消息集群、全量 Elastic 搜索、事件溯源或复杂 Workflow Engine。它们都属于可评估方案，只有证据证明收益超过复杂性、可靠性和成本代价时再引入。

### 15.2 关键备选及迁移触发条件

| 决策 | 起步方案 | 迁移/分拆触发证据 | 主要代价 |
|---|---|---|---|
| Module → 微服务 | 模块化单体 | 独立部署/SLO、安全域或团队自治要求无法满足 | 跨服务一致性、观测、运维 |
| PostgreSQL Search → 专用引擎 | DB FTS + ACL 投影 | 实际 p95、相关性、索引规模无法达标 | 双写/投影延迟、运维、隐私 |
| DB Job → Broker | 持久 Job/Outbox | 锁争用、积压/吞吐/消费者数实测超出预算 | 消费去重、集群与成本 |
| 共享门户部署 → 独立实例 | 共享组件、产品隔离入口 | 法规驻留、合同 SLA、客户定制安全域 | 版本协调、部署数量 |
| 单区域 → 多区域 | 单区域候选 | RTO/灾备/驻留要求明确且可测 | 跨域一致性、复制冲突、成本 |

成本评估采用 `工程建设 + 许可证/维护 + 运行基础设施 + 安全与合规 + 迁移退出` 的全生命周期模型。没有真实供应商报价和流量数据时，不计算“节省 X%”之类不可审计的收益指标。F07/F08 应形成配置与成本假设清单，并用 CargoPai 与第二个产品接入验证复用价值。

## 16. 架构演进、AI-Native 与兼容界面

V2 的 Agent/MCP 能力可以通过 `Release Ref + Policy Ref + Tool Capability Descriptor + Risk Classification` 的受控只读投影衔接，但 **不默认把 OpenAPI 操作直接暴露为可执行 Agent 工具**。更改、取消、提交报关、费用确认等高副作用操作，始终由业务产品的真实执行授权和必要人工批准控制。Fabric 仅治理工具描述及发布关系，不接管通用 Agent Runtime。

SDK 发布、Arazzo 业务流程、Webhook 管理、消费者影响图等属于后续演进，不应进入 MVP 核心事务不变量。公共契约从一开始保留稳定 ID 和版本来源，以便后续可以在不改写历史 Release 的条件下扩展。

## 17. 关键场景、可验证架构断言与证据状态

### 17.1 F02 设计验证场景

| ID | 设计验证场景 | 对应 F01 | 预期可观察的结果 | 当前状态 |
|---|---|---|---|---|
| AR-01 | CargoPai 与 Parcel 同时注册同名 API | FR-001–009；AT-01 | 不同产品有独立 Asset 与授权，不发生自动合并 | NOT_RUN |
| AR-02 | Parcel 成员搜索/下载 CargoPai 私有 Release | FR-008/063；AT-04 | 搜索、详情、下载与缓存均拒绝 | NOT_RUN |
| AR-03 | Git 来源提交内容/摘要不匹配 | FR-013–017；AT-05 | 禁止 READY，保留失败诊断 | NOT_RUN |
| AR-04 | 同一 Target 内同 Slot 双并发、不同 API 的两 Slot 并发发布 | FR-031–036；AT-08/09 | 同 Slot 仅一个 CAS 成功；不同 Slot 均可成功且互不覆盖；目标级 AccessBlock 不改变其它 Slot 生效指针 | NOT_RUN |
| AR-05 | 对象存储成功但 DB 提交失败 | FR-017/033/064；AT-08 | 未公开、孤儿制品可清理、可安全重试 | NOT_RUN |
| AR-06 | 错误公开发布紧急撤销 | FR-034/036；AT-03/09 | Edge deny 优先，指针/缓存对账、匿名负向核验 | NOT_RUN |
| AR-07 | Worker 反复投递相同任务 | FR-033/056/061；AT-08/12 | 去重与幂等，有可观察结果，无重复有效发布 | NOT_RUN |
| AR-08 | 权威 Registry 有效、索引落后/损坏 | FR-035/066；AT-10 | 投影延迟可见、可重建、发布事实不丢 | NOT_RUN |
| AR-09 | 外部门户与内部工作台同一 Release | FR-040–050；AT-11 | 仅在各自范围公开相应可见内容 | NOT_RUN |
| AR-10 | 恢复 DB + Artifact Store 与签名密钥 | FR-066；AT-10 | RPO/RTO 由批准目标判定，历史摘要/指针对账 | NOT_RUN |
| AR-11 | F01 规范采用批准与六道门禁 | §1.2；F01 §17 | Project Manifest、责任和证据完整，不伪造 PASS | NOT_RUN |
| AR-12 | 第二产品接入，复用治理与双门户 | FR-005/047，NFR-13 | 无需克隆平台核心且不越权共享 | NOT_RUN |

**验证纪律**：本表是架构场景与测试输入，不是已执行的测试结果。静态文档检查通过不能替代并发、故障注入、渗透、消费者和灾难恢复的实际证据。

### 17.2 追溯与标准门禁

文档关联：`F01 BG/FR/NFR/AT → F02 架构元素与 ADR → F03/F04/F05/F06/F07 → F08 验收 → Evidence`。设计期关联详见 `tests/acceptance/F02_architecture_trace.csv`，所有真实执行证据标记为 `NOT_RUN`。

| 上位门禁 | F02 的输入与责任 | 可作为后续验收的证据（尚未执行） |
|---|---|---|
| STD-CP1 范围与采用 | F01 Manifest、Profile 与具名责任 | 批准采用记录 |
| STD-CP2 领域与事实 | §5/§7、F03 Context Map 与聚合 | 领域反例、状态/权威检验 |
| STD-CP3 契约与恢复 | §8/§9/§12/§13、F04 契约 | 幂等、并发、失败恢复测试 |
| STD-CP4 专业体验 | §10、F06 双门户详细 UX | 用户任务、无障碍实测 |
| STD-CP5 工程与质量 | §11–§15、F07 工程与 SLO | 性能、安全、迁移、恢复实测 |
| STD-CP6 发布与运营 | §9/§14、F08 交付 | 批准发布、监控、运营与发布后真实结果 |

所有门禁当前保持 `NOT_RUN` 或“待具名批准”，与上位规范“未实际执行不能标 PASS”的要求一致。

## 18. 决策清单与 F03–F08 的接口

### 18.1 已形成推荐方案、待具名审查的 ADR

| ADR | 主题 | 推荐方案 | 执行前必须验证 |
|---|---|---|---|
| ADR-0001 | 联邦治理、多产品自治 | 一个共享平台 + 独立产品契约 | 第二产品接入、授权隔离 |
| ADR-0002 | 服务组织和演进 | Go 模块化单体 + 分离 Worker | 模块依赖、容量/运维边界 |
| ADR-0003 | 修订来源权威 | Git-first + 受控上传双来源类型、单修订单权威 | 来源溯源、同步/重试 |
| ADR-0004 | Release/Publication/Projection | 不可变制品 + CAS 指针 + 可重建投影 | 并发、撤销、Edge 缓存与恢复 |
| ADR-0005 | 双门户隔离 | 共享引擎，独立授权面/品牌/目标 | 威胁模型、域名/会话、UX |
| ADR-0006 | 外部基础设施边界 | Ports & Adapters，业务数据平面独立 | IAM/Gateway 适配、失败策略 |

### 18.2 未被设计结论掩盖的待决问题

- **D-01**：具名产品、架构、安全、运营 Owner 及 Risk R1/R2/R3 风险级别，由项目治理确认。
- **D-02**：Go 精确版本、Bento-Go 是否采用及版本、PostgreSQL 版本、对象存储和操作系统/运行方式，在 F07 的兼容性与运维验证后确定。
- **D-03**：外部身份系统、内部 IdP、Gateway、Git Provider、CI 平台的现状、合同与部署位置；没有连接数据时只保留适配协议。
- **D-04**：Release 的详细命名、破坏性兼容规则、Policy 例外与保留期由 F04 决定。
- **D-05**：发布权限矩阵、关键威胁、Cookie/CSP/Token/Network 策略及保留合规由 F05 决定。
- **D-06**：双门户页面与交互、WCAG 2.2 AA 验证、多语言版本降级由 F06 决定。
- **D-07**：量化容量、p95、SLO、RPO/RTO、资源预算、备份演练、数据迁移与值守由 F07/F08 决定。
- **D-08**：跨区域/独立客户实例、复杂搜索、事件总线和 AI Agent 等仅当真实需求触发时再行 ADR。

### 18.3 后续文档的明确交付边界

| 下一份 | 必须从 F02 继承的东西 | 不应重复写成新的事实权威 |
|---|---|---|
| F03 | BC 责任、聚合、状态机、读写权威、唯一约束、事务 | 不可把推荐的模块直接当固定 DB/服务拆分 |
| F04 | 契约发布链、规则版本、兼容性、Revision/Release/Publication | 不以工具输出代替审核/发布授权 |
| F05 | 五个信任区、多维隔离、内容供应链、紧急阻断 | 不把门户隐藏/Token 认证当授权证明 |
| F06 | 双门户用户旅程、授权内容、可解释状态与多品牌 | 不把一个 HTML 模板视为两个独立用户体验 |
| F07 | Go 包依赖、Outbox/Job、制品库、观测、容量/恢复 | 不凭技术名称推断高可用或 Exactly-Once |
| F08 | AR-01–12、上位六道门禁与证据语义 | 不把 `NOT_RUN` 写成 PASS |

## 19. 参考依据与采用边界

- IDP Supply Chain DDD Product Engineering Standard V1.0（上位规范原文由项目治理另行引用）：内部上位规范；应以正式具名采用记录及对应版本为准，当前仅有设计层引用。
- [F01 — Product Requirements](F01_Product_Requirements.md)：产品需求、阶段和既定边界；与本文存在实质冲突时必须显式提交架构/需求复核。
- [OpenAPI Specification 3.2.1](https://spec.openapis.org/oas/v3.2.1.html)：截至本文日期可核实的正式规范版本；不意味着 Go 工具链已全部支持。
- [OpenAPI Specification 3.1.2](https://spec.openapis.org/oas/v3.1.2.html)：本文建议的工程支持候选基线。
- [AsyncAPI Specification 3.1.0](https://www.asyncapi.com/docs/reference/specification/v3.1.0)：异步契约标准，实际采用由兼容测试决定。
- [RFC 9457 — Problem Details for HTTP APIs](https://www.rfc-editor.org/rfc/rfc9457.html)：错误契约候选，详细响应形式由 F04 定义。
- [RFC 9700 — OAuth 2.0 Security Best Current Practice](https://www.rfc-editor.org/rfc/rfc9700.html)：安全架构参考，由 F05 将控制映射至实际身份基础设施。
- [OWASP API Security Top 10 (2023)](https://owasp.org/API-Security/editions/2023/en/0x00-header/)：威胁建模基础，不能替代特定部署的风险评估。
- [OpenTelemetry Specification](https://opentelemetry.io/docs/specs/otel/)：追踪、指标与日志关联参考。

**结论**：F02 已提供系统总览、C4、DDD、发布事实权威、安全、多租户、双门户、存储一致性、运维及演进的可评审推荐架构。完成文档并不等于批准架构或运行验证；后续必须用 F03–F08 的具体模型、契约、PoC 与真实证据关闭未决风险。
