# PolyPilot

事件驱动的 **Polymarket 自动化交易引擎**，专注于短周期二元预测市场（如 `btc-updown-5m`）的高频策略。
使用 Go 编写，单进程内通过事件总线串联行情订阅、概率估计、策略决策、风控校验、订单执行与状态恢复。

模块导入路径：`github.com/xiangxn/polypilot`

---

## 数据流

```
                ┌──────────────────────────┐
                │      core.EventBus       │  ← fan-out 发布订阅，每订阅者 1024 缓冲
                └──────────────────────────┘
                       ▲              ▲
   ┌───────────────────┘              │
   │ MARKET/ORDERBOOK/SIGNAL          │ EXECUTION/RISK/METRICS
   │                                  │
┌───────────┐   ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│   Feeds   │ → │ Probability  │→ │  Strategy    │→ │     Risk     │→ │   Executor   │
│ (market/) │   │(probability/)│  │ (strategy/)  │  │   (risk/)    │  │ (execution/) │
└───────────┘   └──────────────┘  └──────────────┘  └──────────────┘  └──────────────┘
                                                                            │
                                                                            ▼
                                                              ┌────────────────────────┐
                                                              │  State（仓位/余额预留）  │
                                                              │       (state/)         │
                                                              └────────────────────────┘
```

核心规则（见 `docs/flow.mmd`）：

- 引擎事件循环只处理 `MARKET` / `ORDERBOOK` / `SIGNAL` / `EXECUTION` 四类事件
- 策略生成的 `OrderIntent` 必须先过 `Risk.Check`，通过后才进入 `Executor`
- `State` 维护三级预留：`Provisional`（intent 临时占用）→ `OrderReservation`（交易所确认）→ 释放
- 通过 WS 监听 `TradeMonitor` 与 HTTP `PostOrder` 的双路确认，并用 `bufferExecution` 处理乱序

---

## 模块结构

| 目录 | 职责 |
| --- | --- |
| `main.go` | 入口，装配 Engine 并启动事件循环 |
| `core/` | 事件总线 `EventBus`、事件类型、常量（`MARKET`/`ORDERBOOK`/`EXECUTION`/`RISK`/`METRICS`） |
| `runtime/` | 引擎核心：事件分发、TTL 跟踪、订单生命周期、指标聚合；接口契约都在 `runtime/types.go` |
| `config/` | Viper + mapstructure 配置加载；支持 `.env` + `config.yaml` + AES 加密字段 |
| `market/` | 行情 Feed：`PolymarketSlugFeed`（slug 订阅）、`CryptoPriceFeed`（Chainlink）、`MockPriceFeed`（测试） |
| `state/` | 仓位 / 余额 / 订单预留管理；On-chain 余额对账（multicall3）；交易所断点恢复 |
| `strategy/` | 交易策略；当前内置 `Strategy{}`：基于 Z-Score 单边趋势止损 |
| `risk/` | 下单前风控（余额/仓位/最小预留校验） |
| `execution/` | 订单执行；`PostOrder` / `PostOrders` 批量；WS 跟踪 LIVE/CANCELED/MINED/FAILED |
| `probability/` | 价格 → 概率引擎，输出 `Observation`（含 Z-Score、imBalance、orderbook 快照） |
| `observer/` | 观察者（当前只有 `Logger`，订阅 Bus 打印结构化日志） |
| `indicators/` | `ZScore`（log return + 时间缩放）、`CalcImBalance`（盘口失衡） |
| `internal/atomicx/` | 原子 `Float64` 包装 |
| `internal/buffer/` | 线程安全 `RingBuffer`（Z 窗口） |
| `internal/multicall/` | Multicall3 批量 ERC20 余额查询 |
| `logx/` | 基于 `phuslu/log` 的异步日志 + 按日 rotate |
| `tools/` | Python 分析脚本（`exec_winrate.py`、`plot_stoploss.py`） |
| `docs/flow.mmd` | 数据流 mermaid 图 |

---

## 构建与运行

```bash
# 编译（默认 dev tag，输出 ./polypilot）
./build.sh
./build.sh polypilot linux release   # Linux release
./build.sh polypilot mac dev         # macOS dev

# 直接运行
go run .

# 测试
go test -race -cover ./...
go test -race ./runtime/... -v
```

