# F08 — IDP API Fabric 实施路线图与验收规范

**正式路径**：`docs/F08_Delivery_and_Acceptance.md`　**版本**：V1.0（原工程设计基线，不新增版本）　**日期**：2026-11-09  
**文件状态**：完整的**交付与验收设计候选稿**；项目尚未具名批准、尚未执行生产验收；不是上线授权、工期承诺或系统完工证明。  
**唯一上游**：F01–F07 Markdown 权威正文、ADR-0001–0007（全部 `Proposed`）、项目 Manifest 草案、F02 AI-01–07、F03 INV-01–13、F04 28 治理规则、F05 35 授权规则与 29 STRIDE 风险、F06 两套 LOCAL_MOCK 门户及 F07 30 工程控制。  
**上位规范**：`IDP-SCG-STD-001`《IDP Supply Chain DDD Product Engineering Standard V1.0》。门禁编号与语义以该标准的正式文本为准，若先前摘要存在不同表述，以标准原文裁决。  
**数据状态约定**：`TBD`=尚待具名决定；`PROPOSED`=方案待批；`NOT_RUN`=尚无执行证据；`BLOCKED`=执行受条件阻断；`PASS`/`FAIL`/`WAIVED` 必须由真实证据和有权责任人判定。离线 Mock、静态校验和文件哈希 PASS 不可推导业务/安全/运行 PASS。

## 0. 执行摘要与实施裁决

F08 是**第八份且最后一份核心设计文档**。它不增加产品能力、不重画 F02 的 Go 架构、不修改 F03 聚合、不改变 F01 已分配的 MVP/V1 范围，而是把设计转换为有序开发工作包、依赖矩阵、职责与资源计划、可复现真实测试、证据包和六道上位标准门禁。第一条工程主线始终为：**CargoPai 受控 Contract Source → Revision → CheckRun / ReviewDecision → Immutable Release → `(target_id, api_asset_id)` PublicationSlot CAS → 外部门户授权读取 → AccessBlock 立即阻断、撤销、回退与恢复**。同步以 Parcel 未授权访问、恶意 Schema、并发与失败恢复作为负向对照。

| 评审对象 | 设计结论 | 当前状态 |
|---|---|---|
| 核心范围 | MVP 先跑通一个非生产纵切，V1 才接入真实 Sandbox / Applications / Grants 与 AsyncAPI 完整链路 | 范围继承 F01 |
| 架构 | Go 模块化单体 + Worker 候选、独立双门户、受控外部适配，不重新拆成微服务 | ADR 待审批 |
| 关键安全 | 各产品服务端拒绝优先、AccessBlock 源站立即拒绝、缓存风险分级、不靠前端隐藏 | 真实安全测试 `NOT_RUN` |
| 交付治理 | 以一个完整交付单位冻结代码、配置、Schema、策略、制品及证据摘要，按风险审批 | 签收人 `TBD` |
| 生产投入 | `STD-CP1…6` 适用要求和生产前门槛真实满足后，才允许 Go/No-Go 决策 | **未授权上线** |

<figure class="architecture-figure"><div class="fig-label">FIGURE 01 · 一份设计基线 → 逐阶段真实证据 → 准入决定</div><div class="fig-grid cols-3"><div class="fig-box"><b>F01–F07</b><small>范围 · 边界 · 契约 · 安全 · UX · 工程</small></div><div class="fig-box fig-primary"><b>F08 Work Packages</b><small>责任 · 依赖 · 切片 · 交付批次</small></div><div class="fig-box tone-green"><b>Evidence & Gate</b><small>执行环境 · 结果 · 签署 · 版本匹配</small></div></div><div class="fig-arrow">↓ 只有有效证据与具名批准才允许进入发布决策</div><div class="fig-head"><strong>Build → Verify → Pilot → Production Go / No-Go</strong><small>缺少证据时保持 NOT_RUN / BLOCKED；不默认通过，不自动升级风险例外</small></div><figcaption>图表示意程序治理闭环；当前交付仅为文档与验证计划。</figcaption></figure>

## 1. 交付边界、输入权威与变更控制

### 1.1 不可改变的设计边界

1. **产品自治**：CargoPai、IDP Parcel、IDP Brant、IDP Prism 的接口、Release、消费者和授权独立；共用的是治理底座。不得统一货代/小包/仓储的业务实体。
2. **Fabric 不承担产品业务执行**：不替代 API Gateway、IAM/OIDC Provider、Git、CI/CD Runner、Broker、WMS/ERP/运单等业务核心。
3. **DDD 与事实权威**：七个语义上下文是候选边界，不是七套服务；`SourceSnapshot/Revision/Release/Target/Slot/AccessBlock` 各司其职；不可变 Release 不得原地修改。
4. **发布粒度**：`PublicationTarget = Product × Audience × Environment × Portal`；`PublicationSlot = Target × APIAsset`，CAS generation 单调，不能把一个 Target 的所有 API 捆成一个指针。
5. **安全撤销**：源站 AccessBlock 是强制读取门；外部缓存传播未得到证据时显示 `UNKNOWN/AT_RISK`，而不是虚称全球删除已完成。
6. **双门户**：IDP Engineering Portal 用于内部治理，CargoPai Developers 用于独立品牌外部接入；F06 HTML 为 LOCAL_MOCK，不是已经连通的身份系统。
7. **标准优先**：执行时按上位标准实际规则/Profile 适用性与风险判定；F01 Project Manifest 仍为 DRAFT。必须记录适用/不适用、差异和批准，不能凭 F08 自动生效。

### 1.2 范围变更协议

