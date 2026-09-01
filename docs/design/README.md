# lite_settings 技术方案

轻量级配置中心。

- module: `github.com/mbeoliero/lite_settings`
- Go: **1.27**（依赖 generic methods、`encoding/json/v2`）
- 存储: MySQL / PostgreSQL
- 备选方案的取舍记录见 [../decisions.md](../decisions.md)

---

按章节拆分，改动前只读相关章节即可。

| 章 | 内容 | 文件 |
|---|---|---|
| 1-2 | §1 目标与非目标、§2 总体架构 | [目标与架构](01-overview.md) |
| 3 | §3 数据模型 | [数据模型](02-data-model.md) |
| 4 | §4 核心机制 | [核心机制](03-mechanics.md) |
| 5 | §5 模块划分 | [模块划分](04-modules.md) |
| 6 | §6 store | [store](05-store.md) |
| 7 | §7 client SDK | [client SDK](06-client.md) |
| 8 | §8 server | [server](07-server.md) |
| 9 | §9 lsctl | [lsctl](08-lsctl.md) |
| 10 | §10 故障与降级 | [故障与降级](09-operations.md) |
| 11-12 | §11 实施计划、§12 后续演进 | [实施与演进](10-roadmap.md) |
