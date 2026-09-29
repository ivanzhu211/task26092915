package funnel

import (
	"context"
	"testing"
	"time"
)

var testBase = time.Unix(1700000000, 0).UTC()

func tp(customer string, offsetSec int64, ch Channel, ev EventType) Touchpoint {
	return Touchpoint{CustomerID: customer, OccurredAt: testBase.Add(time.Duration(offsetSec) * time.Second), Channel: ch, EventType: ev}
}

// setup 建内存库并写入若干批触点。
func setup(t *testing.T, batches ...[]Touchpoint) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, b := range batches {
		if err := s.InsertBatch(context.Background(), b); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func analyze(t *testing.T, s *Store, req AnalysisRequest) *AnalysisResult {
	t.Helper()
	res, err := Analyze(context.Background(), s, req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }

func eqF64(got *float64, want float64) bool {
	return got != nil && *got == want
}

// 同一秒的触点按写入顺序排：email_open 与 page_view 同秒，
// page_view 先写则在 open 之后没有 page_view，走不到第 2 步；
// page_view 后写则能走到，且间隔为 0 秒。
func TestSameSecondOrdering(t *testing.T) {
	req := AnalysisRequest{
		Steps: []FunnelStep{
			{EventType: EventEmailOpen},
			{EventType: EventPageView},
		},
		Start:  testBase.Add(-time.Hour),
		End:    testBase.Add(time.Hour),
		Window: time.Hour,
	}

	s1 := setup(t, []Touchpoint{
		tp("c1", 0, ChannelWebsite, EventPageView), // 先写
		tp("c1", 0, ChannelEmail, EventEmailOpen),  // 后写
	})
	res := analyze(t, s1, req)
	if res.Steps[0].Reached != 1 || res.Steps[1].Reached != 0 {
		t.Fatalf("page_view written first: want reached [1 0], got [%d %d]", res.Steps[0].Reached, res.Steps[1].Reached)
	}

	s2 := setup(t, []Touchpoint{
		tp("c1", 0, ChannelEmail, EventEmailOpen),  // 先写
		tp("c1", 0, ChannelWebsite, EventPageView), // 后写
	})
	res = analyze(t, s2, req)
	if res.Steps[0].Reached != 1 || res.Steps[1].Reached != 1 {
		t.Fatalf("page_view written second: want reached [1 1], got [%d %d]", res.Steps[0].Reached, res.Steps[1].Reached)
	}
	if res.Steps[1].MedianGapSeconds == nil || *res.Steps[1].MedianGapSeconds != 0 {
		t.Fatalf("want median gap 0, got %v", res.Steps[1].MedianGapSeconds)
	}
}

// 相邻两步条件相同：必须是两条不同的触点，同一条不能复用。
func TestAdjacentIdenticalSteps(t *testing.T) {
	req := AnalysisRequest{
		Steps: []FunnelStep{
			{EventType: EventPageView},
			{EventType: EventPageView},
			{EventType: EventPageView},
		},
		Start:  testBase.Add(-time.Hour),
		End:    testBase.Add(time.Hour),
		Window: time.Hour,
	}
	s := setup(t, []Touchpoint{
		tp("two", 0, ChannelWebsite, EventPageView),
		tp("two", 10, ChannelWebsite, EventPageView),
		tp("three", 0, ChannelWebsite, EventPageView),
		tp("three", 10, ChannelWebsite, EventPageView),
		tp("three", 20, ChannelWebsite, EventPageView),
	})
	res := analyze(t, s, req)
	want := []int{2, 2, 1}
	for i, w := range want {
		if res.Steps[i].Reached != w {
			t.Fatalf("step %d: want reached %d, got %d", i+1, w, res.Steps[i].Reached)
		}
	}
}

// 间隔恰好等于转化窗口算转化；超出 1 秒即不算。
func TestGapExactlyWindow(t *testing.T) {
	req := AnalysisRequest{
		Steps: []FunnelStep{
			{EventType: EventEmailOpen},
			{EventType: EventPurchase},
		},
		Start:  testBase.Add(-time.Hour),
		End:    testBase.Add(2 * time.Hour),
		Window: time.Hour,
	}
	s := setup(t, []Touchpoint{
		tp("exact", 0, ChannelEmail, EventEmailOpen),
		tp("exact", 3600, ChannelWebsite, EventPurchase), // 恰好 1 小时
		tp("over", 0, ChannelEmail, EventEmailOpen),
		tp("over", 3601, ChannelWebsite, EventPurchase), // 超出 1 秒
	})
	res := analyze(t, s, req)
	if res.Steps[0].Reached != 2 || res.Steps[1].Reached != 1 {
		t.Fatalf("want reached [2 1], got [%d %d]", res.Steps[0].Reached, res.Steps[1].Reached)
	}
	if !eqF64(res.Steps[1].ConvFromPrev, 0.5) {
		t.Fatalf("want conv 0.5, got %v", res.Steps[1].ConvFromPrev)
	}
}

// 后续步骤可以落在时间段结束之后；时间段结束时刻的触点不能作为起点（左闭右开）。
func TestStepsBeyondPeriodEnd(t *testing.T) {
	end := testBase.Add(24 * time.Hour)
	req := AnalysisRequest{
		Steps: []FunnelStep{
			{EventType: EventEmailOpen},
			{EventType: EventPurchase},
		},
		Start:  testBase,
		End:    end,
		Window: 3 * time.Hour,
	}
	s := setup(t, []Touchpoint{
		tp("c1", 23*3600, ChannelEmail, EventEmailOpen),  // 时间段内最后 1 小时
		tp("c1", 26*3600, ChannelWebsite, EventPurchase), // 时间段结束后 2 小时，窗口内
		tp("c2", 24*3600, ChannelEmail, EventEmailOpen),  // 恰好等于 End，不是起点
		tp("c2", 24*3600+60, ChannelWebsite, EventPurchase),
	})
	res := analyze(t, s, req)
	if res.Steps[0].Reached != 1 || res.Steps[1].Reached != 1 {
		t.Fatalf("want reached [1 1], got [%d %d]", res.Steps[0].Reached, res.Steps[1].Reached)
	}
}

// 第一次尝试走不远、之后的尝试走得更远：取走得最远的；
// 走得一样远时取起点最早的（中位数间隔应来自最早那次）。
func TestBestAttemptSelection(t *testing.T) {
	req := AnalysisRequest{
		Steps: []FunnelStep{
			{EventType: EventEmailOpen},
			{EventType: EventPurchase},
		},
		Start:  testBase,
		End:    testBase.Add(24 * time.Hour),
		Window: time.Hour,
	}
	s := setup(t, []Touchpoint{
		tp("c1", 0, ChannelEmail, EventEmailOpen),
		tp("c1", 50*60, ChannelEmail, EventEmailOpen),
		tp("c1", 80*60, ChannelWebsite, EventPurchase), // 距第 2 次 open 30 分钟
		tp("c1", 100*60, ChannelEmail, EventEmailOpen),
		tp("c1", 120*60, ChannelWebsite, EventPurchase), // 距第 3 次 open 20 分钟
	})
	// 第 1 次 open 后 1 小时内无 purchase（80 分钟才买），只走 1 步；
	// 第 2、3 次都走 2 步，取起点最早的第 2 次，间隔 30 分钟。
	res := analyze(t, s, req)
	if res.Steps[0].Reached != 1 || res.Steps[1].Reached != 1 {
		t.Fatalf("want reached [1 1], got [%d %d]", res.Steps[0].Reached, res.Steps[1].Reached)
	}
	if res.Steps[1].MedianGapSeconds == nil || *res.Steps[1].MedianGapSeconds != 1800 {
		t.Fatalf("want median gap 1800 (earliest best attempt), got %v", res.Steps[1].MedianGapSeconds)
	}
}

// 流失恰好 40% 不标卡点；超过 40% 才标。上一步没人时不标。
func TestBottleneckExactlyFortyPercent(t *testing.T) {
	mkReq := func() AnalysisRequest {
		return AnalysisRequest{
			Steps: []FunnelStep{
				{EventType: EventEmailOpen},
				{EventType: EventPurchase},
			},
			Start:  testBase,
			End:    testBase.Add(24 * time.Hour),
			Window: time.Hour,
		}
	}
	// 10 人 open，6 人 purchase：流失恰好 40%。
	var batch []Touchpoint
	for i := 0; i < 10; i++ {
		c := string(rune('a' + i))
		batch = append(batch, tp(c, 0, ChannelEmail, EventEmailOpen))
		if i < 6 {
			batch = append(batch, tp(c, 60, ChannelWebsite, EventPurchase))
		}
	}
	s := setup(t, batch)
	res := analyze(t, s, mkReq())
	if res.Steps[1].Bottleneck {
		t.Fatal("exactly 40% drop must not be a bottleneck")
	}
	if !eqF64(res.Steps[1].ConvFromPrev, 0.6) || !eqF64(res.Steps[1].ConvFromFirst, 0.6) {
		t.Fatalf("want conv 0.6/0.6, got %v/%v", res.Steps[1].ConvFromPrev, res.Steps[1].ConvFromFirst)
	}

	// 10 人 open，5 人 purchase：流失 50% > 40%，标卡点。
	batch = batch[:0]
	for i := 0; i < 10; i++ {
		c := string(rune('a' + i))
		batch = append(batch, tp(c, 0, ChannelEmail, EventEmailOpen))
		if i < 5 {
			batch = append(batch, tp(c, 60, ChannelWebsite, EventPurchase))
		}
	}
	s2 := setup(t, batch)
	res = analyze(t, s2, mkReq())
	if !res.Steps[1].Bottleneck {
		t.Fatal("50% drop must be a bottleneck")
	}

	// 第 1 步没人：不标卡点，转化率为空。
	s3 := setup(t, []Touchpoint{tp("x", 0, ChannelWebsite, EventPageView)})
	res = analyze(t, s3, mkReq())
	if res.Steps[1].Bottleneck {
		t.Fatal("no one at previous step must not be a bottleneck")
	}
	if res.Steps[1].ConvFromPrev != nil || res.Steps[1].ConvFromFirst != nil || res.Steps[1].MedianGapSeconds != nil {
		t.Fatal("rates and median must be nil when denominator is 0")
	}
}

// 渠道限定与转化率四舍五入、中位数（偶数个向下取整）。
func TestChannelFilterRoundingMedian(t *testing.T) {
	req := AnalysisRequest{
		Steps: []FunnelStep{
			{EventType: EventEmailOpen},
			{EventType: EventPageView, Channels: []Channel{ChannelWebsite, ChannelMobileApp}},
		},
		Start:  testBase,
		End:    testBase.Add(24 * time.Hour),
		Window: time.Hour,
	}
	s := setup(t, []Touchpoint{
		tp("c1", 0, ChannelEmail, EventEmailOpen),
		tp("c1", 10, ChannelWebsite, EventPageView), // 间隔 10
		tp("c2", 0, ChannelEmail, EventEmailOpen),
		tp("c2", 21, ChannelSocial, EventPageView),    // 渠道不符，忽略
		tp("c2", 31, ChannelMobileApp, EventPageView), // 间隔 31
		tp("c3", 0, ChannelEmail, EventEmailOpen),     // 无后续
	})
	res := analyze(t, s, req)
	if res.Steps[0].Reached != 3 || res.Steps[1].Reached != 2 {
		t.Fatalf("want reached [3 2], got [%d %d]", res.Steps[0].Reached, res.Steps[1].Reached)
	}
	// 2/3 = 0.66666... → 0.6667
	if !eqF64(res.Steps[1].ConvFromPrev, 0.6667) {
		t.Fatalf("want conv 0.6667, got %v", res.Steps[1].ConvFromPrev)
	}
	// 间隔 [10, 31]，平均 20.5 向下取整 20
	if res.Steps[1].MedianGapSeconds == nil || *res.Steps[1].MedianGapSeconds != 20 {
		t.Fatalf("want median gap 20, got %v", res.Steps[1].MedianGapSeconds)
	}
}

// 参数校验。
func TestRequestValidation(t *testing.T) {
	s := setup(t)
	valid := AnalysisRequest{
		Steps:  []FunnelStep{{EventType: EventEmailOpen}, {EventType: EventPurchase}},
		Start:  testBase,
		End:    testBase.Add(24 * time.Hour),
		Window: time.Hour,
	}
	if _, err := Analyze(context.Background(), s, valid); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	bad := valid
	bad.Steps = valid.Steps[:1]
	if _, err := Analyze(context.Background(), s, bad); err == nil {
		t.Fatal("1-step funnel accepted")
	}
	bad = valid
	bad.Steps = make([]FunnelStep, 11)
	for i := range bad.Steps {
		bad.Steps[i] = FunnelStep{EventType: EventPageView}
	}
	if _, err := Analyze(context.Background(), s, bad); err == nil {
		t.Fatal("11-step funnel accepted")
	}
	bad = valid
	bad.End = valid.Start.Add(366 * 24 * time.Hour)
	if _, err := Analyze(context.Background(), s, bad); err == nil {
		t.Fatal("366-day range accepted")
	}
	bad = valid
	bad.Window = 30 * time.Second
	if _, err := Analyze(context.Background(), s, bad); err == nil {
		t.Fatal("30s window accepted")
	}
	bad = valid
	bad.Window = 91 * 24 * time.Hour
	if _, err := Analyze(context.Background(), s, bad); err == nil {
		t.Fatal("91-day window accepted")
	}
	bad = valid
	bad.Steps = []FunnelStep{{EventType: EventEmailOpen}, {EventType: "nope"}}
	if _, err := Analyze(context.Background(), s, bad); err == nil {
		t.Fatal("invalid step event type accepted")
	}
}
