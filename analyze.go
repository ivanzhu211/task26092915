package funnel

import (
	"context"
	"math"
	"sort"
	"time"
)

// Analyze 对库中触点执行一次漏斗分析。
// 同样的数据和参数，结果完全一致（确定性）。
func Analyze(ctx context.Context, s *Store, req AnalysisRequest) (*AnalysisResult, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	n := len(req.Steps)
	reached := make([]int, n)  // reached[i]：最佳尝试走到第 i+1 步的客户数
	gaps := make([][]int64, n) // gaps[i]：最佳尝试中第 i 步到第 i+1 步的间隔（秒）

	// 后续步骤可落在时间段结束之后，但距起点不超过转化窗口，
	// 因此相关触点最晚不超过 End+Window。
	scanEnd := req.End.Add(req.Window)
	startUnix := req.Start.Unix()
	endUnix := req.End.Unix()
	windowSec := int64(req.Window / time.Second)

	err := s.scanByCustomer(ctx, req.Start, scanEnd, func(_ string, pts []StoredTouchpoint) error {
		best := bestAttempt(pts, req.Steps, startUnix, endUnix, windowSec)
		if best.reached == 0 {
			return nil
		}
		for i := 0; i < best.reached; i++ {
			reached[i]++
		}
		for i := 1; i < best.reached; i++ {
			gaps[i] = append(gaps[i], best.gaps[i-1])
		}
		return nil
	})
	if err != nil {
		return nil, err
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
				m := medianFloor(gaps[i])
				st.MedianGapSeconds = &m
			}
			// 流失严格超过 40% 标为卡点；用整数精确判断，恰好 40% 不算。
			if reached[i-1] > 0 && int64(reached[i-1]-reached[i])*5 > int64(reached[i-1])*2 {
				st.Bottleneck = true
			}
		}
		res.Steps[i] = st
	}
	return res, nil
}

// attempt 一次尝试的结果：走到的步数与相邻步间隔（秒）。
type attempt struct {
	reached int
	gaps    []int64 // gaps[i] = 第 i+1 步到第 i+2 步的间隔，长度 reached-1
}

// bestAttempt 计算单个客户的最佳尝试：走到步数最多；并列取起点最早。
// pts 为该客户按 (occurred_at, seq) 升序的触点（含时间段之外、窗口内的后续触点）。
// 只有发生在 [startUnix, endUnix) 内且满足第 1 步的触点可作为起点。
func bestAttempt(pts []StoredTouchpoint, steps []FunnelStep, startUnix, endUnix, windowSec int64) attempt {
	n := len(steps)
	m := len(pts)

	// next[k][i]：下标 i 之后第一个满足第 k+1 步条件的触点下标，无则为 -1。
	// 对每个客户只构建一次，之后每次尝试的每步都是 O(1)。
	next := make([][]int32, n)
	for k := 1; k < n; k++ {
		row := make([]int32, m)
		nxt := int32(-1)
		for i := m - 1; i >= 0; i-- {
			if i+1 < m && steps[k].matches(pts[i+1].Touchpoint) {
				nxt = int32(i + 1)
			}
			row[i] = nxt
		}
		next[k] = row
	}

	best := attempt{}
	gapBuf := make([]int64, n-1)
	for si, st := range pts {
		t0 := st.OccurredAt.Unix()
		if t0 < startUnix {
			continue
		}
		if t0 >= endUnix {
			break // 之后更晚，不可能再作起点
		}
		if !steps[0].matches(st.Touchpoint) {
			continue
		}
		if best.reached == n {
			break // 已走满，且当前起点最早，不可能更优
		}
		// 从 si 出发走一遍。
		reached := 1
		cur := si
		for k := 1; k < n; k++ {
			j := next[k][cur]
			if j < 0 {
				break
			}
			tk := pts[j].OccurredAt.Unix()
			if tk-t0 > windowSec {
				break // 之后的只会更晚
			}
			gapBuf[k-1] = tk - pts[cur].OccurredAt.Unix()
			cur = int(j)
			reached = k + 1
		}
		if reached > best.reached {
			best.reached = reached
			best.gaps = append(best.gaps[:0], gapBuf[:reached-1]...)
		}
	}
	return best
}

// round4 四舍五入到四位小数。
func round4(v float64) float64 {
	return math.Round(v*1e4) / 1e4
}

// medianFloor 中位数；偶数个取中间两个的平均后向下取整。会原地排序。
func medianFloor(vals []int64) int64 {
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	m := len(vals)
	if m%2 == 1 {
		return vals[m/2]
	}
	return (vals[m/2-1] + vals[m/2]) / 2
}
