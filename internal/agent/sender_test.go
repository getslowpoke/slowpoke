package agent

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

func TestSenderPostsGzippedJSONWithToken(t *testing.T) {
	var got protocol.Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ingest" || r.Header.Get("Authorization") != "Bearer spk_test" || r.Header.Get("Content-Encoding") != "gzip" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		zr, _ := gzip.NewReader(r.Body)
		_ = json.NewDecoder(zr).Decode(&got)
		_ = json.NewEncoder(w).Encode(protocol.IngestResponse{OK: true, Explain: []protocol.ExplainRequest{{Hash: "abc", SQL: "select 1"}}})
	}))
	defer srv.Close()

	s := NewSender(srv.URL, "spk_test")
	resp, err := s.Send(context.Background(), protocol.Payload{BatchID: "b1", HTTP: []protocol.HTTPBucket{{Source: "nginx:app", N: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	if got.BatchID != "b1" || got.HTTP[0].N != 3 {
		t.Fatalf("payload %+v", got)
	}
	if len(resp.Explain) != 1 || resp.Explain[0].Hash != "abc" {
		t.Fatalf("response %+v", resp)
	}
}

func TestSenderKeepsPayloadsWhileServerIsDown(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	var batches []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		zr, _ := gzip.NewReader(r.Body)
		var p protocol.Payload
		_ = json.NewDecoder(zr).Decode(&p)
		batches = append(batches, p.BatchID)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	s := NewSender(srv.URL, "spk_test")
	if _, err := s.Send(context.Background(), protocol.Payload{BatchID: "1", HTTP: []protocol.HTTPBucket{{N: 1}}}); err == nil {
		t.Fatal("expected error")
	}
	if s.Pending() != 1 {
		t.Fatalf("pending %d", s.Pending())
	}

	fail.Store(false)
	if _, err := s.Send(context.Background(), protocol.Payload{BatchID: "2", HTTP: []protocol.HTTPBucket{{N: 1}}}); err != nil {
		t.Fatal(err)
	}
	if s.Pending() != 0 || len(batches) != 2 || batches[0] != "1" || batches[1] != "2" {
		t.Fatalf("old payload must be sent first: %v pending=%d", batches, s.Pending())
	}
}

func TestSenderDropsOldestWhenQueueIsFull(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	s := NewSender(srv.URL, "x")
	s.maxPending = 3
	for i := 0; i < 5; i++ {
		_, _ = s.Send(context.Background(), protocol.Payload{BatchID: string(rune('a' + i)), HTTP: []protocol.HTTPBucket{{N: 1}}})
	}
	if s.Pending() != 3 || s.pending[0].BatchID != "c" {
		t.Fatalf("pending %d first %q", s.Pending(), s.pending[0].BatchID)
	}
}

func TestSenderDoesNotRetryRejectedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	s := NewSender(srv.URL, "wrong")
	if _, err := s.Send(context.Background(), protocol.Payload{BatchID: "1", HTTP: []protocol.HTTPBucket{{N: 1}}}); err == nil {
		t.Fatal("expected error")
	}
	if s.Pending() != 0 {
		t.Fatal("a 401 will not heal by retrying the same payload")
	}
}

func TestSenderAcceptsQueuedResponsesAndRetriesOn429(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if r.Header.Get("X-Slowpoke-Capabilities") != "explain" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	s := NewSender(srv.URL, "t")
	s.Capabilities = "explain"
	if _, err := s.Send(context.Background(), protocol.Payload{BatchID: "1"}); err == nil || s.Pending() != 1 {
		t.Fatalf("429 must keep the payload: err=%v pending=%d", err, s.Pending())
	}
	if _, err := s.Send(context.Background(), protocol.Payload{BatchID: "2"}); err != nil || s.Pending() != 0 {
		t.Fatalf("202 is success: err=%v pending=%d", err, s.Pending())
	}
}

func decodeGzip(r *http.Request, v any) error {
	zr, err := gzip.NewReader(r.Body)
	if err != nil {
		return err
	}
	return json.NewDecoder(zr).Decode(v)
}
