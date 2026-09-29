package funnel

import (
	"context"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"
)

// referenceAnalyze 按业务规则逐客户、逐次尝试穷举的参考实现，
// 刻意写得直接（O(n^2)），与 Analyze 的优化实现互为对照。
func referenceAnalyze(pts []StoredTouchpoint, req AnalysisRequest) *AnalysisResult {
	n := len(req.Steps)
	startUnix := req.Start.Unix()
	endUnix := req.End.Unix()
	windowSec := int64(req.Window / time.Second)

	byCustomer := map[string][]StoredTouchpoint{}
	for _, p := range pts {
		byCustomer[p.CustomerID] = append(byCustomer[p.CustomerID], p)
	}
	// 每个客户内部按 (时间, 写入顺序) 排序。
	for _, list := range byCustomer {
		sort.Slice(list, func(i, j int) bool {
			a, b := list[i], list[j]
			if !a.OccurredAt.Equal(b.OccurredAt) {
				return a.OccurredAt.Before(b.OccurredAt)
			}
			return a.Seq < b.Seq
		})
	}

	reached := make([]int, n)
	gaps := make([][]int64, n)
	for _, list := range byCustomer {
		bestReached := 0
		var bestGaps []int64
		for si, st := range list {
			t0 := st.OccurredAt.Unix()
			if t0 < startUnix || t0 >= endUnix {
				continue
			}
			if !req.Steps[0].matches(st.Touchpoint) {
				continue
			}
			// 从 si 出发穷举：每步线性找上一条之后最早满足条件的触点。
			r := 1
			cur := si
			curGaps := make([]int64, 0, n-1)
			for k := 1; k < n; k++ {
				found := -1
				for j := cur + 1; j < len(list); j++ {
					if req.Steps[k].matches(list[j].Touchpoint) {
						found = j
						break
					}
				}
				if found < 0 {
					break
				}
				tk := list[found].OccurredAt.Unix()
				if tk-t0 > windowSec {
					break
				}
				curGaps = append(curGaps, tk-list[cur].OccurredAt.Unix())
				cur = found
				r = k + 1
			}
			if r > bestReached {
				bestReached = r
				bestGaps = curGaps
			}
		}
		if bestReached == 0 {
			continue
		}
		for i := 0; i < bestReached; i++ {
			reached[i]++
		}
		for i := 1; i < bestReached; i++ {
			gaps[i] = append(gaps[i], bestGaps[i-1])
		}
	}

	res := &AnalysisResult{Steps: make([]StepStats, n)}
	for i := 0; i < n; i++ {
		st := StepStats{Reached: reached[i]}
		if i > 0 {
			if reached[i-1] > 0 {
				v := round4(float64(reached[i]) / float64(reached[i-1]))
				st.ConvFromPrev = &v
			}
			if reached[0] > 0 {
				v := round4(float64(reached[i]) / float64(reached[0]))
				st.ConvFromFirst = &v
			}
			if len(gaps[i]) > 0 {
				m := medianFloor(append([]int64(nil), gaps[i]...))
				st.MedianGapSeconds = &m
			}
			if reached[i-1] > 0 && int64(reached[i-1]-reached[i])*5 > int64(reached[i-1])*2 {
				st.Bottleneck = true
			}
		}
		res.Steps[i] = st
	}
	return res
}

// TestAgainstBruteForce 随机小数据上，Analyze 必须与穷举参考实现完全一致。
func TestAgainstBruteForce(t *testing.T) {
	channels := []Channel{ChannelEmail, ChannelWebsite, ChannelInStore, ChannelMobileApp, ChannelCallCenter, ChannelSocial}
	events := []EventType{EventPageView, EventEmailOpen, EventEmailClick, EventAddToCart, EventCartAbandon, EventPurchase, EventSupportCall}

	for trial := 0; trial < 200; trial++ {
		rng := rand.New(rand.NewSource(int64(trial)*7919 + 13))
		base := testBase

		// 随机触点：少量客户、少量触点，含同秒与时段外数据。
		nCustomers := 1 + rng.Intn(12)
		nTouch := rng.Intn(150)
		var batch []Touchpoint
		for i := 0; i < nTouch; i++ {
			batch = append(batch, Touchpoint{
				CustomerID: string(rune('a' + rng.Intn(nCustomers))),
				OccurredAt: base.Add(time.Duration(rng.Intn(10*24*3600)) * time.Second),
				Channel:    channels[rng.Intn(len(channels))],
				EventType:  events[rng.Intn(len(events))],
			})
		}
		// 随机分成若干批写入（写入顺序影响同秒排序）。
		s, err := Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		for len(batch) > 0 {
			k := 1 + rng.Intn(len(batch))
			if err := s.InsertBatch(context.Background(), batch[:k]); err != nil {
				t.Fatal(err)
			}
			batch = batch[k:]
		}

		// 随机漏斗与参数。
		nSteps := MinSteps + rng.Intn(4)
		steps := make([]FunnelStep, nSteps)
		for i := range steps {
			steps[i] = FunnelStep{EventType: events[rng.Intn(len(events))]}
			if rng.Intn(2) == 0 {
				k := 1 + rng.Intn(2)
				perm := rng.Perm(len(channels))
				for _, idx := range perm[:k] {
					steps[i].Channels = append(steps[i].Channels, channels[idx])
				}
			}
		}
		start := base.Add(time.Duration(rng.Intn(3*24*3600)) * time.Second)
		req := AnalysisRequest{
			Steps:  steps,
			Start:  start,
			End:    start.Add(time.Duration(1+rng.Intn(5*24*3600)) * time.Second),
			Window: time.Duration(1+rng.Intn(3*24*3600)) * time.Second,
		}

		got, err := Analyze(context.Background(), s, req)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		// 参考实现需要时段内 + 窗口内的全部触点。
		all, err := s.Touchpoints(context.Background(), req.Start, req.End.Add(req.Window))
		if err != nil {
			t.Fatal(err)
		}
		want := referenceAnalyze(all, req)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d mismatch\n req=%+v\n got=%+v\nwant=%+v", trial, req, got.Steps, want.Steps)
		}
		s.Close()
	}
}

// TestDeterministic 同样的数据和参数，重复分析结果一致。
func TestDeterministic(t *testing.T) {
	s := setup(t, []Touchpoint{
		tp("c1", 0, ChannelEmail, EventEmailOpen),
		tp("c1", 60, ChannelWebsite, EventPageView),
		tp("c2", 0, ChannelEmail, EventEmailOpen),
		tp("c2", 120, ChannelWebsite, EventPageView),
		tp("c2", 180, ChannelWebsite, EventPurchase),
	})
	req := AnalysisRequest{
		Steps:  []FunnelStep{{EventType: EventEmailOpen}, {EventType: EventPageView}, {EventType: EventPurchase}},
		Start:  testBase.Add(-time.Hour),
		End:    testBase.Add(time.Hour),
		Window: time.Hour,
	}
	first := analyze(t, s, req)
	for i := 0; i < 5; i++ {
		got := analyze(t, s, req)
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d differs", i)
		}
	}
}
