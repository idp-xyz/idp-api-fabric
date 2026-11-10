# IDP API Fabric · 核心设计文档索引

项目根目录 `README.md` 是目录与工程约定的入口；`index.html` 是可离线阅读的统一 HTML 文档中心。

| 编号 | 正式文档 | 当前状态 |
|---|---|---|
| F01 | [Product Requirements & Capability Blueprint](F01_Product_Requirements.md) · [HTML](F01_Product_Requirements.html) | **V1.0 内容定稿，待具名签收** |
| F02 | [System Architecture](F02_System_Architecture.md) · [HTML](F02_System_Architecture.html) | **V1.0 架构设计候选，待具名评审/验证** |
| F03 | [Domain & Data Model](F03_Domain_and_Data_Model.md) · [HTML](F03_Domain_and_Data_Model.html) | **V1.0 领域模型设计候选，待具名评审** |
| F04 | [API Contract Governance](F04_API_Contract_Governance.md) · [HTML](F04_API_Contract_Governance.html) | **V1.0 设计候选，待具名审查及 F07 工具链 PoC** |
| F05 | [Security & Multitenancy](F05_Security_and_Multitenancy.md) · [HTML](F05_Security_and_Multitenancy.html) | **V1.0 设计候选，待具名安全审查与真实测试** |
| F06 | [Dual Portal Product & UX](F06_Developer_Experience.md) · [HTML](F06_Developer_Experience.html)（A/B/C） | **V1.0 设计候选，待具名 UX/安全评审和 F07/F08 实证** |
| F07 | [Engineering & Quality](F07_Engineering_and_Quality.md) · [HTML](F07_Engineering_and_Quality.html) | **V1.0 工程设计候选，待具名评审及真实 PoC** |
| F08 | [Delivery & Acceptance](F08_Delivery_and_Acceptance.md) · [HTML](F08_Delivery_and_Acceptance.html) | **V1.0 交付与验收设计候选，待批准、真实验收 NOT_RUN** |

与原规划完全一致：F06 在一份顶层文档内完成 F06-A 内部工作台、F06-B 外部开发者门户和 F06-C 设计系统。F07/F08 均已形成设计候选，真实系统实施与验收尚未开始。

## F08 交付与验收设计（当前有效）

- [F08 Markdown 权威源](F08_Delivery_and_Acceptance.md) / [离线 HTML](F08_Delivery_and_Acceptance.html)：12 工作包、10 里程碑、24 核心验收、六道标准门禁与九幅示意图。
- [F08 交付工作包](../tests/acceptance/F08_delivery_work_packages.csv) / [里程碑](../tests/acceptance/F08_milestones.csv) / [24 项核心验收](../tests/acceptance/F08_acceptance_matrix.csv) / [51 项详细验收](../tests/acceptance/F08_detailed_acceptance_cases.csv) / [89 项需求交付追溯](../tests/acceptance/F08_delivery_trace.csv)。
- [10 项待决](../tests/acceptance/F08_open_decisions.csv) / [真实证据模板](../tests/acceptance/F08_evidence_record_TEMPLATE.json)。**所有真实执行测试仍为 NOT_RUN；无人签署生产 GO。**
- F01–F07 Markdown 权威正文和 ADR-0001–0007 保持不变。原交接说明保留在本索引下方，供追溯。F08 指出 F07 门禁概括映射与上位标准的局部差异，待具名裁决。

## 当前可检查资产

