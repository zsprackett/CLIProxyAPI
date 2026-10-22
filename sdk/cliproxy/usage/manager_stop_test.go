package usage

import (
	"context"
	"sync/atomic"
	"testing"
)

// closeGatedPlugin holds its first record until the manager is closed, so the
// rest of the queue is still undelivered when Stop starts.
type closeGatedPlugin struct {
	manager *Manager
	gated   atomic.Bool
	seen    atomic.Int64
}

func (p *closeGatedPlugin) HandleUsage(_ context.Context, _ Record) {
	if p.gated.CompareAndSwap(false, true) {
		p.manager.mu.Lock()
		for !p.manager.closed {
			p.manager.cond.Wait()
		}
		p.manager.mu.Unlock()
	}
	p.seen.Add(1)
}

func TestManagerStopWaitsUntilQueuedRecordsReachPlugins(t *testing.T) {
	const published = 50
	manager := NewManager(0)
	plugin := &closeGatedPlugin{manager: manager}
	manager.Register(plugin)
	manager.Start(context.Background())
	for i := 0; i < published; i++ {
		manager.Publish(context.Background(), Record{Provider: "claude", Model: "claude-opus-5-5"})
	}

	manager.Stop()

	if got := plugin.seen.Load(); got != published {
		t.Fatalf("plugin saw %d records when Stop returned, want %d", got, published)
	}
	manager.Publish(context.Background(), Record{Provider: "claude"})
	manager.Stop()
	if got := plugin.seen.Load(); got != published {
		t.Fatalf("plugin saw %d records after a publish past Stop, want %d", got, published)
	}
}

func TestManagerStopWithoutStartReturns(t *testing.T) {
	manager := NewManager(0)
	manager.Stop()
	manager.Stop()
}
