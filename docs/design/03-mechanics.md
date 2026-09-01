# 核心机制

> lite_settings 技术方案 · 第 4 章。索引见 [README.md](README.md)。

## 4. 核心机制

### 4.1 全局 revision 与写事务

所有写操作（set / delete / rollback）走同一个事务模板：

```sql
BEGIN;
  UPDATE lite_config_meta SET revision = revision + 1 WHERE id = 1;
  SELECT COALESCE(MAX(version), 0) FROM lite_config_history WHERE config_key = ?;
  <UPSERT lite_config 或 DELETE FROM lite_config>
  INSERT INTO lite_config_history (...) VALUES (...);
COMMIT;
```

> **不变量｜`revision` 严格按提交顺序递增且无空洞。**
> 首条 `UPDATE` 持有 `id=1` 行的排他锁直到 commit，因此所有写入被串行化。
>
> 这条不变量是 watcher 正确性的基础：客户端只依据 revision 判断"变没变"。若改用自增主键或 `MAX()` 聚合作为水位，并发事务下号码可能乱序可见，会导致变更被永久跳过。
>
> 代价是写入串行化。配置写入是人工低频操作，无实际影响。

### 4.2 变更感知：prefix 全量拉取

**watch 的单位是 prefix，不是 key。**

```
心跳:  SELECT revision FROM lite_config_meta WHERE id = 1     -- 单行主键查找
变化 → SELECT config_key, value, format FROM lite_config
       WHERE config_key LIKE 'prompt_group:%'                 -- 整组全量
     → 本地整组替换
```

不存在 `WHERE revision > N` 的增量查询。由此得到两个性质：

- **每次拉取都是自洽的完整快照。** 漏掉一次通知只导致延迟一个轮询周期，下一轮自动修正，不会丢失变更。
- **删除被天然感知。** 客户端拉到 9 条而本地缓存有 10 条，自行 diff 即可得知删除，无需软删标记。

已知取舍：任一 key 变更都会推高全局 revision，导致所有客户端重拉各自的 prefix。配置变更低频、单组数据量小，该开销可忽略。

### 4.3 版本与回滚

`version` 按 key 独立计数，从 1 开始，每次写入 +1。

**`Rollback(key, toVersion)` 不是特殊存储操作**：读出 history 中该版本的 value，走普通写入路径，仅将 `op` 标记为 `rollback`。

> **不变量｜history 只追加，从不倒退。**
> 回滚本身产生一个新版本。因此"回滚后再滚回去"天然可用，无需额外逻辑。

### 4.4 删除

硬删除：`DELETE FROM lite_config`，同时向 history 追加一条 `op='delete'` 记录（含完整 value）。

误删恢复即回滚到删除前的版本。

> **实现注意｜`History` 与 `Rollback` 必须以 `lite_config_history` 为准。**
> 硬删后 `lite_config` 中已无该 key，但 history 中仍有。这两个操作不得先查 `lite_config` 确认 key 存在。

---

---

← [数据模型](02-data-model.md) · [模块划分](04-modules.md) →
