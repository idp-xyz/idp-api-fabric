# CargoPai Developers 只读门户

`go run ./cmd/fabric-developer-portal` 在独立地址提供接口参考。部署在 CargoPai 合作方 HTTPS 登录代理之后，由代理验证会话，并在服务端附上面向合作方源站的短时 Bearer 令牌。使用 CargoPai 合作方源站及其已发布的资产。[源站实现与配置](https://github.com/idp-xyz/idp-api-fabric/pull/63)

| 配置 | 用途 |
| --- | --- |
| `IDP_FABRIC_DEVELOPER_PORTAL_HTTP_ADDR` | 网页监听地址，默认 `127.0.0.1:8083` |
| `IDP_FABRIC_DEVELOPER_PORTAL_ORIGIN` | 合作方源站的 HTTPS 地址，本地联调可用回环 HTTP |
| `IDP_FABRIC_DEVELOPER_PORTAL_ASSET_ID` | 本站展示的一项 CargoPai API 资产 UUID |

网页显示当前发布契约的 OpenAPI 版本和基础地址，以及每个操作的参数、请求体和响应的 Schema，支持按方法、路径或摘要搜索。响应为 `application/problem+json` 时标明 RFC 9457。Schema 最多列出 6 层、200 个字段，其余见源文件包。网页、搜索和源文件包下载都向源站重新查询授权，并核对制品摘要。页面与下载响应使用 `no-store`。部署联调可用独立开发签发者和测试 Grant 服务验证读取、拒绝与撤销。

每个操作附 cURL、Go 和 TypeScript 静态示例，都由契约生成。示例从环境变量 `API_BASE_URL` 读基础地址，从 `API_ACCESS_TOKEN` 或 `API_KEY` 读凭证，页面不含真实凭证。必填的路径、查询和请求头参数写成 `{名称}` 占位。请求体用契约里的示例，没有示例时按 Schema 生成，不含只读字段。契约中的文字在示例里都写成该语言的字符串字面量，复制运行不会执行其中的内容。