配置：

- `.env` —— 敏感变量（私钥、API key），**不入库**
- `config.yaml` —— 业务配置（`balance_sync`、`sdk_config`、`logging` 等），**不入库**
- `PM_CONFIG_DECRYPT_PASSWORD` —— 启动密码，用于解密 `owner_key`/`clob_creds` 等加密字段；未设置时启动会交互式提示

---

## 代码 Review 记录

> 每次 review 单独成节，按"日期 → 改动建议（必须 / 强烈建议） → 优化建议（可选）"组织。
> 改动建议聚焦正确性、并发安全、状态一致性；优化建议聚焦可维护性、性能、可测试性。

### Review @ 2026-05-18

**Scope**：全量 37 个 Go 源文件（不含 `tools/` Python 脚本与外部 SDK）。

#### 🔴 改动建议（必须修复）

1. **`probability.Engine` 存在数据竞争**（`probability/engine.go`）
   - 现象：`market`、`token.items`、`signal` 等字段在 `OnUpdate` 中被串行写入（引擎事件循环单 goroutine），但 `CurrentObservation()` 会被 `runtime.handleStrategyTick` 在 **独立的 ticker goroutine** 中调用读取。无锁保护，触发 `-race` 必报错。
   - 建议：给 `Engine` 加 `sync.RWMutex`，或将所有读路径走 atomic.Value 快照模式。

2. **`Strategy.OnUpdate` 对 `Features` 做无防御类型断言**（`strategy/strategy.go:148-155`）
   - 现象：`o.Features["openPrice"].(float64)` 没有 `, ok` 检查，key 不存在或类型变化时直接 panic，整个引擎 crash。
   - 建议：所有 `Features[xxx].(T)` 改为 `v, ok := o.Features[xxx].(T); if !ok { return nil }`。

3. **`PolymarketSlugFeed.Start` 遇到拉取失败时永久退出**（`market/polymarket_slug_feed.go:73`）
   - 现象：`FetchMarketBySlug` 返回 err 时 `return`，外层 goroutine 直接结束，feed 整段 feed 流程停止，不会自愈。
   - 建议：err 时 sleep（如 5s）+ continue 重试，或者改成有最大重试次数后 publish `RISK` 事件。

4. **`probability/engine.go` 的 log 模块名错写为 "observer"**（`probability/engine.go:25`）
   - 现象：`var log = logx.Module("observer")`，应为 `"probability"`，会污染日志聚合。
   - 建议：改为 `logx.Module("probability")`。

5. **`runtime/engine.go` 用字符串比较 sentinel 错误**（`runtime/engine.go:378`）
   - 现象：`err.Error() != "order already reserved"` —— 错误信息一旦改写就静默失败。
   - 建议：在 `state` 包导出 `ErrOrderAlreadyReserved = errors.New(...)`，调用方用 `errors.Is`。

6. **`MarketQueue.Add` 接收 `*gjson.Result` 直接缓存**（`strategy/strategy.go:128` → `market_queue.go`）
   - 现象：策略把 `gjson.Result` 指针存进队列，但 `gjson.Result` 内部持有原 JSON 字符串引用。外层 `Bus.Publish` 是 fan-out，若其他订阅者修改或 GC 时序异常，可能读到不一致数据。
   - 建议：要么 deep-copy 值，要么改成只缓存需要的字段（已经在 `SlugMarket` 里抽出来了，直接用结构体即可）。

#### 🟡 强烈建议

7. **`strategy/strategy.go:54` `cfg.Sub(...)` 可能返回 nil**
   - `Unmarshal` 在 nil 上 panic。增加 nil 检查（实际看 viper 的 `Sub` 返回值，`v != nil` 后再 `Unmarshal`），并把 `Unmarshal` 的 err 落日志。

8. **`requiredCollateral` 与 `floatEpsilon` 在 `state` 和 `risk` 两个包重复实现**
   - 抽到 `core` 或单独 `pricing` 工具包，避免未来逻辑分叉。

