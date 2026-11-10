# F05 — IDP API Fabric 安全架构与多租户设计

> **文档编号**：F05 · Security & Multitenancy  
> **项目**：IDP API Fabric / `idp-api-fabric`  
> **工程基线**：V1.0 · 2026-11-09（与 F01–F04 同一版本，不另立版本）  
> **状态**：安全架构设计候选，待具名安全/架构评审；本文件不是安全认证、渗透测试报告或生产上线批准  
> **权威输入**：[F01](F01_Product_Requirements.md) · [F02](F02_System_Architecture.md) · [F03](F03_Domain_and_Data_Model.md) · [F04](F04_API_Contract_Governance.md) · [阶段交接约束](README.md)  
> **上位规范**：IDP-SCG-STD-001《IDP Supply Chain DDD Product Engineering Standard V1.0》；项目 Profile 采用仍待具名批准。  
> **验证状态**：本地文件结构/矩阵一致性检查可执行；真实 IAM、Gateway、CDN、API Explorer、授权安全、数据恢复与渗透测试全部 `NOT_RUN`。

## 0. 执行摘要与安全架构裁决

**设计目标：在跨产品统一的控制平面上，证明每一条管理命令、契约读取、搜索结果、版本下载、发布操作和外部开发者接入都经过服务器端的多维授权；产品自治不能被共享缓存、统一身份或共享域名击穿。** Fabric 不是 IdP、API Gateway 或业务 API 运行时。F05 只为 Fabric 自身及其适配器定义安全责任；各产品真实 API 的授权由其 Gateway/业务资源服务器负责。

| 安全原则 | 必须满足的可验证约束 | 对应上游 |
|---|---|---|
| Deny by default | 未登录、权限缺失、对象不在范围、授权来源异常时拒绝，不以 UI 隐藏代替服务端授权 | F01 FR-003/049/063；F03 INV-02/11 |
| Scope is a tuple | Product、Workspace、Organization、Principal、Audience、Environment、Asset、Release、Grant 和 Action 均是独立判定维度 | F02 AI-03；F03 §7 |
| Artifact ≠ Publication | 不可变 Release 可保留；对外可见性由 Target×APIAsset Slot、Grant、AccessBlock 和策略共同决定 | F03 INV-05/08/09；F04 §10 |
| Document access ≠ API execution | 查看 API Reference 的权限不表示调用业务 API 的权限；应用凭证由外部 IAM 保管 | F01 FR-049/053；F02 ADR-0006 |
| Fail closed with honest status | 关键授权/撤销状态不可验证时拒绝受限访问并显示 `UNKNOWN`，不能假报同步完成 | F03 §11；F04 §10 |
| Measurable evidence | 安全规则、威胁、风险接受、测试场景与实际证据严格分开 | SCG-GOV / STD-CP1–CP6 |

<figure class="architecture-figure"><div class="fig-label">FIGURE 01 · 安全域与控制责任</div><div class="fig-head"><strong>联合控制平面 ≠ 统一信任区</strong><small>每条跨边界请求重新验证身份与资源范围</small></div><div class="fig-grid cols-3"><div class="fig-box tone-light"><b>Internal Engineering</b><small>公司 IdP · 工程人员 · 管理操作</small></div><div class="fig-box tone-light"><b>External Developer</b><small>客户组织 · 产品隔离 · 自助服务</small></div><div class="fig-box tone-light"><b>Untrusted Contract Input</b><small>Git / 上传 / URL / 示例与附件</small></div></div><div class="fig-arrow">↓ WAF / TLS · Session BFF · Request Admission（并非最终授权）</div><div class="fig-grid cols-3"><div class="fig-box fig-primary"><b>Go Control Plane</b><small>PEP → PDP · Object-level Scope · Audit</small></div><div class="fig-box"><b>Trusted Worker</b><small>受限 egress · 固定身份 · 任务 fencing</small></div><div class="fig-box"><b>Portal Delivery</b><small>可见性验证 · 缓存分区 · 阻断</small></div></div><div class="fig-arrow">↓ 主库权威 / 制品库保护 / 可重建索引（不可代替鉴权）</div><div class="fig-grid cols-2"><div class="fig-box tone-green"><b>External IAM / Gateway</b><small>身份与业务调用授权最终执行者</small></div><div class="fig-box tone-green"><b>Audit / KMS / Backup</b><small>受限证据与密钥边界</small></div></div><figcaption>图示为安全逻辑与信任区，不表示已有专用 WAF、KMS、BFF 或网络隔离产品完成部署。外部契约内容始终属于不可信输入。</figcaption></figure>

## 1. 适用边界、标准与规范效力

### 1.1 明确责任和非目标

**Fabric 负责**：其自身登录会话的安全集成、资源授权、受控文档访问、Contract Import 安全、发布阻断、审计、密钥引用、集成适配及安全事件证据。**不负责**：为 CargoPai/Parcel 提供用户目录权威、发放生产 OAuth Client Secret、替代业务 API Gateway、在文档站执行业务请求代理或保证已被第三方复制的公开内容可被召回。

**阶段边界**：MVP 必须覆盖服务端授权、跨产品隔离、契约输入安全、不可变制品受限读取、基本发布阻断/恢复及最小审计；V1 增强开发者组织/应用/Grant、在线 Explorer、身份和 Gateway 自助集成、运行态漂移与运营合规；V2 AI 工具执行安全独立设计，不得因 F05 写入而提前视为已实现。

### 1.2 外部依据及采用约束

| 依据 | 本项目用途 | 保留事项 |
|---|---|---|
| OWASP ASVS 5.0.0 (2025) | Web/BFF/管理 API 安全要求与测试基准 | 需在 F07/F08 挑选具名条目并验证，不能宣称 ASVS 达标 |
| OWASP API Security Top 10 (2023) | BOLA/BFLA、SSRF、资源消耗及资产管理威胁覆盖 | 风险映射不等于扫描通过 |
| RFC 9700 (OAuth 2.0 Security BCP) | OIDC/OAuth 重定向、PKCE、令牌与危险授权模式 | IdP 真实兼容性待验证 |
| RFC 8725 (JWT BCP) | 令牌算法约束、aud/iss/exp、密钥选择与混淆防护 | 资源服务器必须匹配令牌类型与信任域 |
| NIST SP 800-63-4 (2025) | 身份保证、身份联邦、认证器治理的参考框架 | 不擅自宣称 AAL/IAL/FAL 认证级别 |
| NIST SSDF SP 800-218 | 构建、供应链、漏洞响应及可验证证据 | 工程门禁由 F07 细化 |
| IDP-SCG-STD-001 | CORE、触发式 Profile、具名复核及六道门禁 | 项目采用 Manifest 仍待具名批准 |

