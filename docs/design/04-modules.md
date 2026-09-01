# 模块划分

> lite_settings 技术方案 · 第 5 章。索引见 [README.md](README.md)。

## 5. 模块划分

```
lite_settings/
├── go.work                     # 本地跨 module 开发
├── go.mod                      # github.com/mbeoliero/lite_settings
├── docs/design/       # 技术方案，按章节拆分
├── docs/decisions.md
│
├── api/                        # 线路格式：server 与 client 共用的 JSON 结构
│   └── api.go                  #   仅 stdlib，只有数据结构、没有行为
│
├── client/                     # SDK，业务方 import 此包
│   ├── client.go               #   Client（具体 struct）、Source（接口）、Option
│   ├── decode.go               #   泛型解码：json/v2 + yaml
│   ├── group.go                #   Group（具体 struct）
│   ├── source_http.go          #   长轮询 Source
│   ├── cache.go                #   不可变快照 + diff + 本地文件兜底
│   ├── errors.go               #   ErrNotFound / ErrDecode / ErrNoSnapshot
│   └── dbsource/
│       └── dbsource.go         #   DB 直连 Source
│
├── store/                      # schema · CRUD · 版本 · 回滚 · 方言
│   ├── store.go
│   ├── dialect.go
│   ├── mysql.go
│   └── postgres.go
│
├── server/                     # HTTP API + 长轮询 revisionWatcher
│   ├── server.go               #   New / Handler / Start（仅 revisionWatcher）/ Run
│   ├── revision_watcher.go
│   └── handler.go
│
├── internal/testdb/            # 集成测试的数据库隔离（见 §11.1）
│
└── cmd/
    ├── lite-settings/          # server 二进制
    └── lsctl/                  # 独立 module（go.mod + replace ../..）
        ├── main.go             #   root 命令、全局参数、后端解析、退出码
        ├── backend.go          #   Backend 接口 + errNotFound / errNotSupported
        ├── backend_http.go     #   打服务端 HTTP API
        ├── backend_db.go       #   直连数据库
        ├── cmd_read.go         #   get / list / history / diff
        ├── cmd_write.go        #   set / rm / rollback
        ├── cmd_migrate.go      #   migrate（仅 DB 后端）
        ├── cmd_ui.go           #   ui（TUI 入口、TTY 检查）
        ├── ui_model.go         #   TUI 状态：装载、导航、确认写入
        ├── ui_view.go          #   TUI 渲染：三栏布局与覆盖层
        ├── ui_edit.go          #   $EDITOR 委托
        ├── diff.go             #   LCS unified diff（无外部依赖）
        └── output.go           #   table / json / raw 渲染
```

### 5.1 依赖规则

```
cmd/lite-settings  →  server  →  store
cmd/lsctl          →  store + api              (独立 module)
业务服务            →  client                    (stdlib + yaml.v3)
业务服务            →  client/dbsource → store   (+ 数据库 driver)
```

- **`client` 不得 import `server`，也不 import `server/api`。** 两端共用的
  是一份线路协议，不是一份类型：各自定义自己那侧的结构体，用对拍测试锁住
  兼容性。SDK 因此只依赖 stdlib 与 `yaml.v3`。
- **`server/api` 只放数据结构，不放行为。** 一旦它开始持有逻辑，server 与
  它的使用方就会通过它耦合。
- **`cmd/lsctl` 是独立 module**，其 CLI 与 TUI 依赖（cobra、bubbletea 等）不进入根 module，业务方的依赖图不受影响。它用 `replace ../..` 指向根 module，`GOWORK=off` 也能单独构建。
- 根 module 的外部依赖限于：数据库 driver、`yaml.v3`。`store` 直接用
  `database/sql`。

---

---

← [核心机制](03-mechanics.md) · [store](05-store.md) →
