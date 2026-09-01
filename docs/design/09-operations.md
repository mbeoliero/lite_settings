# 故障与降级

> lite_settings 技术方案 · 第 10 章。索引见 [README.md](README.md)。

## 10. 故障与降级

| 场景 | 行为 |
|---|---|
| server / DB 不可用 | 继续使用内存中最后一份快照，后台重试，业务不受影响 |
| 首次启动即无法获取配置 | 默认快速失败；配置了 `WithFallbackFile` 则以落盘快照冷启动 |
| 长轮询超时 | 正常路径，立即重连 |
| revision 回退（更换数据库） | 检测到 `newRev < currentRev` 时全量重拉 |
| 配置格式非法 | 写入时被语法校验拦截，不入库 |

---

---

← [lsctl](08-lsctl.md) · [实施与演进](10-roadmap.md) →
