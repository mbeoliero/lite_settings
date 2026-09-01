# client SDK

> lite_settings 技术方案 · 第 7 章。索引见 [README.md](README.md)。

## 7. client SDK

### 7.1 类型结构

Go 1.27 规定：**接口方法不能声明类型参数，泛型方法也不能实现接口方法。** 因此多态置于 `Source` 层，`Client` 与 `Group` 为具体类型。

```go
package lite

// PollRequest 承载一次拉取的全部参数。用结构体而非位置参数：
// 两种 Source 关心的字段不同，且加字段不会破坏已有实现。
type PollRequest struct {
    Since    int64         // -1 表示无基线，Source 必须立刻返回全量
    Prefixes []string      // 空表示全量
    Interval time.Duration // DB 直连的水位检查间隔
    Timeout  time.Duration // 本次愿意挂起的上限
}

// Snapshot / Config 定义在 client 包内，第三方实现 Source 时只依赖 SDK；
// server/api 保留一份字段兼容的类型，业务方不必因此引入 server。
// 多态层。仅非泛型方法。
type Source interface {
    // 返回 (nil, nil) 表示时限内无变更——正常情况，客户端不因此退避。
    Poll(ctx context.Context, req PollRequest) (*Snapshot, error)
    Close() error
}

// 具体类型，可声明泛型方法
type Client struct {
    src  Source
    snap atomic.Pointer[snapshot]
}

func New(src Source, opts ...Option) (*Client, error)   // 同步完成首拉

func (c *Client) Get[T any](key string) (T, error)
func (c *Client) GetOr[T any](key string, def T) T
func (c *Client) Raw(key string) (string, bool)
func (c *Client) Revision() int64
func (c *Client) Group(prefix string) Group
func (c *Client) Watch(prefix string, fn func(Group))
func (c *Client) Close() error

// Group 绑定取用时的那份快照，值语义、只读、并发安全。
type Group struct{ ... }
func (g Group) Get[T any](key string) (T, error)
func (g Group) GetOr[T any](key string, def T) T
func (g Group) Raw(key string) (string, bool)
func (g Group) Keys() []string   // 相对 key，可直接喂回 Get
func (g Group) Len() int
func (g Group) Revision() int64
```

> **约束｜`New` 必须同步完成首拉后再返回。**
> 否则紧跟 `New` 之后的 `Get` 读到的是空快照，是个必踩的坑。
> 首拉失败时依次尝试：`WithFallbackFile` 的落盘快照 → `WithStartupDegrade` 的空快照 → 快速失败。

> **约束｜首拉不得触发 `OnChange`。**
> 此刻 `New` 尚未返回，回调闭包拿不到 client 句柄，在回调里读别的配置就是空指针。
> "第一份快照"不是变更；要拿初值请用 `Watch`，它在注册时同步回调一次。

> **约束｜快照必须整份替换，不得比较新旧水位大小。**
> 每次拉的都是完整快照，水位倒退（数据库从备份恢复）时直接替换就是正确行为。
> 加一个 `if new <= cur { skip }` 的"优化"会让客户端在回档后永远停在旧配置上。

### 7.2 用法

```go
// Server 模式
// 构造函数放在 client/httpsource，不是 lite.HTTP：httpsource 依赖 lite，
// 反向再引一次就成了 import 环。
c, err := lite.New(httpsource.New("http://cfg:8080"),
    lite.WithPrefixes("prompt_group:", "feature:"))

// DB 直连模式
// driver 必须显式给：*sql.DB 反查不到驱动名，而 store 要靠它选方言。
src, err := dbsource.New(db, "mysql")  // 也可 dbsource.Wrap(st) / dbsource.Open(driver, dsn)
c, err := lite.New(src,
    lite.WithPrefixes("prompt_group:"),
    lite.WithPollInterval(3*time.Second))

// 两种模式之后的 API 一致
timeout, _ := c.Get[time.Duration]("http:timeout")
debug      := c.GetOr("feature:debug", false)        // T 由默认值推断
cfg, _     := c.Get[PromptConfig]("prompt_group:main")

g := c.Group("prompt_group:")
sys, _ := g.Get[string]("system")

c.Watch("prompt_group:", func(g lite.Group) { reload(g) })
```