需求新增或 MVP/V1 调整必须形成 `ChangeRequest`：触发源、上游 FR/NFR、受影响领域/契约/安全/UX、成本与进度影响、兼容性、证据重跑集、审批人及决定；更新批准前，旧范围和原发布范围仍生效。ADR `Proposed` 不是已批准决策；高风险变更须独立复核。不得直接篡改历史验收证据。

### 1.3 当前上游成熟度

现有 71 项 FR + 18 项 NFR 可追溯至设计资产；F04 自定义规则子集、F06 本地 Mock 和 Markdown/HTML/ZIP 结构验证曾实际执行，但 **Go 后端、真实 PostgreSQL、IAM、Object Store、CDN、Gateway、真实用户、性能与 DR 均尚未执行验收**。F08 中所有真实运行项初始化 `NOT_RUN`，不得写为 PASS。

## 2. 实施策略与可交付纵切

不按页面、数据库表或七个语义上下文单独“做完一片”后再尝试集成；采用**从最低可验证纵切逐步扩展**的路径。每一批都产出同一 Source/Revision/Release/Slot/Block 语义的真实接口、测试和证据，逐层引入产品及外部适配器。首批设计及实现使用真实可解析的 **DESIGN_EXAMPLE CargoPai 契约**；如尚无真正的 CargoPai 生产 API，必须在证据中标示 `synthetic_contract=true`、环境为非生产，不得冒充真实客户运营事实。

<figure class="architecture-figure"><div class="fig-label">FIGURE 02 · 单一 CargoPai 纵向主线与必须并行的反例</div><div class="fig-stack"><div class="fig-grid cols-3"><div class="fig-box"><b>1 · Source</b><small>Git / 受控上传 · digest</small></div><div class="fig-box"><b>2 · Governance</b><small>Parse → Diff → Scan → Review</small></div><div class="fig-box"><b>3 · Release</b><small>Immutable artifact / manifest</small></div></div><div class="fig-arrow">↓ 同一 APIAsset、同一 Target×Asset Slot、generation CAS</div><div class="fig-grid cols-3"><div class="fig-box tone-green"><b>4 · Publish</b><small>审计 / Outbox / 可对账</small></div><div class="fig-box tone-green"><b>5 · Portal Read</b><small>Role / Product / Grant / Block</small></div><div class="fig-box fig-primary"><b>6 · Revoke</b><small>源站拒绝 → 缓存传播状态</small></div></div><div class="fig-separator">同步负向验证：Parcel 越权拒绝 · CAS 冲突 · 审批/Policy 过期 · Poison Ref · Worker 重投 · Storage/CDN UNKNOWN</div></div><figcaption>整条纵切由 F01/F03/F04/F05/F07 承接；任何一步 FAIL 都不能因为 UI 看起来完整而跳过。</figcaption></figure>

## 3. 工作分解结构 WBS 与实施顺序

建议采用 `WP-01…WP-12` 稳定工作包 ID；**工作包不等于微服务、团队或预算采购项**。其生产交付需指派具名 Owner。详细机器可读计划见 `tests/acceptance/F08_delivery_work_packages.csv`。

| 工作包 | 交付能力与最小完成证据 | 直接前置 | 阶段 | 设计责任角色 |
|---|---|---|---|---|
| WP-01 | 采用批准、Profile、ADR、容量预算、责任矩阵 | F01–F07 评审 | Pre-MVP | Product+Governance |
| WP-02 | CI/仓库、Go 依赖边界、生成器 PoC、版本锁定、可复现构建 | WP-01 关键决策 | MVP | Engineering |
| WP-03 | Product/Workspace/APIAsset、服务端多维授权与审计 | WP-02 | MVP | Platform+Security |
| WP-04 | Git/受控导入、闭包 Digest、Revision、元数据与 Blob | WP-03 | MVP | Registry |
| WP-05 | OAS3.1 验证、版本 Diff、敏感扫描、CheckRun/Review | WP-04 | MVP | Governance |
| WP-06 | Immutable Release、Slot CAS、AccessBlock、历史与 Outbox | WP-05 | MVP | Publication+Security |
| WP-07 | Internal Portal 真实工作台与 F06-A 关键流程 | WP-03,WP-06 | MVP | UX+Engineering |
| WP-08 | CargoPai 外部只读 Portal、私有授权读取及误发布撤销 | WP-06,WP-07 的服务能力 | MVP | Developer Experience+Security |
| WP-09 | 集成/竞争/安全/恢复/负向验收、门禁运行、非生产 MVP 签收 | WP-02…WP-08 | MVP | QA+Security+Operations |
| WP-10 | V1 ConsumerApplication、订阅/Grant、真实沙箱首调与 IAM/Gateway 适配 | WP-09 | V1 | Developer Access+Integration |
| WP-11 | V1 AsyncAPI、漂移、公告、SDK/管理与多品牌扩展（按 F01 范围） | WP-09，工具 PoC | V1 | Contract+Developer Experience |
| WP-12 | 容量/SLI、DR、生产 Runbook、UAT、运营交接与受控发布 | 适用上线范围的 WP-09…11 | Production Gate | Operations+Release |

> **先验限制**：WP-01 至 WP-09 允许在隔离的非生产环境完成 MVP 验收；计划生产运行时，WP-12 的相关安全/备份/值守和批准前提不可豁免。V1 的真实 Sandbox 首调、Developer Application 管理不作为 MVP 已实现能力。

