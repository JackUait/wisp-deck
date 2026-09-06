package attention

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Each supervisor tick asks for the whole process table twice: once to track
// the launch tree's descendants, once inside the registry poll. They run
// milliseconds apart and want the same answer, so the tick reads it once and
// hands it to both. At 4Hz in every open session the duplicate read was ~35%
// of a core on a 17-session deck.
func TestClaudeSupervisorTick_reads_the_process_table_once_and_shares_it(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var mu sync.Mutex
	snapshots := 0
	polls := 0
	sharedEmpty := 0
	s := ClaudeSupervisor{
		PollInterval: 5 * time.Millisecond,
		Snapshot: func(context.Context) ([]SupervisorProcess, error) {
			mu.Lock()
			snapshots++
			mu.Unlock()
			return []SupervisorProcess{{PID: 1, PPID: 0, StartSec: 1_752_397_200}}, nil
		},
		Poll: func(_ context.Context, _ int, processes []SupervisorProcess) error {
			mu.Lock()
			polls++
			if len(processes) == 0 {
				sharedEmpty++
			}
			mu.Unlock()
			return nil
		},
	}
	if _, err := s.Run(ctx, []string{"bash", "-c", "sleep 0.12"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if polls == 0 {
		t.Fatal("poll callback was never called")
	}
	if sharedEmpty != 0 {
		t.Errorf("%d of %d polls received no process table; the tick's own read must be handed over", sharedEmpty, polls)
	}
	// One read per tick. The pre-loop priming tick reads once too, so the count
	// tracks the polls rather than doubling them.
	if snapshots > polls {
		t.Errorf("process table read %d times across %d polls; it must be read once per tick", snapshots, polls)
	}
}
