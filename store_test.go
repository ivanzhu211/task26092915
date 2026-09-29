package funnel

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func tp(customer string, ts time.Time, ch Channel, ev EventType) Touchpoint {
	return Touchpoint{CustomerID: customer, OccurredAt: ts, Channel: ch, Event: ev}
}

func TestInsertBatchRejectsInvalidEntry(t *testing.T) {
	s := openTestStore(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	batch := []Touchpoint{
		tp("c1", base, ChannelEmail, EventEmailOpen),
		tp("c2", base, Channel("Pigeon"), EventEmailOpen),
		tp("c3", base, ChannelEmail, EventPurchase),
	}
	err := s.InsertBatch(batch)
	var be *BatchError
	if !errors.As(err, &be) {
		t.Fatalf("expected BatchError, got %v", err)
	}
	if be.Index != 1 {
		t.Fatalf("expected index 1, got %d", be.Index)
	}
	if !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("expected ErrInvalidChannel, got %v", err)
	}
	rows, err := s.loadRange(base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("batch should be fully rejected, found %d rows", len(rows))
	}
}

func TestInsertBatchRejectsEmptyCustomerAndBadEvent(t *testing.T) {
	s := openTestStore(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.InsertBatch([]Touchpoint{tp("", base, ChannelEmail, EventEmailOpen)}); !errors.Is(err, ErrEmptyCustomerID) {
		t.Fatalf("expected ErrEmptyCustomerID, got %v", err)
	}
	if err := s.InsertBatch([]Touchpoint{tp("c1", base, ChannelEmail, EventType("fly"))}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("expected ErrInvalidEvent, got %v", err)
	}
}

func TestInsertBatchTooLarge(t *testing.T) {
	s := openTestStore(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	batch := make([]Touchpoint, MaxBatchSize+1)
	for i := range batch {
		batch[i] = tp("c1", base, ChannelEmail, EventEmailOpen)
	}
	if err := s.InsertBatch(batch); !errors.Is(err, ErrBatchTooLarge) {
		t.Fatalf("expected ErrBatchTooLarge, got %v", err)
	}
	batch = batch[:MaxBatchSize]
	if err := s.InsertBatch(batch); err != nil {
		t.Fatalf("5000-entry batch should succeed: %v", err)
	}
}

func TestSameSecondOrderedByInsertion(t *testing.T) {
	s := openTestStore(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.InsertBatch([]Touchpoint{
		tp("c1", base, ChannelEmail, EventEmailOpen),
		tp("c1", base, ChannelWebsite, EventPageView),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertBatch([]Touchpoint{
		tp("c1", base, ChannelWebsite, EventPurchase),
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.loadRange(base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	want := []EventType{EventEmailOpen, EventPageView, EventPurchase}
	for i, r := range rows {
		if r.event != want[i] {
			t.Fatalf("row %d: expected %s, got %s", i, want[i], r.event)
		}
	}
}