参考官方链接见 §23。隐私和地域合规按实际用户、数据位置、服务模式与法律主体适用性决定，不把任何法域的义务凭空写成已确认项目事实。

## 2. 安全资产分类、主体与信任边界

### 2.1 数据分类（推荐候选）

| 等级 | 数据举例 | 读取控制 | 日志 / 分发规则 |
|---|---|---|---|
| PUBLIC | 经批准公开的 API Reference、迁移指南 | Published Slot + Public Audience + 无有效阻断 | 公开版本可经审批缓存；不能包含秘密 |
| PARTNER_RESTRICTED | 合作伙伴专属契约、沙箱操作、未公开版本 | Subject + Active Grant + Scope + AccessBlock | 不使用公共 CDN 缓存键；`no-store` 为安全默认 |
| INTERNAL | 草稿、ReviewDecision、内部 API、治理规则与差异 | 内部 Product/Workspace 角色与对象范围 | 禁止映射到外部静态站点 |
| SENSITIVE | 客户联系人、审计主体标识、集成元数据 | 最小字段、用途限制、精确角色 | 去标识化、审计导出受控 |
| SECRET | Token、私钥、连接凭证、签名材料 | Vault/KMS 授权 + 短期使用 | 禁止落入契约、页面、请求 URL、日志、浏览器存储与备份明文 |

分类与实际生命周期是两个维度。公开内容发布前仍需要敏感信息检查；Secret 不应因为出现在历史 Release 中就获得长期存储豁免，必须触发泄露响应与制品清理/阻断程序。

### 2.2 Principal / resource / action / context

- `Principal`：来自已信任身份系统的 `issuer + subject`，附可信认证强度和租户组织映射；用户输入的组织 ID 不产生身份权威。
- `Resource`：`product_id`、`workspace_id`、`api_asset_id`、`release_id`、`target_id`、`slot_id`、`developer_org_id` 等，均由服务端按权威关系加载并校验。
- `Action`：最小操作单位，如 `asset.read`、`contract.download`、`release.approve`、`publication.activate`、`publication.withdraw`、`access.grant`、`audit.export`。
- `Context`：Audience、Environment、有效时间、来源渠道、访问风险、策略版本、会话新鲜度和与当前操作相关的审批事实。

<figure class="architecture-figure"><div class="fig-label">FIGURE 02 · 安全判定最小语义</div><div class="fig-head"><strong>Allow = Authenticated ∧ CorrectScope ∧ ActionGrant ∧ StateValid ∧ NoEffectiveBlock</strong><small>缺少任何一项时拒绝，策略不可得则禁止高风险动作</small></div><div class="fig-grid cols-3"><div class="fig-box"><b>Identity</b><small>iss · sub · token_type · aud · exp</small></div><div class="fig-box"><b>Relation</b><small>Product ↔ Workspace ↔ Org / Grant</small></div><div class="fig-box"><b>Object</b><small>Asset ↔ Release ↔ Target × Slot</small></div></div><div class="fig-arrow">↓ PDP（Policy Decision Point）：可信数据源、明确拒绝优先</div><div class="fig-grid cols-2"><div class="fig-box tone-green"><b>ALLOW</b><small>输出最小可见字段 + 审计关联</small></div><div class="fig-box tone-light"><b>DENY / INDETERMINATE</b><small>无权限、数据不一致、策略超时、阻断仍有效</small></div></div><figcaption>展示前端没有权限按钮、在 OpenAPI 中标注 Scope，均不能替代服务端每次授权检查。</figcaption></figure>

## 3. STRIDE 威胁模型与攻击面

### 3.1 威胁建模对象

**入口**：内部/外部浏览器、静态门户、BFF、控制平面 HTTP API、Git webhook、CI 机器人、契约上传、外部 `$ref`、API Explorer、异步 Worker、对象存储、搜索与 CDN。**信任交界**：互联网 → Portal/BFF；BFF → Go Control Plane；Go → IAM/DB/Registry；Worker → Git/外网；可信发布命令 → 公共或私有分发。

<figure class="architecture-figure"><div class="fig-label">FIGURE 03 · STRIDE 威胁面映射</div><div class="fig-grid cols-3"><div class="fig-box tone-light"><b>S — Spoofing</b><small>伪造身份 / OAuth callback / webhook</small></div><div class="fig-box tone-light"><b>T — Tampering</b><small>恶意 `$ref` / Release 内容替换</small></div><div class="fig-box tone-light"><b>R — Repudiation</b><small>发布审批无法举证</small></div><div class="fig-box"><b>I — Disclosure</b><small>跨产品搜索、对象链接、缓存泄露</small></div><div class="fig-box"><b>D — DoS</b><small>Schema bomb、递归、巨型压缩包</small></div><div class="fig-box"><b>E — Privilege Escalation</b><small>越权审批、组织映射错误、角色继承</small></div></div><div class="fig-arrow">↓ 映射：风险 ID → 资产/边界 → 预防/检测/恢复控制 → 设计验证场景</div><div class="fig-box tone-green"><b>威胁登记册为源资产</b><small>`governance/policies/F05_Threat_Register.csv`；本阶段风险评级是提议值，Owner 及残余风险接受待指定。</small></div><figcaption>STRIDE 用于组织安全分析，不能替代手工安全评审、测试、红队或渗透验证。</figcaption></figure>

对应的风险和控制见独立 CSV；每一项都有稳定的 `TH-xx` ID 和负向安全测试入口。禁止仅以“使用 HTTPS”关闭 SSRF、BOLA 或跨产品缓存泄露风险。

## 4. 身份联邦、会话及令牌信任

### 4.1 身份系统集成原则

**内部员工登录**：与经过批准的企业 IdP 通过 OIDC Authorization Code Flow + PKCE 集成，OIDC token 在 BFF 或服务端受控组件处理；浏览器只持有安全会话 Cookie，原则上不把长期 Token 保存在 `localStorage`。**外部开发者登录**：可使用单独的 IdP realm/issuer 或可区分的可信配置；须针对产品、组织与成员关系在 Fabric 内再授权，不能把通过认证理解为跨产品授权。

