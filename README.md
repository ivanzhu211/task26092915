# task26092915

客户旅程漏斗分析库（Go），触点持久化在本地 SQLite，由上层分析服务以库形式调用，无接口层。

## 用法

```go
s, _ := funnel.OpenStore("touchpoints.db")
defer s.Close()

// 批量写入触点（每批最多 5000 条；批内有非法条目则整批拒收，
// 返回 *funnel.BatchError，Index 为批内第几条）
err := s.InsertBatch([]funnel.Touchpoint{
    {CustomerID: "c1", OccurredAt: ts, Channel: funnel.ChannelEmail, Event: funnel.EventEmailOpen},
})

// 漏斗分析
res, err := s.Analyze(funnel.Params{
    Steps: []funnel.Step{
        {Event: funnel.EventEmailOpen},
        {Event: funnel.EventEmailClick},
        {Event: funnel.EventPurchase, Channels: []funnel.Channel{funnel.ChannelWebsite, funnel.ChannelMobileApp}},
    },
    WindowStart:      start,          // 左闭
    WindowEnd:        end,            // 右开，最长 365 天
    ConversionWindow: 24 * time.Hour, // 1 分钟 ~ 90 天
})
```

`Result.Steps` 逐步给出：到达人数、相对上一步/相对第 1 步转化率（四位小数，
分母为 0 时为 nil）、上一步到本步间隔中位数（秒，无人到达为 nil）、是否卡点
（相对上一步流失严格超过 40%）。

## 规则要点

- 触点排序：发生时间（秒）升序，同一秒按写入顺序。
- 时间段内每条满足第 1 步的触点都是一次尝试起点；逐步取排在上一步之后最早
  满足的触点，距起点超过转化窗口即停止（恰好等于不算超过）；第 1 步之后的
  步骤可落在时间段结束之后。
- 每个客户只计走到步数最多的一次尝试，并列取起点最早者。

## 测试

```sh
go test ./...          # 全部（含机器人账号性能用例）
go test -short ./...   # 跳过性能用例
```
