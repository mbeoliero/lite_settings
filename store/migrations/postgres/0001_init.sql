-- lite_settings 初始 schema (PostgreSQL)。可重复执行。

CREATE TABLE IF NOT EXISTS lite_config (
  config_key  VARCHAR(191) PRIMARY KEY,
  value       TEXT         NOT NULL,
  format      VARCHAR(16)  NOT NULL DEFAULT 'raw',
  updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
  updated_by  VARCHAR(64)  NOT NULL DEFAULT ''
);

-- 不可省。非 C collation 下 LIKE 'prefix%' 不会走普通 btree 索引，
-- 而 prefix 拉取是客户端的主查询路径，缺这个索引会静默退化成全表扫描。
CREATE INDEX IF NOT EXISTS idx_lite_config_prefix
  ON lite_config (config_key varchar_pattern_ops);

CREATE TABLE IF NOT EXISTS lite_config_history (
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

CREATE TABLE IF NOT EXISTS lite_config_meta (
  id        SMALLINT PRIMARY KEY,
  revision  BIGINT NOT NULL DEFAULT 0
);

INSERT INTO lite_config_meta (id, revision) VALUES (1, 0)
  ON CONFLICT (id) DO NOTHING;
