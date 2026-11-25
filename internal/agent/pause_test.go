package agent

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestPauserLease(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	p := &Pauser{now: func() time.Time { return now }}
	ran := 0
	if !p.RunTick(func() { ran++ }) || ran != 1 {
		t.Fatal("an unpaused tick did not run")
	}
	if err := p.Pause("cli-1", "cwm migrate", time.Minute); err != nil {
		t.Fatal(err)
	}
	if p.RunTick(func() { ran++ }) || ran != 1 {
		t.Fatal("a paused tick ran")
	}
	if err := p.Pause("dashboard", "the dashboard's migration", 0); !errors.Is(err, ErrPaused) {
		t.Fatalf("second holder = %v", err)
	}
	if err := p.Pause("cli-1", "cwm migrate", time.Minute); err != nil {
		t.Fatalf("renewal = %v", err)
	}
	p.Resume("someone-else")
	if h, _ := p.Paused(); h != "cli-1" {
		t.Fatal("a foreign resume dropped the lease")
	}
	// The holder died: the lease runs out on its own.
	now = now.Add(61 * time.Second)
	if h, _ := p.Paused(); h != "" {
		t.Fatal("an expired lease still holds the tick")
	}
	if err := p.Pause("dashboard", "x", 0); err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	if h, _ := p.Paused(); h != "dashboard" {
		t.Fatal("an in-process pause expired")
	}
	p.Resume("dashboard")
	if !p.RunTick(func() {}) {
		t.Fatal("tick not resumed")
	}
}

// Pause returns only after a tick that was already running has finished.
func TestPauseWaitsForRunningTick(t *testing.T) {
	p := &Pauser{}
	started, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	finished := false
	go func() {
		defer wg.Done()
		p.RunTick(func() { close(started); <-release; finished = true })
	}()
	<-started
	done := make(chan struct{})
	go func() { _ = p.Pause("cli-1", "m", time.Minute); close(done) }()
	select {
	case <-done:
		t.Fatal("Pause returned while a tick was running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-done
	wg.Wait()
	if !finished {
		t.Fatal("tick did not finish")
	}
}
