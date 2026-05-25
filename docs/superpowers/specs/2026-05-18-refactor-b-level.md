# PolyPilot B 级重构 + 策略优化 + Polymarket 权威对账设计

**Date**: 2026-05-18
**Branch**: benj
**Status**: Draft v2（待 user 审核）

---

## 1. 背景

PolyPilot 已稳定运行，但 2026-05-18 全量 review 暴露出 22 条问题（6 critical / 7 high / 9 optional），其中 `probability.Engine` 的 data race 已修复（preview commit）。剩余问题集中在：

- 公共代码重复（`requiredCollateral`/`floatEpsilon` 两份实现、Bus 丢消息、错误用字符串比较等）
- 模块耦合（Probability/Executor 内部 new sdk.Client、RelayClient 每次重建）
- 风控仅校验"当前下单合法性"，**没有**日内亏损 / 单市场敞口 / 滑点 / 同时挂单数 / 冷却期等硬墙
- 持仓只记数量不记成本，无法计算 unrealized PnL，限制了后续策略类型
- 订单状态机两条 Accepted 路径靠字符串错误比较粘合，脆弱
- **本地 state 与 Polymarket 实际状态可能脱节**：用户会手动在 Polymarket 网站下单/取消/卖出，当前 `RestoreFromExchange` 只在启动跑一次，订单和持仓没有周期对账
- 测试覆盖空白（strategy/probability/market/execution.split-merge/indicators 全无测试）

## 2. 目标

1. **修完** README 列出的全部 22 条问题（含 critical / high / optional 三档）
2. **加入** 10 条新策略优化（风控硬墙 4 条 + 持仓增强 2 条 + 状态机收敛 2 条 + 可观测 2 条）
3. **新增"Polymarket 权威对账"**：本地 state 视为缓存，远端为唯一权威源，30s 定时 + WS 即时触发对账，冲突时以远端为准
4. **测试覆盖率**：`state` / `risk` / `runtime` / `execution` ≥ 90%，其他模块 ≥ 80%
5. **对外形态不变**：`main.go` 装配方式不变，可执行二进制行为兼容（风控默认值开启可能拒绝部分原本会成功的下单，这是预期行为）

### 包内 API 变更点（不算"对外"）

- `runtime.RiskManager.Check` 签名加 `midPrices map[string]float64` 参数
- `state.Snapshot` 新增 `DailyPnL` / `OpenOrderCount` 字段
- `state.TokenPosition` 新增 `AvgCost` / `AvgCostKnown` 字段
- `state.OrderReservation` 新增 `ExternalOrigin bool` 字段
- `core.RiskEvent` 新增 `Type RejectionType` 字段（旧 `Reason` 保留）
- `core.EventType` 新增 `EventPositionExpiring` / `EventReconcile`

外部 SDK 接口 / config 字段 / main.go 装配方式不变。

### 不在范围（留给 C 级或后续）

- State 持久化（SQLite）
- Strategy 接口拆 Signal/Sizing/Execution 三层
- 离线回测框架
- 持续部署 / Dockerfile / k8s 清单

## 2.1 全局约定

- **时区**：所有时间统一使用 **UTC+0**。`dailyPnL` 跨日判断、日志时间戳、`EventReconcile.At`、`MetricsEvent.At` 等一律 `time.Now().UTC()`。本地时区（北京时间等）仅用于人类可读输出（如启动 banner），不影响业务逻辑。

## 3. 设计原则（Go 最佳实践）

- **接收 interface，返回 struct**（依赖在 caller 注入；Engine 不再 new sdk.Client）
- **小接口**（在使用方定义；不抽出"上帝接口"）
- **Sentinel error + `errors.Is/As`**（禁止字符串比较错误）
- **Zero-value useful**（State/Engine 等核心 struct 不依赖额外 Init）
- **Functional options**（新增配置时不破坏构造签名）
- **errgroup + context.Context** 用于协调 goroutine；`defer recover()` 包裹所有长 goroutine
- **文件 < 400 行**；包内文件按职责拆分
- **TDD**：先写测试 → minimal impl → refactor；表驱动 + `t.Helper`/`t.Cleanup`
- **`-race` 必跑**：所有 mutex 边界写 race test

## 4. 工程基础

### 4.1 sentinel errors（新建 `core/errors.go`）

