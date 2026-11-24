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

### 6.1 已实现的产品负责人和列表权限（PR #6 / #7，2026-11-17 同步）

身份由配置的 IdP 的 issuer×subject 确认，角色和成员关系逐次从数据库读取。管理 API 的平台管理员由部署配置指定，授权范围见下表及 §6.3。完整操作级设计矩阵见 `governance/policies/F05_Access_Matrix.csv`。AC-36…43 是本节的设计索引，状态为 PROPOSED。运行时审计使用 AC-08、AC-10、DENY-03。

| 操作 | 允许范围 | 拒绝与边界 | 运行时审计 |
|---|---|---|---|
| `POST …/role-assignments`（AC-36/37） | 产品负责人、平台管理员；产品 ACTIVE，主体签发方受信任 | 管理员可把自己加成负责人，保留授予证据；其余已知产品的成员 403，陌生人 404 `PRODUCT_NOT_FOUND` | 成功：负责人 AC-10、管理员 AC-08，`product_role.grant`；拒绝 DENY-03 |
| `GET …/role-assignments/{id}`（AC-38） | 该产品负责人 | 管理员/成员得到 404 `ROLE_ASSIGNMENT_NOT_FOUND`；陌生人 404 `PRODUCT_NOT_FOUND`；跨产品与已撤销分配不可读 | 成功不记；拒绝 DENY-03、`product_role.read` |
| `DELETE …/role-assignments/{id}`（AC-39） | 该产品负责人，包括尚有其他负责人时撤销自己 | 管理员不能撤销；已知产品的非负责人 403，陌生人 404；最后一位负责人返回 409 `LAST_OWNER`；已撤销分配 404 | 成功 AC-10、`product_role.revoke`；授权拒绝 DENY-03；状态冲突不算授权拒绝 |
| `GET /v1/products`（AC-40） | 管理员看全部；其他已认证主体仅看持有有效角色/成员关系的产品 | 没有可见产品时是空列表，不泄露总数 | 成功不记，无对象级拒绝 |
| `GET …/workspaces`（AC-41） | 负责人看全部；成员仅看自己所属 Workspace | 管理员无产品角色时 403；陌生人 404 `PRODUCT_NOT_FOUND` | 成功不记；拒绝 DENY-03、`workspace.read` |
| `GET …/workspaces/{id}/memberships`（AC-42） | 该产品负责人 | 本 Workspace 成员 403；管理员/其他 Workspace 成员 404 `WORKSPACE_NOT_FOUND`；陌生人 404 `PRODUCT_NOT_FOUND` | 成功不记；拒绝 DENY-03、`member.read` |
| `GET …/role-assignments`（AC-43） | 该产品负责人 | 管理员/成员 403，陌生人 404 `PRODUCT_NOT_FOUND` | 成功不记；拒绝 DENY-03、`product_role.read` |

表中的省略前缀是 `/v1/products/{product_id}`。授予、撤销的分配事实、成功审计和 Outbox 同事务提交；授权拒绝单独留证，审计失败仍拒绝并记录运维错误。重复授予返回 409 `ROLE_ASSIGNMENT_EXISTS`；重复撤销返回 404，不再产生成功事件。暂停产品拒绝新增负责人但允许撤销；暂停/停用的管理接口尚未实现。

负责人列表已补齐“管理员修正登记时填错的负责人”的闭环：管理员新增正确负责人，正确负责人列出分配并撤销错误分配，全程无需查数据库。撤销后使用原 JWT 的下一页请求立即重新授权，令牌不能维持已撤销的权限。平台管理员自我授予目前没有双人审批或专门告警，这些尚未实现，不应从此矩阵推导具备这些能力。数据库并发、迁移与旧进程下线约束见 F03 §5.5；分页格式、过滤和参数校验见 F04 §3.1.1。

负载记录见[四类列表压测](../tests/performance/README.md)，功能回归覆盖在 `cmd/fabric-api/slice_test.go`、`lists_test.go` 及各层测试中。该证据使用真实 PostgreSQL 与本地替代 IdP，不是生产 IAM 联调、渗透测试或 SEC-01…36 的正式验收，原设计验收状态保持 NOT_RUN。

### 6.2 其余双门户角色设计

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

### 6.3 已实现的受众配置和产品品牌权限（PR #10 / #11）

平台管理员、当前产品负责人和该产品任一 Workspace 的有效成员可读取产品及品牌，也可读取该产品的有效受众配置。产品缺失或调用方无权读取产品时，返回 404 `PRODUCT_NOT_FOUND`。下表路径以 `/v1/products/{product_id}` 开头。

| 操作 | 允许范围与状态条件 | 成功写入的审计动作 |
|---|---|---|
| `POST …/audience-configs`（AC-44） | 该产品负责人，产品为 ACTIVE，且受众与环境组合可用 | `audience_config.configure` |
| `GET …/audience-configs/{id}`（AC-45） | 可读该产品，且配置在该产品内有效 | — |
| `GET …/audience-configs`（AC-46） | 可读该产品，只返回有效配置 | — |
| `PUT …/audience-configs/{id}`（AC-47） | 该产品负责人，产品为 ACTIVE，且该产品内的配置有效、版本匹配 | `audience_config.change` |
| `DELETE …/audience-configs/{id}`（AC-48） | 该产品负责人，且该产品内的配置有效，暂停时仍可移除 | `audience_config.remove` |
| `PUT …/brand`（AC-49） | 该产品负责人，产品为 ACTIVE，且产品版本匹配 | `product.change_brand` |

可读该产品的调用方若缺少负责人角色，写请求返回 403 `FORBIDDEN`，包括仅持有平台管理员身份的调用方。按配置 ID 操作时，完成授权后若配置缺失、已移除或属于其他产品，返回 404 `AUDIENCE_CONFIG_NOT_FOUND`。受众取值为 `public` 仅记录目标受众，公共文档仍须通过发布授权。

写操作在事务内读取并锁定调用方当前的角色分配。实际写入的审计规则为 `AC-09`，审计与数据和 Outbox 同事务提交。授权拒绝使用 `DENY-03`，受众详情和列表的拒绝动作均为 `audience_config.read`。AC-44…49 是设计矩阵索引，状态为 PROPOSED。配置事件只含标识、受众、环境和生效状态等字段，品牌事件只含通用事件字段。文档入口与品牌内容通过受授权的详情接口读取。

功能回归见 `cmd/fabric-api/audienceconfig_test.go` 和 `cmd/fabric-api/brand_test.go`，涵盖权限及真实 PostgreSQL 下的负责人撤销并发场景。SEC-01…36 的正式安全验收状态为 NOT_RUN。

### 6.4 已实现的 API 目录和契约修订权限

本节对应 PR #12、#14–#17 以及随后的 Git 来源和原文读取。下表路径以 `/v1/products/{product_id}` 开头。每次请求都从数据库读取当时有效的角色和成员关系。“能看到资产”指调用方是该产品的负责人，或在资产所属的 Workspace 持有任一有效成员关系。这些操作对应设计矩阵里已有的 AC-01、AC-02、AC-11、AC-13、AC-14，本次没有新增矩阵条目；矩阵状态仍为 PROPOSED。

| 操作 | 允许范围与状态条件 | 拒绝 | 运行时审计 |
|---|---|---|---|
| `POST …/assets`（AC-11） | 在目标 Workspace 持有有效的 `workspace_maintainer` 成员关系，产品为 ACTIVE | 不知道该产品的人 404 `PRODUCT_NOT_FOUND`；Workspace 不存在或调用方看不到它，404 `WORKSPACE_NOT_FOUND`；看得到但不是维护者（包括产品负责人）403 | 成功 AC-11、`asset.create`；拒绝 DENY-03、`asset.create` |
| `GET …/assets/{id}`（AC-14） | 能看到资产 | 知道产品但看不到该资产（包括平台管理员和其他 Workspace 的成员）404 `API_ASSET_NOT_FOUND`；不知道产品 404 `PRODUCT_NOT_FOUND` | 成功不记；拒绝 DENY-03、`asset.read` |
| `GET …/assets`（AC-13） | 负责人看到全部；成员只看到自己所在 Workspace 的资产；过滤和文本匹配在授权过滤之后 | 没有产品角色和成员关系的平台管理员 403；不知道产品 404 | 成功不记；拒绝 DENY-03、`asset.search` |
| `POST …/assets/{id}/revisions`（AC-01） | 能看到资产，并在资产所属的 Workspace 持有有效的 `internal_author` 成员关系；产品为 ACTIVE，资产为 `HTTP` | 看不到资产 404；看得到但不是作者（包括负责人和维护者）403 | 成功 AC-01、`revision.create`；403 记 DENY-03、`revision.create`；404 由目录记 DENY-03、`asset.read` |
| `GET …/revisions`、`GET …/revisions/{id}`、`GET …/revisions/{id}/source`（AC-02） | 能看到资产，并在资产所属的 Workspace 持有有效的 `internal_author` 或 `internal_reviewer` 成员关系（评审人自 0.17.0 起，见 §6.6），0.24.0 起还有 `internal_publisher`（§6.8）；产品暂停时仍可读 | 同上；修订不属于该资产或不存在，404 `REVISION_NOT_FOUND` | 成功不记；403 记 DENY-03、`revision.read`；404 `REVISION_NOT_FOUND` 不记 |