**CI/Worker/服务账户**：使用受约束的 Client Credentials、工作负载身份或 mTLS 等经批准机制，绑定唯一技术主体、最小 Scope、明确 Audience；禁止把员工高权限令牌复制到 CI 或 Worker。外部真实业务 API 的 OAuth Client Secret 由授权 IAM 持有，Fabric 仅存受控引用与状态元数据。

### 4.2 JWT 与会话最低验证

| 对象 | 必须验证 | 不允许的捷径 |
|---|---|---|
| JWT Access Token | `iss`、`aud`、签名算法白名单、可信 JWKS key、`exp`、必要的 `nbf`、token type/scope | 只 Decode 不 Verify；接受 `alg=none`；不区分 ID/Access token |
| OIDC 登录 | state、PKCE verifier、nonce（如要求）、exact redirect URI、client/issuer binding | 以用户可控 redirect 参数拼装回调 URL |
| BFF Session | HTTP-only / Secure / SameSite Cookie、会话轮换、到期、CSRF 防护、退出清理 | 浏览器长期保存 bearer secret |
| 服务身份 | mTLS 或经批准的工作负载 token、受限 `aud`、短有效期、关联 Deployment Identity | 共享长期系统管理员令牌 |
| 权限状态 | 服务端 Membership/Grant 当前状态、撤销代际或有效性 | 仅相信旧 JWT 中嵌入的角色直到长 Token 过期 |

对于高风险审批、撤销与安全配置，要求短时登录新鲜度（step-up）或等效强保证，目标时长由安全 Owner 在 F07/F08 参数基线中明确。所有会话级 Cookie 应采用相适应的 CSRF 防御与严格的 CORS 来源白名单。

<figure class="architecture-figure"><div class="fig-label">FIGURE 04 · 两种身份与授权流程不可合并</div><div class="fig-grid cols-2"><div class="fig-lane"><strong>Internal · 工程人员</strong><span>OIDC IdP → BFF Session</span><div class="fig-mini">Product/Workspace Membership</div><div class="fig-mini">PEP/PDP 操作级授权</div><div class="fig-mini">审批人职责分离 / Step-up</div><div class="fig-mini">Audit + 事务性决策证据</div></div><div class="fig-lane"><strong>External · 合作伙伴开发者</strong><span>外部 IdP → Developer Session</span><div class="fig-mini">DeveloperOrg Membership</div><div class="fig-mini">Product × Audience × Env Grant</div><div class="fig-mini">Publication Slot / Block 核验</div><div class="fig-mini">受限 API Reference / 文档下载</div></div></div><div class="fig-separator">仅查看文档的授权 ≠ Gateway 执行业务 API 的 Access Token Scope</div><figcaption>Fabric 的 AccessGrant/Subscription 元数据不是 IAM 发出的真实访问令牌，系统断连时只能显示 UNKNOWN / STALE。</figcaption></figure>

## 5. 多产品、租户与组织授权体系

### 5.1 关系模型

`Product` 是被治理的产品边界；`Workspace` 属于唯一 Product；平台内部 `Organization/Tenant` 与 `DeveloperOrganization` 是不同的身份/商务关系；`ConsumerApplication` 属于开发者组织且须明确连接 Product 的访问申请。一个组织使用 CargoPai 和 IDP Parcel 不会自动获得两个产品全部权限。Product 名称、邮箱、开发者组织名称、同名 API Asset 不产生跨产品授权。

授权结构采用 **RBAC 管功能 + ABAC/关系策略管资源范围和状态**：`Role → Action` 是必要但不充分条件；仍须检查 Product/Workspace/Resource Owner、Audience/Environment、Grant、时间、PublicationSlot 当前指针与 AccessBlock。产品管理员不可凭自己角色越权成为平台全局管理员。

<figure class="architecture-figure"><div class="fig-label">FIGURE 05 · 多维范围不是 tenant_id 单键</div><div class="fig-grid cols-3"><div class="fig-box fig-primary"><b>Product</b><small>CargoPai / Parcel / Brant / Prism</small></div><div class="fig-box"><b>Workspace</b><small>单一 Product 所有权</small></div><div class="fig-box"><b>Organization / Principal</b><small>有效成员、角色、身份保证</small></div></div><div class="fig-arrow">↓ Resource 归属必须和访问范围相交</div><div class="fig-grid cols-3"><div class="fig-box"><b>APIAsset / Release</b><small>不可变内容 · 私有历史受控</small></div><div class="fig-box"><b>Audience / Environment</b><small>internal / partner / public · sandbox / production</small></div><div class="fig-box"><b>DeveloperOrg / Grant</b><small>外部独立申请与撤销关系</small></div></div><div class="fig-arrow">↓ Target × APIAsset = Slot · Current Release + AccessBlock（Deny 最高优先级）</div><div class="fig-box tone-green"><b>Effective Access Is a Computed Decision</b><small>不得从 Product ID 推导一个无条件 Permit，不能用客户端给出的 tenant_id 作为权威范围。</small></div><figcaption>所有箭头为关系检查，不是跨领域共享写入。模型必须遵守 F03 不变量与提议 ADR-0004。</figcaption></figure>

### 5.2 授权决策次序及结果

1. **认证**：确认可信 issuer、token 用途和主体；匿名仅允许明确标记 PUBLIC 的经过批准内容。
2. **资源定位**：以服务端可信 ID 加载资源，校验 Asset/Release/Slot/Target 的 Product 归属，不相信客户端拼接的 product_id。
3. **策略和关系**：验证 Role→Action、Workspace Membership、DeveloperOrg Membership、Grant、受众/环境以及职责分离；策略过期按拒绝处理。
4. **状态**：验证 Revision/Release/Publication 合法状态，当前 Slot Generation、AccessBlock 的单调代际和 API Lifecycle；紧急阻断优先于允许。
5. **返回**：ALLOW、DENY 或 INDETERMINATE；INDETERMINATE 在私有读/写和高风险路径一律表现为拒绝，同时在内部运维面报告依赖不确定性。

`403` 与 `404`：对匿名或跨租户对象查询，建议返回 `404` 防止枚举；合法范围内权限不足可返回 `403`。但两者均须经过内部审计，响应长度和错误细节不得泄漏对象是否存在；API 错误结构按 F04 的 RFC 9457 候选规范。具体统一映射在 F07 实施与安全测试中验证。

