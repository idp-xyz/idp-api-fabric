# F06 — IDP API Fabric 双门户产品与体验设计

> **版本**：V1.0（延续 F01–F05；不提高版本）  
> **状态**：产品与交互设计候选，待具名 UX、安全、产品审查；并非系统已实现  
> **权威输入**：F01 `FR-001–071`、`NFR-01–18`；F02 总体架构；F03 DDD 发布/权限模型；F04 契约治理；F05 安全多租户；上位 `IDP-SCG-STD-001` CORE/UX 适用规则  
> **范围**：F06-A 内部工程门户、F06-B 外部产品开发者门户、F06-C 共享设计系统；示范产品 CargoPai  
> **阶段约束**：MVP 有契约管理到文档发布的可读闭环；注册应用/授予访问/真实沙箱首调/SDK 运营属于 V1，不在 MVP 演示中标为已实现。

## 0. 产品体验设计裁决

F06 解决的是**谁能通过哪个门户完成哪些任务，以及成功、失败、权限拒绝、冲突和不确定性如何可理解地呈现**。不重新定义 F03 聚合、不修改 F04 治理规则、不弱化 F05 授权，不写 F07 生产后端实现。

- **两个独立体验**：IDP Engineering Portal 服务内部责任人，CargoPai Developers 服务外部开发者；其身份会话、品牌、导航、授权和可见性必须隔离。
- **同一个控制平面**：复用 Registry、Catalog、Governance、Publication 的服务端能力和受控只读投影；页面 DTO、BFF/接口契约在 F07 落地。
- **可验证页面**：所有核心动作保留需求 ID、页面 ID、来源读模型、异常状态和对应验收场景。
- **演示诚实性**：离线 HTML 是交互原型，所有按钮的业务结果为 Local Mock；不能显示虚构的实际 IAM、Gateway、CDN 调用成功。

<figure class="architecture-figure" aria-label="产品体验边界"><div class="fig-label">EXPERIENCE MODEL · 产品体验边界</div><div class="fig-head"><strong>同一治理平台，两套独立入口</strong><small>F06 · 设计模型 / 非运行系统证据</small></div><div class="fig-grid cols-3"><div class="fig-box"><b>内部工程门户</b><small>管理 Product/Workspace · 契约审查 · 发布/撤销</small></div><div class="fig-box"><b>共享只读投影</b><small>授权过滤 · 版本锁定 · 发布事实 · 操作审计</small></div><div class="fig-box"><b>CargoPai Developers</b><small>产品独立品牌 · 受众与环境授权 · 文档自助</small></div></div><figcaption>不共享会话、客户权限或业务 API 调用凭证；外部产品隔离由服务端执行。</figcaption></figure>

## 1. 设计输入、适用性与不变量

| 输入 | 设计承接 | 不允许偏移 |
|---|---|---|
| F01 产品需求 | FR-040–055 直接驱动双门户，FR-007–039 提供其管理对象 | 不改变 MVP/V1 阶段边界 |
| F02 AI-01–07 | Go 控制平面、共享服务、产品自治 | 不让门户承担业务执行或网关职责 |
| F03 | Product/Workspace/DeveloperOrg、Revision/Release/Target/Slot | 不把 Publication 和 Release 合为一条状态 |
| F04 | Rule/CheckRun/ReviewDecision、API Reference | 不展示未发布契约作为正式文档 |
| F05 | Grant、AccessBlock、Deny 优先、敏感数据保护 | 隐藏 UI 不等于授权；不得泄露私有元数据 |
| 上位规范 | CORE/UX 适用性、可追溯与 STD-CP 门禁 | 未有具名采用/签收不能宣称完全符合 |

