package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunLeaderScopedWorkersWaitsForShutdownBeforeRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var leader atomic.Bool
	var active atomic.Int32
	var maxActive atomic.Int32
	var starts atomic.Int32
	worker := func(workerCtx context.Context) {
		current := active.Add(1)
		starts.Add(1)
		for {
			maximum := maxActive.Load()
			if current <= maximum || maxActive.CompareAndSwap(maximum, current) {
				break
			}
		}
		<-workerCtx.Done()
		// Simulate cleanup that must finish before another leader worker starts.
		time.Sleep(50 * time.Millisecond)
		active.Add(-1)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		runLeaderScopedWorkers(ctx, leader.Load, 5*time.Millisecond, []func(context.Context){worker})
	}()

	leader.Store(true)
	waitForAtomicValue(t, &starts, 1)
	leader.Store(false)
	waitForAtomicValue(t, &active, 0)
	leader.Store(true)
	waitForAtomicValue(t, &starts, 2)

	if maximum := maxActive.Load(); maximum != 1 {
		t.Fatalf("leader workers overlapped: max active = %d", maximum)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("leader worker coordinator did not stop")
	}
}

func waitForAtomicValue(t *testing.T, value *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if value.Load() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("value = %d, want %d", value.Load(), want)
}