```go
package core

import "errors"

var (
    ErrOrderAlreadyReserved    = errors.New("order already reserved")
    ErrIntentAlreadyReserved   = errors.New("intent already reserved")
    ErrReservationNotFound     = errors.New("reservation not found")
    ErrInsufficientBalance     = errors.New("insufficient available balance")
    ErrInsufficientPosition    = errors.New("insufficient token position")
    ErrBelowMinReserve         = errors.New("balance reached minimum reserve")
    ErrInvalidPrice            = errors.New("invalid price")
    ErrInvalidSize             = errors.New("invalid size")
    ErrInvalidSide             = errors.New("invalid side")
    ErrInvalidMarket           = errors.New("invalid market id")
    ErrInvalidToken            = errors.New("invalid token id")
    ErrFillExceedsRemaining    = errors.New("filled size exceeds remaining size")
    ErrFillMarketTokenMismatch = errors.New("fill market/token mismatch")
    ErrFillSideMismatch        = errors.New("fill side mismatch")
    ErrReconcileFailed         = errors.New("reconcile failed")
)
```

调用方一律 `if errors.Is(err, core.ErrOrderAlreadyReserved)`；`runtime/engine.go:378` 的字符串比较被替换。

### 4.2 公共常量与函数（新建 `core/pricing.go`）

```go
package core

const FloatEpsilon = 1e-9

func RequiredCollateral(side orders.Side, price, size float64) float64 {
    switch side {
    case orders.BUY:
        return size * price
    case orders.SELL:
        return size
    default:
        return 0
    }
}
```

`state/state.go` 和 `risk/engine.go` 的两份重复实现删除，引用 core。

### 4.3 lint（新建 `.golangci.yml`）

启用：errcheck / gosimple / govet / ineffassign / staticcheck / unused / gofmt / goimports / misspell / unconvert / unparam。本地运行 `golangci-lint run` 必须零警告。**不**加 CI（按 user 决定）。

## 5. 包内重构

### 5.1 依赖注入

| 当前 | 改后 |
|---|---|
| `probability.Engine.resetForNewMarketLocked` 内 `sdk.NewClient(sdk.DefaultConfig())` | `probability.NewEngine(client *sdk.PolymarketClient) *Engine`，client 由 main 注入 |
| `execution.Executor.submitSplits/submitMerges` 每次 new `relayer.NewRelayClient` | `Executor.relayClient` 字段，`Init` 时构造一次 |
| `market.PolymarketSlugFeed` 内部 new client | 接收 `*sdk.Client` 通过字段 |
| `strategy.NewMarketQueue(3)` 硬编码 | 走 `StrategyConfig.MarketQueueCap` |

### 5.2 文件拆分

| 当前文件 | 拆为 |
|---|---|
| `runtime/engine.go` (594 行) | `engine.go`（Start/Close）+ `order_tracking.go`（accepted/finalized/pending）+ `event_handler.go`（handleInputUpdate/handleExecutionEvent）+ `metrics.go` |
| `state/state.go` (540 行) | `state.go`（核心 struct）+ `reservation.go`（Provisional/Order 状态机 + AttachOrder）+ `fill.go`（ApplyFill + AvgCost + dailyPnL）+ `balance.go`（Reconcile USDC）+ `reconcile.go`（订单 + 持仓周期对账，见 5.4） |
| `execution/executor.go` (946 行) | `executor.go`（Init/Execute/工作队列）+ `placements.go`（submitPlacements/handlePostOrdersResults）+ `splits_merges.go`（submitSplits/submitMerges）+ `trade_events.go`（onOrderEvent/onTradeEvent + 未知 orderID 触发 reconcile） |
| `probability/engine.go` (357 行) | `engine.go`（核心 + OnUpdate 分发）+ `market_state.go`（resetForNewMarketLocked）+ `features.go`（fillFeaturesLocked）+ `book_store.go`（GetOrderBook/getBook/updateOrderBook） |

每个新文件保持单一职责，便于独立测试。

### 5.3 修 22 条已知问题