**设计不变量 DX-01–DX-10：** DX-01 统一平台不合并产品契约；DX-02 内外 Session 隔离；DX-03 无授权不返回私有元数据；DX-04 当前可见发布按 `PublicationTarget × APIAsset`；DX-05 Release 不可变；DX-06 发布具有 CAS 冲突状态；DX-07 撤销后源站必须拒绝访问；DX-08 CDN/Edge 传播未知不得显示“全球清除成功”；DX-09 文档访问权与业务调用权分别授权；DX-10 本地 Mock 不得冒充真实 Sandbox。

## 2. 用户、角色与关键目标

| 角色 | 入口与范围 | 主要任务 | 禁止或受限行为 |
|---|---|---|---|
| Platform Admin | Engineering / 授权全局 | 产品登记、组织归属、策略配置 | 不因平台身份越过生产双人批准与审计 |
| Product Owner | Engineering / 本 Product | API Catalog、Owner、产品门户配置 | 不自动查看其他产品私有契约 |
| API Author | Engineering / 本 Workspace | Revision、检查、提交审核 | 不自行审核自身高风险发布 |
| Reviewer | Engineering / 授权审核 | Diff、Rule、风险评定、批准/驳回 | 职责分离、策略变更后旧决定失效 |
| Release Operator | Engineering / 发布 Target | 生效发布、撤销、回退 | 不修改 Release 内容或跳过 AccessBlock |
| Security Auditor | Engineering / 经授权 | 权限轨迹、风险、访问审计 | 默认只读，无令牌明文 |
| Anonymous Developer | CargoPai / Public | 快速入门、公开 API Reference | 私有文档、Application/Grant 管理 |
| Partner Developer | CargoPai / DeveloperOrg | 已授权契约查看、接入流程（V1） | 不自动拥有 API 运行时调用权 |

## 3. 产品信息架构、站点地图与优先级

<figure class="architecture-figure" aria-label="双门户信息架构"><div class="fig-label">EXPERIENCE MODEL · 双门户信息架构</div><div class="fig-head"><strong>以用户任务组织导航</strong><small>F06 · 设计模型 / 非运行系统证据</small></div><div class="fig-grid cols-2"><div class="fig-box"><b>Engineering</b><small>总览 / Catalog / Revision / Governance</small></div><div class="fig-box"><b>Control</b><small>Review / Release / Publication / Audit</small></div><div class="fig-box"><b>Developer</b><small>Quickstart / API Reference / Versions / Guides</small></div><div class="fig-box"><b>V1 extension</b><small>Applications / Grants / Sandbox / SDK/Webhook</small></div></div><figcaption>外部门户只使用本产品批准且允许可见的契约投影。</figcaption></figure>

按业务任务组织导航：内部以“发现 → 修订 → 检查 → 审核 → 发布 → 运营”为主链；外部以“了解 → 学习 → 查找接口 → 获取授权 → 测试 → 上线”为主链。内部主页提供角色待办和异常，不以各种指标替代待办列表。

## 4. F06-A 内部门户：页面清单与深度要求

| 页面 ID | 名称 | MVP/V1 | 主要用户任务 | 权威数据和只读投影 |
|---|---|---|---|---|
| IN-01 | 工作台 Dashboard | MVP | 查看待审核、检查失败、可发布、撤销风险 | Product, Worklist, CheckRun, Publication |
| IN-02 | Product / Workspace Switcher | MVP | 切换授权产品和空间 | Product Governance、Membership |
| IN-03 | API Catalog | MVP | 搜索/过滤/定位 API、Owner、生命周期 | Asset Index（服务端授权过滤） |
| IN-04 | API Detail & Revision | MVP | 比较 Revision、来源和制品摘要 | Contract Registry |
| IN-05 | Governance Check | MVP | 阅读 Lint/Breaking Diff/安全检查与建议 | CheckRun、Policy Digest |
| IN-06 | Review & Approval | MVP | 审核、拒绝、重试检查，查看审计证据 | ReviewDecision、Role Separation |
| IN-07 | Publication & Recovery | MVP | 选择 Target×Asset，CAS 发布与紧急撤销 | PublicationSlot、AccessBlock |
| IN-08 | Audit & Timeline | MVP 只读 | 查询操作和关联 ID | Audit Projection |
| IN-09 | Product / Role Administration | V1 | 管理成员、授权、角色和策略 | Product/Workspace/Auth Projection |
| IN-10 | Integrations & Jobs | V1 | 查看 Git/CI/Gateway/IAM 状态与重试 | Integration Job / Reconciliation |
| IN-11 | Governance Analytics | V1.x | 趋势、消费者影响、批量操作 | Metrics & Impact Projection |