- [F01 需求追溯](../tests/acceptance/F01_requirement_trace.csv)：全部 FR/NFR 的设计期关联，实际验收未运行。
- [F01 规范采用讨论输入](../governance/policies/F01_Project_Manifest_DRAFT.json)：不是已批准的 Project Manifest。
- [F02 架构追溯](../tests/acceptance/F02_architecture_trace.csv)：71 项 FR + 18 项 NFR 的设计章节与 ADR 关联。
- [F02 架构验证场景](../tests/acceptance/F02_architecture_scenarios.csv)：12 个场景均为 `NOT_RUN`。
- [F03 逻辑实体字典](F03_Logical_Model.csv)：从 F03 Markdown §5.4 的逻辑模型表生成，37 条设计条目，非 PostgreSQL DDL。
- [F03 需求—模型追溯](../tests/acceptance/F03_domain_trace.csv)：71 FR + 18 NFR，区分领域负责、支撑与后续范围。
- [F03 领域验证场景](../tests/acceptance/F03_domain_scenarios.csv)：18 个 DM-01–18 场景，均为 `NOT_RUN`。
- [F04 候选治理规则](../governance/policies/F04_Rules_Registry.json)：28 条规则，部分有本地示范子集，完整产品验证未执行。
- [F04 治理追溯](../tests/acceptance/F04_contract_trace.csv)：覆盖 71 FR + 18 NFR。
- [F04 治理验证计划](../tests/acceptance/F04_governance_scenarios.csv)：24 项场景，全部 `NOT_RUN`。
- [F05 候选权限矩阵](../governance/policies/F05_Access_Matrix.csv)：35 项操作级授权设计。
- [F05 STRIDE 登记册](../governance/policies/F05_Threat_Register.csv)：29 项威胁；Risk Owner 待确认。
- [F05 策略结构草案](../governance/policies/F05_Authorization_Policy_DRAFT.json)：可解析的设计样例，不是真实 PDP。
- [F05 需求—安全追溯](../tests/acceptance/F05_security_trace.csv)：89 项需求承接。
- [F05 负向测试计划](../tests/acceptance/F05_security_scenarios.csv)：36 个真实安全场景均 `NOT_RUN`。
- [ADR-0007](../decisions/ADR-0007.md)：新的安全撤销/隔离设计候选，未批准。
- [ADR-0001](../decisions/ADR-0001.md) 至 [ADR-0006](../decisions/ADR-0006.md)：所有 ADR 当前均为 **Proposed**，不是已批准的架构例外或实施授权。

F01、F02、F03 与 F04 均以 Markdown 为可维护正文源，HTML 为脚本生成的自包含阅读制品。脚本无须访问互联网；静态校验不等于工程、审查、上线或安全测试通过。

## F03 阶段执行交接（已用于 F03；原约束保留供审计）

> **本节为 F03 编制时继承的约束，非新增核心文档；F03 已形成设计候选稿，但 F02/F03 均未取得具名批准。** 版本仍为 V1.0。下一份核心文档的固定源文件名是 `docs/F03_Domain_and_Data_Model.md`，对应离线阅读版 `docs/F03_Domain_and_Data_Model.html`；不另起项目或复制独立版本仓库。

### A. 唯一权威输入和状态

1. **F01**：`docs/F01_Product_Requirements.md`，尤其 §3、§6、§7、§9、§12、§17；原有 `FR-001–071`、`NFR-01–18`、`AT-01–12` 必须保留编号与阶段边界。F01 内容定稿，但尚待具名签收。
2. **F02**：`docs/F02_System_Architecture.md`，尤其 §1.3、§5–§9、§11–§13、§17–§18；`AI-01–07` 是不得无记录削弱的拟议架构不变量。F02 为设计候选，需验证和批准。
3. **上位规范**：`IDP-SCG-STD-001` 《IDP Supply Chain DDD Product Engineering Standard V1.0》，以适用性声明、规则 ID、风险责任和 STD-CP1–CP6 门禁为约束；`governance/policies/F01_Project_Manifest_DRAFT.json` 仍是草案。
4. **决策现状**：`decisions/ADR-0001.md` 至 `ADR-0006.md` 全部为 `Proposed`；不能写成已批准。`tests/acceptance/F02_architecture_scenarios.csv` 中 12 个架构验证场景全部 `NOT_RUN`。

### B. F03 唯一使命和禁止漂移范围

**使命**：将 F01 的业务语义和 F02 的候选架构转化为可评审、可追溯、可实施的 DDD 领域模型与逻辑数据模型；明确**每个事实的所有者、每个聚合的不变量、每个事务的边界、每次状态迁移的合法条件、每个跨上下文协作的一致性方案**。

