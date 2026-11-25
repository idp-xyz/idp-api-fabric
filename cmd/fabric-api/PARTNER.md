# CargoPai 合作方授权读取

先通过管理 API 在 CargoPai 产品登记 `partner` 受众配置和发布目标。使用该目标的产品和目标 UUID 配置 `fabric-api`。同一个进程通过独立监听地址提供合作方读取。部署时由合作方 HTTPS 登录代理验证浏览器会话，并向这个入口转发面向合作方读取的 Bearer 令牌。[接口契约](../../api/partner-reference.openapi.yaml)

| 配置 | 用途 |
| --- | --- |
| `IDP_FABRIC_PARTNER_HTTP_ADDR` | 合作方监听地址，设置后启用该入口 |
| `IDP_FABRIC_PARTNER_PRODUCT_ID` | 产品键为 `cargopai` 的产品 UUID |
| `IDP_FABRIC_PARTNER_TARGET_ID` | 同一产品的一个 `partner` 发布目标 UUID |
| `IDP_FABRIC_PARTNER_OIDC_ISSUER` | 与管理入口不同的合作方签发者 |
| `IDP_FABRIC_PARTNER_OIDC_AUDIENCE` | 合作方读取入口接受的令牌受众 |
| `IDP_FABRIC_PARTNER_OIDC_JWKS_URL` | 合作方签发者的签名密钥地址 |
| `IDP_FABRIC_PARTNER_GRANT_URL` | 受信 Grant 服务提供决策的 HTTPS 地址，本地联调可使用回环 HTTP |
| `IDP_FABRIC_PARTNER_GRANT_TOKEN` | Grant 服务需要 Bearer 认证时使用的服务凭证 |

Grant 接口接收 JSON POST，字段为 `issuer`、`subject`、`product_id`、`target_id`、`api_asset_id`、`audience`、`environment` 和 `action`。受众为 `partner`，动作是 `contract.download`。服务根据当前开发者组织成员关系及该范围的有效 Grant 返回 HTTP 200 和 `{"allowed":true}` 或 `{"allowed":false}`。超时、重定向和无效决策均关闭读取。

调用者使用合作方令牌请求 `GET /v1/reference/{asset}`，成功时得到当前槽位的源文件包和 `X-Artifact-Digest`。Grant、当前批准和阻断状态在每次读取时重新检查。撤销、阻断或失效授权统一返回 404。响应均使用 `Cache-Control: no-store`。

发布人通过管理 API 对绑定目标执行激活、撤销和恢复。首次激活及恢复都要求当前批准。此入口使用现有发布与审计表。真实部署的登录、Grant 服务和边缘传播按 F08 隔离环境验收。
