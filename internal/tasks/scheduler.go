// Package tasks runs UMMarr's background jobs on a schedule and on demand,
// and reports on them - Sonarr's System → Tasks.
package tasks

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

// Task is one job. Interval 0 means it only runs when asked.
type Task struct {
	Name        string
	Description string
	Interval    time.Duration
	Run         func(ctx context.Context) error

	mu           sync.Mutex
	running      bool
	lastStarted  time.Time
	lastFinished time.Time
	lastDuration time.Duration
	lastError    string
	runs         int
}

// Status is a task as the page shows it.
type Status struct {
	Name, Description string
	Interval          time.Duration
	Running           bool
	LastRun           time.Time
	NextRun           time.Time
	LastDuration      time.Duration
	LastError         string
	Runs              int
}

// Scheduler holds the tasks and ticks them.
type Scheduler struct {
	mu    sync.Mutex
	tasks []*Task
	now   func() time.Time
}

// New builds an empty scheduler.
func New() *Scheduler { return &Scheduler{now: time.Now} }

// Register adds a task; a task with an interval first runs one tick after
// Start, so a restart doesn't fire everything at once.
func (s *Scheduler) Register(t *Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = append(s.tasks, t)
}

// Start ticks every tick, running whatever is due, until ctx ends.
func (s *Scheduler) Start(ctx context.Context, tick time.Duration) {
	go func() {
		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runDue(ctx)
			}
		}
	}()
}

func (s *Scheduler) runDue(ctx context.Context) {
	s.mu.Lock()
	tasks := append([]*Task(nil), s.tasks...)
	s.mu.Unlock()
	now := s.now()
	for _, t := range tasks {
		if t.Interval <= 0 {
			continue
		}
		t.mu.Lock()
		due := !t.running && (t.lastStarted.IsZero() || now.Sub(t.lastStarted) >= t.Interval)
		t.mu.Unlock()
		if due {
			go s.run(ctx, t)
		}
	}
}

func (s *Scheduler) run(ctx context.Context, t *Task) {
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return
	}
	t.running, t.lastStarted = true, s.now()
	t.mu.Unlock()
	err := t.Run(ctx)
	t.mu.Lock()
	t.running, t.lastFinished = false, s.now()
	t.lastDuration = t.lastFinished.Sub(t.lastStarted)
	t.lastError = ""
	if err != nil {
		t.lastError = err.Error()
		log.Printf("task %s: %v", t.Name, err)
	}
	t.runs++
	t.mu.Unlock()
}

// RunNow starts a task in the background; it says so if the task is already
// running or unknown.
func (s *Scheduler) RunNow(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tasks {
		if t.Name != name {
			continue
		}
		t.mu.Lock()
		running := t.running
		t.mu.Unlock()
		if running {
			return fmt.Errorf("%s is already running", name)
		}
		go s.run(context.WithoutCancel(ctx), t)
		return nil
	}
	return fmt.Errorf("no task called %q", name)
}

// Statuses reports every task, by name.
func (s *Scheduler) Statuses() []Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Status, 0, len(s.tasks))
	for _, t := range s.tasks {
		t.mu.Lock()
		st := Status{Name: t.Name, Description: t.Description, Interval: t.Interval, Running: t.running, LastRun: t.lastFinished, LastDuration: t.lastDuration, LastError: t.lastError, Runs: t.runs}
		if t.Interval > 0 {
			if t.lastStarted.IsZero() {
				st.NextRun = s.now()
			} else {
				st.NextRun = t.lastStarted.Add(t.Interval)
			}
		}
		t.mu.Unlock()
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