| README # | 文件 | 改动 |
|---|---|---|
| #1 race | probability/engine.go | ✅ 已修（preview commit） |
| #2 Features 类型断言 | strategy/strategy.go | 全部加 `, ok := v.(float64); if !ok { return nil }` |
| #3 Feed 失败永久退出 | market/polymarket_slug_feed.go | err 时 sleep 5s + continue，N 次后 publish RISK |
| #4 log 模块名错写 | probability/engine.go | `"observer"` → `"probability"` |
| #5 字符串比较错误 | runtime/engine.go:378 | `errors.Is(err, core.ErrOrderAlreadyReserved)` |
| #6 MarketQueue 缓存 gjson.Result 指针 | strategy/market_queue.go | 改成缓存 `SlugMarket` 结构体值 |
| #7 viper.Sub nil panic | strategy/strategy.go | nil 检查 + `Unmarshal` err 落日志 |
| #8 requiredCollateral 重复 | core/pricing.go | 抽出（见 4.2） |
| #9-#10 RelayClient/sdk.Client 复用 | execution/probability | 依赖注入（见 5.1） |
| #11 ApplyFill 回退价格 | state/fill.go | 加单测覆盖 + 注释清楚行为 |
| #12 Bus drop 关键事件 | core/bus.go | 加 dropped 阈值告警（见 9.2） |
| #13 shutdown 未 drain | execution/executor.go | drain + Rejected("shutting down") |
| #14 MarketQueue 容量硬编码 | strategy | 走配置（见 5.1） |
| #15 PlacePrice 未使用常量 | strategy/strategy.go | 删除 |
| #16 zscore first-tick 边界 | indicators/zscore.go | 加测试覆盖 |
| #17 注释掉的 log.Printf | strategy/probability 多处 | 改 `log.Debug()` 或删除 |
| #18 Observer 无防御类型断言 | observer/logger.go | 加 `, ok` 保护 |
| #19 测试缺失 | 全项目 | 见 Section 10 |
| #20 redeem TODO 注释 | state/state_restore_pm.go | 加 `config.redeem.enabled` flag，禁用时跳过 |
| #21 ABI Unpack err 忽略 | internal/multicall/multicall3.go | log + 返回 err |
| #22 config.Load 缺关键字段校验 | config/config.go | 启动校验 funder_address / owner_key 等 |

### 5.4 Polymarket 权威对账（重点新增）

**设计目标**：本地 state 是缓存，Polymarket 是唯一权威源。用户手动在 Polymarket 网站下单/取消/卖出后，系统在数秒内发现并同步本地账本。

#### 5.4.1 触发方式

- **30s 定时 tick**（兜底）：State 内部启动一个 goroutine，30s 跑一次全量对账
- **WS 即时触发**（快速响应）：`Executor.onOrderEvent` / `onTradeEvent` 收到事件后，如果 orderID 不在 `tracked` map 中（说明是外部订单），立即向 State 投递一个 reconcile 请求（非阻塞 channel，去重 1s 内同一信号）
- **启动时全量**（已有）：`RestoreFromExchange` 不变

#### 5.4.2 对账逻辑

`state/reconcile.go` 新增：

```go
type ReconcileReport struct {
    OrdersAdded   int  // 远端有 / 本地无
    OrdersRemoved int  // 本地有 / 远端无
    OrdersUpdated int  // 都有但参数变了
    PositionsAdded   int
    PositionsRemoved int
    PositionsUpdated int
    Err              error
}

// ReconcileWithExchange 拉远端 open orders + positions，与本地 state 比对，
// 以远端为准更新本地。返回报告供调用方决定是否 publish 事件。
func (s *State) ReconcileWithExchange(ctx context.Context) ReconcileReport
```

**Orders 差异处理**：

| 情况 | 处理 |
|---|---|
| 本地有 / 远端无 | 调 `ReleaseOrder(orderID)`，视为已 filled/cancelled |
| 本地无 / 远端有 | 调 `AttachExternalOrder(orderID, ...)`，`ExternalOrigin=true`，占用余额 |
| 都有但 RemainingSize/Price 不同 | 以远端为准更新；如果 RemainingSize 减少，差额视为已 filled（更新 dailyPnL） |

**Positions 差异处理**：

| 情况 | 处理 |
|---|---|
| 本地有 / 远端无 | 调 `ClearRedeemedPositions([]string{tokenID})` |
| 本地无 / 远端有 | 加入 position，`AvgCostKnown=false`（无买入历史） |
| 都有但 Available 不同 | 以远端为准；按比例保留 AvgCost；如果 Available 减少且本地没有对应 SELL fill，记一条 RISK 事件（可能是外部手动卖出） |

#### 5.4.3 与主流程的协调