导入时，作者的成员关系在事务内加共享锁后读取：撤销先提交，则导入被拒绝；导入先提交，则修订留下。登记资产对维护者成员关系的处理相同。修订、原文、成功审计和事件同事务提交；授权拒绝单独留证，审计失败仍然拒绝并记录运维错误。输入不合法（400、422）和状态冲突（409，包括 `GIT_ORIGIN_CONFLICT`）不是授权拒绝，不写审计。

**读取原文的权限就是读取修订的权限。** 原文可能是尚未评审的草稿，资产所属 Workspace 的作者和评审人能读，0.24.0 起发布人也能读（§6.8）；产品负责人、维护者和平台管理员不能读修订，也就不能读原文。无权读取的人对原文接口得到的回答，与对修订详情接口得到的回答相同。成功读取不写审计，这一点与其他读取一致。AC-16 `contract.download` 指的是已发布 Release 的制品下载，不是这里的草稿原文。

**Git 来源不带来任何权限。** 带 `git_origin` 的导入与普通上传走同一条规则（AC-01），提交者仍须是该 Workspace 的作者。来源是提交者的声明，Fabric 不向仓库核实，所以它不能当作“内容确实来自某个提交”的安全依据；审计记录的是谁提交了这次导入。设计矩阵里的 AC-26（CI 服务身份提交，要求 `source.allowed`）和 AC-27 尚未实现：现在没有 CI 服务身份，也没有“资产允许哪些仓库”的登记。仓库网址只接受不含凭证的 https 形式，带凭证的请求在进入用例前被拒绝，不进入数据库、审计、事件和日志。

**不可信内容。** 导入的文件按 §10 的处理链只做到了前两步的一部分：限定大小、数量和格式，只在提交的文件之内解析引用，不访问文件系统和网络，拒绝远程引用、越界路径、循环和 YAML 别名。原文读取把文件作为 Base64 放在 JSON 里返回，不以文件自身的媒体类型提供，也不渲染。对内容的规则检查见 §6.5，评审见 §6.6。

**原文完整性。** 应用角色对来源快照和文件两张表只有 `SELECT` 和 `INSERT`，不能改写或删除已登记的原文。对修订表，评审加入后多了 `UPDATE`，表上的触发器只允许它改评审状态，修订指向哪份原文不能改（F03 §5.9）。读取时重新计算摘要，可发现存储层丢失或改动了文件；摘要与文件同库，这不是防篡改证明（见 F03 §5.7）。核对失败时不返回文件，响应不透露原因，原因写入运维日志。

功能回归见 `cmd/fabric-api/catalog_test.go`、`registry_test.go`、`revisionhistory_test.go`、`gitorigin_test.go` 和 `revisionsource_test.go`，使用真实 PostgreSQL 和本地替代 IdP。SEC-01…36 的正式安全验收状态为 NOT_RUN。

### 6.5 已实现的契约检查权限

本节对应 PR #22–#33。下表路径以 `/v1/products/{product_id}/assets/{api_asset_id}/revisions/{revision_id}` 开头。四个操作都以“能读这个修订”为前提，也就是 §6.4 的 AC-02：调用方能看到资产，并在资产所属的 Workspace 持有有效的 `internal_author` 或 `internal_reviewer` 成员关系（评审人自 0.17.0 起），0.24.0 起还有 `internal_publisher`（§6.8）。发起检查还要求是作者。每次请求都从数据库读取当时有效的成员关系；分页令牌和以前拿到的检查编号不带来任何权限。

| 操作 | 允许范围与状态条件 | 拒绝 | 运行时审计 |
|---|---|---|---|
| `POST …/check-runs` | 能读修订，并且是作者；产品为 ACTIVE | 看不到资产 404；看得到但读不了修订（包括负责人和维护者）403；能读但不是作者（评审人，0.24.0 起还有发布人，§6.8）403；产品已暂停 409 `PRODUCT_NOT_ACTIVE` | 记录了新的检查：AC-02、`revision.check`，资源类型 `CheckRun`；返回已有的检查不记；拒绝见下 |
| `GET …/check-runs`、`GET …/check-runs/{id}` | 能读修订；产品暂停时仍可读 | 同上；检查不属于该修订或不存在，404 `CHECK_RUN_NOT_FOUND` | 成功不记；拒绝见下 |
| `GET …/compatibility` | 能读候选和基线两个修订，两者属于同一资产，所以是同一条规则；产品暂停时仍可比较 | 同上；基线不属于该资产或不存在，404 `REVISION_NOT_FOUND` | 成功不记；拒绝见下 |

**拒绝留下的审计。** 这四个操作都先向 Contract Registry 读修订，无权的调用方在这一步就被拒绝，留下的审计与读修订被拒相同：403 记 DENY-03、`revision.read`，资源类型 `ContractRevision`；看不到资产的 404 由目录记 DENY-03、`asset.read`。有两种情况记在 `revision.check` 名下：发起检查的人能读修订但不是作者（评审人，0.24.0 起还有发布人）；或者他通过了读取，但写入事务里加锁重读成员关系时，作者关系已经被撤销。这时返回 403，审计为 DENY-03、`revision.check`，资源类型 `CheckRun`，不留下检查。撤销先提交，检查被拒绝；检查先提交，检查留下。输入不合法（400）、找不到（404 `REVISION_NOT_FOUND`、`CHECK_RUN_NOT_FOUND`）和产品暂停（409）不是授权拒绝，不写审计。

**矩阵里没有对应的条目。** 设计矩阵没有“内部作者发起检查”和“内部作者读取检查”这两行；AC-27 `ci.check.read` 说的是 CI 服务身份读取检查，尚未实现。实现把成功的检查记在 AC-02 名下，而矩阵里 AC-02 的动作是 `revision.read`，审计里的动作名是 `revision.check`。本次没有新增矩阵条目，矩阵状态仍为 PROPOSED；要不要为检查单列条目，待裁决。

**检查不是许可。** 发起检查的人是修订所在 Workspace 的作者，检查由程序完成，不是评审。检查的结论本身不批准任何东西；提交评审和批准以一次检查为依据，职责分离由评审操作执行（§6.6）。独立评审这条规则（F04-GOV-025）没有工具，在每次检查里仍是 `NOT_RUN`。作者和评审人能读检查，0.24.0 起发布人也能读（§6.8）；产品负责人、维护者和平台管理员读不到规则结论和发现。

**发现不成为凭证的第二个存放处。** F04-GOV-004 和 F04-GOV-010 的发现只给出文件、行号和种类，说明里不复述命中的凭证或地址；检查记录、审计、事件和日志里因此不会出现它们。凭证本身仍在修订的原文里，而原文不可变：发现之后应当作废那个凭证，再提交不含它的新修订。这两条规则按已知的形式匹配，`PASS` 不证明内容里没有凭证或内网地址。

**不可信内容。** 检查只读 Contract Registry 登记的文件，不因为任何引用去打开文件或网址。解析有节点数、嵌套深度和引用深度的上限，超出时相关规则为 `UNKNOWN`。官方 OpenAPI Schema 内嵌在程序里，校验器不加载外部资源；过长或指数过大的数值不交给校验器。§10 处理链的第 4 步（Inspect）由此做了一部分：Schema、凭证和内网地址。敏感字段清单、第 5 步的渲染清理（F04-GOV-017 为 `NOT_RUN`）和第 6 步的发布门禁尚未实现。

**检查记录的完整性。** 应用角色对四张表只有 `SELECT` 和 `INSERT`，不能改写或删除已记录的检查。读取时核对各行是否自洽、是否仍对应那个修订（F03 §5.8）；不符时不返回检查，响应不透露原因，原因写入运维日志。数据与摘要同库，这不是防篡改证明。

功能回归见 `cmd/fabric-api/checks_test.go`、`checkhistory_test.go`、`compatibility_test.go` 和 `comparison_check_test.go`，使用真实 PostgreSQL 和本地替代 IdP，其中包括作者在检查等待期间被撤销，以及事件写入失败时全部证据回滚。SEC-01…36 的正式安全验收状态为 NOT_RUN。

### 6.6 已实现的契约评审权限

本节对应管理 API 0.17.0–0.19.0。下表路径以 `/v1/products/{product_id}/assets/{api_asset_id}/revisions/{revision_id}` 开头。评审人是 Workspace 的成员关系 `internal_reviewer`，由产品负责人授予和撤销，与其他成员关系走同一条规则（AC-10）；一个主体可以在同一个 Workspace 同时是作者和评审人。每次请求都从数据库读取当时有效的成员关系。