<figure class="architecture-figure"><div class="fig-label">FIGURE 03 · WBS 依赖与关键路径（非日历工期）</div><div class="fig-grid cols-3"><div class="fig-box"><b>WP-01 → WP-02</b><small>Owner/ADR · Go/CI/工具验证</small></div><div class="fig-box"><b>WP-03 → WP-04</b><small>Product/ACL → Source/Revision</small></div><div class="fig-box fig-primary"><b>WP-05 → WP-06</b><small>Governance → Release/Block/CAS</small></div></div><div class="fig-arrow">↓ 两个 Portal 实现可并行；均依赖后端授权与发布事实</div><div class="fig-grid cols-2"><div class="fig-box tone-green"><b>WP-07 Internal</b><small>管理 · 检查 · 审批 · 发布</small></div><div class="fig-box tone-green"><b>WP-08 External</b><small>独立品牌 · 安全只读</small></div></div><div class="fig-arrow">↓ WP-09 真实联调 / 证据门禁</div><div class="fig-head"><strong>WP-10 / WP-11（V1） → WP-12（按批准的生产发布范围）</strong><small>工期与成本要在能力/容量/人员及外部依赖有真实基线后估算</small></div><figcaption>并行开发不代表门禁可以颠倒；未验收的外部 IAM/CDN 适配不允许切入正式公开发布。</figcaption></figure>

## 4. 阶段、里程碑与完成定义

### 4.1 里程碑定义

使用 `MS-00…MS-09`，**不是未经估算的日期或固定周数**。每一里程碑必须输出版本化证据包；实际开始/结束日期、估算工时和人员由具名项目经理在批准后的计划中填写。

| 里程碑 | 对应工作包 | 退出结果（必须证明） | 未达成处置 |
|---|---|---|---|
| MS-00 采用与裁决 | WP-01 | 有效 Project Manifest、Risk/Profile、全部阻断 ADR 裁决 | `BLOCKED`，不能启动受影响实现 |
| MS-01 工程基座 | WP-02 | Go/CI/生成/依赖可复现，工具 PoC 有具体支持矩阵 | `BLOCKED` 或有权受限范围批准 |
| MS-02 身份与基本事实 | WP-03 | Product/Workspace/ACL、审计、隔离负例真实 E2 | 拒绝外部功能启用 |
| MS-03 契约治理 | WP-04,05 | Digest/Revision/Schema/Review 端到端，错误输入拒绝 | Release 不得 READY |
| MS-04 发布与恢复 | WP-06 | CAS、Outbox、AccessBlock、故障恢复 E2/E3 | Public Publication 不得启用 |
| MS-05 内部门户 | WP-07 | F06-A 角色真实联动、拒绝与错误反馈 | 不开放管理能力 |
| MS-06 外部门户 MVP | WP-08,09 | CargoPai 只读门户、跨产品拒绝、撤销恢复、MVP AT | MVP 不能签收 |
| MS-07 开发者接入 V1 | WP-10 | 真实 IAM/Sandbox/Applications/Grants 与首次调用证据 | V1 对外接入不上线 |
| MS-08 V1 扩展能力 | WP-11 | AsyncAPI 工具兼容与按 F01 V1 的功能验收 | 只发布已验证子集 |
| MS-09 生产准入/交接 | WP-12 | CP1–6 有效结论、UAT、恢复演练、责任人批准和观察计划 | **NO_GO** |

### 4.2 各里程碑通用完成标准

至少包括：源/构建制品及 SHA-256、环境与依赖版本、发布配置、数据库迁移版本、绑定的规则/策略 Digest、真实测试结果和失败记录、追溯矩阵、Owner/Reviewer/Approver、未决事项、恢复步骤、下一阶段依赖。任何“代码完成但接口未通/测试未运行”的条目都不是 `DONE`。设计工件可以标记 `DESIGN_READY`，不能标记 `IMPLEMENTED`。

## 5. 上位标准六道门禁：精确语义与裁决

**本节按 `IDP-SCG-STD-001` 门禁正文定义，而非从编号推测。** 门禁是按本次适用范围运行的正式判定，不是“文档齐全”即通过。门禁适用规则的 MUST/SHOULD、R1/R2/R3 复核、证据有效期及豁免逻辑，以已批准的 Manifest/规则集合为依据。

| 门禁 | 本项目可检查的最低证据 | 负责角色（具名待定） | 拒绝/暂停示例 |
|---|---|---|---|
| `STD-CP1` 范围与采用 | 具名责任、Profile/依赖、范围、项目风险、批准的 Manifest | Governance/Product | 范围/Owner/适用规则缺失 |
| `STD-CP2` 领域与事实 | F02/F03 Context Map、唯一事实权威、13 INV、状态、反例 | Domain/Architecture | Release/Publication 事实混淆 |
| `STD-CP3` 契约与恢复 | F04/F05 OAS/异步契约、授权、幂等、CAS、故障补偿与对账证据 | Architecture/Contract/Security | 未知结果时重试覆盖或越权 |
| `STD-CP4` 专业体验 | F06-A/B/C 两门户任务、异常可理解、真实用户与无障碍验证 | Product/UX | 错误公开风险被 UI 隐藏 |
| `STD-CP5` 工程与质量 | F07 编译/迁移/安全/并发/负载/恢复/消费者证据，阈值已批准 | Engineering/QA/Security | 必需真实测试 `NOT_RUN` |
| `STD-CP6` 发布与运营 | F08 发布包、前置有效门禁、UAT/回归、值守/回退、批准和发布后观察 | Release/Governance/Operations | 无批准、无恢复人、缺观察证据 |

**跨文档统一声明**：F07 §18 已按上位 `IDP-SCG-STD-001` 的原义修正：STD-CP4＝专业体验（Product/UX），STD-CP5＝工程与质量（Engineering/QA/Security），安全控制同时受 STD-CP3 契约与恢复及 STD-CP5 工程实证约束。**文档语义差异已消除，但 OD-08-01 的具名治理批准仍为 OPEN**；不能因文字修复而自动标记门禁通过。

