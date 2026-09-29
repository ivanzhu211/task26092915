# funnel

客户旅程漏斗分析库（Go）。触点持久化在本地 SQLite，由上层分析服务以库的形式调用，不含接口层。

## 功能

- **触点批量写入**：`Store.InsertBatch`，每批最多 5000 条；批内任一记录非法（渠道/事件类型不在清单、客户为空）则整批拒收，返回 `*BatchError` 指明第一条非法记录的下标。
- **漏斗分析**：`Analyze`，2–10 步漏斗，每步指定事件类型、可选渠道限定；另给时间段（左闭右开，最长 365 天）与转化窗口（1 分钟–90 天）。

## 用法

```go
s, _ := funnel.Open("touchpoints.db") // 或 ":memory:"
defer s.Close()

_ = s.InsertBatch(ctx, []funnel.Touchpoint{
    {CustomerID: "u1", OccurredAt: t0, Channel: funnel.ChannelEmail, EventType: funnel.EventEmailOpen},
    // ...
})

res, _ := funnel.Analyze(ctx, s, funnel.AnalysisRequest{
    Steps: []funnel.FunnelStep{
        {EventType: funnel.EventEmailOpen},
        {EventType: funnel.EventPageView, Channels: []funnel.Channel{funnel.ChannelWebsite}},
        {EventType: funnel.EventPurchase},
    },
    Start:  start,          // 左闭
    End:    end,            // 右开，Start..End 最长 365 天
    Window: 24 * time.Hour, // 转化窗口，1 分钟..90 天
})
// res.Steps[i]：Reached / ConvFromPrev / ConvFromFirst / MedianGapSeconds / Bottleneck
```

## 分析规则

- 触点按发生时间排序，同一秒先写入的排在前。
- 时间段内每条满足第 1 步的触点都是一次尝试的起点；每步取排在上一步之后（不复用同一条）、最早满足条件的触点；距起点超过转化窗口（恰好等于不算）即终止。第 2 步起可落在时间段结束之后。
- 每个客户只计走得最远的一次尝试，并列取起点最早；没有起点的客户不计入。
- 转化率保留四位小数（四舍五入），分母为 0 时为空；间隔中位数单位为秒，偶数个取中间两值平均后向下取整，无人到达时为空。
- 相对上一步流失严格超过 40% 标为卡点（恰好 40% 不算，上一步无人时不标），按整数精确判断。
- 同样的数据和参数，分析结果完全一致。

## 实现要点

- 存储：单文件 SQLite（`modernc.org/sqlite`，纯 Go 无 cgo），`(occurred_at, seq)` 与 `(customer_id, occurred_at, seq)` 索引；`seq` 为自增写入顺序。
- 分析：按客户流式扫描（内存只与单客户最大触点数相关），每客户预计算各步的"下一个匹配"指针，每次尝试每步 O(1)；走满漏斗即提前终止。
- 单自动化账号 20 万触点 / 3 天、10 步漏斗、1 天窗口：实测约 0.4 秒（上限 5 秒）。

## 测试

```sh
go test ./...
```

覆盖：同秒排序、相邻同条件步骤、间隔恰等于窗口、后续步骤落在时段外、最佳尝试选择、流失恰好 40%、渠道限定、转化率四舍五入、中位数取整、参数校验、批量拒收；200 组随机小数据与逐客户逐次尝试的穷举参考实现逐一比对；自动化账号性能用例（`-race` 下跳过耗时断言，仅验证正确性）。