| 操作 | 允许范围与状态条件 | 拒绝 | 运行时审计 |
|---|---|---|---|
| 读修订、原文、检查和比较（AC-02、AC-53） | 作者或评审人（§6.4、§6.5），0.24.0 起还有发布人（§6.8）；产品暂停时仍可读 | 见 §6.4、§6.5 | 成功不记 |
| `POST …/reviews`（AC-50） | 能读修订，并且是作者；产品为 ACTIVE；修订是 `DRAFT` 或 `WITHDRAWN`，0.23.0 起也可以是 `APPROVED`（复审，§6.8） | 看不到资产 404；看得到但读不了修订（包括负责人和维护者）403；能读但不是作者（评审人，0.24.0 起还有发布人）403 | 成功 AC-50、`review.request`；不是作者 DENY-03、`review.request` |
| `POST …/reviews/{id}/withdrawal`（AC-51） | 提交这条申请的人，并且仍是作者；产品暂停时仍可撤回 | 不是作者 403；是作者但不是申请人 403 | 成功 AC-51、`review.withdraw`；两种拒绝都是 DENY-03、`review.withdraw` |
| `POST …/reviews/{id}/decision`（AC-03） | 持有有效的评审人关系，并且没有参与被评审的东西；产品为 ACTIVE | 不是评审人 403 `FORBIDDEN`；是评审人但参与过 403 `REVIEWER_NOT_INDEPENDENT` | 成功 AC-03，批准记 `review.approve`，驳回记 `review.reject`；不是评审人 DENY-03；参与过 DENY-06；动作都是所请求的那一个 |
| `GET …/reviews`、`GET …/reviews/{id}`（AC-52） | 能读修订；产品暂停时仍可读 | 同读修订；评审不属于该修订或不存在，404 `REVIEW_NOT_FOUND` | 成功不记 |

审计的资源类型是 `Review`，成功时带评审编号，拒绝时不带。成功的审计与评审记录、修订状态和事件同事务提交；拒绝单独留证，审计失败仍然拒绝并记录运维错误。这些操作都先读修订，所以读不了修订的人（负责人、维护者、看不到资产的人）在那一步被拒绝，留下的审计与读修订被拒相同（DENY-03、`revision.read` 或 `asset.read`），不记在评审的动作名下。输入不合法（400、422）、找不到（404）和状态冲突（409）不是授权拒绝，不写审计。

**职责分离。** 作决定的人不能是下面三个人中的任何一个，批准和驳回都一样：

| 参与方式 | 取自 |
|---|---|
| 登记了这个修订 | 最先提交这份内容的人，即来源快照的采集主体（F03 §5.7） |
| 发起了评审所依据的那次检查 | 检查记录的发起人 |
| 提交了这条评审申请 | 申请记录的申请人 |

持有评审人角色不改变这一点；同时持有作者角色的评审人，对自己没有参与的评审可以作决定。判断在写入事务里进行：先锁住并重读评审人关系，再判断是否参与过。所以撤销先提交，决定被拒绝；决定先提交，决定留下。没有评审人关系又参与过的人，得到的是 403 `FORBIDDEN`，不会被告知职责分离的结论。

这是 AC-35（不得自批准）和威胁登记册 TH-20（作者评审自己的发布）所要求的控制。设计里的 DENY-06 是 `reviewer_is_author_for_high_risk`，只针对高风险；实现不分风险等级，对每一次评审都执行，并把发起检查和提交申请的人也算在内。

它的边界：比较的是主体（签发方加主体标识），同一个人用两个账号，这里发现不了。没有指派：Workspace 的任何一位评审人都可以对任何一条申请作决定，AC-03 条件里的 `review.assignment` 尚未实现。负责人可以在申请提交之后再授予某人评审人角色，授予本身有审计（AC-10、`member.grant`）。评审人在批准之后被撤销，已有的批准记录不变；0.21.0 起使用这份批准会被拒绝（`REVIEWER_AUTH_REVOKED`，§6.7）。