- **不阻塞主事件循环**：reconcile 在独立 goroutine 执行；State 内部用短临界区写入，主循环只受秒级阻塞影响
- **对账期间 buffering**：reconcile 拉数据期间收到的 WS Fill 事件正常处理（本地正常占用变化）；reconcile 写入时比对**远端 RemainingSize**与**本地 RemainingSize（含中间 fill）**，使用 `Snapshot` 时序避免竞态
- **失败处理**：拉远端失败 → publish RISK 事件 + retry（指数退避 1s/2s/4s 上限 3 次）；持续失败不阻止主流程，但会持续告警
- **去重**：30s tick + WS trigger 可能同时来，用 `singleflight.Group` 合并并发请求

#### 5.4.4 风控覆盖

外部订单（`ExternalOrigin=true`）必须纳入风控限额：
- 计入 `max_open_orders`（总挂单数）
- 计入 `max_exposure_per_market`（单市场敞口）
- 不计入 `cooldown`（用户手动操作不算自动挂单）
- 不计入 `slippage`（外部订单价格已成事实）

## 6. 风控加硬墙

### 6.1 新增配置（`config.yaml` schema）

```yaml
risk:
  max_daily_loss: 20.0          # USDC，超过拒绝所有 PLACE
  max_exposure_per_market: 100  # USDC，单 market reserved+filled 上限
  max_slippage_bps: 200         # 2%，市价单偏离 mid 拒绝
  max_open_orders: 20           # 同时挂单总数（含 ExternalOrigin）
  market_cooldown: 2s           # 同 market 两次本地 PLACE 间隔

reconcile:
  interval: 30s                 # 周期对账频率
  retry_backoff: [1s, 2s, 4s]   # 失败重试

redeem:
  enabled: false                # 是否启用 redeem（当前 TODO 状态下默认 false）
```

默认值保守开启（按 user 决定）。

### 6.2 RiskRejectionType enum

```go
package risk

type RejectionType string

const (
    RejectInsufficientBalance  RejectionType = "INSUFFICIENT_BALANCE"
    RejectBelowMinReserve      RejectionType = "BELOW_MIN_RESERVE"
    RejectInsufficientPosition RejectionType = "INSUFFICIENT_POSITION"
    RejectExposureCap          RejectionType = "EXPOSURE_CAP"
    RejectSlippage             RejectionType = "SLIPPAGE"
    RejectCooldown             RejectionType = "COOLDOWN"
    RejectDailyLoss            RejectionType = "DAILY_LOSS"
    RejectMaxOpenOrders        RejectionType = "MAX_OPEN_ORDERS"
    RejectInvalidIntent        RejectionType = "INVALID_INTENT"
)

type Rejection struct {
    Type   RejectionType
    Detail string  // 人类可读补充
}

func (r *Rejection) Error() string { return string(r.Type) + ": " + r.Detail }
```

`core.RiskEvent` 增加 `Type` 字段（向后兼容：旧 `Reason` 字段保留）。

### 6.3 Risk.Check 新签名（**包内 API 变更**）

```go
type Engine struct {
    MaxDailyLoss         float64
    MaxExposurePerMarket float64
    MaxSlippageBps       int
    MaxOpenOrders        int
    MarketCooldown       time.Duration

    mu                   sync.RWMutex
    lastIntentPerMarket  map[string]time.Time
}

func (r *Engine) Check(
    intents []runtime.OrderIntent,
    snapshot state.Snapshot,
    midPrices map[string]float64,  // tokenID → 当前 mid（用于滑点）
) error {
    // 依次校验：
    // 1. 单条字段合法性（原有）
    // 2. cooldown（同 market 两次 PLACE 间隔，仅对本地 intent）
    // 3. exposure cap（per-market；含 ExternalOrigin）
    // 4. slippage（市价单偏离 mid）
    // 5. daily loss（从 snapshot.DailyPnL 读）
    // 6. balance + min reserve（原有）
    // 7. max open orders（snapshot.OpenOrderCount，含 ExternalOrigin）
    // 失败返回 *Rejection（实现 error）
}
```

`runtime.RiskManager` 接口同步改签名。caller（`runtime/engine.go:submitIntents`）需要构造 midPrices map：从 `Observation.Tokens` 取每个 token 的 `(AskPrice + BidPrice) / 2`。

### 6.4 State 内置 daily PnL

`State` 新增字段：