9. **`Executor.submitSplits` / `submitMerges` 每次循环 new `RelayClient`**（`execution/executor.go:218,299`）
   - HTTP client 重建无必要开销，且 `RelayerKey` 等敏感配置每次重读。建议在 `Init` 时构造一次复用。

10. **`Probability.Engine.resetForNewMarket` 用 `sdk.DefaultConfig()` 新建 client**（`probability/engine.go:232`）
    - 与上游传入的 `Config` 不一致，导致环境变量/自定义 endpoint 在概率引擎里失效。建议通过依赖注入传入 `*sdk.PolymarketClient`。

11. **`State.ApplyFill` 中 `fillPrice <= 0` 时回退到 `res.Price`**（`state/state.go:345`，原作者已注释 "需要实测"）
    - 当前对 SELL 用 `fillPrice * filledSize` 计算 `proceeds`；若回退到挂单价格而非真实成交价，会引入余额漂移。建议加单测覆盖（实际成交价 vs 挂单价）。

12. **`EventBus.Publish` 用 `select default` 丢消息**（`core/bus.go:71-75`）
    - 对 `EXECUTION`、`RISK` 这种状态变更事件丢失会造成账本不一致。建议关键事件单独走 blocking channel，或对 dropped 增加报警阈值。

13. **`Executor.consumeExecuteQueue` 退出时未排空 queue**（`execution/executor.go:183`）
    - `ctx.Done()` 时直接 return，留在 channel 里的 batch 静默丢失。建议先尝试 drain 并 publish `Rejected("shutting down")`。

#### 🟢 优化建议（可选）

14. **`MarketQueue` 容量硬编码 `3`**（`strategy/strategy.go:59`）—— 走配置项。
15. **`PlacePrice = 0.35` 常量未使用**（`strategy/strategy.go:18`）—— 删除或改成 `default` fallback。
16. **`ZScore.OnTick` 首 tick 时 `lastSec==0` 路径丢失第一秒的 push**（`indicators/zscore.go:42-46`）—— 加单测验证窗口对齐。
17. **多处 `log.Printf` 调试代码被注释掉**（`strategy/strategy.go:105,157,177` 等）—— 用 `log.Debug()` 替代或删除。
18. **`Observer.Logger.logEvent` 用未检查的类型断言**（`observer/logger.go:42,49` 等）—— 与 Strategy 同源风险，加 `, ok` 保护。
19. **缺测试覆盖的模块**：`strategy/`、`probability/`、`market/`（除 `slugFor` 单测）、`execution/` 的 split/merge 路径、`indicators/zscore.go`。
20. **`state/state_restore_pm.go:147-151` redeem 主流程被整段注释掉**（含 `// TODO: 暂时关闭 redeem`）—— 要么补充功能开关 flag（`config.redeem.enabled`），要么删除并在 `CLAUDE.md` 记录暂停原因。
21. **`internal/multicall/multicall3.go` 的 ABI Unpack 错误被忽略**（`_ = erc20Parsed.UnpackIntoInterface(...)`）—— 至少 log，否则 balance 异常时无法诊断。
22. **`config.Load` 缺校验**：未校验 `funder_address` / `owner_key` 等关键字段在启动时是否就绪，错误延后到 runtime 才暴露。

#### Tests / Build

- 已覆盖：`risk.Check`、`state` 三级预留状态机、`runtime.handleExecutionEvent` 乱序场景、`config.Load` 加密解密、`slugFor` 时间窗口。
- 命令：`go test -race -cover ./...`（当前 review 未执行实跑，建议执行后把覆盖率截图入库）。

---

## 重构记录

### Refactor @ 2026-05-18 → 完成

**Spec**: `docs/superpowers/specs/2026-05-18-refactor-b-level.md`
**Plan**: `docs/superpowers/plans/2026-05-18-refactor-b-level.md`
**Scope**: B 级（包内重构，对外形态不变）+ 22 条 review 问题 + 10 条策略优化 + Polymarket 权威对账 + 完整测试

#### 改动一览

