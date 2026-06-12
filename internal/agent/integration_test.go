package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAgentRunPushesWhenReady(t *testing.T) {
	var pushes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		pushes.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	config := &Config{
		IngestToken:         "test-token",
		ApiEndpoint:         srv.URL,
		PushSchedule:        "*/1 * * * * *",
		SampleInterval:      50 * time.Millisecond,
		SlowMetricsInterval: time.Hour,
	}

	a := New(config, "2.0.0")
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		a.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("agent run did not stop")
	}

	if pushes.Load() == 0 {
		t.Fatal("expected at least one push")
	}
}