- **必须继承**：联邦式统一治理、产品自治、内部工程门户与外部产品开发者门户的独立授权体验、Go 模块化单体 + Worker *候选*、Git-first/受控导入、不可变 Release、CAS Publication 指针、可重建只读投影。
- **不得擅自改变**：业务产品 API 运行时仍属于各产品；Fabric 不是 API Gateway、IAM、CI/CD、业务 MDM、全局业务模型或 Agent Runtime；不能将 CargoPai 与 Parcel 的 `Shipment` 合并为一个 Fabric 领域实体。
- **不得越界实现**：不在 F03 开始完整编码、DDL 迁移执行、API 端点细节定稿、F04 治理规则全集、F05 安全 Threat Model、F06 高保真门户、F07 技术运维 PoC 或 F08 生产验收。可以列出交接契约和待决问题。
- **不得伪造确定性**：产品/租户负责人、风险等级、Go/数据库精确版本、SLO/RPO/RTO、工具兼容结果、测试通过状态尚未决定；保持 `TBD` / `Proposed` / `NOT_RUN`，必要时提出 ADR，不得静默覆盖 F01/F02。

### C. 必须精确建模的七个候选限界上下文

| 候选 BC（源自 F02 §5） | F03 必须澄清的事实权威 | 不能偷换成 |
|---|---|---|
| Product Governance | Product、Workspace、成员关系与所属范围 | OAuth 令牌签发或客户业务主数据 |
| API Catalog | API Asset、责任人、分组与生命周期 | 契约 Release 原文或搜索索引权威 |
| Contract Registry | Source/Import、Revision、Release、Digest/Manifest | 当前外部可见 Publication 指针 |
| Policy & Review | 策略版本、CheckRun、ReviewDecision、Waiver | Gateway 的最终运行时授权 |
| Publication | Target（Product×Audience×Environment×Portal）定义分发范围；Slot（Target×APIAsset）拥有每 API 生效指针、CAS generation、撤销和恢复事实 | 修改不可变 Release 内容 |
| Developer Access | Developer Organization、Consumer Application、Subscription/Access Grant 申请与状态（按 MVP/V1 范围） | IAM Client Secret、真实调用权的最终执行 |
| Platform Operations | Job、Outbox、Delivery、Audit、Reconciliation 的状态与证据 | 产品物流业务执行事实 |

七项是**候选语义边界**，不是固定的七个独立服务、数据库 Schema 或 Go Module。若调整边界，必须用事务不变量、用例及反例证明，并在 F03 标明与 F02 的差异及需复核的 ADR。

### D. F03 必交付的模型与图表（以正文为单一维护源）

1. **统一语言与标识辞典**：区分 Product、Tenant、Organization、Workspace、Developer Organization、Principal、Consumer Application、OAuth Client、API Asset、Contract Revision、Release、Publication Target 和 Publication；给出身份/归属/可见性语义。
2. **Context Map 与所有权表**：每个业务事实恰有一个权威 BC；标明公开语言、ACL、命令/查询、领域事件/集成事件、上下游；记录不应共享写入的边界。
3. **聚合设计**：聚合根、实体、值对象、实体生命周期、不变量、事务边界、乐观锁与并发写入策略；不能把数据库外键等同于跨聚合强一致性。
4. **逻辑 ERD 与关系约束**：关系基数、必填/可选、稳定 ID、复合唯一键、外键适用性、作用域键、索引候选、删除/保留/归档原则；明确逻辑模型不等于已批准 PostgreSQL 物理设计。
5. **完整的四类状态机**：Revision、Release、Publication、API Asset Lifecycle 分离；每个转换需触发者、前置条件、拒绝情况、幂等和审计证据。不得把发布指针、投递状态和内容状态压在一个 `status`。
6. **契约权威与发布链模型**：Source（Git/上传）→ Revision → Checks/Decision → immutable Release → Publication Target/Pointer → Portal Projection；原文摘要、构建制品摘要和策略版本分别保留。
7. **命令/查询/事件矩阵**：每个核心用例的授权主体、责任 BC、幂等作用域、事务参与者、同步/异步协作及重试语义；区别 Domain Events、Outbox Events、外部 Integration Events。
8. **多维授权所需的数据关系**：Product×Workspace×Organization/Tenant×Principal×Audience×Environment×Application/Grant 的关系及拒绝优先；**F03 设计关系，F05 决定完整策略与 Threat Model**。
9. **故障与竞态反例**：双发布者 CAS 冲突、批准后策略变更、对象存储与数据库部分失败、Worker 重复投递、索引滞后、紧急撤销与 Edge 缓存残留、跨产品权限拒绝、删除与历史追溯冲突。
10. **承接映射与待决清单**：每项关键业务规则关联 F01 FR/NFR、F02 AI/ADR、F03 实体/不变量/图、验收场景及后续 F04–F08 的责任文档。

