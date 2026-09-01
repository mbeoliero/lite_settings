-- lite_settings 初始 schema (MySQL / MariaDB)。可重复执行。
--
-- config_key 显式声明 COLLATE utf8mb4_bin：MySQL 8 的默认排序规则
-- utf8mb4_0900_ai_ci 不区分大小写，而 ValidateKey 允许大小写字母，
-- 默认规则下 Feature:X 与 feature:x 会命中同一主键，后写覆盖先写，
-- prefix 匹配也与 PostgreSQL（大小写敏感）不一致。
--
-- 注意：本文件全部是 CREATE TABLE IF NOT EXISTS，不会修正已建好的表。
-- 升级既有部署需手工执行一次：
--   ALTER TABLE lite_config         MODIFY config_key VARCHAR(191) NOT NULL COLLATE utf8mb4_bin;
--   ALTER TABLE lite_config_history MODIFY config_key VARCHAR(191) NOT NULL COLLATE utf8mb4_bin;
-- 执行前先确认库里不存在仅大小写不同的 key，否则 ALTER 会因主键冲突失败。

CREATE TABLE IF NOT EXISTS lite_config (
  config_key  VARCHAR(191) NOT NULL COLLATE utf8mb4_bin,
  value       LONGTEXT     NOT NULL,
  format      VARCHAR(16)  NOT NULL DEFAULT 'raw',
  updated_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  updated_by  VARCHAR(64)  NOT NULL DEFAULT '',
  PRIMARY KEY (config_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS lite_config_history (
  id          BIGINT       NOT NULL AUTO_INCREMENT,
  config_key  VARCHAR(191) NOT NULL COLLATE utf8mb4_bin,
  value       LONGTEXT     NOT NULL,
  format      VARCHAR(16)  NOT NULL DEFAULT 'raw',
  version     BIGINT       NOT NULL,
  op          VARCHAR(8)   NOT NULL,
  comment     VARCHAR(255) NOT NULL DEFAULT '',
  created_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  created_by  VARCHAR(64)  NOT NULL DEFAULT '',
  PRIMARY KEY (id),
  UNIQUE KEY uk_key_version (config_key, version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS lite_config_meta (
  id        TINYINT NOT NULL,
  revision  BIGINT  NOT NULL DEFAULT 0,
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO lite_config_meta (id, revision) VALUES (1, 0);