## 5. F06-A 核心交互流程与异常恢复

<figure class="architecture-figure" aria-label="内部审核工作流"><div class="fig-label">EXPERIENCE MODEL · 内部审核工作流</div><div class="fig-head"><strong>设计态流程；演示动作不写入后端</strong><small>F06 · 设计模型 / 非运行系统证据</small></div><div class="fig-grid cols-2"><div class="fig-box"><b>Submit</b><small>源信息 + Commit + Revision</small></div><div class="fig-box"><b>Inspect</b><small>Lint · Breaking Diff · Security Scan</small></div><div class="fig-box"><b>Approve</b><small>职责分离 + 策略摘要绑定</small></div><div class="fig-box"><b>Publish</b><small>immutable Release + Slot CAS</small></div><div class="fig-box"><b>Recover</b><small>AccessBlock → Remove/Restore → Reconcile</small></div></div><figcaption>拒绝、过期、CAS 冲突和撤销未知状态均为独立可操作状态。</figcaption></figure>

### 5.1 契约检查、审核与发布

1. 从工作台进入目标 Product 的 API，检索其最新 Revision 与审批状态；更换 Product 时清空上一产品的行级选择、过滤与缓存。
2. 检查页分别显示 **Lint / Breaking Diff / Security**，列出规则 ID、定位（JSON Pointer/OperationId）、严重性、版本、可修复动作与安全阻断原因。只允许经过审查的非关键规则 Waiver。
3. 审核时显示申请者、审核者、Policy Digest、Revision Digest、风险级别、有效期。若发生内容或策略漂移，旧审核不可继续使用。
4. 发布必须展示 Product、Audience、Environment、Portal、APIAsset、Release Digest、expected Slot Version；操作需用户二次确认，CAS 冲突时恢复到最新状态重新决策，**不能自动覆盖**。
5. 紧急撤销先显示服务端权威 AccessBlock，后展示 Edge/缓存传播；传播 UNKNOWN/AT_RISK 应继续阻断公开读取，不显示绿色“已完全清除”。

### 5.2 页面操作级状态

| 状态 | 触发 | 界面反馈 | 后续动作 |
|---|---|---|---|
| Loading | 首次加载或切换 Product | 骨架屏+范围提示 | 可取消/重试、避免展示旧范围 |
| Empty | 当前条件无结果 | 说明当前权限或过滤范围 | 重置筛选、在授权范围内创建 |
| Error | 数据源不可用 | Trace ID、失败依赖及请求标识 | 安全重试/联系 Owner |
| Forbidden | 权限拒绝 | 通用无权页面，不回显私有 API 名 | 申请授权或返回 |
| Conflict | Slot CAS 冲突 | 当前生效版本与期望版本差异 | 重新获取、重新审查 |
| UNKNOWN / AT_RISK | 撤销传播或同步不明 | 持续警示、不显示完成 | 进入补偿任务 / 事故流程 |

### 5.3 审核及发布对话框

对话框必须明确显示不可逆后果、面向的产品和受众、制品摘要与生效环境；确认按钮根据风险采用二次确认、职责分离和 Step-up（具体授权由 F05/F07）。提交后保留请求关联标识，在网络错误时先查询最终状态再决定重试，不能依靠无限重复点击。

## 6. F06-A 工作台、Catalog 与数据密集型交互