建议至少提供：一幅正式 Context Map、一幅核心概念/授权关系图、一幅逻辑 ERD、一幅四状态机对照图、一幅发布与撤销时序图，以及一幅事务与异步一致性示意图。图表必须能对应正文实体/规则，禁止仅为装饰。

### E. 唯一贯通的业务示例（禁止切换到无关行业或虚构已实现系统）

以 **CargoPai 的一个 OpenAPI 契约** 为贯穿样本：Git Commit / 受控导入 → Revision → CheckRun → 审核 → 不可变 Release → 发布到 `CargoPai + partner + sandbox` 的 Portal Target → 授权外部开发者读取；再分别验证 **Parcel 未授权访问拒绝、并发发布 CAS 冲突、错误发布撤销与历史版本恢复、Worker 重试**。可在 F03 设计虚构的稳定样例 ID，但必须明确 `DESIGN_EXAMPLE`，不是真实 API 或真实环境测试证据。

### F. 与其余设计文件的严格分界

- **F04**：精确定义 OpenAPI/AsyncAPI、Schema/规则版本、兼容性判据、审核规则及例外审批；F03 只定义规则结果的领域对象与绑定关系。
- **F05**：具体 RBAC/ABAC、组织/租户威胁模型、凭证、网络和隐私安全；F03 只保障所需身份与授权关系能表达且不会越权。
- **F06**：两个门户页面、交互、设计系统；F03 只保障门户需要的查询视图与发布事实可表达。
- **F07**：实际 Go 包组织、PostgreSQL 迁移、Job/Outbox 代码、基准测试和恢复演练；F03 不声称实现完成。
- **F08**：正式端到端验收、责任批准、风险和六道门禁；F03 提供可验证的场景与设计期追溯。

### G. 完成与移交判据（Definition of Done）

- **文件路径固定**：`docs/F03_Domain_and_Data_Model.md`（权威源）和同名 `.html`（生成的离线阅读版）；同步维护本索引、根目录 `README.md` 和 `index.html`，**累计交付同一个 V1.0 工程包**。
- **追溯可审计**：新增 F03 需求—模型追溯 CSV 与验收场景 CSV；保持 F01 的全部 71 FR、18 NFR 标识，按“领域涉及 / 由后续设计承接 / 明确不适用”逐项说明，不应把 89 项都强行对应成数据库实体。
- **模型无矛盾**：关键名词只定义一次；每个权威事实只有一位 Owner；Release 不可变；Publication 指针和状态独立；所有跨产品读写都具有服务端授权前提；状态转换允许的失败/重试/回滚可被测试描述。
- **验证真实**：模型与图一致，关系/状态机可读、可追踪；执行文档构建、编号/链接/CSV 结构校验、桌面/移动端渲染和 ZIP 文件完整性校验。**架构 PoC、集成测试、安全压测、真实业务验收仍按实际 `NOT_RUN` 记录。**
- **决策纪律**：发现与 F01/F02 冲突时，优先保持已有基线并列为 `Open Decision` / 提议 ADR，由对应责任人批准后再调整；不得无记录重命名产品、增删核心文档或改变 F03 任务范围。

**F03 编制所遵守的原始任务指令（存档）**：直接在现有 `idp-api-fabric/` 工程上完成 F03 的完整领域与数据设计，先锁定统一语言与事实权威，再完成聚合与状态机、逻辑 ERD、跨域协作、一致性/竞态、验收追溯和图文并茂 HTML；无需重新规划产品、无需实施生产数据库、无需编写 F04–F08 正文。