### 5.3 权限缓存

身份会话与权限缓存必须分离；权限缓存键至少包含 `principal_id + resource scope + action + membership_generation + grant_generation + policy_digest`（具体实现待 F07），不能只按 `user_id` 缓存。**撤销与发布阻断路径不得依赖不可控 TTL 才生效**；对高风险写、私有契约读和撤销操作使用权威校验或强版本校验。缓存丢失时对受限数据拒绝，不允许“为保持可用性默认允许”。

## 6. 内部/外部双门户权限矩阵

`governance/policies/F05_Access_Matrix.csv` 是本章的可机读设计清单，覆盖私有列表/详情/搜索/历史、下载、审批、发布、撤销、运营、API Explorer、外部开发者和服务身份。**矩阵里的 Role 是设计角色，非已经创建的 IAM 角色；`ALLOW_IF` 永远包含服务端范围及对象状态检查，不代表无条件 Permit。**

| 角色 | 可以申请的核心操作 | 不可隐含获得的权力 |
|---|---|---|
| Platform Security Admin | 策略、安全事件、紧急阻断（按批准流程） | 普通 Release 自批准及跨产品任意私有内容浏览 |
| Product Owner / Workspace Maintainer | 所属产品的 Asset/Workspace 与非高风险管理 | 平台全局治理、其他产品、外部 IAM 凭证 |
| Contract Author | 所属 Workspace 创建草稿、查看检验结果 | 批准自己的高风险发布 |
| Independent Reviewer | 指定范围内复核与签字 | 修改同一待审 Revision 并自批准 |
| Release Operator | 按有效审批对指定 Slot 发布、撤销和恢复 | 绕过 AccessBlock、修改不可变 Release |
| Internal Reader / Auditor | 授权范围的查询、只读审计 | 修改审批、解密秘密或批量外发审计 |
| External Developer | 所属组织已授权的公开/受限文档 | 其他开发者组织、内部草稿与业务 API 调用权 |
| CI Service / Worker Identity | 固定任务范围下提交、验证和执行受限工作 | 互动登录和跨产品超级权限 |

**职责分离原则**：Author → Reviewer → Publisher 三方职责原则上分离；同一主体参与多个阶段时，必须定义是否允许的风险等级和独立批准程序。紧急阻断允许经过审计的最小 Break-glass，但解除阻断与恢复发布需重新校验授权和审批，不能由“紧急角色”自动放行。具体 SoD/Break-glass 时限和责任人为 `TBD / Proposed`。

## 7. 授权策略表达与执行边界

本阶段提供 `governance/policies/F05_Authorization_Policy_DRAFT.json`，是**可机器解析的策略结构样例**，包含 `deny_overrides`、资源关系、显式 Allow 条件及关键禁止项；这不是运行中的 PDP，不可由 CI “JSON 通过”就宣称安全实现完成。

| 动作 | 关键前置条件（示例） | 任何情况下拒绝的条件 |
|---|---|---|
| `contract.download` | Asset 所属产品匹配；私有需成员/Grant；Release 允许查看；无 Block | 跨产品、Grant 已撤销、Blob 直链缺鉴权 |
| `release.approve` | 独立 Reviewer、策略/摘要/目标一致，审查有效 | Author 自批准、策略变更后沿用旧结论 |
| `publication.activate` | Release READY，决定有效，Target 产品一致，CAS generation 匹配 | 存在 Block、审批过期、发布操作者无权限 |
| `publication.withdraw` | 指定 Slot、具权主体或安全应急路径，操作留证 | 无范围授权的其他产品撤销 |
| `publication.unblock` | 授权安全复核和完整传播确认或受控临时阻断解除策略 | 未证明风控解除即自动重启发布 |
| `developer.application.read` | DeveloperOrg 成员且 Application 属本组织与指定产品 | 枚举其他组织应用 ID |
| `audit.export` | 独立审计授权、目的/字段/范围、导出期限 | 普通产品操作者任意拉取全平台日志 |

实现可采用应用层 PEP（API Adapter 和 Use Case 双重检查）调用集中策略组件/PDP，拒绝原因使用结构化规则 ID，内部可解释但外部只返回必要信息。不能依赖 PostgreSQL RLS 作为唯一授权机制，RLS 只可作为额外防线。

## 8. 数据库、对象存储、搜索和缓存的隔离

**数据库**：任何需要强作用域的业务数据都带稳定 Product/Workspace 归属引用；在应用层校验跨聚合关系；必要时使用 PostgreSQL RLS 作为纵深防御，但必须测试连接池会话变量污染、事务边界、超级用户和 BYPASSRLS 权限。若使用 `SET LOCAL`/交易上下文，需确保每次请求/事务完成后强制复位；共享连接不能用“上次查询的租户”作为默认身份。数据库物理 Schema、RLS 策略和迁移由 F07 验证。

**对象存储**：不可变 Release 原文和 Manifest 使用内容摘要及存储版本标识；存储桶、对象路径或猜不出的 UUID 不构成访问控制；下载通过服务端鉴权后受限发放短时链接或鉴权代理；链接生命周期和预签名 URL 撤销缺陷必须列为残余风险。高敏内容优先不直接向浏览器发放可复用的对象直链。

**搜索**：只能索引允许的最小字段和安全标签，不将敏感全文存入公共索引；查询执行前和返回每个结果时校验授权与状态；搜索投影是可重建的非权威视图。异步索引滞后时，可以漏显示新数据，但不能泄露已撤销的私有数据。

**缓存与 CDN**：私有内容默认 `Cache-Control: private, no-store`，避免身份无关的共享缓存；若使用可缓存的公开内容，必须按产品/受众/环境/版本隔离键，配置撤销 purge 和 Edge deny。缓存 Key 的组织范围、授权状态不能依赖前端参数决定。

## 9. 安全发布、紧急撤销与传播证明

**单一生效事实**：`PublicationSlot(target_id, api_asset_id)` 的 CAS Generation 是主数据库中单个 API 在指定目标的当前发布指针；`AccessBlock` 是独立且优先的阻断事实（F03 INV-08/09）。公开内容还必须通过发布策略和独立安全扫描，不能把 `PortalProjection READY` 当成安全 Allow。

