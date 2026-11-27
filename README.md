# IDP API Fabric — Engineering Baseline

**项目代号：** `idp-api-fabric`  
**文档版本：** V1.0（保持原有版本，不增设 V1.1/V2.0）  
**状态：** F01 内容定稿、待具名签收；**F02 总体架构设计候选、待具名评审及工程 PoC**；**F03 领域与数据设计候选、待具名评审**；**F04 契约与治理规范设计候选、待具名评审与工具验证**；**F05 安全与多租户设计候选、待具名安全评审及真实验证**；**F06 双门户体验设计候选，待具名评审与真实 UX/安全验证**；**F07 工程与质量设计候选、待具名评审与真实 PoC**；**F08 交付与验收设计候选、待具名签收，真实验收 NOT_RUN**。  
**日期：** 2026-11-09

## 工程实现同步（2026-11-19）

> 下面按版本记录实现。0.17.0–0.36.0 各段写到的评审、批准和 `approval_problem` 已由 0.37.0（[CR-0001](decisions/CR-0001.md)）取消，现状见本节 0.37.0 的两段；0.38.0（[CR-0002](decisions/CR-0002.md)）见本节最后一段。

PR #6（产品负责人管理）和 #7（四类分页列表）已合入 main。对应领域与物理模型见 F03 §5.5，分页协议见 F04 §3.1.1，权限规则见 F05 §6.1；授权矩阵新增 AC-36…43，仍保留设计候选状态。可复现的本地 HTTP 负载实验及原始数据见 [列表压测记录](tests/performance/README.md)。该实现记录不改变 F01–F08 正式验收和具名批准的状态。

管理 API 0.6.0 支持按产品管理受众、环境和文档入口。升级时执行 `fabric-migrate -apply -grant-to <application_role>`，应用迁移 005 并为应用角色授予新表的权限。[管理 API 契约](api/openapi.yaml)。

管理 API 0.7.0 支持设置产品的品牌（名称、Logo 网址、主色），品牌随产品一起返回。升级时同样执行 `fabric-migrate -apply -grant-to <application_role>`，应用迁移 006 并为应用角色授予 `product` 表的 `UPDATE` 权限。

PR #10 和 #11 的实现详见 [F03 §5.6](docs/F03_Domain_and_Data_Model.md#56-已实现的受众配置和产品品牌pr-10--11)、[F04 §11](docs/F04_API_Contract_Governance.md#11-fabric-自身-api-的-http幂等错误边界) 和 [F05 §6.3](docs/F05_Security_and_Multitenancy.md#63-已实现的受众配置和产品品牌权限pr-10--11)。

管理 API 0.8.0–0.12.0 加入 API 目录和契约修订：登记和查询 API 资产（PR #12），把 OpenAPI 3.1.2 源文件导入为草稿修订并查看修订历史（PR #14–#17），导入时声明文件来自哪个 Git 提交（0.11.0），读回修订的原文（0.12.0）。Fabric 记录 Git 来源的声明，不读取仓库。

从 0.7.0 之后升级，仍执行 `fabric-migrate -apply -grant-to <application_role>`：应用 `apicatalog/001`、`contractregistry/001` 和 `002`，并授予应用角色新表的权限。0.12.0 需要 `contractregistry.source_file` 的 `SELECT`，它没有对应的迁移步骤，缺少时进程拒绝启动并指出缺少的权限，所以先执行授权再启动新进程。全部进程升级后再开始带 Git 来源导入。