<figure class="architecture-figure"><div class="fig-label">FIGURE 04 · 六道门禁按标准原义，不使用虚假通过状态</div><div class="fig-grid cols-3"><div class="fig-box"><b>CP1 · 范围</b><small>采用 / 风险 / Owner</small></div><div class="fig-box"><b>CP2 · 领域</b><small>事实 / 边界 / 不变量</small></div><div class="fig-box"><b>CP3 · 契约</b><small>授权 / 恢复 / 故障</small></div><div class="fig-box tone-green"><b>CP4 · 体验</b><small>真实任务 / 可访问性</small></div><div class="fig-box tone-green"><b>CP5 · 质量</b><small>真实工程 / 安全 / 容量</small></div><div class="fig-box fig-primary"><b>CP6 · 发布</b><small>运营 / 风险 / 授权</small></div></div><div class="fig-separator">门禁初始结果全部 NOT_RUN；任一不可豁免失败、有效性不足或缺失真实责任人均不能默认为 PASS。</div><figcaption>门禁可按实际受影响范围复用有效证据，不能用旧范围签收替代新产品、新受众或新发布版本的验证。</figcaption></figure>

## 6. 责任治理：RACI、职责分离与签收权限

角色名称是职责占位，不是已具名聘任。实施前需填充 Owner 的实名/组织/有效代理、权限来源和风险等级；不得让实施者自行批准高风险安全例外。

| 交付决策 | Accountable | Responsible | 必须独立参与 | 现状 |
|---|---|---|---|---|
| 范围、优先级、MVP/V1 | Product Owner | PM/Domain Lead | Governance | TBD |
| Context Map/ADR | Architecture Owner | Domain Owners | 影响方 + 安全 | TBD |
| API 规则/豁免 | Governance Owner | Contract Lead | Security/Consumer | TBD |
| 身份、租户、AccessBlock | Security Owner | Security Engineer | QA/Independent Reviewer | TBD |
| 双门户用户任务/无障碍 | UX/Product Owner | UX/Frontend | 独立测试用户 | TBD |
| 工程发布流水线 | Engineering Owner | Go/DevOps | QA/Security | TBD |
| 备份、容量、恢复指标 | Operations Owner | SRE/DBA | Security/Product | TBD |
| 最终生产 Go/No-Go | Release Authority | Release Manager | Product/Security/Operations/QA | TBD |

**SoD**：作者不得独立批准本人高风险发布；检查执行者不能替代业务/安全验收；Waiver 需具名、有范围、有期限、有理由和证据引用。R1/R2/R3 独立复核按照上位规范风险映射执行，不在 F08 擅自统一成一个级别。

### 6.1 正式基线冻结所需的具名批准（非执行事实）

仅 F01–F08 文档一致、验收计划具备，并**不满足正式冻结**。在 `F01_Project_Manifest_DRAFT.json` 中必须由真实组织负责人补齐并批准：项目法人/适用辖区与数据驻留范围、风险等级 R1/R2/R3 及其复核责任、CORE/TECH/能力触发 Profile 与规则集、`GOV/DOMAIN/ARCH/DATA/SEC/ENG/OPS/UX/FIN` 角色、Go/技术候选及可核验 PoC、生产意图和批准的 SLO/RPO/RTO（如适用），并将 STD-CP1 的批准证明关联到本工程包。7 份 ADR-0001…0007 必须逐份做具名批准、否决或退回修订，`ADR-0004` 的 PublicationSlot CAS 和 AccessBlock 粒度尤其不能默认为已经获批。

**未具名以前的强制状态**：Manifest `DRAFT_NOT_APPROVED`，ADR `Proposed`，OD-08-01…10 `OPEN`，门禁 `NOT_RUN`，禁止将这些状态批量替换成 `Approved/PASS`；可继续隔离环境 PoC 与设计修订，但不可正式宣告 CP1 通过、全面实施放行或生产 GO。对应资料只使用原 `governance/policies/`、`decisions/` 和 `tests/acceptance/` 目录，不新增第九份核心设计文档。

## 7. 证据等级、可重现性与证据契约

继承 F07 的等级定义：**E0** 设计候选；**E1** 静态/离线原型；**E2** 隔离环境真实依赖联调；**E3** 类生产链路与故障注入；**E4** 有批准的生产观察/演练。`PASS` 需要证据的等级满足该场景 `required_evidence_level`；即便 E4 也必须与本次发布制品、环境、规则与风险范围一致。

| 证据字段 | 约束 |
|---|---|
| `evidence_id / scenario_id / requirement_refs` | 不变引用，能回连上游 FR/NFR/AT/EG/规则/风险 |
| `source_commit / artifact_digest / config_digest / policy_digest` | 标识真实被测内容；不可把不同版本的结果拼接使用 |
| `environment / dependencies / tool_versions` | 完整环境类型和版本；非生产必须明确标识 |
| `started_at / finished_at / executor` | 真正执行时才填写，未运行必须为 `null` |
| `expected / observed / result` | PASS 需要可检查的观测事实；预期结果不等于实际结果 |
| `evidence_refs / reviewer / approval` | 可访问、可保留、保密分级与独立批准 |
| `validity / retest_triggers / expiry` | 指向产品、受众、环境、版本及变更触发重测 |

机器可读模板：`tests/acceptance/F08_evidence_record_TEMPLATE.json`。`status=NOT_RUN` 的模板不附虚构执行时间、观测或签名。真实执行必须新建具体 evidence ID 的不可变记录，不得覆写模板；演练失败需保留原始 FAIL 与后续重新执行的新证据。

