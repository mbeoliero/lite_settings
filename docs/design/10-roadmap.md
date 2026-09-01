# 实施与演进

> lite_settings 技术方案 · 第 11、12 章。索引见 [README.md](README.md)。

## 11. 实施计划

| # | 模块 | 工作量 | 依赖 | 状态 |
|---|---|---|---|---|
| 1 | `store` + 方言 + migrate | 1 天 | — | 已完成 |
| 2 | `server` + revisionWatcher | 0.5 天 | 1 | 已完成 |
| 3 | `client`（泛型 + 双 Source） | 1 天 | 1 | 已完成 |
| 4 | `lsctl` 非交互命令 | 0.5 天 | 1 | 已完成 |
| 5 | `lsctl` TUI | 1.5 天 | 4 | 已完成 |
| 6 | 双库测试矩阵 | 1 天 | 1–4 | 已完成（随 1–5 落地）|

合计约 5.5 天。第 5 项为独立增量，可推迟或裁剪而不影响其余模块交付。

### 11.1 测试

单测覆盖纯逻辑（校验、LIKE 转义、revisionWatcher 的唤醒语义）；集成测试跑在真库上，
由 `LITE_TEST_MYSQL_DSN` / `LITE_TEST_POSTGRES_DSN` 门控，未设置则跳过。
起库见 `docker-compose.yml`。

```
docker compose up -d
export LITE_TEST_MYSQL_DSN='root:lite@tcp(127.0.0.1:33061)/lite_settings?parseTime=true'
export LITE_TEST_POSTGRES_DSN='postgres://lite:lite@127.0.0.1:54321/lite_settings?sslmode=disable'
go test ./... -race
```

> **约束｜每个测试包必须落在独立的数据库上。**
> `lite_config_meta` 是全局单行水位，而 `go test ./...` 并行跑各个包：
> 任何跨包的并发写入都会在别的包眼里表现成 revision 空洞，而那正是
> `store` 要断言的核心不变量。`internal/testdb` 按包名派生库名来做这个隔离，
> 新增带集成测试的包时传一个新的 suffix 即可。

---

## 12. 后续演进

| 优先级 | 项 | 说明 |
|---|---|---|
| 1 | CAS 发布 | `lite_config` 增加 `version` 列，`UPDATE ... WHERE version = ?` 防并发覆盖 |
| 2 | 鉴权与审计 | 在静态 token 之上做按 prefix 授权 |
| 3 | Web UI | 复用现有 HTTP API |
| 4 | history 归档 | 高频变更的 key 历史无限增长，需按时间或条数截断 |
| 5 | 配置 schema 校验 | key 关联 JSON Schema，写入时做语义校验 |

---

← [故障与降级](09-operations.md)