<figure class="architecture-figure"><div class="fig-label">FIGURE 06 · 发布与紧急撤销的安全顺序</div><div class="fig-grid cols-3"><div class="fig-lane"><strong>正常发布</strong><span>Reviewer + Check Evidence</span><div class="fig-mini">Release Digest/Scope 验证</div><div class="fig-mini">无 AccessBlock · CAS 更新 Slot</div><div class="fig-mini">Portal 再验证 + 发布投影</div></div><div class="fig-lane"><strong>错误公开 / 紧急撤销</strong><span>认证安全主体创建 Deny</span><div class="fig-mini">写入单调 BlockGeneration</div><div class="fig-mini">源站/鉴权层立即拒绝后续请求</div><div class="fig-mini">CAS 撤销 Slot / 回退到安全版本</div></div><div class="fig-lane"><strong>传播与恢复</strong><span>触发 CDN/Edge purge + 对账</span><div class="fig-mini">记录 ACK、边缘实测与时间</div><div class="fig-mini">未确认显示 UNKNOWN / AT_RISK</div><div class="fig-mini">具权复核后才允许 Unblock</div></div></div><div class="fig-separator">Deny 的“记录已提交” ≠ 全球 Edge 全部清除；对已下载内容不存在保证召回的技术手段</div><figcaption>顺序是安全目标而非已实现 SLA。阻断目标、客户端可观测性和传播时间指标必须在 F07/F08 实测并批准。</figcaption></figure>

### 9.1 三种撤销级别

- **敏感内容泄露（SEV 提议为高）**：阻断 Target/Slot/Artifact 下载 + 禁止缓存继续访问，评估泄露范围，必要时轮换泄露的凭证；不能仅修改 release pointer。
- **错误但非敏感的 API 文档**：可以按策略安全切换版本，并发布迁移/澄清；仍保留历史和审计。
- **私有授权撤销**：从权威 Grant/Membership 状态拒绝私有读，终止受控会话/下载授权；无法使第三方已持有的离线副本失效。

### 9.2 可观测性状态

`BLOCK_ACTIVE`：控制平面主库已记录并在受控源站拒绝；`EDGE_CONFIRMED`：在规定检测范围内已收到并验证边缘阻断/清理；`UNKNOWN`：外部缓存或配置传播不可确认；`AT_RISK`：已知仍有可见性缺口。**不定义 `GLOBAL_ERASED` 这类无法证明的状态。** 恢复发布需要明确的安全复核、Slot Generation CAS、有效审批及最小审计链。

## 10. 不可信契约、外部引用及内容解析安全

输入包括 `.yaml/.json`、Markdown、HTML、示例、图像附件、API Links、归档包、OpenAPI `$ref`、AsyncAPI 外部引用、Git URL 和重定向。**所有输入按不可信内容处理**，即使来自内部代码仓库也不放宽隔离。

<figure class="architecture-figure"><div class="fig-label">FIGURE 07 · 不可信输入的处理链</div><div class="fig-grid cols-3"><div class="fig-box"><b>1. Intake</b><small>固定大小/格式限制 · 允许来源 · 关联主体</small></div><div class="fig-box"><b>2. Resolve</b><small>默认禁外网 · 封闭 Ref Closure · DNS/IP 重验</small></div><div class="fig-box"><b>3. Parse</b><small>安全解析器 · 深度/循环/CPU/内存预算</small></div></div><div class="fig-arrow">↓ 不能动态执行输入的脚本/模板或不受控网络请求</div><div class="fig-grid cols-3"><div class="fig-box"><b>4. Inspect</b><small>Schema / Secrets / URLs / 敏感字段</small></div><div class="fig-box"><b>5. Render</b><small>HTML 清洗 / 严格 CSP / 外链政策</small></div><div class="fig-box tone-green"><b>6. Publish Gate</b><small>规则固定摘要 · 独立批准 · 审计制品</small></div></div><figcaption>F04 `validate_f04.py` 的正/负样例是有限的示范 Lint，不具备完整 SSRF 防护或恶意输入解析认证。</figcaption></figure>

| 攻击路径 | 必须设计的防线 | 失败行为 |
|---|---|---|
| `$ref` 引向内部 IP、云元数据地址、回环、Link-local | 默认只允许同提交制品内本地引用；特殊远端引用需明确白名单与隔离拉取器 | 直接拒绝，不去访问 |
| DNS Rebinding / Redirect | 逐跳解析、解析后 IP 分级、禁止内网与敏感网段、目的端重新校验 | 发现任一步骤不符合立即中止 |
| `file://`、`gopher://`、UNC、路径穿越 | 只支持批准协议与根目录、规范化路径、阻止 symlink escape | 拒绝并审计 |
| YAML Anchors/Recursive Schema、指数引用展开 | 深度、节点数、引用次数、CPU 时间与内存硬限额，隔离执行 | 终止 Worker，标记安全失败 |
| Zip bomb / nested archive | 解压大小、文件数、压缩比与嵌套层级配额，归档路径验证 | 拒绝/隔离，不部分发布 |
| 说明文档 XSS/脚本 | Markdown → 可信 HTML 清洗；严格 CSP、禁止未经批准 inline script | 呈现为纯文本或拒绝发布 |
| 示例密钥 / 内部 Host 泄露 | Secret Scan、敏感字段清单、内容审阅与默认禁止公开 | 安全阻断，不允许普通 Waiver 越过 |

输入预算（字节、CPU、节点、递归深度、超时）是**配置参数 TBD**，需通过恶意样例、压测和系统容量设计定值。严格白名单、重定向验证和网络 egress 拒绝应相互独立，不能用一次字符串正则充当完整 SSRF 防御。

## 11. API Explorer 与沙箱执行安全（V1）

Explorer 是高危边界：它可能代表开发者向外部目标发送请求。设计上区分 **Mock**、**受控 Sandbox Proxy** 与 **真实产品 API 请求**，每一次操作必须在 UI 和审计中清晰标明模式。

<figure class="architecture-figure"><div class="fig-label">FIGURE 08 · Explorer 独立执行路径（V1）</div><div class="fig-grid cols-3"><div class="fig-box tone-light"><b>Browser</b><small>明确目标产品 / Env / Mode</small></div><div class="fig-box"><b>Explorer Admission</b><small>身份、Grant、配额、批准的 APIAllowlist</small></div><div class="fig-box"><b>Isolated Egress Proxy</b><small>固定出站网络 · 禁 redirect 绕过 · 无内网访问</small></div></div><div class="fig-arrow">↓ 调用被授权 Sandbox · 或纯 Mock / 无网络</div><div class="fig-grid cols-2"><div class="fig-box tone-green"><b>Sandbox API Endpoint</b><small>Gateway/产品方最终验证真实调用 Scope</small></div><div class="fig-box tone-light"><b>Mock Renderer</b><small>明确标记模拟，不宣称调用已在业务系统执行</small></div></div><figcaption>不能把 Explorer 做成用户可输入任意 URL 的通用 HTTP 代理，也不能将业务 API 令牌写入前端日志或长期存储。</figcaption></figure>

