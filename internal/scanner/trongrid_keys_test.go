package scanner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/degoke/tronvent/internal/config"
)

func TestNewTronGridAPIKeyPool(t *testing.T) {
	pool := newTronGridAPIKeyPool(" key-a, key-b, key-a, ,key-c ")

	got := make([]string, 0, pool.count())
	for range 3 {
		_, key, err := pool.nextAvailable(context.Background())
		if err != nil {
			t.Fatalf("nextAvailable() error: %v", err)
		}
		got = append(got, key)
	}

	want := []string{"key-a", "key-b", "key-c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %#v, want %#v", got, want)
	}
}

func TestTronGridAPIKeyPoolSkipsCooldown(t *testing.T) {
	pool := newTronGridAPIKeyPool("key-a,key-b")
	index, key, err := pool.nextAvailable(context.Background())
	if err != nil {
		t.Fatalf("nextAvailable() error: %v", err)
	}
	if key != "key-a" {
		t.Fatalf("first key = %q, want key-a", key)
	}
	pool.cooldown(index, time.Minute)

	_, key, err = pool.nextAvailable(context.Background())
	if err != nil {
		t.Fatalf("nextAvailable() after cooldown error: %v", err)
	}
	if key != "key-b" {
		t.Fatalf("key after cooldown = %q, want key-b", key)
	}
}

func TestRetryAfterDuration(t *testing.T) {
	now := time.Date(2026, 9, 8, 14, 0, 0, 0, time.UTC)
	if got := retryAfterDuration("3", now); got != 3*time.Second {
		t.Errorf("seconds Retry-After = %s, want 3s", got)
	}
	if got := retryAfterDuration(now.Add(4*time.Second).Format(http.TimeFormat), now); got != 4*time.Second {
		t.Errorf("date Retry-After = %s, want 4s", got)
	}
	if got := retryAfterDuration("invalid", now); got != 0 {
		t.Errorf("invalid Retry-After = %s, want 0", got)
	}
}

func TestTronGridDoUsesNextKeyAfterRateLimit(t *testing.T) {
	var mu sync.Mutex
	var keys []string
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		keys = append(keys, r.Header.Get("TRON-PRO-API-KEY"))
		requestNumber := requests
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if requestNumber == 1 {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	p := &Poller{
		cfg:        &config.Config{TronGridAPIKey: "key-a,key-b"},
		httpClient: srv.Client(),
		sem:        make(chan struct{}, 5),
		keys:       newTronGridAPIKeyPool("key-a,key-b"),
	}

	var response map[string]bool
	if err := p.tronGridDo(context.Background(), http.MethodGet, srv.URL, nil, &response); err != nil {
		t.Fatalf("tronGridDo() error: %v", err)
	}
	if !response["ok"] {
		t.Fatalf("unexpected response: %#v", response)
	}

	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(keys, []string{"key-a", "key-b"}) {
		t.Fatalf("request keys = %#v, want [key-a key-b]", keys)
	}
}