| 类别 | 改动 |
|---|---|
| 工程基础 | `core/errors.go` 集中 sentinel error；`core/pricing.go` 抽 `RequiredCollateral`/`FloatEpsilon`；`.golangci.yml` |
| 错误处理 | `errors.Is` 替换字符串比较；自定义 `risk.Rejection` 类型 |
| 依赖注入 | `probability.NewEngine(client)`；`Executor.relayClient` 字段一次构造 |
| 文件拆分 | `runtime/{event_handler,order_tracking,metrics}.go`；`state/{reservation,fill,reconcile,pnl,position_expiring}.go`；`execution/{placements,splits_merges,trade_events}.go`；`probability/{market_state,features,book_store}.go` |
| 风控硬墙 | `MaxDailyLoss` / `MaxExposurePerMarket` / `MaxSlippageBps` / `MaxOpenOrders` / `MarketCooldown` + 9 类 `RejectionType` |
| 持仓增强 | `TokenPosition.AvgCost` / `AvgCostKnown`；`State.UnrealizedPnL`；`EventPositionExpiring` |
| 状态机收敛 | `state.AttachOrder` / `AttachExternalOrder` 单入口；移除字符串错误比较 |
| 可观测 | `Executor.DryRun`；`EventBus.DropThreshold`；`EventReconcile` 事件 |
| Polymarket 权威对账 | `state.ReconcileWithExchange` 30s 定时 + WS 即时触发；以远端为准；外部订单 `ExternalOrigin=true` 计入风控 |
| 配置 | `risk` / `reconcile` / `redeem` 三个 section，保守默认开启；启动校验 `funder_address` / `owner_key` / `chain_id` |
| 韧性 | Feed 失败重试不退出；Executor shutdown drain；probability reset RPC 移出锁外（generation 计数器防并发竞态） |
| 测试 | 各包均补齐测试；race test 覆盖关键并发点 |

#### 收益

| 维度 | 之前 | 现在 |
|---|---|---|
| 风险 | 余额够就下单 | 5 道硬墙：daily PnL / exposure / slippage / open orders / cooldown |
| 一致性 | 本地账本 vs 远端可脱节 | 30s 定时 + WS 触发双驱动对账，远端为权威源 |
| 可观测 | 5 分钟一次 metrics log | + `UnrealizedPnL` + `DailyPnL` + `ReconcileRuns/Diffs` + `RejectionType` 枚举可聚合 |
| 可测性 | 仅 state/risk/runtime 部分覆盖 | 全包补齐；mock 接口分离；race test 系统化 |
| 可维护性 | 单文件 500–900 行 | 单文件 < 400 行；职责单一 |

#### 用户使用说明

**手动在 Polymarket 操作的兼容性**：
- 手动挂单后 ≤ 30s 本地账本自动同步（标记 `ExternalOrigin=true`），并计入 `max_open_orders` / `max_exposure_per_market` 风控限额
- 手动取消订单 ≤ 30s 本地 release
- 手动卖出仓位 ≤ 30s 本地 position 减少；本地 `AvgCost` 按比例保留；新出现的 token 标记 `AvgCostKnown=false`，不参与 `UnrealizedPnL` 计算

**新增配置项（`config.yaml`）**：

```yaml
risk:
  max_daily_loss: 20.0          # USDC，超过拒绝所有 PLACE（CANCEL 仍允许）
  max_exposure_per_market: 100  # 单 market reserved+filled 上限
  max_slippage_bps: 200         # 2%，市价单偏离 mid 拒绝
  max_open_orders: 20           # 同时挂单总数（含 ExternalOrigin）
  market_cooldown: 2s           # 同 market 两次本地 PLACE 间隔

reconcile:
  interval: 30s                 # 周期对账频率
  retry_backoff: [1s, 2s, 4s]   # 失败重试

redeem:
  enabled: false                # redeem 当前仍为 TODO 状态，需要时再开启
```

所有时间字段统一使用 **UTC+0**。

#### 已知后续工作（C 级，本次未做）

- State 持久化（SQLite）— 重启后能保留 finalized OrderID，避免 WS 重放导致重复处理
- Strategy 接口拆 Signal/Sizing/Execution 三层
- 离线回测框架（基于 historical orderbook replay）
- 持续部署 / Dockerfile / k8s 清单