要求按产品配置可信目标源（Scheme、Host、Path、方法、环境）、阻止第三方重定向与 DNS Rebinding、隔离 egress、限制响应大小与执行时间；优先使用短期授权、按用户隔离的会话临时存储；不保留完整请求/响应中的 PII、认证头和秘密。若安全需求无法达到，MVP 仅提供静态 API Reference，V1 Explorer 模块继续保持关闭。

## 12. Git、CI、Webhook、Worker 与外部适配安全

- **Git/CI**：使用安装范围最小化的 App 或服务账户；Webhook 签名、时间窗、防重放和事件 ID 去重；指定仓库、分支与提交摘要绑定，禁止把未经确认的分支名称当成不可变来源。
- **Workers**：不能直接使用控制平面的全局 DBA 或平台管理员权限；使用按作业类型/范围隔离的凭证、Lease/Fencing Token 与资源预算；导入输入沙箱与发布任务隔离。
- **外部 IAM/Gateway**：通过 Ports/ACL 集成；断连时真实凭证与业务调用状态标识 `UNKNOWN/STALE`；不得为了体验把过期授权当作有效。
- **SSRF and egress**：Git/文件拉取器和 Explorer 不共用不受限网络出站；配置 DNS、网段、代理与审计。CI 不能以“构建阶段”绕过正式 Publication 规则。
- **策略来源**：对规则和部署制品进行固定 digest、签名/受控来源校验；第三方政策更新后原审批不能自动沿用。

## 13. 加密、密钥及凭证生命周期

生产凭证以引用方式使用受管理 Secret Vault/KMS/云服务；应用数据库只持有最小元数据，密钥访问使用工作负载身份与用途隔离。TLS 必须覆盖公开入口和敏感内部链路；存储加密、备份加密、对象加密、密钥轮换和应急吊销应统一设计。

**关键点**：JWT 签名公钥缓存有受控轮换窗口；服务账户密钥不能写入仓库；凭证日志脱敏是默认处理；泄露要能够定位关联 Application/Connector 并触发撤销。KMS 失联时新的敏感操作拒绝；不可承诺所有加密历史对象会自动失效，须定义数据密钥与主密钥的实际作用关系。

## 14. 审计、防抵赖与证据完整性

核心审计事实（参考 F03 INV-13）：`actor_ref`、`impersonation_ref`（如有）、`product_id`、`resource_ref`、`action`、`policy_digest`、`decision_id`、`before/after generation`、`timestamp`、`result`、`correlation_id` 和最小安全标签。**业务事件成功提交时的审计与 Outbox 建议同事务**；外部日志系统采用追加/导出留存与完整性保护，不能把“数据库不开放普通修改权限”误写成物理不可篡改。

| 证据类型 | 最小保留内容 | 安全界限 |
|---|---|---|
| 授权拒绝 | 稳定主体引用、资源范围、安全规则 ID、结果 | 不记录 Access Token、密码或详细私有对象名称 |
| Release 审核 | Revision/Policy/Check Digest、Reviewer、风险/有效期 | 审批历史不可因新批准而静默覆盖 |
| Publication / AccessBlock | Slot/Target、Generation、原因与结果、下游传播状态 | `EDGE_CONFIRMED` 与主库提交必须分开 |
| 凭证操作 | ConnectorRef、事件、有效状态、密钥版本引用 | 绝不留存真实 Client Secret |
| 导出与访问记录 | 目的、范围、下载主体、保留期与审批 | 下载链接短期有效、审计导出同样需授权 |

<figure class="architecture-figure"><div class="fig-label">FIGURE 09 · 审计证据分层</div><div class="fig-grid cols-3"><div class="fig-box"><b>Business Decision</b><small>Subject / Resource / Policy / Outcome</small></div><div class="fig-box"><b>Transactional Evidence</b><small>AuditRef + Outbox + DB Commit</small></div><div class="fig-box"><b>Independent Evidence Store</b><small>完整性校验 · 保留 · 隔离访问</small></div></div><div class="fig-arrow">↓ 跨系统 Correlation ID · 不把缺少外部 ACK 写成成功证明</div><div class="fig-grid cols-2"><div class="fig-box tone-green"><b>Reviewable</b><small>按角色、范围、目的最小权限查看</small></div><div class="fig-box tone-light"><b>Retention / Deletion Case</b><small>隐私保留期限与法定留存冲突需授权裁决</small></div></div><figcaption>这是设计期审计链；完整性证明依赖后续部署配置和实际测试，不能从模式图认定审计防篡改已生效。</figcaption></figure>

## 15. 隐私、数据保留、区域合规与删除

最小化采集开发者姓名、邮箱、组织关联和使用数据；按目的隔离、控制访问，示例中禁止真实客户运单与个人数据。运营日志保留期限、审计期限、敏感数据删除/归档策略由法律和安全责任人批准，均为 `TBD`。不能为便捷支持而长期保存 Explorer 的完整输入/输出。

数据主体删除请求、不可变审计、历史制品和法律留存可能存在冲突。设计应区分：删除可识别个人资料、去标识化证据、依法必须保留的最小审计事实、可公开历史文档等处理路径；需要审查和记录适用依据，而不是直接删除不可变 Release 或无条件拒绝删除。

境内/跨境监管场景下，通过产品部署区域与实际数据流建立适用性矩阵；本阶段不假定所有用户位于同一法域，也不宣称符合 GDPR/PIPL 等法律要求。跨境数据流与第三方处理方审批由 F08 组织正式评审。

## 16. 安全运营、事件响应与恢复

定义事件分类（敏感契约公开、越权访问、令牌泄露、权限漂移、恶意契约解析、审计不可用、数据完整性异常）和统一处理：**检测 → 可信报警 → 先阻断 → 保存证据 → 评估影响 → 恢复/轮换 → 复核与回归**。