## F04 阶段执行交接（已用于 F04；保留原始约束供审计）

> 此段是既定文档索引中的交接约束，不是第九份核心文档或新版本。F04 唯一正式源文件路径：`docs/F04_API_Contract_Governance.md`；阅读版同名 `.html`。**禁止在尚未审查 F02/F03 候选 ADR 的情况下把其建议写成已批准决策。**

**唯一输入**：F01 §6/§7/§9/§12（71 FR、18 NFR、12 AT 不改号）；F02 §7–§9/§12/§17（AI-01…07，ADR-0001…06 均 Proposed）；F03 §2/§4/§6/§8/§9/§11/§12/§16（INV-01…13、DM-01…18、OD-01…10）。`F03_Logical_Model.csv` 是逻辑关系索引，不是已经部署的 Registry Schema。

**F04 使命**：把 F03 的 `SourceSnapshot → Revision → CheckRun/ReviewDecision → immutable Release → Target×Asset Slot` 领域语义落为可机器检验的 **OpenAPI/AsyncAPI 契约与治理规范**；定义支持矩阵、版本与兼容性、规则与例外、审核许可、事件及错误契约、公共文档加工和发布门禁，维持产品自治及受众隔离。

**必须明确的九个设计输出**：

1. OpenAPI 3.1.2 工程候选基线与 OAS 3.2.1 兼容评估矩阵；AsyncAPI 3.1.0 的预期支持及依赖工具验证义务，禁止宣称未经测试的工具已全兼容。
2. API Catalog 元数据和 Contract Source/Revision/Release 的 Schema/ID/引用与 `digest` 语义；修订原文、引用闭包、Build Manifest、正式制品分别校验。
3. 以 HTTP 语义、请求/响应、鉴权、事件、错误/枚举、异步消费等为单位的 **Breaking / Risky / Compatible / Unknown** 分类与门禁策略；不能把静态 diff 等同业务兼容性证明。
4. 版本命名、Release Label、策略版本、废弃通知与 Sunset 适用范围；禁止将 API Path Version、OpenAPI Spec Version 和 Release ID 混为一谈。
5. 检查流水线：Parse → Lint → Diff → Sensitive Scan → Policy Evaluate → Independent Review → Release；每项检查关联固定 Revision Digest、Policy Digest 与 Tool Version。
6. 全局强制规则、产品追加规则、可豁免/不可豁免分类、例外期限、审查职责分离，以及失效后重新审批条件。
7. PublicationTarget 与 PublicationSlot `(target_id,api_asset_id)` 的**准确发布作用域**，按受众/环境区别文档与实际业务 API 调用权；对 F03 OD-03 和 ADR-0004 做具名裁决前不得静默将其写成已批准。
8. Fabric 自身管理 API 与事件契约（Design Example）、RFC 9457 错误语义候选、Idempotency-Key 作用域、CAS 冲突、`PENDING/UNKNOWN` 外部依赖状态；**设计示例不等于已实现路由或验证 PASS**。
9. 可执行最小治理样例、Golden Samples、负向样例、机器可读 Rules Registry、需求/契约/测试追溯和所有场景 `NOT_RUN` 初始状态。

**不属于 F04**：不重新定义 F03 聚合与逻辑 ERD；不替代 F05 的完整身份威胁模型；不编写 F06 高保真双门户；不替代 F07 的 Go/工具兼容 PoC、压测和上线演练；不签署 F08 正式门禁。可以给这些文档提供接口与验收输入。

**F04 设计交付完成定义（历史交接要求）**：所有约定内容有明确 Owner、语义、错误/拒绝行为和可执行样例；与 F01/F02/F03 的需求和不变量能够双向追溯；关键跨租户、错误公开、策略过期、同 Slot CAS 并发、重复事件、工具不支持规范版本均有拒绝/恢复契约；Markdown 可重建离线 HTML，统一 `index.html` 仅累积新增 F04；ZIP 可重新解压并通过清单验证。任何正式标准采纳/审查结果依然需要真实批准和测试证据。


## F05 阶段执行交接（F04 完成后的受控入口，F05 正文尚未创建）