<figure class="architecture-figure"><div class="fig-label">FIGURE 05 · 需求到真实证据的链路与有效性</div><div class="fig-grid cols-3"><div class="fig-box"><b>Business Goal / FR</b><small>F01 稳定需求 ID</small></div><div class="fig-box"><b>Design & Control</b><small>F02–07 · INV · GOV · ENG</small></div><div class="fig-box fig-primary"><b>Test Case</b><small>AT · EG · Security · UX</small></div></div><div class="fig-arrow">↓ Test Run 执行事实（而非计划）</div><div class="fig-grid cols-2"><div class="fig-box tone-green"><b>Evidence</b><small>commit + env + digests + observed</small></div><div class="fig-box tone-green"><b>Gate Decision</b><small>规则适用性 + 风险 + 具名批准</small></div></div><div class="fig-separator">变更代码、Policy、受众、Environment 或显著依赖 → 重新评估证据有效性 / 需重跑场景</div><figcaption>本包提供完整的设计追溯，但未生成假冒的 TestRun 或签收事实。</figcaption></figure>

## 8. 验收矩阵：最小纵切与正式适用性

`tests/acceptance/F08_acceptance_matrix.csv` 记录 24 条首批关键验收项 `AC-01…24`；`tests/acceptance/F08_detailed_acceptance_cases.csv` 增加 51 条专项验收设计 `DC-001…051`，记录具体步骤、正反判据、证据与阶段；两者共同构成 F08 直接验收输入，并保留真实状态 `NOT_RUN`。除此之外，F01 的 12 个 AT、F02 12、F03 18、F04 24、F05 36、F06 32、F07 42 个场景**全部保留为原始资产**，不可合并后误删或把未跑的测试标 PASS；F08 验收矩阵是“关键门禁样本”，不是全部适用测试的豁免表。

| 验收族 | 首批覆盖的关键行为 | 最高强度要求（依据风险决定） |
|---|---|---|
| 数据/制品 | Source/Ref Digest、Release 不可变、状态机 | E2–E3 |
| 并发/可靠性 | CAS、幂等、Outbox、Worker 崩溃和恢复 | E3 |
| 安全/隔离 | 多产品、多组织、直链/搜索、撤销与 Edge 缓存 | E3，生产观察另需 E4 |
| 契约 | OAS3.1 正/负、Diff、Go 类型、AsyncAPI 版本 | E2–E3 |
| 双门户 | 内部独立审核、外部只读、异常体验、无障碍 | E2–E3，真实任务 |
| 运维 | 恢复点、索引重建、SLO、部署回退、事件响应 | E3 / 上线后 E4 |

**覆盖规则**：每一首批 MVP `FR` / `NFR` 至少有**明确验收承接**；后续 V1/V2 的需求以 `DEFERRED` 标示，不能假装 MVP 已实现，也不能把 89 条需求全部强制绑定同一测试。完整 89 行映射见 `tests/acceptance/F08_delivery_trace.csv`，其中 `delivery_status=NOT_IMPLEMENTED` 为设计时保守事实；未来按真实交付证据更新，不凭设计引用自动变更。

**F08 需求直接验收设计覆盖（已细化，均未执行）**：保留 24 项核心门禁用例 `AC-01…24`，并在 [详细验收案例表](../tests/acceptance/F08_detailed_acceptance_cases.csv) 追加 51 项 `DC-001…051`（原有直接验收缺口 46 项，另有 5 项因纠正 MVP/V1 阶段错配而改为 V1 详细验收）。每个 DC 明确阶段、前置数据、执行步骤、可观察的正/负判据、证据等级、责任角色和上位门禁；`F08_delivery_trace.csv` 现有 89 项 FR/NFR 均至少关联一个 AC 或 DC。**这仅关闭“设计期直接测试定义缺失”的问题**，不表示 89 项功能实现或真实测试已通过：所有执行状态保持 `NOT_RUN`，具名 Owner 为 `TBD`。

**分阶段退出约束**：MVP 的 DC 为本阶段必须执行的验收输入；V1 的 DC 只在相应 V1 能力交付时要求完成；V1.x/V2 项为 `DEFERRED_TO_PHASE_EXIT`，不得被纳入 MVP 通过率。`MVP/V1_CONDITIONAL` 的 NFR 需由正式风险/上线范围决定何时验收，不能用延期条目自动批准生产上线。证据需绑定实际构建、环境、摘要和独立评审；仅展示 UI、Mock 截图或静态 CSV 不能认定通过。

## 9. 测试执行计划与环境分层

### 9.1 环境不串用

`DEV` 本地研发仅验证快速反馈；`CI` 固定容器运行静态与契约工具；`INT` 真实 PostgreSQL/对象存储/IAM 假受控替身或测试身份；`STAGE` 使用隔离但等效的真实依赖、生产式 IAM/Gateway/CDN 策略和完整发布链；`PROD` 必须在正式批准后用于观察/演练。**不能把真实依赖的 Stub 结果冒充生产链路兼容证明。** 环境中的客户数据使用经批准的脱敏或合成样本，严禁借用生产个人凭证。

### 9.2 测试层顺序

1. 静态/领域：Go build/lint、DDD import guards、单元与属性测试、生成代码重复构建一致性。
2. 契约/解析：OAS/AsyncAPI 正负样例、版本 Diff、Schema/引用闭包限制及 Golden 输出。
3. 数据/集成：真 PostgreSQL CAS/隔离/迁移；Blob 不可变性与原子引用；IAM 服务端鉴权。
4. 安全/韧性：跨产品拒绝、SSRF/XSS、审批绕过、故障注入、Outbox at-least-once、发出未知传播状态。
5. UX/业务旅程：两个真实后端门户、键盘与读屏、真实用户角色、沙箱首调（仅 V1）。
6. 性能与 DR：基于批准容量模型采样 p95、成功率、索引滞后、恢复时间及 RPO/RTO，保留完整执行记录。