- 工作台首屏展示**待处理任务、风险警报、最近变更、产品范围**；统计卡片不作为唯一信息来源。
- Catalog 默认服务器端分页/过滤/搜索；每条记录展示 `APIAsset ID`、Owner、领域、类型、生命周期、当前发布与最近检查结果；不得将不可见记录下载到浏览器再过滤。
- 工作表格支持列排序、条件、清空、分页、行级操作、键盘导航、空结果、加载与错误；大数据量状态与筛选参数可链接分享但不可泄露敏感筛选值。
- 比较页支持并排/内联差异，明确“规则告警”和“确认的破坏性变更”区别；不凭颜色区分影响。
- 审计只能展示脱敏主体和关联 ID，导出需要额外权限，且查询页面不使审计事件可被编辑。

## 7. F06-A 权限可见性与审批责任界面

| 操作 | Author | Reviewer | Release Operator | Admin/Auditor | 界面必需条件 |
|---|---|---|---|---|---|
| 创建 Revision | 本 Workspace | 只读 | 只读 | Admin 授权 | `product_id + workspace_id` |
| 审核自己提交的高风险变更 | 拒绝 | 如是提交者则拒绝 | 拒绝 | 仅授权例外 | 显示明确职责分离原因 |
| 执行发布 | 拒绝（默认） | 拒绝（默认） | 授权 Target | 受控分配 | 确认 CAS/Release Digest |
| 紧急撤销 | 可发起申请 | 可审查 | 按策略执行 | Break-glass 可审计 | AccessBlock 优先并保持 UNKNOWN 状态 |
| 查看其他 Product 的私有 API | 拒绝 | 拒绝 | 拒绝 | 单独授权 | 不返回存在性提示 |

> 权限矩阵是 UI 设计输入，不是身份提供方、PDP 或 Gateway 的最终规则；最终操作授权依赖 F05 的服务端策略。

## 8. F06-B 外部门户：独立品牌与页面结构

CargoPai Developers 是**对外独立品牌门户**，不应把其他 IDP 内部产品名称暴露在 UI、链接、搜索索引、错误消息和缓存键中。

| 页面 ID | 名称 | MVP/V1 | 核心交付任务 | 权威数据 |
|---|---|---|---|---|
| EX-01 | Home / Discover | MVP | 找到业务能力与最短路径 | Product Public Content |
| EX-02 | Quickstart & Authentication Guide | MVP | 理解接入先决条件与示例 | Published Guide/Release |
| EX-03 | API Reference | MVP | 搜索操作、查看 Schema/错误/代码示例 | Authorized Publication Projection |
| EX-04 | Version & Access Denial | MVP | 检查可用版本/拒绝无权版本 | Current Publication, AccessBlock |
| EX-05 | Guides / SDK / Webhook | V1（MVP 说明） | 消费事件/SDK 的正确用法 | Released Guide & SDK Catalog |
| EX-06 | Developer Organization | V1 | 组织邀请、成员和授权范围 | Developer Access BFF |
| EX-07 | Applications & Grants | V1 | 登记应用并提交访问申请 | DeveloperOrg, ConsumerApplication, Grant |
| EX-08 | Sandbox Explorer | V1 | 执行受控真实沙箱首调 | IAM / Gateway / Trace Evidence |
| EX-09 | Changelog, Status & Support | V1 | 迁移、服务状态、获取支持 | Product Announcements / Status Integration |

<figure class="architecture-figure" aria-label="外部开发者接入旅程"><div class="fig-label">EXPERIENCE MODEL · 外部开发者接入旅程</div><div class="fig-head"><strong>MVP 与 V1 的验收界限</strong><small>F06 · 设计模型 / 非运行系统证据</small></div><div class="fig-grid cols-2"><div class="fig-box"><b>MVP · Discover</b><small>产品文档 / Quickstart</small></div><div class="fig-box"><b>MVP · Learn</b><small>API Reference / 代码示例</small></div><div class="fig-box"><b>V1 · Enroll</b><small>组织 / Application / Grant</small></div><div class="fig-box"><b>V1 · Test</b><small>授权沙箱 · 实际首次请求</small></div><div class="fig-box"><b>V1 · Go live</b><small>生产访问 / 支持 / 版本迁移</small></div></div><figcaption>只有真实沙箱返回及可信日志才能证明首次调用成功；本原型仅展示 Mock。</figcaption></figure>