> F05 唯一正式源文件为 `docs/F05_Security_and_Multitenancy.md`，离线阅读版同名 `.html`。**继续 V1.0 工程目录；F01/F02/F03/F04 正文不得在无变更裁决情况下被 F05 静默覆盖。**

### F05 必须继承的已知输入

- **F01** `FR-001–006 / FR-025–028 / FR-032–036 / FR-047–053 / FR-062–068` 与 `NFR` 以及正式上位 `IDP-SCG-STD-001` 规则采用草案。
- **F02** 信任边界、控制平面/数据平面区分、双门户隔离、外部 IAM/Gateway 适配和 `AI-01–07`；全部 ADR 仍处 Proposed。
- **F03** `Product/Workspace/Principal/DeveloperOrg/ConsumerApplication/Grant` 身份引用，`PublicationTarget/Slot/AccessBlock`，`INV-01…13` 和 `OD-01…10`。
- **F04** 公开分发安全门禁、受控引用闭包/HTML 清理、`F04-GOV-001…028`，风险状态和豁免原则、24 个场景全部 `NOT_RUN`，不得把示例 lint 当成正式安全扫描器。

### F05 必须输出的专项内容

1. **Trust Boundary 与 STRIDE 威胁模型**：内部工程门户、外部多品牌门户、BFF、Go Control Plane、Worker、Git/IAM/Gateway、对象存储、搜索/CDN、API Explorer；明确安全假设、攻击面、缓解、残余风险和 Owner。
2. **多维访问控制**：产品×Workspace×组织×主体×受众×环境×APIAsset×Release×Grant 的读取/写入/审核/发布矩阵。所有私有搜索、详情、历史、下载、直连 Blob、发布/恢复、事件订阅均须做服务端检查；deny-first 和无权资源是否返回 403/404 的一致策略。
3. **Identity & Credential Boundary**：外部 IdP/OIDC/Client Credentials、短时令牌、Service Identity、Token Ref/Secret Vault、职责分离与应急访问；Fabric 不签发真实 Client Secret，不复制客户真实凭证。
4. **内容/解析器/SSRF 安全**：外部 `$ref`、重定向、DNS rebinding、内网 IP、路径穿越、Schema bombs、XSS、Markdown/HTML、URL 白名单、文件压缩炸弹及 CPU/内存预算；针对 F04 的候选限制给出可操作策略。
5. **公开发布与缓存阻断**：Scope 与 PublicationSlot、CAS、紧急 AccessBlock、BFF/CDN/Edge 私有内容缓存、撤销时效、传播证明、缓存未回收风险；无法证明传播完成必须显示 UNKNOWN。
6. **运营和合规**：审计证据不可篡改性、个人信息最小化、保留/删除冲突、依赖供应链安全、SBOM 与安全更新、事件响应、备份与加密、地域与跨境数据合规适用性。
7. **安全验收设计**：负面矩阵、跨产品/跨租户越权、审批权限失效、并发撤销、受控和非受控缓存、服务不可用 fail-closed、真实集成测试输入；F05 不得声称实际安全渗透与恢复演练已经通过。

### F05 严格边界与交付格式

不得改变 F03 的发布 Slot 粒度、不可变 Release 权威或 F04 已标为 Proposed 的规范支持范围。不要在 F05 生成 F06 高保真页面、F07 Go 生产实现、F08 签收结论。可以新增与安全直接相关的 ADR Proposed、机器可读授权矩阵、威胁/风险登记册与负面安全验收计划。继续 Markdown 单一正文 + 离线 HTML + 清晰图表 + 完整 ZIP 解压与 SHA-256 校验。所有运行验证一律保留真实 `NOT_RUN` 状态。


## F06 阶段执行交接（F05 设计候选完成后）

> 固定正式源文件 `docs/F06_Developer_Experience.md` 与同名 `.html`；内部 F06-A、外部 F06-B、共用 F06-C 必须在一份正式核心文档中分别完整设计。保持 V1.0 与原工程目录。不得用 F06 改写 F01–F05 Markdown 的权威正文，也不得宣称 F02–F05 Proposed ADR 已批准。

