# IDP API Fabric — Engineering Baseline

**项目代号：** `idp-api-fabric`  
**文档版本：** V1.0（保持原有版本，不增设 V1.1/V2.0）  
**状态：** F01 内容定稿、待具名签收；**F02 总体架构设计候选、待具名评审及工程 PoC**；**F03 领域与数据设计候选、待具名评审**；**F04 契约与治理规范设计候选、待具名评审与工具验证**；**F05 安全与多租户设计候选、待具名安全评审及真实验证**；**F06 双门户体验设计候选，待具名评审与真实 UX/安全验证**；**F07 工程与质量设计候选、待具名评审与真实 PoC**；**F08 交付与验收设计候选、待具名签收，真实验收 NOT_RUN**。  
**日期：** 2026-11-09

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

- [F05 授权矩阵](governance/policies/F05_Access_Matrix.csv)：35 条候选操作级规则，所有服务器端授权执行仍未实现。
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
