# Fabric Outbox 投递

`fabric-worker` 用与 API 相同的 PostgreSQL 应用角色连接已迁移的数据库。配置 `IDP_FABRIC_POSTGRES_DSN` 和 `IDP_FABRIC_OUTBOX_URL` 后运行 `go run ./cmd/fabric-worker`。接收地址指向控制平面内的受信服务，远程地址使用 HTTPS，本地联调可使用回环 HTTP 地址。接收端需要 Bearer 认证时，通过 `IDP_FABRIC_OUTBOX_TOKEN` 注入凭证。未配置接收地址时进程待命，已提交事件继续保存在 Outbox。

Worker 将 Fabric 各模块的集成事件以 `application/json` POST 到同一接收端。请求体使用 Bento 事件信封，包括 `source`、`id`、`type`、`scope`、`partition_key`、`occurred_at` 和 `payload`。接收端先以 `(source, id)` 持久化去重并接受事件，再返回 2xx。重复收到同一事件也应返回 2xx。

每个分区按入队顺序领取事件。接收端未确认时，Worker 从 1 秒开始退避，最长等待 5 分钟后重试。到达 12 次领取或失败上限的事件保留为 `ABANDONED`。进程退出或确认结果丢失后，另一个 Worker 可以在租约到期后重领。通过 `bento.outbox` 的状态、尝试次数和失败码可检查积压和处理结果。

`PUBLISHED` 表示接收端已确认接收事件。发布槽位的 `delivery_status` 和访问阻断的边缘传播状态仍取决于实际边缘适配与对账。
