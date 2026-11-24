# IDP API Fabric — Engineering Baseline

**项目代号：** `idp-api-fabric`  
**文档版本：** V1.0（保持原有版本，不增设 V1.1/V2.0）  
**状态：** F01 内容定稿、待具名签收；**F02 总体架构设计候选、待具名评审及工程 PoC**；**F03 领域与数据设计候选、待具名评审**；**F04 契约与治理规范设计候选、待具名评审与工具验证**；**F05 安全与多租户设计候选、待具名安全评审及真实验证**；**F06 双门户体验设计候选，待具名评审与真实 UX/安全验证**；**F07 工程与质量设计候选、待具名评审与真实 PoC**；**F08 交付与验收设计候选、待具名签收，真实验收 NOT_RUN**。  
**日期：** 2026-11-09

## 工程实现同步（2026-11-19）

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

管理 API 0.26.0–0.28.0 加入查询与追溯：资产所属 Workspace 的作者、评审人和发布人能列出一个发布目标上的槽位，也能列出一个资产发布到了哪些目标，列表里的每一项和单独读到的槽位完全一样，含 `blocked` 和 `approval_problem`（0.26.0）；能按编号下载一个 Release 的制品，已经被替换或撤回的也行，也能读出一个 Release 的来源：它依据的修订（含提交人声明的 Git 来源）、检查和批准，复审之后不变（0.27.0）；产品多了一个角色 `internal_auditor`（审计员），由产品负责人授予，审计员按产品读审计记录，新的在前，可以按时间、动作、结果、行动人、资源和请求编号筛选（0.28.0）。新增 5 个操作，到 0.28.0 共 57 个。按编号下载不看发布目标上的访问阻断，也不重新核对批准；审计记录里的行动人如实给出，没有脱敏。这两处与设计正文不一致，待裁决。

从 0.25.0 升级需要重新授权：先执行 `fabric-migrate -apply -grant-to <application_role>`，应用 `productgovernance/009` 和 `platformoperations/002`，并给应用角色补上审计表的 `SELECT`（它仍然没有 `UPDATE` 和 `DELETE`），再换进程。只迁移不授权时，新进程拒绝启动：`db: the application role lacks privileges this build needs; run fabric-migrate -apply -grant-to <role>: missing SELECT on platformoperations.audit_record`。建索引用普通 `CREATE INDEX`，建的时候审计表的写入要等。旧进程在迁移和授权之后照常运行。所有进程升级以后，才授予 `internal_auditor`：旧进程的契约里没有这个角色，会拒绝授予请求；库里有了审计员，它列产品角色会返回 500。0.26.0 和 0.27.0 本身没有迁移，也不需要授权。

