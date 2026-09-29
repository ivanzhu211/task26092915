package funnel

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"
	"time"
)

// TestAutomationAccountPerformance 单个自动化账号 20 万条触点集中在 3 天内，
// 反复进入漏斗却几乎走不到最后一步；窗口 1 天的 10 步漏斗分析须在 5 秒内完成。
func TestAutomationAccountPerformance(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "touchpoints.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	// 20 万条触点，3 天，事件以 page_view/email_open 为主，几乎走不完 10 步。
	rng := rand.New(rand.NewSource(42))
	events := []EventType{EventPageView, EventEmailOpen, EventEmailClick, EventPageView, EventEmailOpen, EventSupportCall}
	const total = 200_000
	const spanSec = 3 * 24 * 3600
	batch := make([]Touchpoint, 0, MaxBatchSize)
	for i := 0; i < total; i++ {
		batch = append(batch, Touchpoint{
			CustomerID: "bot",
			OccurredAt: testBase.Add(time.Duration(i*spanSec/total) * time.Second),
			Channel:    ChannelWebsite,
			EventType:  events[rng.Intn(len(events))],
		})
		if len(batch) == MaxBatchSize {
			if err := s.InsertBatch(ctx, batch); err != nil {
				t.Fatal(err)
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		if err := s.InsertBatch(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}

	steps := []FunnelStep{
		{EventType: EventEmailOpen},
		{EventType: EventEmailClick},
		{EventType: EventPageView},
		{EventType: EventAddToCart},
		{EventType: EventPurchase},
		{EventType: EventPageView},
		{EventType: EventAddToCart},
		{EventType: EventPurchase},
		{EventType: EventSupportCall},
		{EventType: EventPurchase},
	}
	req := AnalysisRequest{
		Steps:  steps,
		Start:  testBase,
		End:    testBase.Add(3 * 24 * time.Hour),
		Window: 24 * time.Hour,
	}

	start := time.Now()
	res, err := Analyze(ctx, s, req)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("automation account analysis: %v (reached step1=%d)\n", elapsed, res.Steps[0].Reached)
	if !raceEnabled && elapsed > 5*time.Second {
		t.Fatalf("analysis took %v, exceeds 5s", elapsed)
	}
	if res.Steps[0].Reached != 1 {
		t.Fatalf("single bot customer must count once, got %d", res.Steps[0].Reached)
	}
}