```go
type State struct {
    // ... existing
    dailyPnL     float64    // 当前 UTC+0 日已实现 PnL（time.Now().UTC().Format("2006-01-02")）
    dailyPnLDate string     // "2026-05-18"（UTC），跨日重置
    openOrders   int        // 含 ExternalOrigin
}
```

`ApplyFill` 计算 realized PnL（SELL 时 `proceeds - cost_basis_sold`，使用 AvgCost），加到 dailyPnL；`Snapshot()` 包含 `DailyPnL` 和 `OpenOrderCount` 字段供 risk 使用。

## 7. 持仓增强

### 7.1 TokenPosition.AvgCost / AvgCostKnown

```go
type TokenPosition struct {
    Available     float64
    Reserved      float64
    AvgCost       float64  // 加权平均买入价
    AvgCostKnown  bool     // false 表示历史不完整（如对账新增）
    totalBought   float64  // 内部累计买入数量，用于加权平均
}
```

**ApplyFill BUY**：
```go
newAvg := (tp.AvgCost*tp.totalBought + fillPrice*filledSize) / (tp.totalBought + filledSize)
tp.AvgCost = newAvg
tp.AvgCostKnown = true
tp.totalBought += filledSize
```

**ApplyFill SELL**：用 AvgCost 计算 realized PnL = `(fillPrice - AvgCost) * filledSize`（仅 `AvgCostKnown=true` 时累计 dailyPnL）

**Reconcile 新增 position**：`AvgCost=0, AvgCostKnown=false`

**Reconcile 减少 Available**（外部卖出）：按比例保留 AvgCost，`AvgCostKnown` 保持原状

### 7.2 UnrealizedPnL

```go
func (s *State) UnrealizedPnL(midPrices map[string]float64) float64 {
    s.mu.RLock()
    defer s.mu.RUnlock()
    var pnl float64
    for tokenID, tp := range s.position.Tokens {
        if !tp.AvgCostKnown {
            continue  // 历史不全的仓位跳过
        }
        if mid, ok := midPrices[tokenID]; ok && tp.Available > 0 {
            pnl += (mid - tp.AvgCost) * tp.Available
        }
    }
    return pnl
}
```

`MetricsEvent` 加 `UnrealizedPnL` / `DailyPnL` 字段。

### 7.3 PositionExpiring event

新增 `core.EventPositionExpiring`：
```go
type PositionExpiringEvent struct {
    MarketID  string
    EndTime   int64
    TokenIDs  []string
    Available map[string]float64  // tokenID → available size
}
```

State 内部 ticker 每秒检查所有 `endTime - now < 30s` 的 market（市场过期前 30 秒），publish 一次（用 `firedAt` map 防止重复）。Strategy 可订阅 EventPositionExpiring 在临近过期时主动清仓。

## 8. 订单状态机收敛

### 8.1 AttachOrder 单入口

```go
// state/reservation.go

// AttachOrder 是 ConfirmProvisional + ReserveOrder 的统一入口：
//   - 如果 intentID 存在 provisional，转化为正式 reservation
//   - 如果 intentID 为空（WS LIVE 先到），直接创建 orderReservation（external=false）
//   - 如果 orderID 已存在，幂等返回
// 替代 runtime/engine.go 中的字符串比较 hack。
func (s *State) AttachOrder(
    intentID, orderID, marketID, tokenID string,
    side orders.Side,
    price, requestedSize float64,
) error

// AttachExternalOrder 由 Reconcile 调用，标记 ExternalOrigin=true
func (s *State) AttachExternalOrder(
    orderID, marketID, tokenID string,
    side orders.Side,
    price, remainingSize float64,
) error
```

`runtime/engine.go:handleExecutionEvent` 的 Accepted 分支统一调 `AttachOrder`，删除原有两条路径。

### 8.2 IntentID → client_order_id

调研 polymarket-sdk 是否支持 `clientOrderID` 字段。若支持：在 `Executor.submitPlacements` 中传入。若不支持：保留 IntentID 在 ExecutionEvent.ParentOrderID 用于内部对账，**不在本次重构内更改 SDK**。

## 9. 可观测 + 韧性

### 9.1 Executor.DryRun

```go
type Executor struct {
    // ...
    DryRun bool
}
```

DryRun=true 时：所有 PLACE/SPLIT/MERGE 直接 publish `Accepted` + `Filled`（mock 全部成交），不调真实 SDK。配合测试和策略调试。

### 9.2 Bus drop 告警