### 7.3 解码

标量类型直接解析原始文本；复合类型按 `format` 走 json/v2 或 yaml。

```go
func decode[T any](raw, format string, strict bool) (T, error) {
    var v T
    // 类型开关比较精确动态类型，每种标量各占一个 case
    // （`case *int, *int64` 会让 p 退化成 any，不能赋值）。
    switch p := any(&v).(type) {
    case *string:        *p = raw; return v, nil
    case *bool:          ...
    case *int:           ...
    case *int64:         ...
    case *float64:       ...
    case *time.Duration: ...
    }
    switch format {
    case "json": return v, jsonv2.Unmarshal([]byte(raw), &v, jsonOpts(strict)...)
    case "yaml": return v, yamlUnmarshal([]byte(raw), &v, strict)
    }
    return v, ErrDecode  // raw 格式解不到复合类型
}
```

**严格解码默认关闭。** `WithStrictDecode()` 开启 `json/v2` 的 `RejectUnknownMembers`。

> **约束｜不得将严格解码设为默认。**
> 为服务新版本增加配置字段后，尚在运行的旧实例不认识该字段，严格模式下整份配置解析失败，导致旧实例无法加载配置。滚动发布期间会造成事故。

### 7.4 Options

| Option | 默认值 | 说明 |
|---|---|---|
| `WithPrefixes(...string)` | 无 | 声明关心的 prefix，后台仅拉取这些组 |
| `WithPollInterval(d)` | `1s` | DB 直连模式轮询间隔 |
| `WithLongPollTimeout(d)` | `30s` | HTTP 模式挂起上限 |
| `WithFallbackFile(path)` | 关 | 快照落盘，用于冷启动兜底 |
| `WithStrictDecode()` | 关 | 拒绝配置中的未知字段 |
| `WithOnChange(fn)` | 无 | 变更回调，参数为 prefix 与变更的 key 列表 |
| `WithOnError(fn)` | 无 | 后台拉取失败回调，用于打点告警 |
| `WithStartupTimeout(d)` | `10s` | `New` 同步首拉的等待上限 |
| `WithStartupDegrade()` | 关 | 首拉失败时带空快照启动，而不是让 `New` 报错 |
| `WithLogger(*slog.Logger)` | 丢弃 | |

### 7.5 解码缓存

`Get[T]` 的结果按 `(key, T)` 缓存在快照对象上。快照不可变，所以缓存既不需要 TTL 也不需要主动失效——换快照就是换缓存。热路径上的 `Get` 因此从"每次重解一遍 JSON/YAML"降到一次 map 查找。

失败结果同样缓存：一条解不动的配置在快照不变期间会被反复读到，重解每次都会失败，只是白烧 CPU。

> **约束｜`Get` 的返回值必须当只读的用。**
> `T` 是指针、切片或 map 时，同一份快照上的多次 `Get` 返回同一个对象。要改先自己拷一份。

### 7.6 可观测性

配置源断了之后，客户端会一直拿着最后那份快照安静地跑下去——从读取侧完全看不出异常。`Client.Stats()` 暴露 `Revision` / `LastSuccess` / `ConsecutiveFail` / `LastErr`，把 `time.Since(LastSuccess)` 打成指标才能在配置停止更新时报出来。

退避带 ±25% 抖动：否则一批同时启动的实例会在配置源恢复的同一瞬间一起重连，把刚起来的服务再打下去。长轮询连接另设 15s TCP KeepAlive，让被中间设备静默丢弃的半开连接尽早变成一个明确的错误，而不是干等到 ctx 到期。

---

---

← [store](05-store.md) · [server](07-server.md) →