## 9. F06-B 公开与受限内容、版本选择与搜索

- **Public** 可匿名查看，仅限明确发布到 `CargoPai + public + 目标环境` 的内容；**Partner** 必须完成 DeveloperOrg/Grant 与独立会话授权，且不得回显无权资源存在性。
- API Reference 版本按 `Product × Audience × Environment × Portal × APIAsset` 的 PublicationSlot 选择，显示其 Release Digest/发布时间和文档适用环境。
- 未获授权版本统一显示受控拒绝页，搜索不能通过关键词高亮泄露隐藏资源；CDN 缓存必须结合授权范围，撤销后拒绝源站读取。
- 代码切换（例如 cURL、Go、TypeScript）与复制可在 MVP 作为静态示例；代码仅来自受控契约，不执行第三方 JavaScript 示例。

<figure class="architecture-figure" aria-label="权限与数据屏蔽"><div class="fig-label">EXPERIENCE MODEL · 权限与数据屏蔽</div><div class="fig-head"><strong>UI 状态不是最终权限</strong><small>F06 · 设计模型 / 非运行系统证据</small></div><div class="fig-grid cols-2"><div class="fig-box"><b>Unauthenticated</b><small>匿名访问：仅公开内容</small></div><div class="fig-box"><b>Authorized</b><small>服务端判定：产品、Audience、Environment</small></div><div class="fig-box"><b>Forbidden</b><small>无权：不显示文档或元数据</small></div><div class="fig-box"><b>Revoked</b><small>AccessBlock：立即拒绝源站访问</small></div><div class="fig-box"><b>Unknown</b><small>撤销传播不明：禁止错误绿灯</small></div></div><figcaption>不使用前端隐藏按钮代替服务端对象级授权。</figcaption></figure>

## 10. F06-B 外部首次接入与分期验收

### 10.1 MVP 文档阅读闭环

公开首页 → Quickstart → 认证/环境指南 → API Reference → 错误与重试示例 → 查看集成下一步。验证的是**可发现、易理解、无越权的正确文档**，不是 API 真实业务执行。

### 10.2 V1 真实沙箱闭环（由 F07/F08 验证）

开发者注册/邀请 → 组织审核 → Application 注册 → 最小授权 Scope/Grant → IAM 发行凭证 → Gateway 目标检查 → 带 Trace ID 的真实沙箱调用 → 结果与日志关联 → 生产权限申请。权限拒绝、凭证失效、速率限制和超时必须有清楚的故障指引。

**原型中的 Applications / Sandbox 标签全部明确标注“V1 规划 / 本地演示”**，不得输出令人误解的有效 Client Secret、真实 Access Token 或生产响应。

## 11. F06-B SDK、Webhook、错误与自助支持

- 每个 API 操作展示请求、响应、状态码、RFC 9457 结构说明、版本和重试/幂等语义；版本切换后示例同步刷新。
- SDK 页面必须显示 SDK 与契约 Release 的兼容范围、生成来源、支持语言、下载校验和退出维护政策；V1 实现由 F07/F08 验证。
- Webhook 文档需要订阅前提、签名与重放防护、投递/重试/死信策略、事件版本及顺序保证的实际边界，不能承诺至少一次投递同时又暗示恰好一次。
- 状态/支持页面显示运营负责方、状态来源和更新时间，不用 Mock 状态冒充产品实际运行健康。

## 12. 双门户的共同状态与边界