<figure class="architecture-figure"><div class="fig-label">FIGURE 10 · 安全事故响应与可靠恢复</div><div class="fig-grid cols-3"><div class="fig-box tone-light"><b>Detect / Triage</b><small>Audit · Alert · Operator report</small></div><div class="fig-box"><b>Contain</b><small>AccessBlock / Session revoke / Disable connector</small></div><div class="fig-box"><b>Preserve</b><small>不可变 Revision/Release 证明 + 最小日志</small></div></div><div class="fig-arrow">↓ 由授权职责执行、记录时间线与外部依赖 ACK</div><div class="fig-grid cols-3"><div class="fig-box"><b>Assess</b><small>受影响产品、受众、数据和窗口</small></div><div class="fig-box"><b>Recover</b><small>安全版本恢复 · 密钥轮换 · 索引重建</small></div><div class="fig-box tone-green"><b>Verify & Learn</b><small>负向回归 · 负责人签认 · 风险整改</small></div></div><figcaption>RTO/RPO、告警响应时间、阻断传播时间均待具名批准，图不代表现有事故响应系统已达到这些指标。</figcaption></figure>

**备份与恢复**：加密备份可恢复主库、不可变 Registry/Manifest、策略和审计；搜索索引可从权威源重建；恢复时必须先载入最近的 AccessBlock/撤销事实，再打开受限门户访问。不能因为旧快照里的 Slot 指向旧 Release，就恢复为未经授权的可见状态。测试要求断网、备份失效、秘钥丢失和索引重建顺序场景，具体 SLO / RPO / RTO 在 F07/F08 批准。

## 17. 安全供应链及开发交付门禁

安全依赖、构建来源、SBOM、可重复构建可能性、签名、依赖漏洞、容器扫描、机密扫描、SAST/DAST、配置检查和 CI 身份最小权限均由 F07 建立执行流程。**静态检查器不能等同完整渗透测试**；F05 的威胁登记册应驱动重点红队/安全用例。涉及漏洞引入或供应链事件时，必须有安全补丁、隔离、复测和回滚机制。

第三方文档渲染器和 API Explorer 依赖也属于供应链攻击面；升级时要检验 CSP、HTML 清洗、网络代理和权限过滤行为不退化。外包/开源许可与数据处理协议应具备供应商级审核记录。

## 18. 关键安全质量指标和待定阈值

| 指标 / SLI | 必须具备的定义 | 状态 |
|---|---|---|
| Cross-product authorization | 跨产品负例必须被所有读/写/搜索/下载路径拒绝，含直链和缓存 | 验收用例已设计，执行 NOT_RUN |
| Release integrity | 正式制品 digest 与 manifest 校验、审批绑定内容和策略版本 | 设计完成，真实加密/存储验证 NOT_RUN |
| Revocation latency | 主库 Block commit、源站阻断、CDN/Edge ACK 分别测量；目标值 TBD | 未批准指标 |
| Token/session revocation | Session TTL、令牌刷新、成员变更与权限失效之间的最大窗口 | TBD，需 IAM 联调 |
| Sensitive content leakage | 公共扫描假阳性/漏报、授权搜索及日志泄露测试 | NOT_RUN |
| SSRF / parser limits | URL、网段、重定向、CPU、内存、压缩展开预算 | TBD，恶意输入试验 NOT_RUN |
| Audit completeness | 核心操作存在审计链，失败操作有足够且最小化的证据 | NOT_RUN |
| Restore & fail-closed | 备份恢复后不复活被阻断的内容，外部依赖不可用时拒绝高风险动作 | NOT_RUN |

**不允许编造** SLA、RPO、RTO、撤销传播秒数、吞吐量或“零泄露”指标。要先由产品/安全/运维 Owner 定义环境、规模、预算和测试方法，再形成批准值。

## 19. CargoPai 贯通案例：发布、撤销与跨产品拒绝

所有下列 ID 仅为 `DESIGN_EXAMPLE`：`prod:cargopai`、`workspace:integrations`、`asset:shipment-api`、`release:R-0007`、`target:partner-sandbox`、`slot:shipment-api`；不是已部署资源。

**正常路径**：企业员工从 IdP 登录 → Workspace Membership 校验 → 固定 Revision 与策略摘要检查 → 独立 Reviewer 审批 → Publisher 对 `target×asset` CAS 发布 → 外部开发者组织取得对应 Product/Audience/Environment Grant → 受控 BFF 再核对当前 Slot/Block 并输出 Reference。外部 OAuth 对真实业务 API 的调用由产品 Gateway 单独判断。

**跨产品负例**：即使开发者使用相同邮箱注册了 IDP Parcel，只要缺少 CargoPai 对应 Grant，搜索、详情、Release 历史、预签名下载和 Portal 页面都拒绝；Gateway 的业务 Token 也不能被 Fabric 自动当作文档授权证明。

**错误发布负例**：安全管理员按经批准的应急机制对 `prod:cargopai / partner-sandbox / shipment-api` 建立 `AccessBlock`；读侧先拒绝，再 CAS 回退指针，触发 CDN purge、收集 Edge ACK。未知的边缘传播保持 `UNKNOWN/AT_RISK`，不是完成证明；历史 Release 保持不可变供取证。不同 Asset（如 Tracking API）不被此 Slot 回退误伤，除非安全 Block 明确以 Target 为作用域。

## 20. 机器可读安全资产与验证计划

| 资产 | 精确位置 | 说明 |
|---|---|---|
| 授权策略草案 | `governance/policies/F05_Authorization_Policy_DRAFT.json` | 候选条件语义，不是已启用的 PDP |
| 授权矩阵 | `governance/policies/F05_Access_Matrix.csv` | 每个资源与操作的 PEP、条件、拒绝策略 |
| STRIDE 威胁登记册 | `governance/policies/F05_Threat_Register.csv` | 风险、攻击面、缓解、Owner/TBD、测试引用 |
| 安全负向场景 | `tests/acceptance/F05_security_scenarios.csv` | 每项 Scenario ID、预期拒绝行为，全部 `NOT_RUN` |
| 需求追溯 | `tests/acceptance/F05_security_trace.csv` | F01 全部 71 FR + 18 NFR 的直接/间接承接关系 |
| 静态一致性验证器 | `scripts/validate_f05.py` | 检查编号、引用、必填值和文件结构，不执行渗透测试 |