### 9.3 缺少外部适配时的处理

IAM/Gateway/CDN 未提供测试接口时，记录 `BLOCKED` + 依赖 Owner + 替代证据范围；可以推进不依赖该项的隔离任务，但**不得通过相关端到端门禁**。不允许用 Mock 测试覆盖真实依赖被禁用的问题。

## 10. 安全负向与失效恢复的不可省略验收

- 跨产品：Parcel 用户查询 CargoPai APIAsset、Revision、Release、直链 Blob、审计/搜索索引都必须被服务器拒绝且不能泄露私有元数据。
- 过期授权：Principal/Grant 被撤销后，源站读取、API Explorer / 短时下载链接不得持续放行；缓存传播独立判定。
- 并发发布：同 Slot 两提交只能一方成功；授权撤销与发布交错时阻断优先；旧工作流不得因重试覆盖新 generation。
- 来源安全：外部 `$ref` SSRF、目录逃逸、循环膨胀、恶意 Markdown、XSS、SDK 样例中的密钥不得进入公开站点。
- 故障补偿：Release Blob 写入成功但 DB 失败，孤儿可对账；DB 成功但 Blob 缺失不得公开；Worker 重投不重复生效；Poison 消息可人工修复并保留审计。
- 紧急撤销：先同步阻断源站，再校验缓存/Edge 传播；传播不可证明为 `UNKNOWN/AT_RISK`；回退需有新批准和审计，禁止修改旧 Release。

<figure class="architecture-figure"><div class="fig-label">FIGURE 06 · 撤销与恢复的安全状态图（验收导向）</div><div class="fig-grid cols-3"><div class="fig-box"><b>Incident</b><small>错误公开 / 权限异常</small></div><div class="fig-box fig-primary"><b>AccessBlock COMMIT</b><small>服务端立即拒绝读取</small></div><div class="fig-box"><b>Propagation</b><small>CDN / Proxy / Index</small></div></div><div class="fig-arrow">↓ 只允许用真实探测证据细化外部传播结果</div><div class="fig-grid cols-2"><div class="fig-box"><b>UNKNOWN / AT_RISK</b><small>传播未证实，不可宣传撤销完成</small></div><div class="fig-box tone-green"><b>VERIFIED_BLOCKED</b><small>已测试受控范围内一致拒绝</small></div></div><div class="fig-separator">如需恢复发布：修复根因 → 重新审批 → 校验旧 Blob/新 Slot 目标 → CAS/解除阻断 → 新一轮传播/审计验证</div><figcaption>Block 之后的公网缓存是否完全失效属于外部系统可见事实；不允许用数据库提交成功直接推断。</figcaption></figure>

## 11. 运维指标、容量、资源和成本估算方法

### 11.1 先量测后锁定指标

Catalog 资产/版本增长、事件量、Blob/索引容量、门户 QPS、p95、变更率、失败/重试率、人员响应窗口由业务 Owner 与 Operations 在 **MS-00 / MS-02** 采样并记录。SLO、RPO/RTO 和成本上限必须基于场景、保留策略、业务后果和预算进行批准；目前全部 `TBD`，**不得将推测数值写成承诺**。

### 11.2 估算模型与容量压力

| 驱动 | 估算方法 | 关键风险 |
|---|---|---|
| 工程人日 | WBS 每包 `乐观 / 最可能 / 悲观` 三点估算，依赖乘关键路径；标明人员可用性 | 无名义人员时不承诺工期 |
| 存储 | Contract 数 × 修订/发布增长 × 平均 Blob/Manifest 大小 × 保留系数 | 索引与 Blob 备份膨胀 |
| 计算 | CI 治理任务 × 解析成本，后台 Job 事件量与峰值并发 | Poison Schema、队列拥塞 |
| 安全/运营 | 安全扫描、渗透、值守、恢复演练、证书/密钥与身份租约成本 | 初期低估治理/运维 |
| 外部依赖 | IAM/Gateway/CDN/存储/域名、授权产品计费与数据驻留 | 服务支持与合同责任 |

建议约束：试点先给出小/中/大三档场景，并以实测压力及业务风险签收容量上限；超过边界触发架构扩容评审而非默认满足。

## 12. CI/CD、制品与发布链的证据要求

Build 阶段应绑定 Git Commit、Go/Node/CLI Toolchain、SBOM、签名/摘要、数据库迁移与 Policy Revision。环境部署需记录配置/Secret 引用版本，生成 `ReleaseCandidateManifest`，链接本次所用的 **精确 Contract Release/PublicationSlot generation**。禁止：仅用“最新标签”无法重建；只在门户展示测试徽章；把被旧代码测试过的 Schema 发布为新版本；在生产环境中未经授权执行数据库破坏性迁移。

发布流程：Candidate → 各适用门禁综合校验 → Security/Operations/User Acceptance → Release Authority `GO` 或 `NO_GO` → 窗口受控部署 → Smoke/观察 → 如失败立即启用 Runbook、记录 Incident 与重新准入。`GO` 不能替代实际生产后检查，发生严重事故可从 GO 进入 STOP/ROLLBACK。

## 13. 回滚、撤销、数据库恢复与事故响应

必须区分四种动作，不能一概使用 “Rollback”：

