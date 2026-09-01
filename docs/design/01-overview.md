# 目标与架构

> lite_settings 技术方案 · 第 1、2 章。索引见 [README.md](README.md)。

## 1. 目标与非目标

### 1.1 目标

- 配置项 = 一个 key 对应一个 Value。Value 可以是整份 YAML/JSON，也可以是 bool/int/string 标量
- 两种接入模式，SDK 上层 API 完全一致：
  - **Server 模式** —— 业务通过 HTTP 长轮询接入配置服务
  - **DB 直连模式** —— 不部署配置服务，SDK 直连数据库
- 按 prefix 成组拉取与监听
- 版本历史与回滚
- 同时支持 MySQL 与 PostgreSQL

### 1.2 非目标（v1 不做）

命名空间与多环境（一个环境一套库，服务不感知环境概念）、Web UI、鉴权体系、key 级增量推送、灰度发布、配置继承。

---

## 2. 总体架构

SDK 与 Server 共用同一个 `store` 包，共用同一套 schema。两种接入模式的差异收敛在 `Source` 接口的两个实现上。

```
                       ┌──────────────┐
                       │    Client    │   缓存 · Watch 回调 · 重试 · 冷启动兜底
                       └───────┬──────┘
                               │  Source 接口
                   ┌───────────┴───────────┐
              httpSource                dbSource
           长轮询 server              轮询 lite_config_meta
                   │                       │
            ┌──────┴──────┐                │
            │   Server    │                │
            │  watcher+API │                │
            └──────┬──────┘                │
                   └───────────┬───────────┘
                               │
                          ┌────┴────┐
                          │  store  │
                          └────┬────┘
                        MySQL / PostgreSQL
```

---

---

[数据模型](02-data-model.md) →
