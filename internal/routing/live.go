package routing

import (
	"context"
	"net/http"
	"sync/atomic"
)

// LiveGateway only swaps a complete verified route snapshot. A reload failure
// lowers readiness while preserving the last healthy public route. Private
// requests continue to consult the current DB-backed session authorizer.
type LiveGateway struct {
	control  *DurableRegistry
	sessions *PreviewSessions
	snapshot atomic.Pointer[Registry]
	ready    atomic.Bool
}

func NewLiveGateway(control *DurableRegistry, sessions *PreviewSessions) *LiveGateway {
	return &LiveGateway{control: control, sessions: sessions}
}

func (g *LiveGateway) Reload(ctx context.Context) error {
	snapshot, err := g.control.LoadSnapshot(ctx)
	if err != nil {
		g.ready.Store(false)
		return err
	}
	g.snapshot.Store(snapshot)
	g.ready.Store(true)
	return nil
}

func (g *LiveGateway) Ready() bool { return g.ready.Load() }

func (g *LiveGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	snapshot := g.snapshot.Load()
	if snapshot == nil {
		http.Error(w, "route snapshot unavailable", http.StatusServiceUnavailable)
		return
	}
	NewGatewayWithSessions(snapshot, g.sessions, g.control).ServeHTTP(w, r)
}