**本地 `validate_f05.py` PASS 仅表示文件格式、关键设计 ID 和跨文件引用一致。** 真实安全测试执行环境、测试身份、授权状态、接口行为、CDN/Edge 与 IAM/Gateway 结果归 F07/F08。所有验收场景初始 `NOT_RUN`，不能静默改为 PASS。

### 20.1 安全场景测试最低覆盖

测试必须至少包括以下类别：跨 Product/Workspace/DeveloperOrg 的 BOLA/BFLA、匿名访问私有 Release、直接对象 URL、搜索与缓存泄露、过期批准和多发布者 CAS、撤销并发、不可达 IAM/Policy/Edge、旧 Session、JWT `iss/aud/alg` 错配、CSRF/CORS、恶意 `$ref`、DNS Rebinding、压缩炸弹、XSS、Explorer 任意 URL、Webhook 重放、Worker 越权、审计导出、恢复后旧 Block 重新生效。每项有明确“预期拒绝/不泄露”断言。

## 21. 与 F01–F04 的规范、模型与需求追溯

| 上游对象 | F05 承接 | 设计证据与待执行验证 |
|---|---|---|
| F01 FR-001–006、FR-063、NFR-01/02 | Product/Workspace/DeveloperOrg Scope；默认拒绝 | §5–7；Matrix；SEC-01… |
| F01 FR-013–018、FR-025、NFR-18 | 不可信契约、私有制品、防 SSRF/敏感数据泄漏 | §8/10；TH Register；SEC-… |
| F01 FR-027–036 | 审查职责分离、PublicationSlot、AccessBlock、撤销 | §6–9；SEC-… |
| F01 FR-047–055、FR-053 | 双门户身份和文档访问、开发者组织和 Explorer | §4/6/11；SEC-… |
| F01 FR-062–068、NFR-09/16/17 | 审计、备份、保留、安全事件与供应链 | §12–18；SEC-… |
| F02 AI-01…07、ADR-0001…06 | 联邦治理、分布式信任与外部适配，不改动提案状态 | §0/1/2/5/8/9/12 |
| F03 INV-01…13、OD-01…10 | 聚合范围、CAS、Deny、审计与恢复作为安全边界 | §5/7/9/14/16/19 |
| F04-GOV-001…028 | 发布检查、扫描、豁免、策略和制品分发 | §9–10/17/20 |
| 上位 IDP-SCG-STD-001 | CORE/TECH 规则、独立复核、Profile 适用性与 STD-CP1…CP6 | 项目 Manifest 仍 DRAFT；由 F08 收集具名签收 |

`F05_security_trace.csv` 按 `DIRECT_SECURITY / SECURITY_SUPPORT / NEXT_STAGE` 区分责任，不把所有 89 条功能需求生硬声明为“F05 单独实现”。对 `FR-069…071` AI 能力仍为 V2 后续独立威胁评审，不以 F05 自动授权工具执行。

## 22. 未决决策、残余风险与下一阶段接口

| ID | 需要确定的决策 | 建议 Owner | 不可跨越的执行门槛 |
|---|---|---|---|
| SD-01 | IdP、可信 Issuer、员工/开发者身份域及 MFA/step-up 强度 | Identity/Security Lead TBD | F07 实测 token/cookie/session 行为 |
| SD-02 | Policy Engine/PDP 模型与语义实现：自研简化策略或成熟适配器 | Security + Architecture TBD | F07 负向授权测试和性能测试 |
| SD-03 | 私有文档对象交付：BFF Proxy 或短时签名 URL | Security + Platform TBD | 含撤销/缓存/对象直链测试 |
| SD-04 | CDN/Edge 托管范围及传播证明口径 | Security + Operations TBD | 不能把主库 Block 当全球缓存清除 |
| SD-05 | Parser/Worker/Explorer 的网络与容器隔离参数 | Platform Security TBD | 恶意输入/SSRF 试验与资源预算 |
| SD-06 | 数据保留、地域部署、跨境数据与审计导出 | Legal/Privacy + Product TBD | 具名适用性评估 |
| SD-07 | RPO/RTO、撤销时间、故障响应与保留策略 | Product + SRE + Security TBD | F07/F08 测试与批准值 |
| SD-08 | ADR-0004 PublicationSlot、安全阻断模型的正式批准 | Architecture Board TBD | 审核及具名签收 |
| SD-09 | Break-glass、独立复核、风险接受与安全例外授权 | Security + Risk Owner TBD | 必须留痕、复核且不能越过硬性安全门禁 |

**F06（双门户）承接**：独立登录与组织切换、内部审批与事故界面、外部授权状态/未知状态、版本和可见性、Explorer 安全提示、错误空态及可访问性。**F07（工程）承接**：Go PEP/PDP、PostgreSQL RLS 适用性、Session/CSP、Worker Sandbox、Vault、CDN 验证、可观测性、备份与真实测试。**F08（验收）承接**：全部 Scenario 的真实验证、责任人批准、R1/R2/R3 风险复核及六道门禁。

## 23. 参考依据与采用边界

1. OWASP ASVS 5.0.0 (2025-05-30)：https://asvs.owasp.org/ ；用于形成安全控制验证要求，不是合规证书。
2. OWASP API Security Top 10 (2023)：https://owasp.org/API-Security/editions/2023/en/0x11-t10/ ；重点 BOLA、BFLA、SSRF、资源消耗及 API 资产可见性。
3. IETF RFC 9700 (2025)：https://www.rfc-editor.org/rfc/rfc9700.html ；OAuth 2.0 Security Best Current Practice。
4. IETF RFC 8725：https://www.rfc-editor.org/rfc/rfc8725.html ；JWT Best Current Practices。
5. NIST SP 800-63-4 (2025)：https://csrc.nist.gov/pubs/sp/800/63/4/final ；身份验证与联邦参考框架。
6. NIST SP 800-218 (SSDF)：https://csrc.nist.gov/pubs/sp/800/218/final ；安全软件开发框架。
7. IDP-SCG-STD-001：本工程现有上位规范，具名项目采用仍待批准；对其特定规则 ID 的映射是设计期判断。

**文档完成定义**：当且仅当本文、矩阵/风险/验收/追溯资产、HTML、目录和 ZIP 的静态一致性检查成功，可描述为“F05 设计候选交付完成”；真实部署、渗透测试、审计完整性、传播时效、恢复演练与正式标准六道门禁仍需后续实际证据，不得标为 PASS。