实现详见 [F03 §5.7](docs/F03_Domain_and_Data_Model.md#57-已实现的-api-目录和契约修订)、[F04 §11.3](docs/F04_API_Contract_Governance.md#113-已实现的-api-资产与契约修订接口) 和 [F05 §6.4](docs/F05_Security_and_Multitenancy.md#64-已实现的-api-目录和契约修订权限)。授权矩阵没有新增条目。

管理 API 0.13.0–0.16.0 加入契约检查（PR #22–#33）：作者对一个草稿修订发起检查，Fabric 按内置的 23 条基线规则逐条给出结论并留存证据；可以读取检查的详情和历史；可以比较同一资产的两个修订是否向后兼容，也可以在检查时指定基线。23 条里有 13 条由内置工具评估（其中兼容性那一条只在指定基线时评估），其余 10 条如实记为 `NOT_RUN`，所以一次检查的总状态目前只会是 `FAIL` 或 `UNKNOWN`。检查是证据，本身不批准也不发布任何东西；评审引用其中一次检查作为证据。

从 0.12.0 升级，仍执行 `fabric-migrate -apply -grant-to <application_role>`：应用 `policyreview/001` 和 `002`，并授予应用角色新表的权限。缺少迁移或权限时，新进程拒绝启动并指出缺什么。迁移 002 需要 PostgreSQL 15 或更高版本。

实现详见 [F03 §5.8](docs/F03_Domain_and_Data_Model.md#58-已实现的契约检查)、[F04 §11.4](docs/F04_API_Contract_Governance.md#114-已实现的契约检查接口) 和 [F05 §6.5](docs/F05_Security_and_Multitenancy.md#65-已实现的契约检查权限)。授权矩阵没有新增条目。

管理 API 0.17.0–0.19.0 加入契约评审：Workspace 多了评审人这种成员关系，评审人能读修订、原文和检查（0.17.0）；作者拿一次检查作依据，把草稿修订提交评审，写明要批准给哪些受众和环境，也可以撤回（0.18.0）；评审人批准或驳回，批准有到期时间，检查里没通过的可豁免规则要逐条写明理由豁免（0.19.0）。登记修订的人、发起那次检查的人和提交申请的人不能作决定。不可豁免的规则没通过的修订不能提交评审。修订的状态从 `DRAFT` 起随评审流转，内容不变。基线里仍有 10 条规则没有工具，批准不证明它们成立。

从 0.16.0 升级，仍执行 `fabric-migrate -apply -grant-to <application_role>`：应用 `productgovernance/007`、`contractregistry/003`、`policyreview/003` 和 `004`，并授予应用角色 `contract_revision` 的 `UPDATE` 和四张评审表的权限。缺少迁移或权限时，新进程拒绝启动并指出缺什么。全部进程升级后再授予评审人角色、提交评审和作出决定：0.16.0 的进程列不出有评审人的 Workspace 的成员（返回 500），0.18.0 的进程读不出已经批准或驳回的评审（返回 500），详见 F03 §5.9。

实现详见 [F03 §5.9](docs/F03_Domain_and_Data_Model.md#59-已实现的契约评审)、[F04 §11.5](docs/F04_API_Contract_Governance.md#115-已实现的契约评审接口) 和 [F05 §6.6](docs/F05_Security_and_Multitenancy.md#66-已实现的契约评审权限)。授权矩阵新增 AC-50…53，仍保留设计候选状态。

管理 API 0.20.0–0.22.0 加入 Release 和发布：Workspace 多了发布人，产品多了安全管理员和安全评审人两种角色（0.20.0）；发布人把已批准的修订做成不可变的 Release，制品按 SHA-256 寻址（0.21.0）；产品负责人登记发布目标，发布人在目标上按资产激活、撤销和恢复 Release，安全管理员可以阻断读取，由另一位安全评审人解除（0.22.0）。准备 Release 和发布时都会重新核对批准。目前只有 `internal` 受众的目标能发布和读取，对外门户的授权和边缘分发尚未接入，交付状态如实报告为 `UNKNOWN`。

从 0.19.0 升级，仍执行 `fabric-migrate -apply -grant-to <application_role>`：应用 `productgovernance/008`、`contractregistry/004` 和 `publication/001`，并授予应用角色新表的权限。缺少迁移或权限时，新进程拒绝启动并指出缺什么。0.19.0 的进程不认识新角色，遇到有发布人的 Workspace 或有安全角色的产品，列成员和列产品角色会返回 500，所以全部进程升级后再授予新角色。

实现详见 [F03 §5.10](docs/F03_Domain_and_Data_Model.md#510-已实现的-release-与发布)、[F04 §11.6](docs/F04_API_Contract_Governance.md#116-已实现的-release-与发布接口) 和 [F05 §6.7](docs/F05_Security_and_Multitenancy.md#67-已实现的-release-与发布权限)。授权矩阵没有新增条目。

管理 API 0.23.0–0.25.0 让已批准的修订可以再次评审：复审不改变修订的状态，批准之后修订多一次批准，范围、到期时间和豁免各自独立（0.23.0）；资产所属 Workspace 的发布人能读修订、原文、检查、比较和评审，应答与作者相同，写操作仍然不允许（0.24.0）；读槽位时，如果它指向的 Release 的批准对这个目标已经不成立，应答里的 `approval_problem` 说明原因，取值是激活这个 Release 会得到的错误码（0.25.0）。没有新的操作。

从 0.22.0 升级只需要 `fabric-migrate -apply`，不需要重新授权：应用 `policyreview/005`。库没迁移时新进程拒绝启动并指出这一步没有记录。新旧进程可以混跑，但复审要由新进程来结束：0.22.0 的进程在迁移后的库上读复审时不带 `supplementary`，决定和撤回都返回 409 `REVISION_STATUS_CONFLICT`，数据不受影响，第一次评审和制品照常。

实现详见 [F03 §5.11](docs/F03_Domain_and_Data_Model.md#511-已实现的复审与批准状态的可见性)、[F04 §11.7](docs/F04_API_Contract_Governance.md#117-已实现的复审接口与批准状态) 和 [F05 §6.8](docs/F05_Security_and_Multitenancy.md#68-已实现的复审与批准状态的权限)。授权矩阵没有新增条目。

管理 API 0.26.0–0.28.0 加入查询与追溯：资产所属 Workspace 的作者、评审人和发布人能列出一个发布目标上的槽位，也能列出一个资产发布到了哪些目标，列表里的每一项和单独读到的槽位完全一样，含 `blocked` 和 `approval_problem`（0.26.0）；能按编号下载一个 Release 的制品，已经被替换或撤回的也行，也能读出一个 Release 的来源：它依据的修订（含提交人声明的 Git 来源）、检查和批准，复审之后不变（0.27.0）；产品多了一个角色 `internal_auditor`（审计员），由产品负责人授予，审计员按产品读审计记录，新的在前，可以按时间、动作、结果、行动人、资源和请求编号筛选（0.28.0）。新增 5 个操作，到 0.28.0 共 57 个。按编号下载不看发布目标上的访问阻断，也不重新核对批准；审计记录里的行动人如实给出，没有脱敏。这两处与设计正文不一致，已由 [CR-0002](decisions/CR-0002.md) 裁决：0.38.0 起按编号下载看这个 Release 的阻断，审计如实给出行动人是认可的做法。

从 0.25.0 升级需要重新授权：先执行 `fabric-migrate -apply -grant-to <application_role>`，应用 `productgovernance/009` 和 `platformoperations/002`，并给应用角色补上审计表的 `SELECT`（它仍然没有 `UPDATE` 和 `DELETE`），再换进程。只迁移不授权时，新进程拒绝启动：`db: the application role lacks privileges this build needs; run fabric-migrate -apply -grant-to <role>: missing SELECT on platformoperations.audit_record`。建索引用普通 `CREATE INDEX`，建的时候审计表的写入要等。旧进程在迁移和授权之后照常运行。所有进程升级以后，才授予 `internal_auditor`：旧进程的契约里没有这个角色，会拒绝授予请求；库里有了审计员，它列产品角色会返回 500。0.26.0 和 0.27.0 本身没有迁移，也不需要授权。

实现详见 [F03 §5.12](docs/F03_Domain_and_Data_Model.md#512-已实现的查询与追溯)、[F04 §11.8](docs/F04_API_Contract_Governance.md#118-已实现的查询接口) 和 [F05 §6.9](docs/F05_Security_and_Multitenancy.md#69-已实现的查询与审计读取的权限)。授权矩阵没有新增条目。

管理 API 0.29.0–0.31.0 加入内部工作台要先读的几样东西，都是读：调用人能问自己是谁、是不是平台管理员，也能问自己在一个产品里持有哪些角色、是哪些 Workspace 的成员，不能问别人（0.29.0，平台管理员来自部署配置，不在库里）；资产所属 Workspace 的作者、评审人和发布人能列出一个 Workspace 里的评审，先提交的在前，可以按状态和提交人筛选，读到的是整个 Workspace 的全部评审（0.30.0）；他们读到的资产多一个概况：最新修订、最新修订最近一次检查、最新 Release，以及最新修订已批准而还没做 Release，资产列表可以按检查结论和“可发布”筛选（0.31.0）。概况是读的时候现算的，没有汇总表；负责人和维护者读得到资产，读不到概况。新增 3 个操作，共 60 个。没有读权限的人每调用一次留下一条拒绝记录，页面应当先问身份再决定调不调。没有做汇总表和事件投影，设计里目录列表的可重建投影 `AssetSearchIndex` 仍然没有，这与设计不一致，已由 [CR-0002](decisions/CR-0002.md) 裁决：读的时候算是认可的做法，资产数量需要时再建索引；平台管理员留在部署配置也由 CR-0002 认可；概况只给读得了修订的人，评审队列沿用读评审的名单，也待裁决。

从 0.28.0 升级只需要 `fabric-migrate -apply`，不需要重新授权，再换进程：它应用 `policyreview/006`，只建一个索引，不改行，应用角色的权限不变。库没迁移时，0.30.0 起的新进程拒绝启动：`db: the database is not migrated for this build; run fabric-migrate -apply first: migration policyreview/006_review_queue_order is not recorded`。建索引用普通 `CREATE INDEX`，建的时候新提交评审的写入要等，读不受影响。旧进程在迁移之后照常运行，对三个新路径答 404 `ROUTE_NOT_FOUND`，对带 `check_status` 或 `release_pending` 的资产列表答 400 `REQUEST_INVALID`，资产的应答里没有概况，所以用这些接口的页面等所有进程升级完再上。0.29.0 和 0.31.0 本身没有迁移，也没有新角色和新配置。

实现详见 [F03 §5.13](docs/F03_Domain_and_Data_Model.md#513-已实现的工作台查询)、[F04 §11.9](docs/F04_API_Contract_Governance.md#119-已实现的工作台查询接口) 和 [F05 §6.10](docs/F05_Security_and_Multitenancy.md#610-已实现的工作台查询的权限)。授权矩阵没有新增条目。

管理 API 0.32.0 支持产品负责人读取本产品各 Workspace 的资产概况、契约修订和原文、检查、评审、Release 及发布历史。作者、评审人和发布人的写操作继续按 Workspace 授权，审计和安全操作使用各自的产品角色。升级不涉及数据库迁移或重新授权。

管理 API 0.33.0 将批准有效性和已生效的内部发布分开判定。源站按当前读取权限、槽位指针及 AccessBlock 提供制品。准备 Release 和 `ACTIVATE` 要求当前批准。内部 `RESTORE` 核对原批准证据、独立发布人及槽位历史。

管理 API 0.35.0 将审计员的任命和撤销交给对该产品独立的平台管理员。产品负责人仍可查询本产品审计员。管理员使用 `role=internal_auditor` 分页查询审计员。既有任命保留，可由有权管理员复核。

管理 API 0.36.0 扩充治理与发布的读取权限。维护者读所属 Workspace 的治理材料，安全角色读本产品的资产目录和发布记录，`internal_reader` 读所属 Workspace 的资产目录、Release 元数据和发布记录，并通过当前槽位下载内部制品。服务端每次从槽位下载都核对 AccessBlock 和现行权限。先执行 `fabric-migrate -apply` 应用 `productgovernance/010_internal_reader`，再更换所有 API 进程，最后授予新角色。 [读取权限与升级说明](docs/F05_Security_and_Multitenancy.md#612-内部发布审计与读取权限033003500360)

管理 API 0.37.0 按 [CR-0001](decisions/CR-0001.md) 取消评审和批准（PR #74）。发布人准备 Release 时指定修订的一次检查（`check_run_id`），检查须在现行策略和工具下完成；结果为 `FAIL` 或 `UNKNOWN` 的可豁免规则逐条写理由豁免（`waivers`），不可豁免的规则没通过时不能准备。同一个人可以导入、检查、准备 Release 并发布；访问阻断仍由另一位安全评审人解除。激活和恢复只核对 Release 记录的检查，策略更新后已有 Release 仍可发布和恢复；`ACTIVATE` 替换另一修订的 Release 时，检查仍须以那个修订为对比基线。提交、撤回和决定评审的三个操作删除，现有 57 个操作；评审只能读取，`internal_reviewer` 不再授予，槽位不再有 `approval_problem`。这是破坏性变更，调用方要改用 `check_run_id` 准备 Release。

升级执行 `fabric-migrate -apply`，不需要重新授权：应用 `contractregistry/005`、`006` 和 `policyreview/007`，仍在评审中的修订改为 `WITHDRAWN`，开着的评审记为撤回，理由引用 CR-0001，已有结论不变。迁移只处理执行时还开着的评审，旧进程应尽快换完。此后的构建（契约不变）收回应用角色对四张评审表的 `INSERT` 和 `contract_revision` 的 `UPDATE`：`fabric-migrate -apply -grant-to <application_role>` 会撤销这五项，所以执行前先停掉所有 0.36.x 及更早的进程，否则它们写评审会被拒绝，重启时也拒绝启动；0.37.0 的进程不受影响。门户随 PR #75 改为“导入 → 检查 → 准备 Release → 发布”。实现详见 [F03 §5.15](docs/F03_Domain_and_Data_Model.md#515-release-依据检查cr-000103700)、[F04 §11.6](docs/F04_API_Contract_Governance.md#116-已实现的-release-与发布接口) 和 [F05 §6.14](docs/F05_Security_and_Multitenancy.md#614-取消评审后的权限cr-000103700)。授权矩阵中 AC-03、AC-35、AC-50、AC-51 退役，编号保留。

管理 API 0.38.0 按 [CR-0002](decisions/CR-0002.md)（内部使用从简）让按编号下载看这个 Release 的阻断：产品的任一发布目标上有范围是这个 Release、而且没有解除的访问阻断时，`getContractReleaseArtifact` 答 409 `RELEASE_ACCESS_BLOCKED`，解除以后恢复；目标和资产的阻断、产品暂停不挡，来源和元数据照常读。拒绝记 DENY-03、`release.download`。读阻断之前先对产品的全部发布目标加共享锁，放置和解除阻断与下载因此有确定的先后。没有迁移，不需要重新授权，操作数仍是 57；全部进程换完以后才对每次下载生效。CR-0002 另有三项只改文档：审计员看到行动人的原值（AC-29 去掉 `fields.minimized`），平台管理员留在部署配置（每个副本同一份设置，改动走部署自身的变更控制，Fabric 不审计），暂不建 `AssetSearchIndex`。实现详见 [F05 §6.15](docs/F05_Security_and_Multitenancy.md#615-内部使用从简cr-000203800)、[F04 §11.8](docs/F04_API_Contract_Governance.md#118-已实现的查询接口) 和 [F03 §5.12](docs/F03_Domain_and_Data_Model.md#512-已实现的查询与追溯)。

## 阅读入口

- [index.html](index.html) — 统一的离线 HTML 文档阅读入口。
- [docs/F01_Product_Requirements.html](docs/F01_Product_Requirements.html) — F01 产品需求与能力蓝图（八幅图）。
- [docs/F02_System_Architecture.html](docs/F02_System_Architecture.html) — F02 总体架构设计（八幅图）。
- [docs/F03_Domain_and_Data_Model.html](docs/F03_Domain_and_Data_Model.html) — F03 领域与逻辑数据设计（十幅图）。
- [docs/F04_API_Contract_Governance.html](docs/F04_API_Contract_Governance.html) — F04 API 契约与治理规范（八幅图）。
- [docs/F05_Security_and_Multitenancy.html](docs/F05_Security_and_Multitenancy.html) — F05 安全与多租户设计（十幅图）。
- [docs/F06_Developer_Experience.html](docs/F06_Developer_Experience.html) — F06 双门户设计（八幅图）。
- [docs/F07_Engineering_and_Quality.html](docs/F07_Engineering_and_Quality.html) — F07 Go/DDD、工程实现、质量、证据设计（九幅图）。
- [docs/F08_Delivery_and_Acceptance.html](docs/F08_Delivery_and_Acceptance.html) — F08 实施路线图、六道门禁与真实验收设计（九幅图）。
- [内部门户检查与发布工作台运行说明](cmd/fabric-portal/README.md)
- [CargoPai 合作方授权读取运行说明](cmd/fabric-api/PARTNER.md)
- [内部工程门户原型](portal/prototypes/internal-engineering.html) · [CargoPai Developers 原型](portal/prototypes/cargopai-developers.html) — 离线 Local Mock。
- Markdown 权威源：[F01](docs/F01_Product_Requirements.md) · [F02](docs/F02_System_Architecture.md) · [F03](docs/F03_Domain_and_Data_Model.md) · [F04](docs/F04_API_Contract_Governance.md) · [F05](docs/F05_Security_and_Multitenancy.md) · [F06](docs/F06_Developer_Experience.md) · [F07](docs/F07_Engineering_and_Quality.md) · [F08](docs/F08_Delivery_and_Acceptance.md)。

**阶段交接：** [F03 原始约束任务单（文档索引内）](docs/README.md) 已用于编写 F03；F03 设计完成不意味着上游 ADR 或域模型已获具名批准。F04 已依据交接完成设计稿；F05 已依据交接完成设计候选；F06 已依据交接完成双门户设计候选；F07 工程与质量设计已形成候选；F08 已形成交付与验收设计候选，当前八份核心文档齐备，下一步为集中审查与正式采用裁决。

## 正式核心文档目录（保持既定路径）

| 编号 | 正式源文件路径 | 当前状态 |
|---|---|---|
| F01 | `docs/F01_Product_Requirements.md` | 内容定稿、待具名签收 |
| F02 | `docs/F02_System_Architecture.md` | 架构设计候选、待验证与批准 |
| F03 | `docs/F03_Domain_and_Data_Model.md` | 设计完成候选，待评审和 PoC 验证 |
| F04 | `docs/F04_API_Contract_Governance.md` | 设计候选，待具名审查与完整工具链验证 |
| F05 | `docs/F05_Security_and_Multitenancy.md` | 安全设计候选，待具名评审与真实验证 |
| F06 | `docs/F06_Developer_Experience.md` | 设计候选，包含 F06-A/B/C、双离线交互原型、待具名 UX 与安全审查 |
| F07 | `docs/F07_Engineering_and_Quality.md` | 工程设计候选，含 30 控制、15 工具项、42 场景，未执行真实 PoC |
| F08 | `docs/F08_Delivery_and_Acceptance.md` | 交付与验收设计候选，真实运行和发布签收均 NOT_RUN |

## 工程目录

```text
idp-api-fabric/
├── README.md
├── index.html
├── docs/
│   ├── README.md
│   ├── F01_Product_Requirements.md
│   ├── F01_Product_Requirements.html
│   ├── F02_System_Architecture.md
│   ├── F02_System_Architecture.html
│   ├── F03_Domain_and_Data_Model.md
│   ├── F03_Domain_and_Data_Model.html
│   ├── F03_Logical_Model.csv
│   ├── F04_API_Contract_Governance.md
│   ├── F04_API_Contract_Governance.html
│   ├── F05_Security_and_Multitenancy.md
│   ├── F05_Security_and_Multitenancy.html
│   ├── F06_Developer_Experience.md
│   ├── F06_Developer_Experience.html
│   └── F06_Page_Inventory.csv
├── contracts/examples/
│   ├── openapi/                     # F04 OpenAPI DESIGN_EXAMPLE
│   ├── asyncapi/                    # F04 AsyncAPI DESIGN_EXAMPLE
│   └── catalog/                     # F04 Catalog DESIGN_EXAMPLE
├── governance/policies/
│   ├── F01_Project_Manifest_DRAFT.json
│   ├── F04_Rules_Registry.json
│   ├── F05_Authorization_Policy_DRAFT.json
│   ├── F05_Access_Matrix.csv
│   └── F05_Threat_Register.csv
├── tests/acceptance/
│   ├── F01_requirement_trace.csv
│   ├── F02_architecture_trace.csv
│   ├── F02_architecture_scenarios.csv
│   ├── F03_domain_trace.csv
│   ├── F03_domain_scenarios.csv
│   ├── F04_contract_trace.csv
│   ├── F04_governance_scenarios.csv
│   ├── F05_security_trace.csv
│   ├── F05_security_scenarios.csv
│   └── fixtures/F04_negative_*.yaml
├── decisions/
│   ├── ADR-0001.md
│   ├── ADR-0002.md
│   ├── ADR-0003.md
│   ├── ADR-0004.md
│   ├── ADR-0005.md
│   ├── ADR-0006.md
│   ├── ADR-0007.md
│   ├── ADR-0008.md
│   ├── ADR-0009.md
│   ├── CR-0001.md
│   ├── CR-0002.md
│   └── CR-0003.md
├── portal/prototypes/              # F06-A / F06-B 离线高保真交互原型和共享 CSS
└── scripts/
    ├── requirements-docs.txt
    ├── build_docs.py               # F01
    ├── build_f02.py                # F02
    ├── update_index.py
    ├── generate_trace.py           # F01
    ├── generate_f02_trace.py       # F02
    ├── validate_f01.py
    ├── validate_f02.py
    ├── build_f03.py
    ├── generate_f03_assets.py
    ├── update_f03_index.py
    ├── validate_f03.py
    ├── build_f04.py
    ├── validate_f04.py
    └── verify_manifest.py
```

`contracts/examples` 现仅包含 F04 的 **DESIGN_EXAMPLE** 样本，不代表已实现业务契约；`portal/prototypes` 已形成两套独立离线交互原型（仅 LOCAL_MOCK，无真实授权、API 调用或发布）。`.gitkeep` 仅保留规划目录。

## 构建和静态检查

```bash
python -m pip install -r scripts/requirements-docs.txt
python scripts/generate_trace.py
python scripts/generate_f02_trace.py
python scripts/generate_f03_assets.py
python scripts/build_docs.py
python scripts/build_f02.py
python scripts/build_f03.py
python scripts/build_f04.py
python scripts/generate_f05_assets.py
python scripts/build_f05.py
python scripts/update_index.py
python scripts/update_f03_index.py
python scripts/validate_f01.py
python scripts/validate_f02.py
python scripts/validate_f03.py
python scripts/validate_f04.py
python scripts/validate_f05.py
python scripts/validate_f06.py
python scripts/generate_f07_assets.py     # 设计资产初始化；会拒绝覆盖后来填报的真实证据
python scripts/build_f07.py
python scripts/update_f07_index.py
python scripts/validate_f07.py
python scripts/verify_manifest.py
```

生成的 HTML 不依赖在线字体、远程脚本或图片资源。建议通过浏览器打开 `index.html`，再查阅 F01–F08。

## 内容和状态原则

1. **单一版本 V1.0**：在原始工程目录内累计扩展，不为每份设计另建产品版本。
2. **一个权威正文源**：编辑 Markdown；HTML 由脚本重建。不得只修改 HTML。
3. **上位规范不降级**：继续以 `IDP Supply Chain DDD Product Engineering Standard V1.0` 为项目设计规则来源；具名采用和 Profile 由治理责任人审批。
4. **架构意见不是已实施**：F02 的 Go/存储/发布方案和 ADR 均为设计候选，不表示已通过工程 PoC、压测、安全/恢复演练。
5. **真实证据要求**：F01/F02/F03/F04/F05 的真实业务与运行测试状态是 `NOT_RUN`，发布门禁 `STD-CP1…CP6` 未做正式结论，不得虚构 PASS。

## F04 本地验证与限制

`python scripts/validate_f04.py` / `python scripts/validate_f05.py` 仅执行本地 PyYAML/自定义规则**子集**，检验规则注册表、正/负样例、CSV 完整性，绝不代表完整 OAS 3.1/AsyncAPI 3.1 规范认证、代码生成、发布权限、运行时审计或安全认证通过。F04 24 个端到端场景均为 `NOT_RUN`。F05 交接要求见 `docs/README.md`。


## F05 安全设计的可审查资产与界限

- [F05 授权矩阵](governance/policies/F05_Access_Matrix.csv) 收录 54 条候选操作级规则。负责人管理、列表、受众配置和品牌的实现见 F05 §6.1、§6.3，契约评审的实现见 F05 §6.6，评审队列的条目 AC-54 见 F05 §6.13。AC-03、AC-35、AC-50、AC-51 已按 CR-0001 退役（`RETIRED`，编号保留），取消评审后的权限见 F05 §6.14。AC-29 按 CR-0002 去掉 `fields.minimized`，见 F05 §6.15。
- [F05 STRIDE 风险登记册](governance/policies/F05_Threat_Register.csv)：29 项威胁及待具名 Owner/残余风险。
- [F05 授权策略设计 JSON](governance/policies/F05_Authorization_Policy_DRAFT.json)：仅为可读/可检查样例，不是可投入生产的 PDP。
- [F05 安全测试计划](tests/acceptance/F05_security_scenarios.csv)：36 个场景均为 `NOT_RUN`。
- [F05 需求追溯](tests/acceptance/F05_security_trace.csv)：覆盖 71 项 FR + 18 项 NFR；按直接责任、协作和后续阶段分类。
- [ADR-0007](decisions/ADR-0007.md)：受限访问失败关闭及安全撤销，状态仍为 Proposed。
- [ADR-0008](decisions/ADR-0008.md)：检查之外执行的 10 条规则如何计入检查结论，状态仍为 Proposed。CR-0001 之后没有评审人读检查结论，开发侧建议不再推进，留待 Governance Owner 关闭。
- [CR-0001](decisions/CR-0001.md)：内部使用不再设审批，准备 Release 以检查为准，已决定，管理 API 0.37.0 已实现（PR #74）。设计正文已同步：AC-03、AC-35、AC-50、AC-51 退役，AC-04 去掉 `decision.valid`，AC-52–AC-54 只读历史；F04-GOV-016、025 的含义变化只写在 F04 §11.4，规则登记表不改。
- [CR-0002](decisions/CR-0002.md)：内部使用从简，裁决四处设计与实现不一致，已决定（项目开发负责人，2026-11-26）。按编号下载看这个 Release 的阻断，管理 API 0.38.0 已实现；审计员看到行动人的原值、平台管理员留在部署配置、暂不建 `AssetSearchIndex` 三项只改文档。设计正文、F05 资产和 MANIFEST 已同步。
- [ADR-0009](decisions/ADR-0009.md)：Fabric 经 IDP Gateway 的发布意图接口发布外部提供方 API 的出站路由，状态 Proposed。外部提供方 API 是资产的一种来源，网关路由目标是发布目标的第二种，一次激活按路由表展开成若干条发布意图，`approval_ref` 指向 Fabric 的 Release 和它的检查；附 20 个需与网关负责人对齐的问题。
- [CR-0003](decisions/CR-0003.md)：Fabric 接下外部提供方 API 的出站路由发布，放在 V1，MVP 范围不变，已决定（项目开发负责人，2026-11-26）。外部提供方 API 单设一个平台产品；网关路由沿用 CR-0001、不另设确认，待网关负责人书面接受。实现前先与网关负责人对齐 ADR-0009 的问题，F01–F08 正文随实现同步。

**注意**：本阶段所谓通过仅指文档、矩阵、脚本与压缩包的静态一致性；不存在真实渗透、安全审计、IAM/Gateway/CDN 联调或生产恢复测试 PASS。F06 双门户设计已完成；下一阶段应依据 `docs/README.md` 的 F07 交接约束开展工程实现与质量设计。

## F06 双门户交付

- [F06 设计 Markdown](docs/F06_Developer_Experience.md) / [HTML](docs/F06_Developer_Experience.html)（F06-A 内部工程、F06-B CargoPai Developers、F06-C 共享设计系统）
- [F06-A 内部交互原型](portal/prototypes/internal-engineering.html) · [F06-B CargoPai 外部门户](portal/prototypes/cargopai-developers.html) · [共享 Token 样式](portal/prototypes/shared.css)。全部为 LOCAL_MOCK。
- [页面清单](docs/F06_Page_Inventory.csv)：21 页/共享视图；[需求追溯](tests/acceptance/F06_experience_trace.csv)：89 行；[体验场景](tests/acceptance/F06_experience_scenarios.csv)：32 行，状态 NOT_RUN。
- 页面清单的 `status` 列：`MOCK/NOT_RUN` 只有离线原型；`IMPLEMENTED/NOT_RUN` 和 `PARTIAL/NOT_RUN` 表示已有完整或部分实现，斜杠后面是真实验收，仍未执行。下列内部门户的状态假定 PR #72（已合入）和 PR #75（门户依据检查准备 Release，尚未合入）都已合入 main。
  - IN-01 已实现：[内部门户](cmd/fabric-portal/README.md)的「工作台」汇总当前 Workspace 最新检查失败的资产、已检查但还没准备 Release 的修订和被访问阻断的发布，每项打开资产详情。CR-0001 之后没有待审核。
  - IN-02 已实现：产品和 Workspace 两个选择框，列表来自服务端按角色和成员关系过滤的结果；切换产品时中止上一产品的请求，清空工作台和目录；加载中、没有可见产品或 Workspace 时有文字提示。加载时显示的是文字，不是骨架屏。
  - IN-03 已实现：「API 目录」按关键字、类型、最近检查结论和“已检查、未准备 Release”筛选，筛选和分页在服务端；列出领域、类型、生命周期、最新修订、最近检查和最新 Release。
  - IN-04 已实现：资产详情按从新到旧列出修订及其源文件摘要和引用闭包摘要，选中修订显示它的检查、Release 及豁免；CR-0001 之前的评审列为只读的「历史评审」。
  - IN-05 已实现：检查详情按类别列出每条规则的结论、级别、可否豁免、工具和发现，检查列表标明是否带兼容性对比基线；修订面板的「兼容性比较」用 `compareRevisionCompatibility` 比较该修订与同一资产的另一修订，列出整体判定和每处变化的分类、代码和 JSON Pointer，判定用文字标明。不显示源文件的并排差异。
  - IN-06 已实现，页面改为「检查与准备 Release」（编号不变，原为 Review & Approval）：作者运行检查，可选对比基线；发布人选定检查，逐条填写豁免理由后准备 Release，有不可豁免的规则没通过时不能提交。不设评审人。门户里看不到审计证据。
  - IN-07 已实现：资产详情的「发布」按内部目标显示槽位和最近操作，带代次发布、撤销和恢复；「访问阻断与解除」建立和解除访问阻断。
  - EX-03 部分实现：[CargoPai Developers](cmd/fabric-developer-portal/README.md) 展示一个资产的操作、参数、请求体和响应的 Schema，cURL、Go、TypeScript 示例，响应码说明和搜索（PR #70）。只展示一项资产，不显示发布时间和环境。

**F08 已完成交付设计**：见 `docs/F08_Delivery_and_Acceptance.md`，真实测试与具名批准仍需实施，Mock 和静态校验不能充当运行验证。

### F06 设计原型的可复现验证

```bash
python scripts/validate_f06.py
# 可选：需要安装 playwright 并提供可执行 Chromium（参见脚本）
python scripts/test_f06_browser.py
```

**边界**：浏览器校验仅验证离线原型的交互、布局和本地状态逻辑；不涉及真实产品 API、安全鉴权、Gateway、CDN、正式无障碍或用户验收。


## F07 工程设计交付

- [F07 工程实现与质量设计（HTML）](docs/F07_Engineering_and_Quality.html) · [Markdown 权威源](docs/F07_Engineering_and_Quality.md)。
- [30 项工程控制（DRAFT）](governance/policies/F07_Engineering_Controls_DRAFT.yaml)，[42 项工程验证场景](tests/acceptance/F07_engineering_scenarios.csv)，[15 项工具兼容验证](tests/acceptance/F07_tool_compatibility_matrix.csv)，[全部 89 项 FR/NFR 承接](tests/acceptance/F07_engineering_trace.csv)。
- `python scripts/generate_f07_assets.py` 用于生成**尚未执行**的验证资产，不会覆盖以后被填报的真实执行证据；`python scripts/build_f07.py` 生成离线 HTML，`python scripts/validate_f07.py` 验证设计结构。
- F07 工程蓝图**不是 Go 生产服务**；Tool Matrix 所有真实测试 `NOT_RUN`，ADR-0001–0007 仍 `Proposed`。下一阶段仅新增 F08。

## F08 交付与验收资产

- [F08 设计 HTML](docs/F08_Delivery_and_Acceptance.html) · [Markdown](docs/F08_Delivery_and_Acceptance.md)。
- [12 工作包](tests/acceptance/F08_delivery_work_packages.csv)、[10 里程碑](tests/acceptance/F08_milestones.csv)、[24 核心验收](tests/acceptance/F08_acceptance_matrix.csv)、[51 详细验收](tests/acceptance/F08_detailed_acceptance_cases.csv)、[89 需求追溯](tests/acceptance/F08_delivery_trace.csv)、[10 待决](tests/acceptance/F08_open_decisions.csv)。
- [真实证据模板](tests/acceptance/F08_evidence_record_TEMPLATE.json) 含环境、摘要、观测与审核字段，默认 `NOT_RUN`。
- `python scripts/generate_f08_assets.py` 只初始化设计计划且拒绝覆盖已有执行证据；`python scripts/build_f08.py` 构建 HTML，`python scripts/validate_f08.py` 做静态与结构校验。
- F08 不宣布 F01–F07 的设计得到正式批准，更不宣称 Go 生产系统、IAM/Gateway/CDN 联调、渗透、DR、UAT 已通过。

## V1.0 设计一致性与冻结状态

- 发布权威：`PublicationTarget` 定义分发范围，`PublicationSlot = Target × APIAsset` 为唯一生效 Release 指针；`UNIQUE(target_id, api_asset_id)` + Slot `generation` CAS；Target-wide AccessBlock 与各 Slot 指针独立。参见 F02、F03、ADR-0004。
- 上位门禁：`STD-CP4 = 专业体验（Product/UX）`，`STD-CP5 = 工程与质量（Engineering/QA/Security）`；详见 F07/F08。
- 直接验收设计：原 24 个 AC + 新增 51 个 DC，覆盖 89 项 FR/NFR；**均为计划，真实执行全为 NOT_RUN**。
- 正式冻结仍被 `Manifest DRAFT`、`ADR-0001…0007 Proposed`、`OD-08-01…10 OPEN` 和真实技术/安全验收前置条件阻断；文档一致性修复不构成组织批准。

## 许可证

本仓库采用 [PolyForm Noncommercial License 1.0.0](LICENSE)：允许非商业用途，禁止任何商业使用。