| 动作 | 本质 | 证据与责任 |
|---|---|---|
| Publication Revert | 同一 Slot CAS 指回经过批准的旧不可变 Release | Source/目标 generation、审批、Portal 可见性、审计 |
| Emergency AccessBlock | 首先阻断对敏感文档的服务端访问 | 安全 Owner、外部缓存风险与撤销探测 |
| Software Rollback | 回退当前应用可执行制品/配置 | 兼容迁移状态、构建摘要、观察与快速验证 |
| Database Restore | PITR/快照恢复元数据与审计/Blob 对账 | RPO/RTO 实测、业务事件重放、索引重建 |

数据库 Schema 的 `down` 迁移不等于业务数据恢复；遇到不可逆迁移必须有批准的正向修复/恢复方案。对于已公开泄露的资料，回滚代码不能保证第三方已经删除副本；应按信息安全事件单独处理。

<figure class="architecture-figure"><div class="fig-label">FIGURE 07 · 四类失败操作，不用一个 Rollback 概念替代</div><div class="fig-grid cols-2"><div class="fig-box"><b>Publication Revert</b><small>Slot 指针 / 新 generation</small></div><div class="fig-box fig-primary"><b>Emergency Block</b><small>立即拒绝访问 / 风险状态</small></div><div class="fig-box"><b>Software Rollback</b><small>应用 Binary / Config</small></div><div class="fig-box tone-green"><b>Database Restore</b><small>PITR / 重建投影 / 对账</small></div></div><div class="fig-separator">每种操作拥有不同触发权、最小证据、独立批准和恢复目标；不能互相替代。</div><figcaption>参照 F03/F05/F07 的状态与 Runbook；上线前均需实际演练。</figcaption></figure>

## 14. Go / No-Go 决策模型与例外处理

**决定函数**：对“拟上线的精确范围、制品和环境”判断所有适用不可豁免规则是否有效 PASS；可豁免要求是否有权批准的 WAIVED 且范围未过期；关键证据完整、没有未裁决的安全高风险，具名批准链齐全，外部依赖与回滚计划可用。否则 `NO_GO`。对未知测试、无法访问的证据、过期批准、旧版本结果，一律 `BLOCKED/NO_GO`。**不能因为测试总通过率很高就忽略单项阻断项**。

| 决策结果 | 触发情况 | 后续动作 |
|---|---|---|
| `GO` | 全部门禁满足，具名 Authority 批准，实施条件仍有效 | 受控部署与生产观察 |
| `GO_WITH_LIMITS` | **仅在标准允许且具名批准**、限制范围明确且未触及不可豁免控制时 | 仅限定受众/能力/环境，留存截止与复查 |
| `NO_GO` | 不可豁免失败/未经批准/证据缺失/事故未闭环 | 停止对应上线、修复并重新测试 |
| `STOP_AND_REVERT` | 发布后出现严重越权、损坏或超出允许的 SLI 风险 | 触发安全阻断和专项 Runbook |

`GO_WITH_LIMITS` 是**提议的项目决策标签**，不能自行扩展上位规范许可的 WAIVED 条件；如果上位规范不允许，则不得使用。

## 15. 运营交接与发布后观察

上线前要求移交：服务拓扑、告警路由、产品域与门户域 Owner、依赖服务责任、事件响应手册、可访问性支持渠道、凭证轮换、数据库迁移/恢复步骤、备份证明、容量护栏、已知风险/例外到期日、合规保留策略。发布后按已批准时间窗口观察安全拒绝率、发布失败、索引延迟、Portal HTTP 错误、任务队列积压、证据完整性及用户实际任务；未配置观测时不允许宣称“生产稳定”。

## 16. 采购、合规和第三方软件前置清单

需要在 WP-01 和 MS-01 评估开源许可/SBOM/供应商支持、Go/前端工具链兼容、Git/IAM/Gateway/CDN 合同与地区、个人数据和日志保留、第三方数据跨境与企业商标/域名。Fabric 只记录/治理对外 API，不能据此认为其已取得下游承运商 API 的数据再分发许可；应由集成协议和法务/合规确认。

## 17. 风险台账与待决策项

将首批决策/风险按 `OD-08-01…10` 登记在 `tests/acceptance/F08_open_decisions.csv`；Owner、目标时点、严重度待具名评估，当前 `OPEN`，需要裁决时可新增 ADR 而非擅改上游。关键项目包括：CP4/CP5 已统一后的具名批准、ADR-0001…0007 未批、Go/DB/工具支持组合、IAM/Gateway/CDN 接入、撤销缓存传播、SLO/RPO/RTO/容量模型、具名权限与风险等级、跨产品 Data Residency/隐私边界、外部 Sandbox 真实接入，以及安全渗透/无障碍/运营恢复资源。

<figure class="architecture-figure"><div class="fig-label">FIGURE 08 · 风险决策闭环与重测触发</div><div class="fig-grid cols-3"><div class="fig-box"><b>Issue / Dependency</b><small>稳定 ID · 风险 · Owner</small></div><div class="fig-box fig-primary"><b>Decision / ADR</b><small>候选 → 独立复核 → 批准</small></div><div class="fig-box"><b>Impact Set</b><small>FR/NFR · BC · API · Portal · Test</small></div></div><div class="fig-arrow">↓ 必要的重新评审与真实测试</div><div class="fig-head"><strong>Evidence Validity → Applicable Gates → Delivery Decision</strong><small>无 Owner、无批准、无重新验证：不能悄悄改变设计或放行</small></div><figcaption>F08 不替代 ADR 审批，只将其作为可见的里程碑阻断依赖。</figcaption></figure>

