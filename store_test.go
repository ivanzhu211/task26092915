package funnel

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInsertBatchValidation(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Unix(1700000000, 0)

	bad := []Touchpoint{
		{CustomerID: "a", OccurredAt: now, Channel: ChannelEmail, EventType: EventEmailOpen},
		{CustomerID: "", OccurredAt: now, Channel: ChannelEmail, EventType: EventEmailOpen},
	}
	err = s.InsertBatch(ctx, bad)
	var be *BatchError
	if !errors.As(err, &be) || be.Index != 1 {
		t.Fatalf("want BatchError index 1, got %v", err)
	}
	// 整批拒收：库里应无数据。
	pts, err := s.Touchpoints(ctx, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 0 {
		t.Fatalf("rejected batch left %d rows", len(pts))
	}

	if err := s.InsertBatch(ctx, []Touchpoint{{CustomerID: "a", OccurredAt: now, Channel: "SmokeSignals", EventType: EventEmailOpen}}); err == nil {
		t.Fatal("invalid channel accepted")
	}
	if err := s.InsertBatch(ctx, []Touchpoint{{CustomerID: "a", OccurredAt: now, Channel: ChannelEmail, EventType: "teleport"}}); err == nil {
		t.Fatal("invalid event type accepted")
	}
	if err := s.InsertBatch(ctx, make([]Touchpoint, MaxBatchSize+1)); err == nil {
		t.Fatal("oversized batch accepted")
	}
}

func TestTouchpointsOrderingAndRange(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	base := time.Unix(1700000000, 0)

	// 同一秒三条，先写先排；范围外一条。
	b1 := []Touchpoint{
		{CustomerID: "c", OccurredAt: base, Channel: ChannelEmail, EventType: EventEmailOpen},
		{CustomerID: "a", OccurredAt: base, Channel: ChannelWebsite, EventType: EventPageView},
	}
	b2 := []Touchpoint{
		{CustomerID: "b", OccurredAt: base, Channel: ChannelMobileApp, EventType: EventAddToCart},
		{CustomerID: "d", OccurredAt: base.Add(2 * time.Hour), Channel: ChannelEmail, EventType: EventPurchase},
		{CustomerID: "e", OccurredAt: base.Add(-time.Second), Channel: ChannelEmail, EventType: EventPurchase},
	}
	if err := s.InsertBatch(ctx, b1); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertBatch(ctx, b2); err != nil {
		t.Fatal(err)
	}
	pts, err := s.Touchpoints(ctx, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 3 {
		t.Fatalf("want 3 touchpoints, got %d", len(pts))
	}
	want := []string{"c", "a", "b"}
	for i, w := range want {
		if pts[i].CustomerID != w {
			t.Fatalf("position %d: want %s, got %s", i, w, pts[i].CustomerID)
		}
	}
}
