# store

> lite_settings 技术方案 · 第 6 章。索引见 [README.md](README.md)。

## 6. store

### 6.1 接口

```go
type Store interface {
    Revision(ctx context.Context) (int64, error)
    Get(ctx context.Context, key string) (*Config, error)
    ListPrefix(ctx context.Context, prefix string) ([]Config, error)
    // ListPrefixes 一次查询取多前缀并集：多次 ListPrefix 之间的写入
    // 会混进同一份快照，而客户端是整组替换的（§4.2）。
    ListPrefixes(ctx context.Context, prefixes []string) ([]Config, error)
    Set(ctx context.Context, key, value string, format Format, c Change) (Result, error)
    Delete(ctx context.Context, key string, c Change) (Result, error)
    History(ctx context.Context, key string, limit int) ([]HistoryEntry, error)
    Rollback(ctx context.Context, key string, toVersion int64, c Change) (Result, error)
}

type Format string   // FormatRaw | FormatJSON | FormatYAML
type Change struct{ Comment, Author string }
type Result struct{ Version, Revision int64 }
```

三个写方法都返回 `Result`。`Version` 是该 key 的第几版、`Revision` 是写入后的全局水位，
两者都取自同一个事务：调用方要拿它们做回执与审计（`lsctl` 的 `version=N revision=M`
就是它），事后再查一次既多一轮往返，也可能读到别人的写入。

审计信息收进 `Change` 而不是摊成两个 string 形参：三个写方法都要带它，
摊开的话每处都是一串同型参数，`Set(ctx, k, v, f, "调整超时", "jaken")` 里
哪个是 comment、哪个是 author，读的人得回去翻签名。

### 6.2 写入前校验

`ValidateValue` 先卡大小（`MaxValueSize`），再按 format 做语法层校验：
`json` 走 `jsontext` 解码器，`yaml` 走 `yaml.Unmarshal` 到 `yaml.Node`，`raw` 不校验。
不合法一律裹 `ErrInvalidValue` 返回。

服务端不掌握业务 schema，只能校验语法；但这已经足以把格式写错的配置拦在入库之前，
好过入库之后让所有客户端一起解析失败。

JSON 这一路没有用 `jsontext.Value.IsValid()`，而是逐 token 走一遍 `Decoder`，
为的是两件 `IsValid` 给不了的事：

- **带位置的错误。** `missing value after object name within "/temperature" after offset 15`
  会一路透传到 `lsctl` 的使用者手上，而 `IsValid` 只回一个 bool。
- **拒绝多文档。** `Decoder` 默认按 JSON 流工作，`{"a":1}{"b":2}` 在它眼里是两个合法值。
  一个配置项必须恰好是一个文档，读完首个值后再读到东西就报 trailing data——
  那多半是复制粘贴事故。

### 6.3 方言抽象

本包的 SQL 一律按 MySQL 的 `?` 占位符书写，`Rebind` 负责翻成目标库的写法。
除此之外，方言接口仅覆盖四处真实差异：

```go
type Dialect interface {
    Name() string             // 方言名，用于诊断输出
    Rebind(query string) string // ? → ?（MySQL）/ $1、$2……（PostgreSQL）
    DDL() []string            // 建表语句（PG 版含 varchar_pattern_ops 索引），需可重复执行
    UpsertConfig() string     // ON DUPLICATE KEY UPDATE  |  ON CONFLICT DO UPDATE
    PrefixCondition() string  // prefix 匹配的 WHERE 条件，参数为 likePrefix 处理过的模式串
    BumpRevision(ctx context.Context, tx *sql.Tx) (int64, error)
}
```

其余 SQL 两库共用。

`BumpRevision` 是方法而不是 SQL 字符串：PostgreSQL 用 `UPDATE ... RETURNING`
一条语句就能抬水位并取回新值，MySQL 必须拆成 UPDATE + SELECT 两条。
返回字符串的话，这个"一条还是两条"的差异就没地方安放。

---

---

← [模块划分](04-modules.md) · [client SDK](06-client.md) →