## 18. 89 项需求追溯和所有验证计划的保存约束

`F08_delivery_trace.csv` 通过稳定 ID 引用 F01 的全部 FR-001–071 / NFR-01–18，并保留各自的 `phase`, `business_goals`, `acceptance_scenarios`, F07 工程控制与 F08 最小验收映射。对于仅在 V1/V2 才开展的项，标明计划阶段与 `DEFERRED_TO_PHASE`，不假装 MVP 验收。资产链接与对应的责任人/证据引用须在后续实施过程中维护，不能对设计期间缺失的代码路径或运行结果硬填虚构值。

**不允许机械地把 89 条需求全部归为 MVP 或全部标 PASS。** 任何实际需求剔除必须回到 F01 变更流程；本阶段只做结构完整性与依赖覆盖检查。验收场景若不适用，应有规则与责任人的 N/A 依据，不得仅删除行。

## 19. 文档版本、归档与后续开发交接

正式文档固定八份：`docs/F01_...` 至 `docs/F08_Delivery_and_Acceptance.md`；源码仍以 Markdown/YAML/CSV/JSON 为权威、HTML 为离线阅读版；不新增独立的第九份核心文档。工程版本仍为 **V1.0**；未来需求变更采用 Git 提交/ADR/证据有效性重新评估，不因单次修订随意增加文档版本。

移交开发时须交付：八份正文、已获批的 Manifest/ADR（若尚未批准则明确阻断）、WP/里程碑/风险表、完整追溯、验收案例目录、证据模板、Go/CI/Infra 接口责任和两个 Portal 原型链接。**文档编制完成不等于组织已批准实施，也不意味着系统已部署。**

<figure class="architecture-figure"><div class="fig-label">FIGURE 09 · V1.0 单套文档体系与交付材料</div><div class="fig-grid cols-3"><div class="fig-box"><b>Docs F01–F08</b><small>范围 → 工程 → 交付验收</small></div><div class="fig-box"><b>Contracts / Policies</b><small>OAS · AsyncAPI · Rule/ACL</small></div><div class="fig-box"><b>Prototypes</b><small>Internal / CargoPai Local Mock</small></div></div><div class="fig-arrow">↓ 同一项目目录、同一清单、可复现生成</div><div class="fig-grid cols-2"><div class="fig-box tone-green"><b>Delivery Assets</b><small>WBS · trace · cases · decisions</small></div><div class="fig-box fig-primary"><b>Evidence Contract</b><small>NOT_RUN → 真实测试 → 具名 Gate</small></div></div><figcaption>后续 Go 生产代码将在批准后的工程实现阶段产生；设计目录和交付证据不是可运行后端的替代品。</figcaption></figure>

## 20. F08 文档完成判据与设计审查自检

| 自检项目 | F08 可以实际证明的结果 | 不应声称的结果 |
|---|---|---|
| 八份文档体系 | Markdown/HTML、统一索引、前七份内容哈希一致 | 八份已获组织批准 |
| 实施计划 | 12 工作包、10 里程碑、依赖图和角色矩阵 | 人员工期/预算已签字 |
| 验收计划 | 24 重点 AC、89 条需求追溯、证据模板与门禁定义 | 真实业务、安全、DR 测试已通过 |
| 决策控制 | ADR 状态、10 项待决风险清楚 | Proposed 已等于 Approved |
| 双门户 | F06-A/B 原型、真实 V1 Sandbox 边界、角色授权测试规划 | 原型已连接真实 IAM/Gateway |
| 交付包 | ZIP 可解压、SHA-256 可独立复核、离线文档可打开 | Fabric Go 服务已构建或生产运行 |

**F08 文档设计完成定义**：本文及对应的 CSV/JSON 模板通过静态与结构一致性检查；九幅图、目录锚点与本地资源可读；工作包、MVP/V1、门禁和验收资产不违反 F01–F07；八份权威 Markdown 与签名/哈希清单可校验。**正式工程实施准入**还需上位标准具名采用与必要 ADR 决议；**生产准入**还需真实工程、业务、安全、性能、恢复与运营证据及正式 Go/No-Go 签署。

## 21. 参考与附录来源

- 本工程权威资料：`F01_Product_Requirements.md`、`F02_System_Architecture.md`、`F03_Domain_and_Data_Model.md`、`F04_API_Contract_Governance.md`、`F05_Security_and_Multitenancy.md`、`F06_Developer_Experience.md`、`F07_Engineering_and_Quality.md`，以及 `decisions/ADR-0001…0007.md`（全部 Proposed）。
- 上位《IDP Supply Chain DDD Product Engineering Standard V1.0》：六道门禁按其 §18、Project Manifest/证据语义按 §20 执行；上位文档属于外部基线来源，除非明确附带，不暗示已包含在本 ZIP 中。
- 真实证据及实施模板：`F08_delivery_work_packages.csv`、`F08_milestones.csv`、`F08_acceptance_matrix.csv`、`F08_detailed_acceptance_cases.csv`、`F08_delivery_trace.csv`、`F08_open_decisions.csv`、`F08_evidence_record_TEMPLATE.json`。
- 标准参考：OpenAPI 3.1.2/3.2.1、AsyncAPI 3.1.0、RFC 9457、RFC 9700、NIST SSDF、WCAG 2.2 AA。采用版本和适用性以 F04/F05/F06/F07 的正式审查结论与真实兼容 PoC 为准。

**文档结语**：F08 明确的是“如何分批开发并以真实证据验收”，不是“已经开发或已经验收”。在现有工程中增量完成、保持 V1.0、优先保护产品自治和双门户边界、将未知状态显式呈现，是本阶段不可降低的基本原则。
