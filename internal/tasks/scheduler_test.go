package tasks

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestScheduler(t *testing.T) {
	s := New()
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	var runs atomic.Int32
	s.Register(&Task{Name: "Tick", Interval: time.Hour, Run: func(context.Context) error { runs.Add(1); return nil }})
	s.Register(&Task{Name: "Manual", Run: func(context.Context) error { return errors.New("boom") }})

	s.runDue(context.Background())
	waitFor(t, func() bool { return runs.Load() == 1 })
	s.runDue(context.Background())
	time.Sleep(20 * time.Millisecond)
	if runs.Load() != 1 {
		t.Fatalf("want no rerun before the interval, got %d", runs.Load())
	}
	now = now.Add(2 * time.Hour)
	s.runDue(context.Background())
	waitFor(t, func() bool { return runs.Load() == 2 })

	if err := s.RunNow(context.Background(), "Manual"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		for _, st := range s.Statuses() {
			if st.Name == "Manual" && st.Runs == 1 {
				return st.LastError == "boom"
			}
		}
		return false
	})
	if err := s.RunNow(context.Background(), "Nope"); err == nil {
		t.Fatal("want unknown task refused")
	}
	if st := s.Statuses(); st[1].Name != "Tick" || st[1].NextRun != now.Add(time.Hour) {
		t.Fatalf("want statuses by name with the next run, got %+v", st)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
