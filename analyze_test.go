package funnel

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func storeWith(t *testing.T, tps []Touchpoint) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	for len(tps) > 0 {
		n := len(tps)
		if n > MaxBatchSize {
			n = MaxBatchSize
		}
		if err := s.InsertBatch(tps[:n]); err != nil {
			t.Fatalf("insert: %v", err)
		}
		tps = tps[n:]
	}
	return s
}

func dayParams(steps []Step, window time.Duration) Params {
	return Params{
		Steps:            steps,
		WindowStart:      t0,
		WindowEnd:        t0.Add(24 * time.Hour),
		ConversionWindow: window,
	}
}

func TestSameSecondChainUsesInsertionOrder(t *testing.T) {
	s := storeWith(t, []Touchpoint{
		tp("c1", t0, ChannelEmail, EventEmailOpen),
		tp("c1", t0, ChannelWebsite, EventPageView),
		tp("c1", t0, ChannelWebsite, EventPurchase),
	})
	res, err := s.Analyze(dayParams([]Step{
		{Event: EventEmailOpen},
		{Event: EventPageView},
		{Event: EventPurchase},
	}, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range []int{1, 1, 1} {
		if res.Steps[k].Reached != want {
			t.Fatalf("step %d: expected %d reached, got %d", k+1, want, res.Steps[k].Reached)
		}
	}
	if res.Steps[1].MedianGapSeconds == nil || *res.Steps[1].MedianGapSeconds != 0 {
		t.Fatalf("expected median gap 0, got %+v", res.Steps[1].MedianGapSeconds)
	}
}

func TestAdjacentIdenticalStepsNeedDistinctTouchpoints(t *testing.T) {
	s := storeWith(t, []Touchpoint{
		tp("c1", t0, ChannelWebsite, EventPageView),
		tp("c1", t0.Add(time.Minute), ChannelWebsite, EventPageView),
		tp("c2", t0, ChannelWebsite, EventPageView),
	})
	res, err := s.Analyze(dayParams([]Step{
		{Event: EventPageView},
		{Event: EventPageView},
	}, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if res.Steps[0].Reached != 2 || res.Steps[1].Reached != 1 {
		t.Fatalf("expected [2 1], got [%d %d]", res.Steps[0].Reached, res.Steps[1].Reached)
	}
}

func TestGapExactlyEqualToWindowCounts(t *testing.T) {
	s := storeWith(t, []Touchpoint{
		tp("c1", t0, ChannelEmail, EventEmailOpen),
		tp("c1", t0.Add(10*time.Minute), ChannelEmail, EventEmailClick),
		tp("c2", t0, ChannelEmail, EventEmailOpen),
		tp("c2", t0.Add(10*time.Minute+time.Second), ChannelEmail, EventEmailClick),
	})
	res, err := s.Analyze(dayParams([]Step{
		{Event: EventEmailOpen},
		{Event: EventEmailClick},
	}, 10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if res.Steps[0].Reached != 2 || res.Steps[1].Reached != 1 {
		t.Fatalf("expected [2 1], got [%d %d]", res.Steps[0].Reached, res.Steps[1].Reached)
	}
}

func TestLaterStepsMayFallOutsidePeriod(t *testing.T) {
	s := storeWith(t, []Touchpoint{
		tp("c1", t0.Add(23*time.Hour), ChannelEmail, EventEmailOpen),
		tp("c1", t0.Add(26*time.Hour), ChannelWebsite, EventPurchase),
	})
	res, err := s.Analyze(dayParams([]Step{
		{Event: EventEmailOpen},
		{Event: EventPurchase},
	}, 24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if res.Steps[1].Reached != 1 {
		t.Fatalf("expected step 2 reached outside period, got %d", res.Steps[1].Reached)
	}
	if res.Steps[1].MedianGapSeconds == nil || *res.Steps[1].MedianGapSeconds != 3*3600 {
		t.Fatalf("expected median gap 10800, got %+v", res.Steps[1].MedianGapSeconds)
	}
}

func TestLaterAttemptBeatsEarlierShorterOne(t *testing.T) {
	s := storeWith(t, []Touchpoint{
		tp("c1", t0, ChannelEmail, EventEmailOpen),
		tp("c1", t0.Add(2*time.Hour), ChannelEmail, EventEmailOpen),
		tp("c1", t0.Add(2*time.Hour+time.Minute), ChannelEmail, EventEmailClick),
		tp("c1", t0.Add(2*time.Hour+2*time.Minute), ChannelWebsite, EventPurchase),
	})
	res, err := s.Analyze(dayParams([]Step{
		{Event: EventEmailOpen},
		{Event: EventEmailClick},
		{Event: EventPurchase},
	}, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range []int{1, 1, 1} {
		if res.Steps[k].Reached != want {
			t.Fatalf("step %d: expected %d, got %d", k+1, want, res.Steps[k].Reached)
		}
	}
	if res.Steps[2].MedianGapSeconds == nil || *res.Steps[2].MedianGapSeconds != 60 {
		t.Fatalf("expected median gap 60, got %+v", res.Steps[2].MedianGapSeconds)
	}
}

func TestExactlyFortyPercentDropIsNotBottleneck(t *testing.T) {
	var tps []Touchpoint
	for i := 0; i < 10; i++ {
		c := string(rune('a' + i))
		tps = append(tps, tp(c, t0, ChannelEmail, EventEmailOpen))
		if i < 6 {
			tps = append(tps, tp(c, t0.Add(time.Minute), ChannelWebsite, EventPurchase))
		}
	}
	s := storeWith(t, tps)
	res, err := s.Analyze(dayParams([]Step{
		{Event: EventEmailOpen},
		{Event: EventPurchase},
	}, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if res.Steps[1].Bottleneck {
		t.Fatal("exactly 40% drop must not be flagged as bottleneck")
	}
	if res.Steps[1].RateFromPrevious == nil || *res.Steps[1].RateFromPrevious != 0.6 {
		t.Fatalf("expected rate 0.6, got %+v", res.Steps[1].RateFromPrevious)
	}
}

func TestOverFortyPercentDropIsBottleneck(t *testing.T) {
	var tps []Touchpoint
	for i := 0; i < 10; i++ {
		c := string(rune('a' + i))
		tps = append(tps, tp(c, t0, ChannelEmail, EventEmailOpen))
		if i < 5 {
			tps = append(tps, tp(c, t0.Add(time.Minute), ChannelWebsite, EventPurchase))
		}
	}
	s := storeWith(t, tps)
	res, err := s.Analyze(dayParams([]Step{
		{Event: EventEmailOpen},
		{Event: EventPurchase},
	}, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Steps[1].Bottleneck {
		t.Fatal("50% drop should be flagged as bottleneck")
	}
}

func TestChannelRestrictionAndRates(t *testing.T) {
	s := storeWith(t, []Touchpoint{
		tp("c1", t0, ChannelEmail, EventEmailOpen),
		tp("c1", t0.Add(time.Minute), ChannelMobileApp, EventPageView),
		tp("c2", t0, ChannelEmail, EventEmailOpen),
		tp("c2", t0.Add(2*time.Minute), ChannelWebsite, EventPageView),
		tp("c3", t0, ChannelEmail, EventEmailOpen),
		tp("c3", t0.Add(3*time.Minute), ChannelWebsite, EventPageView),
		tp("c4", t0, ChannelEmail, EventEmailOpen),
	})
	res, err := s.Analyze(dayParams([]Step{
		{Event: EventEmailOpen},
		{Event: EventPageView, Channels: []Channel{ChannelWebsite, ChannelMobileApp}},
	}, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if res.Steps[0].Reached != 4 || res.Steps[1].Reached != 3 {
		t.Fatalf("expected [4 3], got [%d %d]", res.Steps[0].Reached, res.Steps[1].Reached)
	}
	if *res.Steps[1].RateFromPrevious != 0.75 || *res.Steps[1].RateFromFirst != 0.75 {
		t.Fatalf("expected rates 0.75, got %v %v", *res.Steps[1].RateFromPrevious, *res.Steps[1].RateFromFirst)
	}
	if *res.Steps[1].MedianGapSeconds != 120 {
		t.Fatalf("expected median gap 120, got %d", *res.Steps[1].MedianGapSeconds)
	}
	if *res.Steps[0].RateFromFirst != 1.0 {
		t.Fatalf("step 1 rate-from-first should be 1.0, got %v", *res.Steps[0].RateFromFirst)
	}
	if res.Steps[0].RateFromPrevious != nil || res.Steps[0].MedianGapSeconds != nil {
		t.Fatal("step 1 should have no previous-step metrics")
	}
}

func TestMedianEvenCountRoundsDown(t *testing.T) {
	s := storeWith(t, []Touchpoint{
		tp("c1", t0, ChannelEmail, EventEmailOpen),
		tp("c1", t0.Add(1*time.Second), ChannelWebsite, EventPurchase),
		tp("c2", t0, ChannelEmail, EventEmailOpen),
		tp("c2", t0.Add(2*time.Second), ChannelWebsite, EventPurchase),
	})
	res, err := s.Analyze(dayParams([]Step{
		{Event: EventEmailOpen},
		{Event: EventPurchase},
	}, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if *res.Steps[1].MedianGapSeconds != 1 {
		t.Fatalf("expected floor(1.5)=1, got %d", *res.Steps[1].MedianGapSeconds)
	}
}

func TestInvalidParams(t *testing.T) {
	s := storeWith(t, nil)
	p := dayParams([]Step{{Event: EventEmailOpen}}, time.Hour)
	if err := p.validate(); err != ErrStepCount {
		t.Fatalf("expected ErrStepCount, got %v", err)
	}
	p = dayParams([]Step{{Event: EventEmailOpen}, {Event: EventPurchase}}, 30*time.Second)
	if err := p.validate(); err != ErrConversionRange {
		t.Fatalf("expected ErrConversionRange, got %v", err)
	}
	p = dayParams([]Step{{Event: EventEmailOpen}, {Event: EventPurchase}}, time.Hour)
	p.WindowEnd = p.WindowStart.Add(366 * 24 * time.Hour)
	if err := p.validate(); err != ErrPeriodRange {
		t.Fatalf("expected ErrPeriodRange, got %v", err)
	}
	p = dayParams([]Step{{Event: EventEmailOpen}, {Event: EventPurchase, Channels: []Channel{Channel("Smoke Signal")}}}, time.Hour)
	if err := p.validate(); err != ErrStepChannel {
		t.Fatalf("expected ErrStepChannel, got %v", err)
	}
	_ = s
}

func TestDeterministicResults(t *testing.T) {
	s := storeWith(t, []Touchpoint{
		tp("c1", t0, ChannelEmail, EventEmailOpen),
		tp("c1", t0.Add(time.Minute), ChannelWebsite, EventPageView),
		tp("c2", t0, ChannelEmail, EventEmailOpen),
		tp("c2", t0.Add(2*time.Minute), ChannelWebsite, EventPageView),
		tp("c2", t0.Add(3*time.Minute), ChannelWebsite, EventPurchase),
	})
	p := dayParams([]Step{
		{Event: EventEmailOpen},
		{Event: EventPageView},
		{Event: EventPurchase},
	}, time.Hour)
	first, err := s.Analyze(p)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := s.Analyze(p)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("run %d differs: %+v vs %+v", i, first.Steps, again.Steps)
		}
	}
}