**唯一输入**：F01 §3/§5–§10/§12（71 FR、18 NFR 和两个门户要求）；F02 §3–§6/§9–§11（双门户与控制平面）；F03 §2/§7–§10/§15（DeveloperOrg、Grant、Target×Asset Slot、多状态机、只读投影）；F04 §3/§8–§12（契约规则、版本和受控 API Reference）；F05 §4–§11/§14–§16/§19（身份、授权、保密、发布撤销、Explorer）。以 IDP-SCG-STD-001 的 CORE+UX 适用性和追溯要求作为上位约束。

**不得漂移的架构边界**：
1. 内部 Engineering Portal 与外部各 Product Developer Portal **用户、授权、任务和可见内容独立**；共享文档引擎/设计系统不意味着共享 Session 或访问权。
2. 外部门户 CargoPai 品牌自治；Product/Workspace/DeveloperOrg/ConsumerApplication 不混为一体；文档可见 ≠ 业务 API 可调用。
3. Release 历史不可变、PublicationTarget×APIAsset Slot 粒度、AccessBlock Deny 优先；UI 禁止显示已撤销或受限的缓存内容。
4. 不让用户任意输入 URL 变成 Explorer 的通用代理；Mock 和真实 Sandbox 必须明确区分。
5. **F06 只做交互/产品界面设计**：不写 Go 生产代码、数据库迁移，不代替 F07 IAM/Gateway/CDN PoC 或 F08 安全验收。

**F06 必须交付**：
- **F06-A 内部工程门户**：能力地图、站点信息架构、角色/工作台、Catalog、Revision、Check/Lint、Diff、Review/Approval、Release/Publication、撤销恢复、权限、安全运营、异常与审计；每个核心页面包含目标、动作、数据来源、Empty/Loading/Error/Forbidden/Conflict/UNKNOWN 状态和键盘操作。
- **F06-B 外部开发者门户**：CargoPai 独立品牌入口、快速入门、API Reference、受限版本、开发者邀请/组织、Application/Grant（V1）、SDK/Webhook、沙箱和支持中心、明确的认证与环境信息；按真实任务走完接入旅程。
- **F06-C 设计系统**：视觉 Token、组件规范、响应式断点、表格/筛选/树/代码区、深浅色及对比度策略、WCAG 2.2 AA V1 目标、翻译回退、错误反馈与可访问性测试规范。
- **可浏览的高保真 HTML 交互原型**：内部和外部各一套可点击演示，尽可能复用共享组件但用独立导航和用户视角；需覆盖多产品切换、页面密集操作与契约查询、审批、发布、回退、首次接入。
- **可机读追溯与验收**：F06 需求—页面—角色—任务—接口/读模型—验收关联 CSV，全部运行态测试仍为 `NOT_RUN`。

**验收边界**：按桌面/平板/手机真实渲染；浏览器操作应能检查图文与导航、页面关键状态及访问屏蔽；不能仅拿静态截图冒充交互原型。所有授权依旧必须服务端实施，不能把前端可视控制写成安全验证通过。F06 完成后把统一 `index.html` 累加至 F01–F06，并提供同一完整 ZIP 的独立解压与 SHA-256 验证。

## F06 阶段成果与 F07 正式交接

F06 按上文阶段任务单完成完整产品与体验设计候选：[`F06_Developer_Experience.md`](F06_Developer_Experience.md)、[HTML](F06_Developer_Experience.html)、[内部门户](../portal/prototypes/internal-engineering.html)及[CargoPai Developers](../portal/prototypes/cargopai-developers.html) 两套离线交互原型（均为 Local Mock），[21 页面清单](F06_Page_Inventory.csv)、[89 需求追溯](../tests/acceptance/F06_experience_trace.csv)、[32 UX 场景](../tests/acceptance/F06_experience_scenarios.csv)。真实业务、真实沙箱、IAM/Gateway/CDN、完整 WCAG 与正式用户验收均未执行。

### F07 不得偏移的工作边界