实现详见 [F03 §5.12](docs/F03_Domain_and_Data_Model.md#512-已实现的查询与追溯)、[F04 §11.8](docs/F04_API_Contract_Governance.md#118-已实现的查询接口) 和 [F05 §6.9](docs/F05_Security_and_Multitenancy.md#69-已实现的查询与审计读取的权限)。授权矩阵没有新增条目。

管理 API 0.29.0–0.31.0 加入内部工作台要先读的几样东西，都是读：调用人能问自己是谁、是不是平台管理员，也能问自己在一个产品里持有哪些角色、是哪些 Workspace 的成员，不能问别人（0.29.0，平台管理员来自部署配置，不在库里）；资产所属 Workspace 的作者、评审人和发布人能列出一个 Workspace 里的评审，先提交的在前，可以按状态和提交人筛选，读到的是整个 Workspace 的全部评审（0.30.0）；他们读到的资产多一个概况：最新修订、最新修订最近一次检查、最新 Release，以及最新修订已批准而还没做 Release，资产列表可以按检查结论和“可发布”筛选（0.31.0）。概况是读的时候现算的，没有汇总表；负责人和维护者读得到资产，读不到概况。新增 3 个操作，共 60 个。没有读权限的人每调用一次留下一条拒绝记录，页面应当先问身份再决定调不调。没有做汇总表和事件投影，设计里目录列表的可重建投影 `AssetSearchIndex` 仍然没有，这与设计不一致，待裁决；概况只给读得了修订的人，评审队列沿用读评审的名单，也待裁决。

从 0.28.0 升级只需要 `fabric-migrate -apply`，不需要重新授权，再换进程：它应用 `policyreview/006`，只建一个索引，不改行，应用角色的权限不变。库没迁移时，0.30.0 起的新进程拒绝启动：`db: the database is not migrated for this build; run fabric-migrate -apply first: migration policyreview/006_review_queue_order is not recorded`。建索引用普通 `CREATE INDEX`，建的时候新提交评审的写入要等，读不受影响。旧进程在迁移之后照常运行，对三个新路径答 404 `ROUTE_NOT_FOUND`，对带 `check_status` 或 `release_pending` 的资产列表答 400 `REQUEST_INVALID`，资产的应答里没有概况，所以用这些接口的页面等所有进程升级完再上。0.29.0 和 0.31.0 本身没有迁移，也没有新角色和新配置。

实现详见 [F03 §5.13](docs/F03_Domain_and_Data_Model.md#513-已实现的工作台查询)、[F04 §11.9](docs/F04_API_Contract_Governance.md#119-已实现的工作台查询接口) 和 [F05 §6.10](docs/F05_Security_and_Multitenancy.md#610-已实现的工作台查询的权限)。授权矩阵没有新增条目。

管理 API 0.32.0 支持产品负责人读取本产品各 Workspace 的资产概况、契约修订和原文、检查、评审、Release 及发布历史。作者、评审人和发布人的写操作继续按 Workspace 授权，审计和安全操作使用各自的产品角色。升级不涉及数据库迁移或重新授权。

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
│   └── ADR-0007.md
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

- [F05 授权矩阵](governance/policies/F05_Access_Matrix.csv) 收录 53 条候选操作级规则。负责人管理、列表、受众配置和品牌的实现见 F05 §6.1、§6.3，契约评审的实现见 F05 §6.6。
- [F05 STRIDE 风险登记册](governance/policies/F05_Threat_Register.csv)：29 项威胁及待具名 Owner/残余风险。
- [F05 授权策略设计 JSON](governance/policies/F05_Authorization_Policy_DRAFT.json)：仅为可读/可检查样例，不是可投入生产的 PDP。
- [F05 安全测试计划](tests/acceptance/F05_security_scenarios.csv)：36 个场景均为 `NOT_RUN`。
- [F05 需求追溯](tests/acceptance/F05_security_trace.csv)：覆盖 71 项 FR + 18 项 NFR；按直接责任、协作和后续阶段分类。
- [ADR-0007](decisions/ADR-0007.md)：受限访问失败关闭及安全撤销，状态仍为 Proposed。

**注意**：本阶段所谓通过仅指文档、矩阵、脚本与压缩包的静态一致性；不存在真实渗透、安全审计、IAM/Gateway/CDN 联调或生产恢复测试 PASS。F06 双门户设计已完成；下一阶段应依据 `docs/README.md` 的 F07 交接约束开展工程实现与质量设计。

## F06 双门户交付

- [F06 设计 Markdown](docs/F06_Developer_Experience.md) / [HTML](docs/F06_Developer_Experience.html)（F06-A 内部工程、F06-B CargoPai Developers、F06-C 共享设计系统）
- [F06-A 内部交互原型](portal/prototypes/internal-engineering.html) · [F06-B CargoPai 外部门户](portal/prototypes/cargopai-developers.html) · [共享 Token 样式](portal/prototypes/shared.css)。全部为 LOCAL_MOCK。
- [页面清单](docs/F06_Page_Inventory.csv)：21 页/共享视图；[需求追溯](tests/acceptance/F06_experience_trace.csv)：89 行；[体验场景](tests/acceptance/F06_experience_scenarios.csv)：32 行，状态 NOT_RUN。

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