**批准的依据和范围。** 批准绑定那次检查读的内容（`input_digest`）、申请的范围与到期时间。审批、准备 Release 和激活 Slot 时重新核对策略与工具。0.21.0 起，使用批准时还会检查批准人当初的授权是否仍然有效、批准和豁免是否过期，见 §6.7。 [发布接口说明](F04_API_Contract_Governance.md#116-已实现的-release-与发布接口)

**豁免的边界。** 只有登记表里标为可豁免的规则能豁免：基线 23 条里的 4 条质量和兼容性规则。类别是 `SECURITY_MANDATORY` 和 `INTEGRITY_MANDATORY` 的 19 条不能豁免，没有任何角色和接口能绕过，包括平台管理员（FR-028）。这 19 条里有一条没通过，修订就不能提交评审。豁免必须写明理由和到期时间，到期时间不晚于批准的到期时间。豁免只属于它所在的那次批准，不能转给别的修订或别的评审。

`NOT_RUN` 的规则不需要豁免。基线里有 10 条规则没有工具，它们都是不可豁免的安全和完整性规则（发布授权、渲染清理、独立评审、公开泄露扫描等）。所以现在的批准不证明这些规则成立；批准记录指向的检查里写着它们没有评估。它们要在各自的环节执行：职责分离已经由本节的决定操作执行，其余的在 Release 和发布实现时执行。

**文字内容。** 说明、理由和补偿措施是人写的文字，各限一行、有长度上限、不含控制字符。它们只通过评审接口返回，能读修订的人才能读；审计和事件里没有这些文字，事件也不含范围和豁免。Fabric 不渲染它们。

**评审记录的完整性。** 应用角色对四张评审表只有 `SELECT` 和 `INSERT`，不能改写或删除申请、范围、结论和豁免；修订的状态只能沿评审往前走，由表上的触发器保证（F03 §5.9）。读取时核对记录是否自洽、是否仍对应修订现在的内容；不符时不返回，响应不透露原因，原因写入运维日志。数据同库，这不是防篡改证明。

**矩阵。** 本次新增四行设计索引，状态为 PROPOSED：AC-50 `review.request`、AC-51 `review.withdraw`、AC-52 `review.read`、AC-53（评审人的 `revision.read`）。运行时审计使用 AC-50、AC-51、AC-03、DENY-03 和 DENY-06。AC-03 在矩阵里的动作是 `release.approve`，实现记的动作名是 `review.approve` 和 `review.reject`，驳回也记在 AC-03 名下；这一处对应关系待裁决。

功能回归见 `cmd/fabric-api/reviewer_test.go`、`reviews_test.go` 和 `reviewdecision_test.go`，使用真实 PostgreSQL 和本地替代 IdP，其中包括参与过的评审人被拒绝、评审人在决定等待期间被撤销、同时到达的几个决定只有一个成功，以及事件写入失败时结论、豁免和状态变化全部回滚。SEC-10 描述的场景有这些回归覆盖，但 SEC-01…36 的正式安全验收状态仍为 NOT_RUN。

### 6.7 已实现的 Release 与发布权限

本节对应管理 API 0.20.0–0.22.0。下表路径以 `/v1/products/{product_id}` 开头。每次请求都从数据库读取当时有效的角色和成员关系；写操作在事务内对调用人的成员关系或产品角色加共享锁后再判断，撤销先提交则操作被拒绝。运行时审计用的是设计矩阵里已有的 AC-04、AC-05、AC-06、AC-07 和 AC-09，本次没有新增矩阵条目，矩阵状态仍为 PROPOSED。

| 操作 | 允许范围与状态条件 | 拒绝 | 运行时审计 |
|---|---|---|---|
| 授予 `internal_publisher`（Workspace 成员关系） | 产品负责人，与其他成员关系相同 | 与其他成员关系相同 | 与其他成员关系相同 |
| 授予 `security_admin`、`security_reviewer`（产品角色） | 产品负责人。平台管理员只能任命负责人，授予安全角色被拒绝 | 403 | 成功 AC-10、`product_role.grant`；拒绝 DENY-03 |
| `POST …/assets/{id}/releases` | 资产所属 Workspace 的 `internal_publisher`，且没有参与这个修订；产品为 ACTIVE | 看不到资产 404；不是发布人 403；参与过 403 `PUBLISHER_NOT_INDEPENDENT` | 成功 AC-04、`release.prepare`，资源类型 `ContractRelease`；拒绝 DENY-03 |
| `GET …/assets/{id}/releases`、`…/releases/{release_id}` | 资产所属 Workspace 的作者、评审人或发布人 | 看不到资产 404；其余 403，包括产品负责人和维护者 | 成功不记；拒绝 DENY-03、`release.read` |
| `POST …/publication-targets` | 产品负责人；产品为 ACTIVE | 403 | 成功 AC-09、`publication.target.create`；拒绝 DENY-03 |
| `GET …/publication-targets`、`…/{target_id}` | 知道这个产品的人：负责人、任一 Workspace 的成员、持有产品角色的人、平台管理员 | 404 `PRODUCT_NOT_FOUND` | 成功不记；拒绝 DENY-03、`publication.target.read` |
| `POST …/publication-targets/{t}/assets/{a}/operations` | 资产所属 Workspace 的 `internal_publisher`。激活和恢复还要求产品为 ACTIVE、没有参与这个修订、批准仍然成立（F03 §5.10）、没有阻断 | 不是发布人 403；参与过 403 `PUBLISHER_NOT_INDEPENDENT` | 成功 AC-04（`publication.activate`、`publication.restore`）、AC-05（`publication.withdraw`），资源类型 `PublicationSlot`；403 和被阻断记 DENY-03 |
| `GET …/assets/{a}`（槽位）、`…/operations` | 资产所属 Workspace 的作者、评审人或发布人 | 403 | 成功不记；拒绝 DENY-03、`publication.read` 或 `publication.history` |
| `GET …/assets/{a}/artifact` | 同上，并且制品当前可见 | 不是这些成员 403；制品不可见 404 `PUBLISHED_ARTIFACT_NOT_FOUND` | 成功不记；403 和 404 都记 DENY-03、`publication.download` |
| `POST …/access-blocks` | 产品的 `security_admin` | 403 | 成功 AC-06、`security.block`，资源类型 `AccessBlock`；拒绝 DENY-03 |
| `POST …/access-blocks/{id}/clearance` | 产品的 `security_reviewer`，且不是建立这个阻断的人 | 不是安全评审人 403；是建立人 403 `SECURITY_REVIEWER_NOT_INDEPENDENT` | 成功 AC-07、`security.unblock`；前者记 DENY-03，后者记 DENY-06 |
| `GET …/access-blocks`、`…/{id}` | 产品的 `security_admin` 或 `security_reviewer` | 403 | 成功不记；拒绝 DENY-03、`security.block.read` |

**矩阵对不上的地方。** 准备 Release 记在 AC-04 名下，而矩阵里 AC-04 的动作是 `publication.activate`，没有“准备 Release”这一行。登记发布目标记在 AC-09 名下，矩阵里 AC-09 是 `workspace.manage`。矩阵里读 Release 历史和下载制品的角色是 `internal_reader`（AC-15、AC-16），这个角色没有实现：现在能读的是资产所属 Workspace 的作者、评审人和发布人。这几处待裁决。

**职责分离。** 一个修订从写到发布要经过四种人：作者导入和检查，评审人批准，发布人准备 Release 和发布，三者不能是同一个主体（§6.6 的评审人不能是参与人，本节的发布人不能是作者、检查发起人、评审提交人或批准人）。撤销是例外：有发布人角色就能撤销，参与过这个修订的人也可以，因为把内容拿下来不需要第二个人同意。阻断由安全管理员建立，由另一个安全评审人解除，对应 F04-GOV-025 的精神，但那条规则在检查里仍是 `NOT_RUN`。比较的是主体，同一个人的两个账号发现不了。

**批准不是一次性的许可。** 批准在每次使用时重新核对（F03 §5.10）：批准人的评审人成员关系被撤销后，他批准过的修订不能再准备 Release、不能激活或恢复，已经发布的制品也读不到；重新授予不恢复。批准或豁免过期同样处理。这对应 F04 §9 的 `REVIEWER_AUTH_REVOKED` 和 `WAIVER_EXPIRED`。已经记录的 Release 本身不会因此改变或消失，撤销槽位不需要批准仍然成立。

**策略或检查工具更新后也是如此。** 重新核对时要求批准所依据的检查仍是按现行策略和现行工具版本做的（`POLICY_STALE`）。检查工具的版本是代码里的常量，只要它判定的结果会变就要升版本。升版本的构建上线后，此前批准的修订都不能准备 Release、不能激活或恢复，已经发布的制品读不到；撤销仍然可以。要恢复就需要一次新的批准。到 0.22.0 为止这做不到：`APPROVED` 是终点（F03 §5.9），已批准的修订不能再提交评审，同样的内容重新导入得到的仍是原来的修订，只能改动内容后从头再走一遍；批准到期和评审人身份被撤销之后也一样。0.23.0 起，已批准的修订可以复审，内容不用改，见 §6.8。

仍然待裁决的是另一件事：批准不成立时，已经发布的内容要不要立刻读不到。现在的做法比 F02 所说的“启动发布前检查授权与策略版本是否仍有效”和矩阵 AC-16 的条件严：设计只要求新的发布重新核对，没有要求已发布的内容下线。复审让下线之后可以恢复，但恢复要每个资产重新检查、评审、准备 Release 和激活；检查工具每升一次版本，所有已发布的内容都要走一遍。

**阻断是拒绝优先。** 阻断不改槽位，只在读取和激活时拒绝；它覆盖的制品经槽位读对所有人都读不到，包括发布人（0.27.0 起，能读这个 Release 的人仍可以按编号下载，那条路径不看阻断，见 §6.9）。发布人不能建立或解除阻断，安全角色不能发布，也读不到制品。解除阻断不会让任何内容重新可见：必须先撤销，解除之后再由发布人恢复，恢复时重新核对批准。阻断只作用于 Fabric 自己的源站读取；已经被别处缓存或取走的内容不在它的控制之内，`edge_status` 因此是 `UNKNOWN`（§9.2）。

**读取不泄露原因。** 制品不可见时一律返回 404 `PUBLISHED_ARTIFACT_NOT_FOUND`，不区分是没有发布、被阻断、产品暂停还是批准已不成立；原因在审计和槽位、阻断各自的读取接口里；0.25.0 起，批准不成立的原因由槽位的 `approval_problem` 说明（§6.8）。例外是目标的受众配置已被移除，这时返回 404 `AUDIENCE_CONFIG_NOT_FOUND`。不知道产品的人得到 404 `PRODUCT_NOT_FOUND`。

**目前只有内部受众。** 只有 `internal` 受众的目标能激活和读取，读的人也限于资产所属 Workspace 的成员。`partner` 和 `public` 的目标可以登记，但不能发布：对外读取需要的身份、授权（Developer Access）和边缘分发都还没有。所以这一版没有任何内容离开内部。

**完整性。** Release 和制品只增不改；制品的表约束保证摘要与内容相符，读出时再核对一次。槽位、目标和阻断可以更新，但触发器把更新限定为“代次加 1”和“解除一次”，对所有写入者生效。数据与摘要同库，这不是防篡改证明。

功能回归见 `cmd/fabric-api/` 下的 `publicationroles_test.go`、`releases_test.go`、`publication_test.go`、`slots_test.go` 和 `accessblocks_test.go`，使用真实 PostgreSQL 和本地替代 IdP，其中包括并发激活只有一个成功、事件写入失败时全部回滚、评审人被撤销后制品不可读，以及上表各项操作谁被允许、谁被拒绝；写操作留下的审计记录和事件也逐项核对。使用批准时的逐项核对见 `internal/policyreview/application/approval_test.go`，表上的约束和触发器见 `internal/contractregistry/adapters/postgres/releases_test.go` 和 `internal/publication/adapters/postgres/schema_test.go`。SEC-01…36 的正式安全验收状态为 NOT_RUN。

### 6.8 已实现的复审与批准状态的权限

本节对应管理 API 0.23.0–0.25.0。下表路径以 `/v1/products/{product_id}` 开头。这三个版本没有新的操作，也没有新的审计规则，沿用 AC-50、AC-51、AC-03 和 DENY-03、DENY-06。授权矩阵没有改，仍是 53 项，状态仍为 PROPOSED。

| 操作 | 允许范围与状态条件 | 拒绝 | 运行时审计 |
|---|---|---|---|
| 对 `APPROVED` 的修订 `POST …/revisions/{r}/reviews`（复审） | 与 §6.6 的提交相同：资产所属 Workspace 的作者，产品为 ACTIVE，依据这个修订的一次现行检查；并且这个修订没有尚未结束的评审 | 拒绝与 §6.6 相同。已有评审在进行时是 409 `REVIEW_IN_PROGRESS`，这不是权限拒绝 | 成功 AC-50、`review.request`；能读修订但不是作者 DENY-03、`review.request`。409 不留审计 |
| 撤回复审（`POST …/reviews/{id}/withdrawal`） | 与 §6.6 相同：申请人本人，且仍是作者 | 与 §6.6 相同 | 成功 AC-51、`review.withdraw`；不是作者，或是作者但不是申请人，都记 DENY-03、`review.withdraw` |
| 决定复审（`POST …/reviews/{id}/decision`） | 与 §6.6 相同：评审人，且不是这个修订的导入人、所依据检查的发起人、这次评审的提交人；产品为 ACTIVE | 不是评审人 403 `FORBIDDEN`；是评审人但参与过 403 `REVIEWER_NOT_INDEPENDENT` | 成功 AC-03，`review.approve` 或 `review.reject`；不是评审人 DENY-03，参与过 DENY-06，动作都是所请求的那一个 |
| 读修订、修订历史、原文、检查、检查历史、修订比较、评审、评审历史 | 0.24.0 起，资产所属 Workspace 的发布人也能读，应答与作者相同。负责人和维护者仍是 403 | 与读修订相同（§6.4、§6.6） | 成功的读取不记审计 |
| 发布人做写操作：导入修订、发起检查、提交或撤回评审、作决定 | 仍然不允许 | 403 `FORBIDDEN` | DENY-03，动作是被拒绝的那个动作本身：`revision.create`、`revision.check`、`review.request`、`review.withdraw`、`review.approve` 或 `review.reject`。0.24.0 之前，发起检查、提交或撤回评审、作决定都会先读修订或原文，只有发布人关系的主体在那一步就被拒绝，记的是 `revision.read`。导入修订不经过读取，当时记的就是 `revision.create` |
| 读槽位里的 `approval_problem` | 能读槽位的人：资产所属 Workspace 的作者、评审人、发布人（§6.7） | 与读槽位相同 | 成功不记审计 |

**复审不放宽任何规则。** 谁能提交、撤回、决定，对检查的要求，豁免怎么给，都与第一次评审相同（§6.6）。之前批准过这个修订的评审人可以再次批准，只要他不是这次复审所依据检查的发起人、这次复审的提交人，也不是修订的导入人。每一次批准各自独立，有自己的范围、到期时间和豁免；复审被驳回或撤回，不收回已有的批准。复审不触碰已有的批准，所以复审进行期间，已发布的内容照常可读；批准仍按自己的条件成立或失效。

**恢复仍然要三种人。** 作者重新发起检查并提交复审，评审人批准，发布人依据新的批准准备 Release 并激活。发布人的独立性按这个 Release 所依据的那次评审来判断：不能是修订的导入人、那次评审所依据检查的发起人、那次评审的提交人或批准人（§6.7）。批准人的评审人关系被撤销造成的失效，要由现任的评审人复审。

**发布人的读取是一处放宽，授权矩阵里没有。** 矩阵里读修订的是作者（AC-02）和评审人（AC-53），读评审的是作者或评审人（AC-52），都不包括发布人。放宽的理由：发布人本来就能读到 Release 和已发布的制品（§6.7），制品里就是全部源文件；准备 Release 必须给出修订和评审的编号，读不到的话只能靠别人转告，也看不到自己要发布的内容依据的是哪次批准。矩阵没有改，这一处待裁决。

**槽位说明原因，不算泄露。** `approval_problem` 只出现在槽位的读取里。能读槽位的人，就是激活这个 Release 时会被告知同一个原因的人。读制品时的 404 仍然不说原因（§6.7）。取值与激活被拒绝时的错误码逐一相同：`APPROVAL_EXPIRED`、`WAIVER_EXPIRED`、`REVIEWER_AUTH_REVOKED`、`POLICY_STALE`、`APPROVAL_REQUIRED`、`APPROVAL_SCOPE_MISMATCH`。契约里没有列出的原因不会被当作应答返回，按服务故障处理（500）。它是读取时算出来的，不存库，不留审计。

**“同时只有一个评审”对复审是应用层的保证。** 靠的是事务里锁住修订这一行（F03 §5.11），不是表约束。应用角色在 `policyreview.review_request` 上仍然只有 `SELECT` 和 `INSERT`，改不了、删不掉已有的申请；但能直接写库的人可以给一个修订插入两条未结束的复审。

**新旧版本混跑。** 0.22.0 的进程在迁移后的库上：读复审时应答里没有 `supplementary`；不能结束复审，决定和撤回都返回 409 `REVISION_STATUS_CONFLICT`，数据不受影响；照常处理第一次评审，照常提供制品。0.24.0 的放宽和 0.25.0 的 `approval_problem` 旧进程都没有：只有发布人关系的主体把读取请求发到旧进程仍是 403，旧进程的槽位应答不说明批准的状态。

**仍待裁决的事。** 批准不成立时，已经发布的内容要不要立刻读不到，仍是 §6.7 里待裁决的那一件。复审解决的是下线之后能不能恢复，没有改变会不会下线。

功能回归见 `cmd/fabric-api/supplementaryreview_test.go`、`publisherreads_test.go` 和 `slotapproval_test.go`，使用真实 PostgreSQL 和本地替代 IdP。复审的允许、拒绝和审计见 `internal/policyreview/application/supplementary_test.go`。表上可以插入两条未结束的复审、应用角色改不了已有的申请，见 `internal/policyreview/adapters/postgres/supplementary_test.go`；锁住修订这一行见 `internal/contractregistry/adapters/postgres/status_test.go`，这两处使用真实 PostgreSQL。槽位说出的错误码与激活被拒绝的码逐一相同、契约枚举与之相同，见 `internal/transport/http/publication_test.go`。SEC-01…36 的正式安全验收状态为 NOT_RUN。

### 6.9 已实现的查询与审计读取的权限

本节对应管理 API 0.26.0–0.28.0。下表路径以 `/v1/products/{product_id}` 开头。每次请求都从数据库读取当时有效的角色和成员关系；分页令牌不带来任何权限。运行时审计用的是矩阵里已有的 DENY-03 和 AC-10：拒绝记 DENY-03，动作是 `publication.read`、`release.download`、`release.read` 或 `audit.search`（最后一个就是 AC-29 的动作名）；授予和撤销审计员沿用 AC-10。成功的读取都不记审计。授权矩阵没有改，仍是 53 项，状态仍为 PROPOSED。

| 操作 | 允许范围与状态条件 | 拒绝 | 运行时审计 |
|---|---|---|---|
| `GET …/publication-targets/{t}/assets` | 调用人现在持有作者、评审人或发布人成员关系的 Workspace 里的资产的槽位 | 知道产品但一个 Workspace 都读不了（负责人、维护者、持有产品角色的人、平台管理员）403；不认识产品 404 `PRODUCT_NOT_FOUND`；能读但目标不存在 404 `PUBLICATION_TARGET_NOT_FOUND`。能读而目标上没有可见的槽位，是 200 和空列表 | 成功不记；拒绝（包括这几种 404）DENY-03、`publication.read`，资源类型 `PublicationSlot` |
| `GET …/assets/{a}/publications` | 能读这个资产的槽位：资产所属 Workspace 的作者、评审人、发布人，与读单个槽位相同 | 看不到资产（包括平台管理员和别的 Workspace 的读者）404 `API_ASSET_NOT_FOUND`；不认识产品 404 `PRODUCT_NOT_FOUND`；看得到资产但不是这三种人（负责人、维护者）403。没有发布过的资产是 200 和空列表 | 成功不记；拒绝 DENY-03、`publication.read`，资源类型 `PublicationSlot` |
| `GET …/assets/{a}/releases/{r}/artifact` | 能读这个 Release：资产所属 Workspace 的作者、评审人、发布人；产品暂停时也可以。不看发布目标上的阻断，不重新核对批准 | 看不到资产 404 `API_ASSET_NOT_FOUND`；不认识产品 404 `PRODUCT_NOT_FOUND`；看得到资产但不能读 403（包括负责人和维护者）。Release 编号不属于这个资产（别的资产、别的产品、不存在）404 `RELEASE_NOT_FOUND`，答案相同 | 成功和 `RELEASE_NOT_FOUND` 不记；其余拒绝（包括看不到资产、不认识产品的 404）DENY-03、`release.download`，资源类型 `ContractRelease`，不带资源编号 |
| `GET …/assets/{a}/releases/{r}/provenance` | 同上 | 同上 | 同上，动作是 `release.read` |
| `GET …/audit-records` | 本产品的审计员：现在持有 `internal_auditor`；产品暂停时仍可读。只读本产品的记录 | 知道产品但不是审计员（负责人、成员、安全角色、平台管理员）403；不认识产品（包括产品不存在、别的产品的审计员）404 `PRODUCT_NOT_FOUND` | 成功不记；拒绝 DENY-03、`audit.search`，资源类型 `AuditRecord`，不带资源编号，记在这个产品名下；产品不存在不记 |
| 授予、撤销 `internal_auditor`（`POST …/role-assignments`、`DELETE …/role-assignments/{id}`） | 产品负责人，可以授给自己；产品为 ACTIVE 才能授予，暂停时仍可撤销。平台管理员不能授予 | 不是负责人 403（平台管理员授予审计员也是 403）；陌生人 404 `PRODUCT_NOT_FOUND`；暂停的产品授予 409 `PRODUCT_NOT_ACTIVE` | 成功 AC-10、`product_role.grant` 或 `product_role.revoke`，资源类型 `RoleAssignment`；拒绝 DENY-03 |

**列表不泄露看不到的东西。** 槽位列表的限制是查询的条件：调用人读的 Workspace 里的资产的编号写在位置和 `LIMIT` 之前，不是取出一页再丢掉别人的，所以每页除最后一页都是满的，页的大小、页数和空页都说不出别的 Workspace 有什么。目标的列表先判权、后看目标：知道产品但一个 Workspace 都读不了的人得到 403，不管目标存不存在；只有能读的人才被告知目标不存在。令牌只记位置，不记调用人，每页重新读调用人现在的成员关系：撤销之后，旧令牌读下一页得到的是没有这个权限的人的答案；别人的令牌交给负责人是 403，交给陌生人是 404，交给另一个 Workspace 的读者，读到的是那个读者自己能读的槽位；别的列表的令牌是 400 `PAGE_TOKEN_INVALID`。别的产品的目标和资产，对在这里读的人，与不存在的目标和资产答案相同，只差请求的编号；别的产品和别的资产的 Release 编号也一样。读单个槽位锁住发布目标和调用人在 Workspace 的成员关系，放置阻断和撤销成员关系要等这次读完；资产的列表锁成员关系，不锁目标；目标的列表既不锁成员关系，也不锁目标，正在放置阻断时它不等（有测试钉住）。所以目标的列表和同时发生的放置阻断、撤销成员关系之间，没有读单个槽位那样的先后顺序：它读的是读的那一刻的状态。某个槽位的批准核对出了意外（证据或制品损坏）时，整页答 500，与读单个槽位一致。

**按编号下载不看访问阻断，也不重新核对批准（与设计不一致，待裁决）。** `GET …/releases/{r}/artifact` 只核对调用人能读这个 Release，然后读出字节并核对摘要。它不看发布目标上的阻断，不重新核对批准（批准或豁免到期、批准人的评审人关系被撤销、策略和工具更新），产品暂停也不挡。代码里下载不调用批准核对；测试钉住的是产品暂停、批准到期、批准人失去评审人关系、整个目标或资产或这个 Release 被阻断、受众配置被移除这几种情况下仍然下载成功。理由是：阻断都挂在某个发布目标下（整个目标、目标上的一个资产，或其中一个 Release），按编号下载不经过任何目标，没有“哪个目标的阻断”可看；能下载的人本来就能读修订的原文，Release 的制品是同一批文件的打包。契约里 `blockPublicationAccess`、`getPublishedArtifact` 和 `getPublicationSlot` 的描述已经写明阻断和批准只管经槽位读，`getContractRelease` 指向了这两个新操作。

设计正文有五处写的是下载要看阻断，原文如下：

- 授权矩阵 AC-16（`contract.download`，角色 `internal_reader`）：`allow_if` 是 `release.scope & product.same & accessblock.absent`。
- F05 §7 的表，`contract.download` 一行：关键前置条件是“Asset 所属产品匹配；私有需成员/Grant；Release 允许查看；无 Block”。
- F07 §6 第 4 条：“制品不可覆盖。服务端授权后的下载要验证当前 AccessBlock 与 Release Scope，短时凭据也不可绕过撤销，私有 Blob 不给永久可猜 URL。”
- F04 §10 的“拒绝优先”：“所有私有内容的查询、下载、搜索结果点击、直连 Release、`/latest` 解析与 CDN/BFF 受控访问路径均必须在可信边界实施当前授权与 deny。”
- F05 §9.1 第一条（敏感内容泄露）：“阻断 Target/Slot/Artifact 下载 + 禁止缓存继续访问，评估泄露范围，必要时轮换泄露的凭证；不能仅修改 release pointer。”

F03 INV-09 写的是“有效 deny 阻断内容读取及后续激活”，没有点名下载，不算在这五处里。

这意味着：安全管理员阻断一个 Release、它的资产或它的目标以后，经槽位读它的人读不到制品，`blocked` 为真；但能读这个 Release 的作者、评审人、发布人仍然可以按编号下载同一份字节，批准过期或批准人失去评审人关系以后也一样。所以 §6.7 里“阻断覆盖的制品对所有人都读不到，包括发布人”现在只对经槽位读成立。眼下读槽位的人和能按编号下载的人是同一批人，而且只有 `internal` 受众的目标能发布，所以阻断拦住的和下载放行的是同一批人；受众放开以后要重新看这一点。如果要改成下载也看阻断，先要定哪些阻断算数：阻断都挂在发布目标下，一个 Release 可以在几个目标上有槽位，按编号下载不经过任何目标；是任何一个目标上覆盖它的阻断就拒绝，还是只看它现在生效的那些目标，设计没有说。

**审计员。** `internal_auditor` 是产品级角色，由产品负责人授予和撤销，成功记 AC-10；负责人可以授给自己。平台管理员只能任命负责人，授予审计员和授予两个安全角色一样被拒绝（403，DENY-03）：登记产品时填错负责人是管理员要能修的事，读审计不是。它只带来读本产品的审计记录。和其他产品级角色一样，持有它的人知道这个产品，能读产品本身、受众配置和发布目标，列 Workspace 得到空列表；看不到资产、修订、原文、检查、评审、Release、槽位、制品、阻断、角色列表和 Workspace 的详情与成员，也不能写任何东西（测试逐条列出了这些路径的答案）。**负责人不因为是负责人就能读审计**，成员、安全角色和平台管理员也不能；负责人要读，先授予自己审计员，这一步有记录。审计员能读被任命之前的记录；产品暂停时它照常读，授予新的审计员被拒绝（409 `PRODUCT_NOT_ACTIVE`），撤销不受影响。每页重新判权，判权时对调用人的角色行加共享锁，撤销要等这次读完，撤销提交以后旧令牌读不到下一页。被审计的人决定谁来审计他：产品负责人的行为也在审计记录里，而审计员由负责人授予，也可以随时撤销；授予和撤销都有记录，但只有当时的审计员读得到，撤销以后就没有人读得到。设计没有写谁来授予审计员（矩阵 AC-29 的条件是 `auditor.scope`），这一点待裁决。

**审计记录里的主体如实给出（与 F06 的说法不一致，待裁决）。** 记录里的 `actor` 是行动人的 issuer 和 subject，原样给出，不打码。F06 §6 写的是：“审计只能展示脱敏主体和关联 ID，导出需要额外权限，且查询页面不使审计事件可被编辑。”授权矩阵 AC-29 的条件是 `auditor.scope & product.allowed & fields.minimized`。F03 的 OD-09 把审计里的个人信息与删除请求的冲突留给 F05 和法务。设计文档没有定义“脱敏”是什么：打码到哪一位，还是换成不可逆的编号；审计员能不能按主体筛选，打码以后这个筛选怎么办，也没有说（现在可以：`actor_subject` 和 `actor_issuer` 按原值精确匹配）。所以实现没有猜，如实给出主体，`fields.minimized` 没有实现为读取时的脱敏。能说的是记录在写入时就只存引用：没有令牌，没有请求体，没有私有对象的名字（测试用带标记的令牌和请求体检查了表和应答）。

**读审计本身留什么记录。** 成功的读取不记，与别的读取一致。被拒绝的记 DENY-03、`audit.search`、资源类型 `AuditRecord`、不带资源编号，记在那个产品名下，所以审计员读得到谁试过读审计。两种人被拒绝都记：知道产品但不是审计员的（403），和产品存在但不认识它的人（404，包括别的产品的审计员）；产品不存在不记，因为没有产品可记。没带令牌或令牌无效（401），以及参数和令牌格式不对（400），在进入用例之前就被拒绝，不记；时间范围颠倒（422）只对审计员给出，别人得到 403 或 404。拒绝写不进审计表时，调用人仍然被拒绝，服务日志里有一条错误，与其他拒绝相同。这件事的另一面：任何持有效令牌的人，对一个存在的产品反复试，每次被拒绝的请求都在那个产品名下留一条拒绝记录；审计员现在读得到它们。代码里没有限制尝试次数的机制。

**审计表仍然只能追加。** 应用角色在 `platformoperations.audit_record` 上的权限从 `INSERT` 变成 `SELECT`、`INSERT`：能读，仍然没有 `UPDATE`、`DELETE`、`TRUNCATE`，没有任何列上的 `UPDATE`，也不能 `SELECT … FOR UPDATE` 或 `FOR SHARE`。这靠三样东西：权限只在一处声明（`internal/platform/db/migrate/grants.go` 的 `ApplicationPrivileges()`）；一个测试要求审计表恰好是 `INSERT` 和 `SELECT`，另一个测试授权之后逐项查询角色实际持有的权限（包括 `TRUNCATE`、`REFERENCES`、`TRIGGER` 和列上的权限）；进程启动时核对声明里的权限都有，缺一项就拒绝启动。启动检查不查有没有多出来的权限：一个被人手工多授了 `UPDATE` 或 `DELETE` 的角色，它发现不了；`fabric-migrate -grant-to` 也只添加，不收回。表的所有者（迁移用的角色）和数据库管理员仍然能改。这是权限层面的限制，不是防篡改的证明（F05 §14：不能把“数据库不开放普通修改权限”误写成物理不可篡改）。

**授权矩阵怎么对应。** AC-29 `audit.search`（`internal_auditor` 读 `AuditRecord`）现在有了实现。`auditor.scope` 实现为：调用人现在持有本产品的 `internal_auditor`，每页重新读；`product.allowed` 实现为：只读本产品的记录，不认识产品的人得到 404；`fields.minimized` 设计没有定义，所以没有实现为读取时的脱敏（见上）。矩阵里的默认失败是 `DENY`：不是审计员的得到 403 或 404。AC-30 `audit.export`（审计导出）没有做，FR-068 是 V1。AC-15（`contract.history`）和 AC-16（`contract.download`）的角色是 `internal_reader`，这个角色仍然没有实现：Release 的列表、元数据、下载和来源现在由资产所属 Workspace 的作者、评审人、发布人读，没有加 `internal_reader`；AC-16 的 `accessblock.absent` 对按编号下载没有实现（见上）。槽位列表和来源在矩阵里没有对应的行，与读单个槽位一样。矩阵文件没有改。

**上线顺序和新旧版本混跑。** 从 0.25.0 升级要重新授权：先执行 `fabric-migrate -apply -grant-to <application_role>`，应用 `productgovernance/009` 和 `platformoperations/002`，并给应用角色补上审计表的 `SELECT`，再换进程。只迁移不授权，新进程拒绝启动：`db: the application role lacks privileges this build needs; run fabric-migrate -apply -grant-to <role>: missing SELECT on platformoperations.audit_record`；补救是再执行一次带 `-grant-to` 的命令，没有东西要迁移。既没有迁移也没有授权时报的是同一条，因为启动时先核对权限、后核对迁移。旧进程在迁移和授权之后照常运行：它只往审计表里写，多出来的 `SELECT` 不影响它，它的启动检查也不查多出来的权限和迁移。所有进程都升级以后，才授予 `internal_auditor`：旧进程的契约里没有这个取值，授予请求会被它拒绝（400 `REQUEST_INVALID`）；库里一有审计员的分配，旧进程列产品角色、读这条分配就返回 500（契约没有声明的取值不发出去），回滚到旧版本也一样。建索引用的是普通 `CREATE INDEX`，建的时候审计表的写入要等，所以那段时间写操作也会等（F03 §5.12）。0.26.0 和 0.27.0 没有迁移，也不需要授权。

功能回归见 `cmd/fabric-api/` 下的 `slotlists_test.go`、`releasedownload_test.go`、`releaseprovenance_test.go` 和 `auditreading_test.go`，使用真实 PostgreSQL 和本地替代 IdP，其中包括：谁被允许、谁被拒绝，拒绝留下的记录，令牌不带权限，撤销之后读不到下一页，审计员的角色在一次读等待期间被撤销，下载在阻断、批准到期和产品暂停时仍然成功，来源在复审之后不变，记录对不上时答 500 而应答不说原因。用例层的判权和审计见 `internal/platformoperations/application/readaudit_test.go` 和 `internal/productgovernance/application/auditscope_test.go`；权限、迁移和启动检查见 `internal/platform/db/migrate/auditreading_test.go`、`internal/platform/db/auditreading_test.go` 和 `internal/platformoperations/adapters/postgres/audit_test.go`；翻页和筛选的查询见 `internal/platformoperations/adapters/postgres/auditreader_test.go` 和 `internal/publication/adapters/postgres/slots_test.go`。SEC-01…36 的正式安全验收状态为 NOT_RUN。

### 6.10 已实现的工作台查询的权限

本节对应管理 API 0.29.0–0.31.0。下表路径以 `/v1/products/{product_id}` 开头，`GET /v1/caller` 除外。每次请求都从数据库读取当时有效的角色和成员关系；分页令牌不带来任何权限。运行时审计用的是矩阵里已有的 DENY-03：拒绝记 DENY-03，动作是 `product.read`、`review.read` 或 `asset.search`；成功的读取都不记审计。授权矩阵没有改，仍是 53 项，状态仍为 PROPOSED。

| 操作 | 允许范围与状态条件 | 拒绝 | 运行时审计 |
|---|---|---|---|
| `GET /v1/caller` | 任何已认证的人，只说调用人自己：签发方、主体和是不是平台管理员。不读库 | 没有人被拒绝；没通过认证的请求是 401，在进入操作之前就答复了。带查询参数或请求体 400 `REQUEST_INVALID` | 不记 |
| `GET …/caller` | 与读产品相同：平台管理员，和现在在这个产品里持有任一产品角色或 Workspace 成员关系的人；产品暂停时仍可读。什么都不持有的平台管理员得到 200 和两个空数组 | 其他人（别的产品的负责人、没有身份的陌生人、曾经持有而已被撤销的人）404 `PRODUCT_NOT_FOUND`，与不存在的产品相同 | 成功不记；拒绝 DENY-03、`product.read`，资源类型 `Product`，资源编号是产品编号，记在这个产品名下；产品不存在、`product_id` 不是标识不记 |
| `GET …/workspaces/{w}/reviews` | 这个 Workspace 里现在持有 `internal_author`、`internal_reviewer` 或 `internal_publisher` 的人，读到整个 Workspace 的全部评审；产品暂停时仍可读。没有评审是 200 和空列表 | 不认识产品的人 404 `PRODUCT_NOT_FOUND`；认识产品、却既不是负责人也不是这个 Workspace 成员的人（别的 Workspace 的成员、平台管理员、只有产品角色的人）404 `WORKSPACE_NOT_FOUND`；是负责人或成员、却没有那三种身份的人（负责人、维护者）403 `FORBIDDEN`；负责人问产品没有的或别的产品的 Workspace 404 `WORKSPACE_NOT_FOUND` | 成功不记；拒绝 DENY-03、`review.read`，资源类型 `Review`，不带资源编号，记在这个产品名下；产品不存在、负责人问产品没有的 Workspace 不记 |
| `GET …/assets`、`GET …/assets/{a}`（AC-13、AC-14） | 与 §6.4 相同；在资产所属 Workspace 现在持有作者、评审人或发布人的人，另外得到 `overview`。带 `check_status` 或 `release_pending` 时，只在调用人读得了修订的 Workspace 里找 | 不带新筛选时与 §6.4 相同；带新筛选而在这个产品里哪个 Workspace 都读不了修订的人（能列资产的负责人、维护者）403 `FORBIDDEN`；本来就不能列资产的人（陌生人、平台管理员、审计员、安全角色）与不带筛选时相同 | 成功不记；带新筛选被拒绝 DENY-03、`asset.search`，资源类型 `APIAsset`，不带资源编号；其余与 §6.4 相同 |

**问不到别人。** 两个“我是谁”的操作没有任何参数能指定别人：`getCaller` 没有参数，`getCallerInProduct` 只有产品编号；带了查询参数或请求体是 400，不是照办。先认证，后检查参数，没有令牌的请求得到的是 401，不会被告知哪个参数不合法。身份只取令牌验过之后交出的签发方和主体，请求头里的 `X-Forwarded-User`、`X-Subject` 之类不参与（测试用一组这样的头验过）。对一个产品什么身份都没有的人，`getCallerInProduct` 的答案与产品不存在的答案逐成员相同（`trace_id` 除外），所以它不是探测产品编号的办法；它的拒绝与读产品（`getProduct`）的拒绝同一个动作、同一种记录。

**只给身份，不给权限；页面显示错了不会越权。** 应答只说持有什么：产品角色和 Workspace 里的角色，没有“能做哪些操作”的清单。服务端对每个后续请求照旧重新读调用人当时的角色和成员关系再判权：页面按 `roles` 显示了不该显示的按钮，点下去只是被拒绝；撤销提交之后的下一次请求就没有那个角色，用的还是原来的令牌；应答带 `Cache-Control: no-store`，旧的应答也不带来任何权限。平台管理员标志不是产品里的权限：管理员在产品里什么都不持有，自己当了负责人才有 `product_owner`，所以 `getCallerInProduct` 对它答 200 和两个空数组，而 `listWorkspaces` 仍然答 403（§6.1）。`workspaces` 是“我持有成员关系的”：负责人读得到全部 Workspace，却不是任何一个的成员，它们不在 `workspaces` 里；要看自己读得到哪些 Workspace，用 `listWorkspaces`。

**评审队列：三档拒绝，谁读到什么。** 队列用的是读单个评审的那份名单（`revisionReaders`）：这个 Workspace 里现在持有 `internal_author`、`internal_reviewer` 或 `internal_publisher` 的人，读到的是**整个 Workspace 的全部评审**：别人提交的、已撤回的、已驳回的都在，可见性只有 Workspace 一层，没有“这条评审这个人读不了”。发布人也一样，沿用 0.24.0（§6.8），那一处放宽在矩阵里没有，待裁决；现在不只是单读，列表也读到全部。每页重新判权，不加锁，令牌不带来权限：令牌只记位置和它属于哪个队列，不记调用人，也不记筛选；撤销提交之后，旧令牌读下一页得到的是没有这个权限的人的答案。拒绝分三档，按调用人有资格知道的程度回答：不认识产品的人，404 `PRODUCT_NOT_FOUND`，与不存在的产品相同；认识产品、却看不到这个 Workspace 的人（别的 Workspace 的成员、平台管理员、只有产品角色的人），404 `WORKSPACE_NOT_FOUND`，与不存在的 Workspace 和别的产品的 Workspace 相同；看得到这个 Workspace、却没有三种身份的人（负责人、维护者），403 `FORBIDDEN`。没有三种身份的人得到的是拒绝，不是空列表，空列表会说这个 Workspace 没有评审。测试逐人比较队列和单读：13 种调用人，队列答 200、403 还是 404，和逐条读单个评审得到的一致，列出的恰是单读读得到的；两边的错误码有一处不同，别的 Workspace 的成员，队列是 `WORKSPACE_NOT_FOUND`，单读是 `API_ASSET_NOT_FOUND`（单读先找资产，队列没有资产），两处的名单不同步，测试会报错。列表与单读在权限上没有别的差别；在内容上有一处：列表不核对评审依据的内容是不是修订现在的内容（F04 §11.9）。

**目录概况不泄露修订的任何事。** 概况带着最新修订、检查和 Release 的信息，这些本来只有读得了修订的人读得到，所以概况只给在资产所属 Workspace 现在持有作者、评审人或发布人的人，用的还是读修订的那份名单。产品负责人和 Workspace 维护者读得到资产，读不到概况：应答里没有 `overview` 这个成员，不管资产有什么，也不是空对象。要不要让他们也看到，待裁决：那要先让他们能读修订，或者给一个专门的权限。同一个人在读得了修订的 Workspace 里看到概况，在别的 Workspace 里看不到：兼任某个 Workspace 作者的负责人，只在那个 Workspace 的资产上看到。带新筛选，等于由概况决定哪些资产留下，所以：只在调用人读得了修订的 Workspace 里找，别的 Workspace 的资产不去问概况，也不会被留下或占页，这个条件写在目录自己的查询里；在这个产品里哪个 Workspace 都读不了修订、却能列资产的人（负责人、维护者），带任一新筛选得 403，不答空页，因为空页会说出筛选留下了什么；本来就不能列资产的人，答案与不带筛选时相同。别的 Workspace 里的资产怎么变（导入、检查、批准、做 Release），不改变调用人的任何一页，也不改变令牌：测试把这些变化做一遍，比较只读另一个 Workspace 的人、维护另一个 Workspace 的人的每一页（带和不带筛选，页大小 1、2、3、100），逐字相同。概况和筛选都每次请求重新判权：读修订的身份被撤销，下一次请求就没有概况，还能列资产的人带筛选是 403，旧令牌不带权限；产品暂停时照常读。`registerAsset` 的应答从来没有概况。

**读这些东西本身留什么记录。** 成功的读取都不记审计，也不发事件，与别的读取一致；`getCaller` 从不记：它不读库，也没有人被拒绝。被拒绝的记 DENY-03，记在被问的那个产品名下，`correlation_id` 就是应答的 `trace_id`：`getCallerInProduct` 记 `product.read`，资源类型 `Product`，资源编号是产品编号；队列记 `review.read`，资源类型 `Review`，不带资源编号；带新筛选的资产列表记 `asset.search`，资源类型 `APIAsset`，不带资源编号。产品不存在的不记，因为没有产品可记；负责人问产品没有的 Workspace 也不记。没带令牌或令牌无效（401），以及参数和令牌格式不对（400），在进入用例之前就被拒绝，不记。拒绝写不进审计表时，调用人仍然被拒绝，服务日志里有一条错误，与其他拒绝相同。这件事的另一面是：没有权限的人每调一次就留下一条记录，代码里没有限制尝试次数的机制，这些记录产品的审计员读得到（§6.9）。页面每刷新一次，一个什么身份都没有的人对一个存在的产品的 `getCallerInProduct` 就多一条 `product.read`，没有读评审身份的负责人或维护者每读一次队列就多一条 `review.read`，没有读修订身份的人每带一次新筛选就多一条 `asset.search`。所以页面应当先问身份（`getCallerInProduct`），再决定调不调队列和带筛选的列表，不要靠试；对一个已被撤销干净的人，这个“先问”本身也留下一条。要不要记成单独的动作、要不要限速，待裁决。

**授权矩阵怎么对应。** 矩阵没有改。AC-52 `review.read`：矩阵的条件是 `workspace.member & product.same & action.role & revision.same`，说的是一个修订的评审；队列读的是整个 Workspace 的评审，没有“这个修订”，所以 `revision.same` 换成了“评审属于这个产品和这个 Workspace”，这是查询里的条件；`action.role` 是作者、评审人，加上 0.24.0 起的发布人（§6.8，矩阵里没有）。实现在这之前没有用过动作名 `review.read`：单读被拒绝记在 `revision.read` 或 `asset.read` 名下（§6.6），队列没有修订可读，所以拒绝记在 `review.read` 名下，资源类型 `Review`。这几处对应关系待裁决。AC-13、AC-14（`internal_reader` 读资产）：这个角色仍然没有，资产由负责人和任一 Workspace 的成员读（§6.4）；概况不属于 `asset.read`，它由读修订的名单决定，对应 AC-02（作者）、AC-53（评审人）和 0.24.0 起的发布人（§6.8，矩阵里没有）；带新筛选的拒绝沿用 `asset.search` 和 DENY-03。AC-02 读修订的条件没有变。“我是谁”在矩阵里没有行：`getCaller` 谁都能调，`getCallerInProduct` 的条件与读产品相同，读产品在矩阵里也没有对应的行。AC-08 的 `platform_admin` 是登记产品的角色（`product.register`），“调用人是不是平台管理员”这一问不对应它的任何条件。`internal_reader` 仍然没有：负责人、维护者和只读角色读修订、检查、评审、Release 的放宽没有做，AC-13…16 的角色还是设计里的。

**平台管理员靠部署配置。** `platform_administrator` 来自部署配置 `IDP_FABRIC_PLATFORM_ADMIN_SUBJECTS`（主体的 JSON 数组，都是受信任的那一个签发方的主体），进程启动时读入，进程内固定：改了要重启，没有接口能增减管理员；没有配置时没有人是管理员，`platform_administrator` 恒为假。几个副本的配置不一致时，同一个人在不同副本上的答案会不同，页面按它显示的东西也会时有时无。应答说的是这个进程现在认定的，不是库里的事实。管理员不在库里，所以没有授予、撤销的审计和事件，这与 §6.1 一致；新的只是页面第一次能知道调用人是不是管理员，以前只能靠试。要不要改成库里的、可撤销且有记录的角色，那要新的迁移和新的规则，待裁决。登录人只有签发方和主体，没有姓名；显示名从哪里来没有定，另存会碰到 F03 的 OD-09 那样的个人信息保留和删除问题，这一点没有做。

**上线顺序和新旧版本混跑。** 从 0.28.0 升级只需要 `fabric-migrate -apply`，不需要重新授权，先迁移再换进程：它应用 `policyreview/006_review_queue_order`，只建一个索引，不改行，应用角色的权限没有变；没有新角色，没有新配置。0.29.0 和 0.31.0 本身没有迁移，也没有状态；0.30.0 起的构建在没迁移的库上拒绝启动：`db: the database is not migrated for this build; run fabric-migrate -apply first: migration policyreview/006_review_queue_order is not recorded`（迁移测试断言了它是 `ErrNotMigrated`，并点名这一步没有记录）；补救是执行 `fabric-migrate -apply`，带不带 `-grant-to` 都行，没有授权要补。旧进程在迁移之后照常运行：评审的读写语句没有变，启动检查只查它自己计划里的步骤，计划以外的步骤接受（`internal/platform/db/open.go` 的 `checkHistory`）；它没有新的路径，对 `GET /v1/caller`、`GET /v1/products/{product_id}/caller` 和 `GET …/workspaces/{workspace_id}/reviews` 答 404 `ROUTE_NOT_FOUND`；它不认识 `check_status` 和 `release_pending`，对带它们的 `listAssets` 答 400 `REQUEST_INVALID`（未声明的参数），资产的应答里没有 `overview`。所以要用这些接口的页面，等所有进程都升级以后再上。建索引用的是普通 `CREATE INDEX`，建的时候新提交评审的写入要等，读不受影响，没有量过。以后加新的产品角色，仍要等所有进程升级以后才授予：`getCallerInProduct` 遇到契约没有的角色答 500，与列产品角色一致。回滚到旧程序没有数据要回退，多出的索引旧程序不用，留着即可。

功能回归见 `cmd/fabric-api/` 下的 `caller_test.go`、`reviewqueue_test.go`、`assetoverview_test.go`、`assetoverview_reach_test.go` 和 `assetoverview_pages_test.go`，使用真实 PostgreSQL 和本地替代 IdP，其中包括：每种角色读到自己的、撤销之后的下一次请求就没有、撤销时另一个会话还没提交也不等、13 种调用人在队列和单读上的答案逐人一致、令牌不带权限、被拒绝时留下的记录、概况跟着每一步变、别的 Workspace 的变化不改变调用人的页、检查的记录被改坏时答 500。用例层的判权和审计见 `internal/productgovernance/application/caller_test.go`、`internal/policyreview/application/queue_test.go` 和 `internal/apicatalog/application/overview_test.go`；队列和概况用的名单与读修订的名单不分叉，见 `internal/productgovernance/application/reviewscope_test.go` 和 `catalogscope_readers_test.go`；迁移见 `internal/platform/db/migrate/reviewqueue_test.go`；传输层和契约见 `internal/transport/http/caller_test.go`、`reviewqueue_test.go` 和 `assetoverview_test.go`；没有跨 schema 的 SQL，见 `internal/architecture/schemas_test.go`。SEC-01…36 的正式安全验收状态为 NOT_RUN。


### 6.11 产品负责人读取契约治理材料（0.32.0）

产品负责人可以读取本产品各 Workspace 的资产概况和筛选结果、修订与原文、检查和比较、评审与评审队列、Release 与来源、发布槽位及历史。经槽位读取制品时，服务端核对当前权限、批准及 AccessBlock。历史 Release 按编号下载沿用 §6.9 的读取语义。

产品负责人角色只为上述治理材料授予读取权限。提交、决定及发布分别检查 Workspace 内的作者、评审人及发布人授权。产品审计查询需要 `internal_auditor`，安全阻断与解除需要各自的安全角色。读取仍按所请求的产品隔离，撤销负责人角色后继续按当前 Workspace 成员关系判权。

权限回归使用真实 PostgreSQL，覆盖同产品多 Workspace、跨产品拒绝、撤权后使用原令牌和分页游标，以及没有评审人授权时拒绝作出评审决定。


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