<figure class="architecture-figure" aria-label="用户界面状态模型"><div class="fig-label">EXPERIENCE MODEL · 用户界面状态模型</div><div class="fig-head"><strong>状态覆盖所有关键页面</strong><small>F06 · 设计模型 / 非运行系统证据</small></div><div class="fig-grid cols-2"><div class="fig-box"><b>Loading / Empty</b><small>占位、重试建议、创建入口</small></div><div class="fig-box"><b>Error</b><small>错误原因、恢复动作、Trace ID</small></div><div class="fig-box"><b>Forbidden</b><small>权限不足且无私有元数据</small></div><div class="fig-box"><b>Conflict</b><small>CAS 竞争、显示最新版本</small></div><div class="fig-box"><b>UNKNOWN / AT_RISK</b><small>紧急撤销、传播未知、不宣称完成</small></div></div><figcaption>语义、颜色、文本和行动建议同时表达状态，不依赖颜色作为唯一提示。</figcaption></figure>

统一状态语义包括 Loading、Empty、Error、Forbidden、Conflict、Deprecated、Retired、Blocked、UNKNOWN/AT_RISK；但两门户的恢复动作不同：内部允许在授权范围内重试发布、撤销或发起安全事故，外部只能查看已允许的替代路径、申请访问或联系支持。

## 13. F06-C 共享设计系统：Token 与视觉层级

共享视图登记 `SH-01`：设计 Token、共用组件和可访问性基线。此项不代表独立部署第三个门户。

<figure class="architecture-figure" aria-label="共享设计系统"><div class="fig-label">EXPERIENCE MODEL · 共享设计系统</div><div class="fig-head"><strong>品牌独立与工程复用</strong><small>F06 · 设计模型 / 非运行系统证据</small></div><div class="fig-grid cols-2"><div class="fig-box"><b>Foundations</b><small>Token · Typography · Spacing · Contrast</small></div><div class="fig-box"><b>Components</b><small>Table · Filter · Modal · Code · Tabs</small></div><div class="fig-box"><b>Patterns</b><small>Worklist · Review · Release · Explorer</small></div><div class="fig-box"><b>Accessibility</b><small>Keyboard · Focus · SR · i18n</small></div></div><figcaption>内部使用密集业务界面，外部侧重文档阅读，两者共享组件语义不共享品牌标识。</figcaption></figure>

| Token / 层级 | 建议值（设计候选） | 规范 |
|---|---|---|
| `--ink` / `--muted` | `#17324D` / `#567084` | 正文与辅助文字须满足对比度目标 |
| `--brand-idp` | `#195F91` | 内部门户工程品牌 |
| `--brand-cargopai` | `#176A80` | 外部门户候选，可由品牌正式规范覆盖 |
| `--surface` / `--canvas` | `#FFFFFF` / `#F4F7FA` | 清晰分区、减少背景噪音 |
| `--danger` / `--warning` / `--success` | 语义色 + 标签 | 不仅用颜色传达状态 |
| Spacing scale | 4 / 8 / 12 / 16 / 24 / 32 | 统一节奏，表格密集区可使用 8/12 |
| Radius | 6 / 10 / 14 | 表单与卡片统一，不靠过度圆角表达重要性 |
| Typography | 系统字体栈、字号分级 | 中文字号、行高和代码字体分别校准 |

颜色为 **F06 交互原型的暂用 Token**，并非改变 CargoPai 已定品牌 Logo 或官方配色。实际品牌资产接入必须以品牌规范为准。

## 14. F06-C 组件库与交互规范

| 组件 | 必需状态 | 必需行为 |
|---|---|---|
| Global Navigation / Product Switcher | Active/Forbidden/Loading | 切换清空跨产品页面状态，不保留私有旧内容 |
| Data Table / Filter Bar | Empty/Loading/Error/Selected | 可见筛选与数量、可恢复排序、键盘可用 |
| Code Panel | Language/Copy/Version | 内容对应单一 Release，复制有反馈 |
| Review Diff / Risk Panel | New/Breaking/Blocked/Waived | 解释规则、原因、风险与审核关系 |
| Dialog / Confirmation | Focused/Busy/Conflict/Done | 焦点限制、Escape（未提交）、返回触发元素 |
| Status Badge | Draft/Approved/Blocked/Unknown | 文案、图标及对比度不依赖颜色 |
| Toast / Inline Error | Info/Warning/Danger | `aria-live` 适当通知；长错误保留在页面 |
| Form / Input | Empty/Invalid/Disabled | 错误说明与字段关联，不能只使用 Placeholder |
| Version Selector | Public/Partner/Deprecated | 当前环境、受众、发布目标准确显示 |
| Explorer (V1) | Mock/Connected/Forbidden/Timeout | 不让任意 URL 成为通用网络代理；真实调用有 Trace |

