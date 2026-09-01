# 数据模型

> lite_settings 技术方案 · 第 3 章。索引见 [README.md](README.md)。

## 3. 数据模型

三张表。列名用 `config_key` 而非 `key`（`KEY` 是 MySQL 保留字）。

### 3.1 表职责

| 表 | 职责 | 特性 |
|---|---|---|
| `lite_config` | 配置当前态 | 读路径唯一来源；prefix 查询在此 |
| `lite_config_history` | 每次变更的完整快照 | **只追加，永不修改或删除** |
| `lite_config_meta` | 全局变更水位 | **恒定单行**（`id = 1`） |

### 3.2 MySQL DDL

```sql
CREATE TABLE lite_config (
  config_key  VARCHAR(191) NOT NULL,
  value       LONGTEXT     NOT NULL,
  format      VARCHAR(16)  NOT NULL DEFAULT 'raw',   -- raw | json | yaml
  updated_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  updated_by  VARCHAR(64)  NOT NULL DEFAULT '',
  PRIMARY KEY (config_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE lite_config_history (
  id          BIGINT       NOT NULL AUTO_INCREMENT,
  config_key  VARCHAR(191) NOT NULL,
  value       LONGTEXT     NOT NULL,
  format      VARCHAR(16)  NOT NULL DEFAULT 'raw',
  version     BIGINT       NOT NULL,                 -- 该 key 的第几版，从 1 开始
  op          VARCHAR(8)   NOT NULL,                 -- set | delete | rollback
  comment     VARCHAR(255) NOT NULL DEFAULT '',
  created_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  created_by  VARCHAR(64)  NOT NULL DEFAULT '',
  PRIMARY KEY (id),
  UNIQUE KEY uk_key_version (config_key, version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE lite_config_meta (
  id        TINYINT NOT NULL,
  revision  BIGINT  NOT NULL DEFAULT 0,
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT INTO lite_config_meta (id, revision) VALUES (1, 0);
```

### 3.3 PostgreSQL DDL

```sql
CREATE TABLE lite_config (
  config_key  VARCHAR(191) PRIMARY KEY,
  value       TEXT         NOT NULL,
  format      VARCHAR(16)  NOT NULL DEFAULT 'raw',
  updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
  updated_by  VARCHAR(64)  NOT NULL DEFAULT ''
);

CREATE INDEX idx_lite_config_prefix
  ON lite_config (config_key varchar_pattern_ops);

CREATE TABLE lite_config_history (
  id          BIGSERIAL PRIMARY KEY,
  config_key  VARCHAR(191) NOT NULL,
  value       TEXT         NOT NULL,
  format      VARCHAR(16)  NOT NULL DEFAULT 'raw',
  version     BIGINT       NOT NULL,
  op          VARCHAR(8)   NOT NULL,
  comment     VARCHAR(255) NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
  created_by  VARCHAR(64)  NOT NULL DEFAULT '',
  UNIQUE (config_key, version)
);

CREATE TABLE lite_config_meta (
  id        SMALLINT PRIMARY KEY,
  revision  BIGINT NOT NULL DEFAULT 0
);

INSERT INTO lite_config_meta (id, revision) VALUES (1, 0) ON CONFLICT DO NOTHING;
```

> **约束｜PG 的 `varchar_pattern_ops` 索引不可省。**
> 非 C collation 下 `LIKE 'prefix%'` 不会走普通 btree 索引。prefix 拉取是主查询路径，缺这个索引会静默退化为全表扫描。MySQL 的主键前缀匹配无此问题。

### 3.4 字段归属

`lite_config` 只有 5 列，不含 `version`、`revision`、`deleted`。

| 字段 | 所在表 | 用途 |
|---|---|---|
| `format` | `lite_config`、`lite_config_history` | SDK 解码时选择 json / yaml / raw |
| `version` | 仅 `lite_config_history` | 回滚目标标识、历史列表展示。写入时取 `MAX(version)+1` |
| `revision` | 仅 `lite_config_meta` | 全局变更水位，watcher 的唯一心跳依据 |

---

---

← [目标与架构](01-overview.md) · [核心机制](03-mechanics.md) →
