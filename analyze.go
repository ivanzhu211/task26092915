package funnel

import (
	"math"
	"sort"
	"time"
)

func (s *Store) Analyze(p Params) (*Result, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	rows, err := s.loadRange(p.WindowStart, p.WindowEnd.Add(p.ConversionWindow))
	if err != nil {
		return nil, err
	}

	m := len(p.Steps)
	reached := make([]int, m)
	gaps := make([][]int64, m)

	start := 0
	for start < len(rows) {
		end := start + 1
		for end < len(rows) && rows[end].customerID == rows[start].customerID {
			end++
		}
		analyzeCustomer(rows[start:end], p, reached, gaps)
		start = end
	}

	res := &Result{Steps: make([]StepResult, m)}
	for k := 0; k < m; k++ {
		sr := StepResult{Reached: reached[k]}
		if k >= 1 {
			if reached[k-1] > 0 {
				r := round4(float64(reached[k]) / float64(reached[k-1]))
				sr.RateFromPrevious = &r
			}
			if len(gaps[k]) > 0 {
				med := median(gaps[k])
				sr.MedianGapSeconds = &med
			}
			if reached[k-1] > 0 && 5*reached[k] < 3*reached[k-1] {
				sr.Bottleneck = true
			}
		}
		if reached[0] > 0 {
			r := round4(float64(reached[k]) / float64(reached[0]))
			sr.RateFromFirst = &r
		}
		res.Steps[k] = sr
	}
	return res, nil
}

func analyzeCustomer(tps []row, p Params, reached []int, gaps [][]int64) {
	m := len(p.Steps)
	n := len(tps)

	next := make([][]int32, m)
	for k := 1; k < m; k++ {
		nk := make([]int32, n)
		nxt := int32(-1)
		for i := n - 1; i >= 0; i-- {
			nk[i] = nxt
			if p.Steps[k].matches(touchpointOf(tps[i])) {
				nxt = int32(i)
			}
		}
		next[k] = nk
	}

	winStart := p.WindowStart.Unix()
	winEnd := p.WindowEnd.Unix()
	winSecs := int64(p.ConversionWindow / time.Second)

	bestDepth := 0
	bestChain := make([]int, m)
	chain := make([]int, m)

	for i := 0; i < n; i++ {
		if bestDepth == m {
			break
		}
		t := tps[i].occurredAt.Unix()
		if t < winStart || t >= winEnd {
			continue
		}
		if !p.Steps[0].matches(touchpointOf(tps[i])) {
			continue
		}
		depth := 1
		pos := i
		chain[0] = i
		for k := 1; k < m; k++ {
			j := int(next[k][pos])
			if j < 0 || tps[j].occurredAt.Unix()-t > winSecs {
				break
			}
			pos = j
			chain[k] = j
			depth++
		}
		if depth > bestDepth {
			bestDepth = depth
			copy(bestChain, chain[:depth])
		}
	}

	if bestDepth == 0 {
		return
	}
	for k := 0; k < bestDepth; k++ {
		reached[k]++
		if k >= 1 {
			gap := tps[bestChain[k]].occurredAt.Unix() - tps[bestChain[k-1]].occurredAt.Unix()
			gaps[k] = append(gaps[k], gap)
		}
	}
}

func touchpointOf(r row) Touchpoint {
	return Touchpoint{CustomerID: r.customerID, OccurredAt: r.occurredAt, Channel: r.channel, Event: r.event}
}

func round4(x float64) float64 {
	return math.Round(x*10000) / 10000
}

func median(vals []int64) int64 {
	sort.Slice(vals, func(a, b int) bool { return vals[a] < vals[b] })
	n := len(vals)
	if n%2 == 1 {
		return vals[n/2]
	}
	return (vals[n/2-1] + vals[n/2]) / 2
}