## 15. 断点、键盘、可访问性和国际化

- 断点设计候选：宽屏 ≥ 1200px 显示完整双栏工作区；平板 768–1199px 压缩信息；手机 < 768px 导航抽屉、单列操作与滚动表格。属于 UI 设计 Token，不是后端分布式系统阈值。
- 内外两个门户 V1 目标 **WCAG 2.2 AA**；MVP 关键任务同样必须键盘可用，不得以 V1 无障碍计划推迟基础键盘操作。
- `Tab/Shift+Tab` 可达；明显 `:focus-visible`；导航 `aria-current`；对话框可返回焦点；错误不靠颜色；页面标题、主区域与跳转链接可被屏幕阅读器识别。
- 中英文标签及时间/货币/单位采用地区化格式，但不可翻译技术 ID、OperationId、错误代码、事件类型；多语言回退到明确的默认语言并提示。
- 生产配色需实测文本对比（普通文字建议 ≥ 4.5:1，大文字 ≥ 3:1）及 200% 缩放布局，不以浏览器截图或静态 lint 冒充整站 WCAG 通过。

## 16. 隐私、缓存与安全运营的 UX

- 凭证不储存于浏览器持久存储；如未来 Explorer 需要认证，只通过受控会话机制处理，不把真实令牌拼入 URL、日志、分享链接或界面截图。
- 保护 Product/Workspace、开发者组织、受众和环境上下文；服务端必须拒绝跨产品或未授权查询。外部公开文档与合作伙伴文档分别审计、缓存和授权。
- 撤销按钮与恢复按钮绝不能被描述为同一简单 Undo；AccessBlock/PublicationSlot/边缘传播分别展示。
- 导航权限不足时，通用拒绝页不显示隐藏页面的标题、API 列表和资产总量。

## 17. 真实使用者评估与产品指标

| 指标 | 分子 / 分母定义 | 阶段 | 测量方式 |
|---|---|---|---|
| Internal review completion | 有效完成审核数 / 有权限进入审核任务数 | MVP | 任务事件与人工评审 |
| Release risk detection | 被正确识别的高风险发布场景 / 预置高风险场景 | MVP | F08 场景回放 |
| Docs findability | 不受提示完成指定文档任务人数 / 参加任务人数 | MVP | 开发者可用性测试 |
| First sandbox call success | 有可信真实调用记录的成功首调人数 / 获得有效沙箱授权人数 | V1 | IAM/Gateway Trace |
| Recovery comprehension | 能准确判断 UNKNOWN/AT_RISK 并采取正确动作的人员 / 参与者 | MVP | 角色任务走查 |
| Accessibility compliance | 关键任务的实际 WCAG 2.2 AA 核验结果 | V1 | 自动检查 + 手工键盘/SR |

指标目标值、样本量、观察窗口与验收责任由 F08 批准；本文件不虚构数值。

## 18. 原型交付文件与演示约束

