# server

> lite_settings 技术方案 · 第 8 章。索引见 [README.md](README.md)。

## 8. server

Server 为薄层：`store` + 长轮询 revisionWatcher + HTTP handler。

### 8.1 revisionWatcher

Server 自身按固定间隔轮询 `Revision()`，水位变化时 close 广播 channel，唤醒全部挂起请求。

```go
type revisionWatcher struct {
    mu  sync.Mutex
    rev int64
    ch  chan struct{}   // revision 变化时 close 并替换
}

func (w *revisionWatcher) Wait(ctx context.Context, since int64) (int64, bool)
```

两个性质：

- **DB 查询量与客户端数量无关。** 1000 个客户端仍是每秒 1 次单行查询。
- **多实例零协调。** 各实例独立轮询 DB，无需选主或共享状态，可直接水平扩展。

### 8.2 HTTP API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/v1/configs?prefix=<p>&prefix=<q>` | 按 prefix 全量拉取，可传多个；不传表示全部 |
| GET | `/v1/configs/{key}` | 读单个 |
| PUT | `/v1/configs/{key}?format=&author=&comment=` | 发布，body 即 value 原文 |
| DELETE | `/v1/configs/{key}?author=&comment=` | 删除 |
| GET | `/v1/configs/{key}/history?limit=100` | 版本列表，倒序 |
| POST | `/v1/configs/{key}/rollback?author=` | body `{"version": 3}` |
| GET | `/v1/watch?revision=N&prefix=a:&prefix=b:` | 长轮询 |
| GET | `/v1/revision` | 当前水位 |
| GET | `/healthz` | |

`format` 取 `raw`（默认）/ `json` / `yaml`。请求体上限 `store.MaxValueSize`。
`key` 直接作为路径段——`ValidateKey` 的字符集排除了 `/`，正是为此。

状态码：`404` key 或版本不存在 · `400` key/value 非法 · `413` value 超限 ·
`503` 未建表或未就绪 · `304` 长轮询超时且无变更。非 2xx 响应体统一为
`{"error": "..."}`。

> **不变量｜快照的水位标签不得高于其数据的真实水位。**
> 即：**先取水位，再读数据**。反过来会把陈旧数据标上新水位，客户端据此
> 认为自己已是最新，那次变更被永久跳过。标低则最坏延迟一个轮询周期。

> **约束｜客户端可见的水位一律取自 revisionWatcher，不得直接查库。**
> `/v1/configs`、`/v1/watch`、`/v1/revision` 必须报同一个数字。若前者报库里的
> 真实水位、后者报 revisionWatcher 的水位，客户端拿 `/v1/configs` 的基线去 watch 会
> 因两数不等而立即返回，每次冷启动白跑一轮。revisionWatcher 未完成首轮轮询时才回落
> 到直接查库，此时 `/healthz` 也报未就绪。
>
> 写入响应里的 `revision` 是例外：那是该次写入自己拿到的号码，取自事务，用于审计。

> **实现注意｜快照的水位标签取 `min(watcher 水位, 库里水位)`。**
> 上面两条约束在一种情况下互相冲突：数据库被回档（从备份恢复）后，库里的
> revision 退回到 5，而 revisionWatcher 手里仍是回档前的 10。此时照搬 watcher
> 的数字会把 revision=5 的数据标成 10，客户端再也拿不到那 5 个号段里的变更。
> 因此 `/v1/configs` 在读数据前先查一次库里的水位并取较小者：稳态下 watcher
> 的水位本就不高于库里的，`min` 是恒等式，三个端点仍报同一个数；只有回档后
> 两者才会不等，而标低只是多等一个轮询周期。代价是每次快照多一次单行查询。

> **约束｜一次请求最多带 `maxPrefixes`（64）个 prefix。**
> 超出返回 `400`。这不是业务限制而是防御：prefix 直接拼进 SQL 的 `OR` 条件，
> 个数无上限意味着单个请求就能让数据库做任意大的扫描；64 个前缀足以覆盖
> 任何真实的多业务线订阅。真需要更多时应当改用不传 prefix 的全量拉取。

> **约束｜`/healthz` 只做启动门控，不随数据库状态起伏。**
> 首轮轮询成功前返回 `503`，之后恒为 `200`，数据库的实时状态放在响应体的
> `db_error` 里。让它跟着 DB 抖动会在数据库闪断时把所有实例同时摘掉，
> 连带掐断全部长轮询——而这些客户端本可以继续用本地缓存正常工作。
> 配置中心发陈旧配置远好过发不出配置。

### 8.3 启动参数

| 参数 | 默认值 |
|---|---|
| `--dsn` | 必填 |
| `--driver` | `mysql` \| `postgres` |
| `--addr` | `:8080` |
| `--poll-interval` | `1s` |
| `--long-poll-timeout` | `30s` |
| `--migrate` | `true`（建表可重复执行） |
| `--log-level` | `info` |

> **实现注意｜关闭 revisionWatcher 必须早于 `http.Server.Shutdown`。**
> 否则挂起的长轮询要等满 `--long-poll-timeout` 才释放，优雅退出被拖成半分钟。
> 先关 revisionWatcher 让它们立即以 `304` 返回，再 Shutdown。

---

---

← [client SDK](06-client.md) · [lsctl](08-lsctl.md) →
