# lite_settings 开发约定

轻量配置中心。Go 1.27，MySQL/PostgreSQL 双库。设计文档 [docs/design/](docs/design/README.md)，取舍记录 [docs/decisions.md](docs/decisions.md)——**改动前先读对应章节**，里面的「不变量 / 约束」条目是硬性要求。

## 构建与测试

仓库没有 Makefile，直接用 go 工具链。`go.work` 把根 module 与 `cmd/lsctl` 串起来。

```bash
go build ./...                    # 根 module
go vet ./...
go test ./... -race               # 未设 DSN 时集成测试自动跳过
go test ./store -run TestValidate # 单个测试
gofmt -l .                        # 提交前必须为空
```

集成测试（真库）：

```bash
docker compose up -d
export LITE_TEST_MYSQL_DSN='root:lite@tcp(127.0.0.1:33061)/lite_settings?parseTime=true'
export LITE_TEST_POSTGRES_DSN='postgres://lite:lite@127.0.0.1:54321/lite_settings?sslmode=disable'
go test ./... -race
```

`cmd/lsctl` 是独立 module，可用 `GOWORK=off go build ./cmd/lsctl` 单独验证 replace 是否自洽。

改了 Go 代码就跑 test + vet + gofmt 再收工。依赖变动跑 `go mod tidy`（两个 module 都要）。

代理不通时：`export GOPROXY=https://proxy.golang.org,direct`。

## 依赖方向（不可违反）

```
cmd/lite-settings → server → store
cmd/lsctl         → store + api        (独立 module)
业务方            → client → api       (stdlib + yaml.v3)
业务方            → client/dbsource → store
```

- `client` 不得 import `server`；`api` 只放数据结构，不放行为。
- 根 module 外部依赖限于数据库 driver + `yaml.v3`；`store` 直接用 `database/sql`。新增依赖要有理由。
- CLI/TUI 依赖（cobra、bubbletea 等）只进 `cmd/lsctl/go.mod`。

## Simplify 规则

用标准库现成的能力，**不要手写等价物**，见到旧的等价写法顺手替换：

- `errors.AsType[T]` 取代 `var e *T; errors.As(err, &e)`。
- `cmp.Or` / `cmp.Or(vals...)` 取代 `if x == "" { x = def }` 的兜底链。
- `min` / `max` 内建函数，不要自写 `minInt`。
- iterator 版 `maps` / `slices` 助手：`slices.Sorted(maps.Keys(m))`、`slices.Collect`、`maps.Collect`、`slices.SortedFunc`——不要手写「取 keys 再 sort.Strings」。
- `slices.Contains` / `Index` / `Chunk` / `Compact`，`maps.Clone`，`sync.OnceValue`。
- `for i := range n` 取代 `for i := 0; i < n; i++`。
- JSON 走 `encoding/json/v2` + `jsontext`（本仓库已定型），不要混用 v1。

## 代码风格

- **导入分组**：标准库 / 第三方 / `github.com/mbeoliero/lite_settings`，三段之间空行。
- **JSON tag** 用 snake_case。
- **错误**：带上下文（key、version、方言名），暴露给 `lsctl` 的错误要能直接给人看；哨兵错误用 `errors.Is` 判定，not-found 必须归一到同一个哨兵（lsctl 退出码 4 依赖它）。
- **测试**：用 `require`，不用 `assert`。函数变量用 `Func` 后缀，不用 `Fn`。
- **导出标识符**要有注释；`main` 包不导出标识符。
- **SQL**：两库共用的 SQL 写在 `store/store.go`，只有真实方言差异才进 `Dialect`（目前 5 个方法）。不要为一处差异新增方言方法之外的分支。
- **注释解释「为什么」**，不复述代码。仓库现有注释是这个风格，跟着写。

## 组织与排序

新增代码按字母序（不要重排已有代码）：类型按名排序；结构体字段按名排序（ID 字段可置顶，mutex 保护的字段跟在 mutex 后）；构造函数紧跟类型定义；方法跟在构造函数后并按名排序，不要与其他类型的定义交错。构造结构体实例时字段也按名给。

包名小写、单数、有实指；不用 `common` / `util` / `models`。

## 测试约定（parallel test bundle）

- 每个 `TestXxx` 首行 `t.Parallel()`；每个 `t.Run` 首行也是，除非有注释说明为什么不能并行。
- `t.Parallel()` 后空一行，再写 preamble（`ctx`、`setup`），再空一行进入子测试/断言。
- 在 `TestXxx` 内定义 `type testBundle struct{...}` 与 `setup := func(t *testing.T) *testBundle`，`setup` 首行 `t.Helper()`。需要 ctx 时优先返回它，其次作为首参接收。不要在并行子测试间共享可变状态。
- 外层与内层 `t.Run` 块各自按名字母序排列。
- **带集成测试的新包必须给 `internal/testdb` 传一个新的 suffix**，落到独立数据库上——`lite_config_meta` 是全局单行水位，共库会让 revision 出现空洞，而那正是要断言的不变量。

```go
func TestThing(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	t.Run("CaseName", func(t *testing.T) {
		t.Parallel()

		bundle := setup(t)

		_ = bundle
	})
}
```

## 容易踩的核心不变量

改到相关代码前回读 [docs/design/](docs/design/README.md) 对应章节：

1. **revision 严格递增无空洞**（§4.1）——写事务首条必须是 `UPDATE lite_config_meta`，不得改用自增 ID 或 `MAX()` 当水位。
2. **watch 拉的是整组全量快照**（§4.2），没有 `WHERE revision > N` 的增量查询。
3. **先取水位、再读数据**（§8.2）——反过来会把陈旧数据标上新水位，变更被永久跳过。
4. **客户端可见水位一律取自 revisionWatcher**（§8.2），不直接查库。
5. **快照整份替换，不比较新旧水位大小**（§7.1）——加 `if new <= cur { skip }` 会让回档后永远停在旧配置。
6. **严格解码不得设为默认**（§7.3）；`client.New` 必须同步完成首拉，且首拉不触发 `OnChange`。
7. **`History` / `Rollback` 以 `lite_config_history` 为准**（§4.4），硬删后 `lite_config` 里已无该 key。
8. **PG 的 `varchar_pattern_ops` 索引不可省**（§3.3），否则 prefix 查询静默全表扫描。
9. **lsctl 双后端行为必须一致**（§9），只有 `migrate` 允许仅 DB 后端支持。
10. **lsctl JSON 输出走具名结构体**，不用 `map[string]any`（键序不稳定）。
