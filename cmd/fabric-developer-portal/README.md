# CargoPai Developers 只读门户

`go run ./cmd/fabric-developer-portal` 在独立地址提供接口参考。部署在 CargoPai 合作方 HTTPS 登录代理之后，由代理验证会话，并在服务端附上面向合作方源站的短时 Bearer 令牌。使用 CargoPai 合作方源站及其已发布的资产。[源站实现与配置](https://github.com/idp-xyz/idp-api-fabric/pull/63)

| 配置 | 用途 |
| --- | --- |
| `IDP_FABRIC_DEVELOPER_PORTAL_HTTP_ADDR` | 网页监听地址，默认 `127.0.0.1:8083` |
| `IDP_FABRIC_DEVELOPER_PORTAL_ORIGIN` | 合作方源站的 HTTPS 地址，本地联调可用回环 HTTP |
| `IDP_FABRIC_DEVELOPER_PORTAL_ASSET_ID` | 本站展示的一项 CargoPai API 资产 UUID |

网页显示当前发布契约的 OpenAPI 版本、操作与响应说明，支持按方法、路径或摘要搜索。网页、搜索和源文件包下载都向源站重新查询授权，并核对制品摘要。页面与下载响应使用 `no-store`。部署联调可用独立开发签发者和测试 Grant 服务验证读取、拒绝与撤销。
