package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOllamaLimitsConcurrentRequests(t *testing.T) {
	var active, peak atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{}, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count := active.Add(1)
		for {
			old := peak.Load()
			if count <= old || peak.CompareAndSwap(old, count) {
				break
			}
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"content":"{\"actions\":[],\"narrative\":\"Ход.\",\"memory\":[]}"}}`))
	}))
	defer server.Close()
	provider := New(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := provider.GenerateTurn(ctx, "test", Input{}); err != nil {
				t.Errorf("request failed: %v", err)
			}
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-ctx.Done():
			close(release)
			t.Fatal("two requests did not start")
		}
	}
	if peak.Load() != 2 {
		close(release)
		t.Fatal("expected two active requests")
	}
	close(release)
	wg.Wait()
	if peak.Load() > 2 {
		t.Fatal("Ollama received more than two simultaneous requests", peak.Load())
	}
}