`EventBus` 增加 `DropThreshold` 字段：dropped 计数每超过阈值（默认 100）打一次 RISK 事件。让运维知道关键事件被丢了。

### 9.3 Executor shutdown drain

`consumeExecuteQueue` 收到 `ctx.Done()` 时：先 drain queue 中剩余 batch，统一 publish `Rejected("shutting down")`，再退出。

### 9.4 reset RPC 移出锁外

`resetForNewMarketLocked` 当前持锁期间做 RPC（`client.GetOrderBooks`、`cpm.FetchOpenPrice`），阻塞读路径。改造为：

1. 锁外：先用 obj 中 tokenIDs / endDate 计算所需输入
2. 锁外：做 RPC 拿 orderbooks / openPrice
3. 锁内：写 e.market.* / e.token.items（短临界区）

需要处理"两次 reset 之间状态变化"的竞态（用 generation counter 校验）。

### 9.5 Reconcile 可观测

每次对账后 publish `EventReconcile`：

```go
type ReconcileEvent struct {
    Type    string  // "ORDERS" | "POSITIONS"
    Added   int
    Removed int
    Updated int
    DurationMs int64
    Err     error
    At      time.Time
}
```

Observer.Logger 加 case 打印；Metrics 累计 `ReconcileRuns` / `ReconcileDiffs` 计数。

## 10. 测试策略

### 10.1 覆盖率目标

| 模块 | 现状 | 目标 |
|---|---|---|
| `core/` | 0% | 80%+ |
| `state/` | 部分 | 90%+ |
| `risk/` | 部分 | 90%+ |
| `runtime/` | 部分 | 90%+ |
| `execution/` | 仅 invalid 路径 | 90%+（含 split/merge/cancel/trade events） |
| `probability/` | 仅 race test | 80%+ |
| `strategy/` | 0% | 80%+ |
| `market/` | 仅 slugFor | 80%+ |
| `indicators/` | 0% | 80%+ |
| `internal/*` | 0% | 80%+ |

### 10.2 测试模式

- **表驱动 + subtest**：所有纯函数（pricing、indicators、reservation 状态机迁移）
- **接口 mock**：external SDK 调用（PolymarketClient / RelayClient / TradeMonitor）抽接口，测试用 fake
- **race test**：所有 mutex/atomic 边界（已修 probability，要补 state、bus、executor.tracked、reconcile）
- **shutdown test**：每个 long-running goroutine 必须有 ctx cancel 测试，确保 30s 内退出
- **reconcile fake**：mock ExchangeStateClient 返回不同的远端状态，验证三种差异都处理正确

### 10.3 每模块测试清单

