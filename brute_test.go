package funnel

import (
	"math"
	"math/rand"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

type refTP struct {
	tp  Touchpoint
	seq int
}

func bruteResult(customers map[string][]refTP, p Params) *Result {
	m := len(p.Steps)
	reached := make([]int, m)
	gaps := make([][]int64, m)
	winSecs := int64(p.ConversionWindow / time.Second)

	ids := make([]string, 0, len(customers))
	for id := range customers {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		tps := customers[id]
		sort.SliceStable(tps, func(a, b int) bool {
			if tps[a].tp.OccurredAt.Equal(tps[b].tp.OccurredAt) {
				return tps[a].seq < tps[b].seq
			}
			return tps[a].tp.OccurredAt.Before(tps[b].tp.OccurredAt)
		})
		bestDepth := 0
		var bestChain []int
		for i := range tps {
			t := tps[i].tp.OccurredAt
			if t.Before(p.WindowStart) || !t.Before(p.WindowEnd) {
				continue
			}
			if !p.Steps[0].matches(tps[i].tp) {
				continue
			}
			depth := 1
			chain := []int{i}
			pos := i
			for k := 1; k < m; k++ {
				found := -1
				for j := pos + 1; j < len(tps); j++ {
					if p.Steps[k].matches(tps[j].tp) {
						found = j
						break
					}
				}
				if found < 0 || tps[found].tp.OccurredAt.Unix()-t.Unix() > winSecs {
					break
				}
				pos = found
				chain = append(chain, found)
				depth++
			}
			if depth > bestDepth {
				bestDepth = depth
				bestChain = chain
			}
		}
		for k := 0; k < bestDepth; k++ {
			reached[k]++
			if k >= 1 {
				gap := tps[bestChain[k]].tp.OccurredAt.Unix() - tps[bestChain[k-1]].tp.OccurredAt.Unix()
				gaps[k] = append(gaps[k], gap)
			}
		}
	}

	res := &Result{Steps: make([]StepResult, m)}
	for k := 0; k < m; k++ {
		sr := StepResult{Reached: reached[k]}
		if k >= 1 {
			if reached[k-1] > 0 {
				r := math.Round(float64(reached[k])/float64(reached[k-1])*10000) / 10000
				sr.RateFromPrevious = &r
			}
			if len(gaps[k]) > 0 {
				gs := append([]int64(nil), gaps[k]...)
				sort.Slice(gs, func(a, b int) bool { return gs[a] < gs[b] })
				var med int64
				if len(gs)%2 == 1 {
					med = gs[len(gs)/2]
				} else {
					med = (gs[len(gs)/2-1] + gs[len(gs)/2]) / 2
				}
				sr.MedianGapSeconds = &med
			}
			if reached[k-1] > 0 && 5*reached[k] < 3*reached[k-1] {
				sr.Bottleneck = true
			}
		}
		if reached[0] > 0 {
			r := math.Round(float64(reached[k])/float64(reached[0])*10000) / 10000
			sr.RateFromFirst = &r
		}
		res.Steps[k] = sr
	}
	return res
}

func TestRandomDataMatchesBruteForce(t *testing.T) {
	channels := []Channel{ChannelEmail, ChannelWebsite, ChannelInStore, ChannelMobileApp, ChannelCallCenter, ChannelSocial}
	events := []EventType{EventPageView, EventEmailOpen, EventEmailClick, EventAddToCart, EventCartAbandon, EventPurchase, EventSupportCall}

	for seed := int64(0); seed < 150; seed++ {
		rng := rand.New(rand.NewSource(seed))
		numCustomers := 1 + rng.Intn(12)
		span := 3 * 24 * time.Hour
		var all []Touchpoint
		customers := make(map[string][]refTP)
		seq := 0
		for c := 0; c < numCustomers; c++ {
			id := string(rune('A' + c))
			n := rng.Intn(25)
			for i := 0; i < n; i++ {
				tp := Touchpoint{
					CustomerID: id,
					OccurredAt: t0.Add(time.Duration(rng.Int63n(int64(span/time.Second))) * time.Second),
					Channel:    channels[rng.Intn(len(channels))],
					Event:      events[rng.Intn(len(events))],
				}
				all = append(all, tp)
				customers[id] = append(customers[id], refTP{tp: tp, seq: seq})
				seq++
			}
		}
		// shuffle insertion order so same-second ordering is exercised
		rng.Shuffle(len(all), func(a, b int) { all[a], all[b] = all[b], all[a] })

		numSteps := 2 + rng.Intn(4)
		steps := make([]Step, numSteps)
		for k := range steps {
			steps[k] = Step{Event: events[rng.Intn(len(events))]}
			if rng.Intn(2) == 0 {
				nCh := 1 + rng.Intn(3)
				perm := rng.Perm(len(channels))
				for _, idx := range perm[:nCh] {
					steps[k].Channels = append(steps[k].Channels, channels[idx])
				}
			}
		}
		p := Params{
			Steps:            steps,
			WindowStart:      t0.Add(time.Duration(rng.Int63n(12)) * time.Hour),
			ConversionWindow: time.Duration(1+rng.Int63n(48)) * time.Hour,
		}
		p.WindowEnd = p.WindowStart.Add(time.Duration(1+rng.Int63n(36)) * time.Hour)

		s, err := OpenStore(filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.InsertBatch(all); err != nil {
			t.Fatalf("seed %d insert: %v", seed, err)
		}
		got, err := s.Analyze(p)
		s.Close()
		if err != nil {
			t.Fatalf("seed %d analyze: %v", seed, err)
		}
		want := bruteResult(customers, p)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d mismatch\ngot:  %+v\nwant: %+v", seed, got.Steps, want.Steps)
		}
	}
}

func TestBotAccountPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf test in short mode")
	}
	rng := rand.New(rand.NewSource(42))
	s, err := OpenStore(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// one bot account: 200k touchpoints within 3 days, almost never purchasing
	const total = 200_000
	batch := make([]Touchpoint, 0, MaxBatchSize)
	base := t0
	for i := 0; i < total; i++ {
		ev := EventEmailOpen
		switch rng.Intn(20) {
		case 0:
			ev = EventEmailClick
		case 1:
			ev = EventPageView
		}
		batch = append(batch, tp("bot", base.Add(time.Duration(rng.Int63n(int64(72*time.Hour/time.Second)))*time.Second), ChannelEmail, ev))
		if len(batch) == MaxBatchSize {
			if err := s.InsertBatch(batch); err != nil {
				t.Fatal(err)
			}
			batch = batch[:0]
		}
	}

	steps := []Step{
		{Event: EventEmailOpen},
		{Event: EventEmailClick},
		{Event: EventPageView},
		{Event: EventAddToCart},
		{Event: EventAddToCart},
		{Event: EventPageView},
		{Event: EventEmailClick},
		{Event: EventPageView},
		{Event: EventAddToCart},
		{Event: EventPurchase},
	}
	p := Params{
		Steps:            steps,
		WindowStart:      t0,
		WindowEnd:        t0.Add(72 * time.Hour),
		ConversionWindow: 24 * time.Hour,
	}
	start := time.Now()
	res, err := s.Analyze(p)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("bot analysis took %v, reached step1=%d final=%d", elapsed, res.Steps[0].Reached, res.Steps[9].Reached)
	if elapsed > 5*time.Second {
		t.Fatalf("bot account analysis took %v, exceeds 5s", elapsed)
	}
}