1. **唯一输入**：F01–F06 正式 Markdown + ADR-0001–0007 Proposed + F03 模型、F04 规则登记册、F05 访问矩阵和 F06 页面资产；不能将门户 Demo 视为安全策略事实。
2. **Go 工程实现与质量**：明确应用层、领域层、端口/适配器、数据库与对象制品、持久任务 Outbox、缓存和观测性；若对 F02 候选架构提出变更，应新提 ADR，不静默调整。
3. **必须落地的纵向闭环**：受控源 → Revision → CheckRun → ReviewDecision → Release → PublicationTarget×Asset Slot CAS → 受控文档投影；紧急 AccessBlock 撤销、恢复和查询证据必须可靠。
4. **双门户**：在同一个项目设计中承接 F06-A/F06-B/F06-C；必须验证服务端身份与对象授权，外部门户独立品牌/受众/环境，UI 隐藏不替代服务端控制。V1 真实沙箱要由 IAM/Gateway 真实执行，Mock 不能算通过。
5. **真实证据**：工具链兼容性、数据库迁移、断点恢复、并发 CAS、拒绝泄露、Edge 缓存、部署/备份、键盘与可访问性、关键用户旅程验证；有执行证据才允许 PASS，未执行则 `NOT_RUN`。
6. **固定交付路径**：`docs/F07_Engineering_and_Quality.md/.html`；同一 V1.0 根工程，不新增独立项目，不复制和覆写 F01–F06 源文件。


## F07 工程设计成果与 F08 正式交接（当前有效）

**F07 唯一权威正文**：[F07 Markdown](F07_Engineering_and_Quality.md) / [F07 离线 HTML](F07_Engineering_and_Quality.html)。本次继承 F01–F06 和 ADR-0001–0007 `Proposed`，**没有修改上游权威设计正文**，也没有编写 Go 生产服务或完成真实 IAM/DB/CDN 验证。

- [F07 工程控制（30 条草案）](../governance/policies/F07_Engineering_Controls_DRAFT.yaml)：候选控制与验收方法，不是已生效的组织安全政策。
- [F07 工程需求追溯（89 项）](../tests/acceptance/F07_engineering_trace.csv)：71 FR + 18 NFR；承接章节和候选控制明确。
- [F07 工具兼容矩阵（15 项）](../tests/acceptance/F07_tool_compatibility_matrix.csv)：具体版本、环境和实际支持程度全部 `TBD/UNVERIFIED/NOT_RUN`。
- [F07 工程验证场景（42 项）](../tests/acceptance/F07_engineering_scenarios.csv)：全部为 `NOT_RUN`，不可将 F04 自定义 lint 或 F06 Mock 浏览器演示冒充真实端到端结果。

### F08 开始前的不可漂移边界

1. **唯一输入**：F01–F07 正式 Markdown、ADR-0001–0007 `Proposed`、F01 Project Manifest 草案、F02 AI、F03 INV/发布粒度、F04 28 规则、F05 35 授权+29 STRIDE+36 安全场景、F06 21 视图+32 UX 场景、F07 30 控制+15 工具兼容+42 工程场景。
2. **唯一任务**：`F08_Delivery_and_Acceptance.md/.html` 设计实施批次、依赖、风险责任、实际验收过程、证据契约、STD-CP1…CP6 签收界限及上线/回退/恢复门禁。不要在 F08 重画一个不同的 Go/DDD 架构，也不要重命名产品或改变 MVP/V1。
3. **核验事实**：所有 Go/数据库/对象存储/IAM/Gateway/CDN/生产安全/DR/真实用户测试目前 `NOT_RUN`；具名负责人、工具版本、SLO、RPO/RTO 均为 `TBD`。F08 可以定义计划和证据格式，不能“批准”没有执行的测试。
4. **正向纵切优先**：CargoPai Git/受控导入 → Revision → Check/Review → Release → Target×Asset Slot CAS → 授权外部门户读取 → Block 撤销与恢复；以 Parcel 跨产品禁止读取和失败恢复反例同步验收。
5. **工程制品**：始终保持相同 `idp-api-fabric/` 目录及 V1.0，Markdown 为唯一权威源，HTML 为离线派生；更新索引，保护 F01–F07 正文及已存在 ADR，最终完整 ZIP 可独立解压并核对 SHA-256。
