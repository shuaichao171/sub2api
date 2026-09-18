# x-codex-turn-state 门票（292 打票）

对 ChatGPT OAuth 账号的门控模型（默认 `gpt-6-astra`、`gpt-5.6-sol`），上游要求请求携带
`x-codex-turn-state` 回执（292 字符、`gAAAAA` 前缀）。本功能在后台自动「打票」：
用账号身份、经专用打票代理、模拟官方 Codex CLI 发最小探测请求，从**响应头**捕获门票，
存入内存缓存与 `accounts.extra`（键前缀 `codex_turn_ticket:`），并在业务请求出站时注入该头。

> 默认**关闭**。关闭时网关行为与上游版本完全一致：不打票、不注入、不拦截。

## 开启步骤（管理后台，热生效）

1. **系统设置 → 网关 → Codex 设置**，打开「292 打票」。
2. 填写「292 打票代理」（必填，不填则永不探测）：`http://` 或 `socks5h://` 完整 URL，
   可含用户名密码；语法校验要求无 path/query/fragment。
3. 保存后约 5 秒（设置缓存 TTL）内生效，下个探测周期开始打票。

### 打票代理的语义

| 输入 | 行为 |
| --- | --- |
| 完整代理 URL | 保存并立即用于后续探测 |
| 留空保存 | 保持已保存值不变（界面回显的是密码打码值） |
| 输入 `none` 保存 | **清除**已保存的代理（若配置文件也未配置，停止打票） |

- 密码永不回显：读取设置时代理密码以 `***` 打码；整段粘贴新 URL 才会覆盖。
- 打票流量与业务流量彻底分离：探测走专用不复用连接的 transport，不占账号业务代理。
- 出口 IP 质量（住宅/机房）与地区（OpenAI 不服务 HK/RU）决定成败，代理服务商负责轮换。

## 行为细节

- **探测节奏**：默认每 6 秒一个检查周期；某 (账号,模型) 已有有效且未临近过期（默认到期前
  10 分钟）的票则跳过。TTL 默认 3600 秒，即每账号每模型约 1 发/50 分钟。
- **并发上限**：单周期最多 8 个并发外呼（`openAICodexTicketMaxConcurrentProbes`），
  避免大量账号无票时瞬间打满代理出口。
- **连败退避**：打不到票（token 失败/网络错误/非 200/长度不合格）的 key 按 30s 起步、
  每连败翻倍、封顶 15 分钟退避，退避期内周期跳过，避免空转重试消耗账号请求。
- **账号范围（plan 过滤）**：只对「已确认付费套餐」的 ChatGPT OAuth 账号生效（凭据
  `plan_type` 非 `free`/空/`abnormal`，由 OAuth 刷新自动保鲜）。免费号、未知套餐号、
  SetupToken 号、shadow 子账号**既不打票探测、也不做 fail-closed 拦截**，与功能关闭时行为一致。
- **fail_closed（默认开）**：开启门票功能后，无有效票的门控模型账号会被暂停调度
  （调度 fastpath 与出站注入两侧判定一致，compact 请求按实际出站模型口径判定）。
  想要「无票也放行」：配置文件 `gateway.openai_codex_ticket.fail_closed: false` 后重启。
- **探测伪装**：自动按模型抬升客户端身份（gpt-6/astra 系要求 `version >= 0.153.4`
  的 codex-tui UA + originator），其余复用账号既有身份头。

## 配置项（config.yaml，均有默认值）

```yaml
gateway:
  openai_codex_ticket:
    enabled: false                 # 总开关（后台设置项可覆盖，热生效）
    target_length: 292
    ttl_seconds: 3600
    refresh_before_seconds: 600
    harvest_proxy_url: ""          # 后台设置项可覆盖，热生效
    harvest_probe_interval_seconds: 6
    harvest_attempt_timeout_seconds: 25
    fail_closed: true
    models: [gpt-6-astra, gpt-5.6-sol]
```

数据库设置键：`openai_codex_ticket_enabled`、`openai_codex_ticket_harvest_proxy_url`。

## 观测与排障

- 账号列表「Codex 292 门票」列：各模型 ready/剩余秒数；启用拦截时无票模型显示 blocked。
- 日志（`docker logs sub2api | grep openai_codex_ticket`）：
  - `harvester started`：进程启动即有（功能未开启时不探测，属正常）。
  - `probe cycle probed=N throttled=M`：每周期外呼数与退避跳过数。
  - `harvested`：命中；`probe miss`（带 `reason=token|error|http|len`）：未命中。
- 门票是 1 小时 TTL 的临时凭据：账号导出已自动脱敏；管理员编辑账号不会误删正在续期的票
  （仓库层在行锁下合并保留）。