**core/**
- `pricing_test.go`: RequiredCollateral 表驱动（BUY/SELL/Invalid × 价格/数量边界）
- `bus_test.go`: 多订阅者 fan-out / dropped 计数 / Close / 并发 race / DropThreshold 触发

**state/**
- `reservation_test.go`: Provisional → AttachOrder / Release / Expire 状态迁移
- `external_order_test.go`: AttachExternalOrder + ExternalOrigin 计入风控
- `fill_test.go`: ApplyFill BUY 计 AvgCost / SELL 计 realizedPnL / 边界（fillPrice<=0、超 remaining、AvgCostKnown=false）
- `balance_test.go`: ReconcileOnchainBalance / OpenOrders 计数
- `pnl_test.go`: UnrealizedPnL（含 AvgCostKnown=false 跳过）/ dailyPnL UTC 跨日重置
- `reconcile_test.go`: 9 种差异组合（orders 3×3）+ positions 3×3 + 失败重试
- `restore_test.go`: RestoreFromExchange mock 客户端

**risk/**
- `engine_test.go`: 9 类 RejectionType 各覆盖至少 1 用例 + Check 通过场景
- `slippage_test.go`: midPrices / bps 边界
- `cooldown_test.go`: 时间冷却 race-free
- `external_inclusion_test.go`: ExternalOrigin 订单计入 max_open_orders / exposure_cap

**runtime/**
- `event_handler_test.go`: handleInputUpdate / handleExecutionEvent / handleStrategyTick / handleExecutionAwareStrategy
- `order_tracking_test.go`: pendingByOrder buffer/replay / TTL 清理

**execution/**
- `executor_test.go`: validatePlacement 边界（已有）
- `placements_test.go`: submitPlacements 单/批 / PostOrder errorMsg / 空 orderID
- `splits_merges_test.go`: submitSplits/submitMerges 成功/失败
- `cancels_test.go`: submitCancels 单/批
- `trade_events_test.go`: onOrderEvent LIVE/CANCELED + 未知 orderID 触发 reconcile / onTradeEvent MINED/FAILED
- `dryrun_test.go`: DryRun=true 时全部 Filled
- `shutdown_test.go`: ctx cancel 后 drain queue

**probability/**
- `engine_test.go`: OnUpdate 三个 case 表驱动（已有 race test）
- `features_test.go`: fillFeaturesLocked 字段完整性
- `market_state_test.go`: resetForNewMarketLocked 用 fake client

**strategy/**
- `strategy_test.go`: OnUpdate(EventMarket) / OnUpdate(EventOrderBook) / OnExecution / OnPositionExpiring 四个分支
- `market_queue_test.go`: LRU 淘汰 / 并发
- `utils_test.go`: LastNGreaterThan / TopNGreaterThan / CalculateMarketPrice 边界

**market/**
- `polymarket_slug_feed_test.go`: slugFor（已有） + 重试逻辑（mock fetch 失败）
- `crypto_price_feed_test.go`: 基础订阅

**indicators/**
- `zscore_test.go`: OnTick first-tick 边界 / 跳秒 fill / Sigma / ZScore 公式
- `imbalance_test.go`: 极端盘口 / topN 取值

**internal/**
- `atomicx/float64_test.go`: Store/Load 并发 race
- `buffer/ring_buffer_test.go`: Add/Values/Last/Reset/IsFull 边界
- `multicall/multicall3_test.go`: ERC20Info.Float / chainID 未配置 / mock client

## 11. 提交策略

**单个大 commit**（按 user 决定）：所有改动一次提交到 `benj` 分支。commit message 引用本 spec。

实施过程中分多次 work 但不分别 commit，确保所有测试和 lint 通过后一次性 push。

## 12. 风险与回退

- **风险 1**：单大 commit diff 巨大，review 困难 → 缓解：本 spec + README 重构记录章节作为导读
- **风险 2**：Risk.Check 签名变更影响 runtime 调用 → 已识别，runtime/engine.go:submitIntents 同步改造
- **风险 3**：新风控默认开启可能拒绝正常下单 → 缓解：默认值取保守值（slippage 2%、daily loss 20 USDC、cooldown 2s），实测后再 tune
- **风险 4**：reset RPC 移出锁外的 generation 计数器引入新 race → 缓解：必须有 race test 覆盖；如果方案复杂超预期，回退到"持锁 RPC + TODO"
- **风险 5**：Reconcile 与 WS Fill 时序竞态（reconcile 拉数据时 WS 又推了 fill） → 缓解：所有写入走 State.mu 短临界区；reconcile 比对 RemainingSize 时使用快照（包括中间 fill），用版本号校验
- **风险 6**：Reconcile 失败持续重试可能耗尽 API rate limit → 缓解：指数退避 + 上限 3 次，3 次后转为只看 WS + 等下个 tick
- **回退**：失败时 `git revert <commit>`，本 spec 不入主分支前所有改动都在 benj 上

## 13. 验收清单

实施完成后必须全部满足：

- [ ] `go build ./...` 通过
- [ ] `go vet ./...` 零警告
- [ ] `golangci-lint run` 零警告（如未安装 lint，跳过此项）
- [ ] `go test -race ./...` 全绿
- [ ] `go test -cover ./...` 各模块达 Section 10.1 目标
- [ ] 22 条 README review 问题全部标记为 ✅
- [ ] 10 条策略优化全部上线（参考 Section 6-9）
- [ ] Polymarket 权威对账上线（Section 5.4），手动场景测试：
  - [ ] 启动后手动在 Polymarket 挂单 → 30s 内本地账本看到该单（ExternalOrigin=true）
  - [ ] 启动后手动取消已挂订单 → 30s 内本地 release
  - [ ] 启动后手动卖出仓位 → 30s 内本地 position 减少 + 打 RISK 警告
- [ ] README 新增"重构记录 2026-05-18"章节，包含：
  - 问题清单（引用本 spec 编号）
  - 改动列表（按 Section 5-9 组织）
  - 收益（风险/性能/可观测/可维护）维度对比表
  - 对账行为说明（用户使用文档）
