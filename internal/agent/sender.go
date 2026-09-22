package agent

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// Sender posts payloads to the server. Payloads that fail for a temporary
// reason are kept (oldest first) and retried with the next call.
type Sender struct {
	// Capabilities advertised to the server (e.g. "explain").
	Capabilities string
	mu           sync.Mutex
	base         string
	url          string
	token        string
	client       *http.Client
	pending      []protocol.Payload
	maxPending   int
}

var errRejected = errors.New("rejected by the server")

func NewSender(server, token string) *Sender {
	return &Sender{
		base:  strings.TrimRight(server, "/"),
		url:   strings.TrimRight(server, "/") + "/api/v1/ingest",
		token: token,
		client: &http.Client{
			Timeout: 20 * time.Second,
			// Never follow redirects: the token must reach the configured server only.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		maxPending: 240,
	}
}

func (s *Sender) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

// Send queues the payload and flushes the queue. It returns the merged
// instructions of every accepted response.
func (s *Sender) Send(ctx context.Context, p protocol.Payload) (protocol.IngestResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pending = append(s.pending, p)
	if over := len(s.pending) - s.maxPending; over > 0 {
		s.pending = s.pending[over:]
	}

	var merged protocol.IngestResponse
	for len(s.pending) > 0 {
		resp, err := s.post(ctx, s.pending[0])
		if errors.Is(err, errRejected) {
			// Retrying the same payload will not help (bad token, invalid body).
			s.pending = s.pending[1:]
			return merged, err
		}
		if err != nil {
			return merged, err
		}
		s.pending = s.pending[1:]
		merged.OK = true
		merged.Explain = append(merged.Explain, resp.Explain...)
		merged.ServerVersion, merged.AgentDownload = resp.ServerVersion, resp.AgentDownload
	}
	return merged, nil
}

// Fetch reads a file from the same server the measures go to, with the same token: the agent
// never downloads from anywhere else.
func (s *Sender) Fetch(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("User-Agent", "slowpoke-agent/"+Version)
	res, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return nil, fmt.Errorf("GET %s: HTTP %d %s", path, res.StatusCode, strings.TrimSpace(string(msg)))
	}
	return io.ReadAll(io.LimitReader(res.Body, 64<<20))
}

func (s *Sender) post(ctx context.Context, p protocol.Payload) (protocol.IngestResponse, error) {
	var body bytes.Buffer
	zw := gzip.NewWriter(&body)
	if err := json.NewEncoder(zw).Encode(p); err != nil {
		return protocol.IngestResponse{}, err
	}
	if err := zw.Close(); err != nil {
		return protocol.IngestResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, &body)
	if err != nil {
		return protocol.IngestResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("User-Agent", "slowpoke-agent/"+Version)
	// So the server can say where its own agent for this machine is, without opening the payload.
	req.Header.Set("X-Slowpoke-Platform", AgentPlatform())
	if s.Capabilities != "" {
		req.Header.Set("X-Slowpoke-Capabilities", s.Capabilities)
	}

	res, err := s.client.Do(req)
	if err != nil {
		return protocol.IngestResponse{}, err
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 && res.StatusCode < 500 && res.StatusCode != http.StatusTooManyRequests && res.StatusCode != http.StatusRequestTimeout {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 500))
		return protocol.IngestResponse{}, fmt.Errorf("%w: HTTP %d %s", errRejected, res.StatusCode, strings.TrimSpace(string(msg)))
	}
	if res.StatusCode >= 300 {
		return protocol.IngestResponse{}, fmt.Errorf("server: HTTP %d", res.StatusCode)
	}

	var out protocol.IngestResponse
	_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out)
	return out, nil
}