| 文件 | 类型 | 交互重点 |
|---|---|---|
| [`portal/prototypes/internal-engineering.html`](../portal/prototypes/internal-engineering.html) | 离线 HTML / 本地 Mock | 产品切换、Catalog 过滤、治理检查、审核、Publication CAS 冲突、撤销 UNKNOWN |
| [`portal/prototypes/cargopai-developers.html`](../portal/prototypes/cargopai-developers.html) | 离线 HTML / 本地 Mock | CargoPai 品牌、Quickstart、API Reference、语言切换、私有版本禁止、V1 沙箱限制 |
| `portal/prototypes/shared.css` | 共享设计 Token / 布局样式 | 复用组件但不同品牌主题 |
| `docs/F06_Page_Inventory.csv` | 21 个页面或共享视图 | 页面职责、阶段、角色和模型 |
| `tests/acceptance/F06_experience_trace.csv` | 89 项 FR/NFR 映射 | DIRECT / SUPPORT / NEXT，全部 NOT_RUN |
| `tests/acceptance/F06_experience_scenarios.csv` | 32 个体验测试场景 | 真实执行状态初始 NOT_RUN |

原型中的用户、数据、API 名称及结果均为 `DESIGN_EXAMPLE / LOCAL_MOCK`。不展示有效 Token，不真实提交、签发、调用、回滚或撤销。

## 19. F06 上位规范、需求追溯与 F07/F08 交接

<figure class="architecture-figure" aria-label="追溯与验收"><div class="fig-label">EXPERIENCE MODEL · 追溯与验收</div><div class="fig-head"><strong>从功能要求到可验证体验</strong><small>F06 · 设计模型 / 非运行系统证据</small></div><div class="fig-grid cols-2"><div class="fig-box"><b>F01</b><small>71 FR + 18 NFR</small></div><div class="fig-box"><b>F02–F05</b><small>Context · Domain · Policy · Security</small></div><div class="fig-box"><b>F06</b><small>Page ID / Task / State / Data Source</small></div><div class="fig-box"><b>F07</b><small>Implementation & instrumentation</small></div><div class="fig-box"><b>F08</b><small>Executed evidence / Gate review</small></div></div><figcaption>本阶段资产为设计追溯，所有生产安全和业务验收均 NOT_RUN。</figcaption></figure>

F06 只提供设计与证据计划：上位规范的 CORE/UX Profile 正式适用范围、规则 ID 与六道门禁需具名确认；F07 负责实施与真实浏览器/无障碍、API、安全适配验证；F08 负责端到端使用者验收、数据真实来源和发布准备结论。

为避免不合理的“89 项需求都由 UI 直接实现”，追溯表分为 **DIRECT_UX、SUPPORTING_UX、FUTURE_STAGE**；F01 FR/NFR 均只出现一次，注明页面、角色、目标阶段、验证责任，不把运行测试填成 PASS。

## 20. 风险、开放决策与设计签收条件

| ID | 未决事项 | 风险 | 后续责任 |
|---|---|---|---|
| UX-OD-01 | 正式 CargoPai 品牌 Token/Logo 与 UI 资源 | 不能在原型里未经批准替换品牌 | F06 产品/品牌负责人 |
| UX-OD-02 | F05 授权实现、匿名/Partner 访问级别 | 界面不能证明后端隔离 | F07 安全负责人 |
| UX-OD-03 | 系统实时状态和缓存传播可观测性 | UNKNOWN 易误读为完成 | F07 运行负责人 |
| UX-OD-04 | SDK、Webhook、Sandbox 实际能力与时间表 | V1 页面尚无可用后台 | F07/F08 产品负责人 |
| UX-OD-05 | 文案翻译、辅助技术与用户研究样本 | 仅有视觉测试不足以验收 WCAG | F08 UX 负责人 |
| UX-OD-06 | ADR-0001–0007 正式批准情况 | 不得将候选架构当已批准约束 | 架构委员会 |

**F06 文档交付完成判据：** A/B/C 同一正文具备可审查内容；21 个页面/视图与 89 项需求关联；32 项可执行场景设计清楚；两套离线 HTML 原型可操作并明确 MOCK；构建与本地链接/浏览器布局/ZIP 解压通过；F01–F05 Markdown 字节不变；F07/F08 尚未完成且不得伪造。**文档交付完成不等于 F08 正式验收。**
