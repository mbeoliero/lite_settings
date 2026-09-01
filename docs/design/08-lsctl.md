# lsctl

> lite_settings 技术方案 · 第 9 章。索引见 [README.md](README.md)。

## 9. lsctl

独立 module。同时支持 `--server <url>` 与 `--dsn <dsn>` 两种后端，未部署 server 的场景亦可发布配置。
两者只能给一个；也可用环境变量 `LSCTL_SERVER` / `LSCTL_DSN`。

> **约束｜两种后端必须给出一致的行为。**
> lsctl 承诺"有没有部署 server 都是同一个工具"。差异只允许出现在
> `migrate` 上——建表需要数据库权限，服务端不提供该接口。
> 其余每条命令都由双后端对拍测试守住（§11.1）。

### 9.1 命令

```
lsctl get      prompt_group:main [--version N]
lsctl list     [prompt_group: ...]
lsctl set      prompt_group:main -f main.yaml -m "调整超时" [--format] [--dry-run]
lsctl rm       prompt_group:old  [-m ...]
lsctl history  prompt_group:main [--limit N]
lsctl diff     prompt_group:main 3 [5]
lsctl rollback prompt_group:main --to 3
lsctl migrate  --dsn ... [--driver ...]
lsctl ui       [prompt_group: ...]
```

`set` 的值来自位置参数、`-f <文件>` 或 `-f -`（标准输入），三选一。
未指定 `--format` 时按来源推断：`.yaml`/`.yml` 为 yaml、`.json` 为 json，
否则看内容首字符；拿不准一律 `raw`——raw 永远能被 `GetOr[string]` 取到。

`diff` 给两个参数是"某版本 vs 当前值"，三个参数是"版本 vs 版本"。
定位版本时拉全量历史，用户既然点了版本号，就不该因为它掉出默认窗口而查不到。

### 9.2 输出与退出码

`-o table|json|raw`，留空则用各命令的默认：`get` 默认 `raw`（可直接进管道），
`list` / `history` 默认 `table`，`diff` 默认 unified diff。
写操作的回执走 stderr，stdout 保持干净。

> **约束｜JSON 输出必须走具名结构体，不得用 `map[string]any`。**
> `encoding/json/v2` 默认不对 map 的键排序，同一条命令跑两次会给出
> 不同的字节序列，黄金文件比对与 CI diff 都会随机失败。

| 退出码 | 含义 |
|---|---|
| 0 | 成功 |
| 1 | 出错 |
| 4 | 配置不存在 |

> **约束｜not-found 必须独占一个退出码。**
> `lsctl get k || 创建它` 这类脚本只该在真的"不存在"时触发。
> 若服务端 500 也归到同一个码，一次故障就会让脚本误建配置。
> 两个后端的 404 / `store.ErrNotFound` 都归一到同一个哨兵。

### 9.3 TUI

DB 直连模式下不存在 Web UI，TUI 是唯一的交互界面。主要服务三个场景：版本回滚、发布前 diff 确认、key 浏览。

```
┌ keys ────────┬ value ──────────────┬ history ──────┐
│ prompt_group:│ system: |           │ v5  2m ago  ← │
│ > main       │   你是一个...        │ v4  1h ago    │
│   fallback   │ temperature: 0.7    │ v3  昨天       │
│ feature:     │                     │               │
│   debug      │ [e]dit [d]iff       │ [enter] 回滚   │
└──────────────┴─────────────────────┴───────────────┘
```

**三条设计约束：**

1. **单屏三栏。** 不做多屏、多标签、设置页、鼠标支持。
2. **TUI 是加法而非替代。** 每个 TUI 操作必须存在等价的非交互命令，保证 CI/CD 可用。
3. **编辑委托给 `$EDITOR`。** 按 `e` 拉起外部编辑器，存盘退出后回到 TUI 展示 diff。不实现内置文本编辑组件。

**按键。** key 栏按**第一个** `:` 之前的前缀分组，组标题不可选中——
`service:db:host` 归在 `service:` 下，与 `WithPrefixes` / `lsctl list <prefix>` 的前缀是同一层含义。

| 键 | 行为 | 等价命令 |
|---|---|---|
| `j` `k` `g` `G` | 在当前栏移动光标 | — |
| `tab` `h` `l` | 切换栏 | — |
| `enter` | keys/value 栏：跳到 history 栏<br>history 栏：回滚到选中版本 | `lsctl rollback <key> --to <v>` |
| `e` | 拉起 `$EDITOR` 编辑当前值 | `lsctl set <key> -f <file>` |
| `d` | 选中版本与当前值的 diff | `lsctl diff <key> <v>` |
| `/` | 按子串过滤 key | `lsctl list <prefix>` |
| `r` | 重新装载 | — |
| `?` `q` `esc` | 帮助 / 关闭覆盖层 / 退出 | — |

> **约束｜写操作必须先看 diff、再按 `y` 确认。**
> 编辑与回滚收敛到同一条路径：渲染 diff 覆盖层 → `y` 写入、`n` 放弃。
> 确认框里一并显示等价的非交互命令，这是约束 2 在产品里的可见形式，照抄即可放进 CI/CD。
> 同理，`enter` 只在 history 栏触发回滚：回滚有副作用，不该在光标随便停在哪儿时被一个回车打出去。
> 退出 TUI 后本次会话的写入重播到 stderr——状态行随退出一起消失，
> 而"我刚才到底改了什么"是事后最常被问的问题。

> **约束｜后端响应必须按序号丢弃过期结果。**
> 按住 `j` 会连发数个详情请求，它们可能乱序返回。keys 与 detail 各持一个
> 单调递增序号，回来的消息对不上号就丢掉，否则值栏会显示成另一个 key 的内容。
> 同一个道理管着编辑：`$EDITOR` 退出时选中项若已变化，这次编辑作废。

> **约束｜配置值渲染前必须剥掉控制字符。**
> 值里混进 ESC 序列的话，直接打到终端上会篡改后续渲染乃至光标位置。
> 宽度计算一律走 `runewidth`，且样式在补齐宽度之后才施加——反过来做的话
> ANSI 转义会被算进显示宽度，三栏就再也对不齐了。

---

---

← [server](07-server.md) · [故障与降级](09-operations.md) →
