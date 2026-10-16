package device

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"mobile_autorun/internal/domain"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Frame struct {
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Status string          `json:"status,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

// RemoteError preserves a Portal RPC error for capability negotiation.
// Model diagnostics intentionally do not use this type.
type RemoteError struct {
	Method  string
	Message string
}

func (e *RemoteError) Error() string { return fmt.Sprintf("portal_%s_failed: %s", e.Method, e.Message) }

type session struct {
	conn       *websocket.Conn
	mu         sync.Mutex
	pending    map[string]chan Frame
	done       chan struct{}
	revision   atomic.Uint64
	generation uint64
	transport  string
}
type Gateway struct {
	mu        sync.RWMutex
	sessions  map[string]*session
	revisions atomic.Uint64
}

func NewGateway() *Gateway { return &Gateway{sessions: map[string]*session{}} }
func (g *Gateway) Online(id string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.sessions[id] != nil
}
func (g *Gateway) Revision(id string) uint64 {
	g.mu.RLock()
	s := g.sessions[id]
	g.mu.RUnlock()
	if s == nil {
		return 0
	}
	return s.revision.Load()
}
func (g *Gateway) Generation(id string) uint64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if s := g.sessions[id]; s != nil {
		return s.generation
	}
	return 0
}
func (g *Gateway) Serve(ctx context.Context, id string, c *websocket.Conn) {
	g.ServeTransport(ctx, id, c, "ws")
}

// Transport is the authenticated client's declared route, not USB hardware detection.
func (g *Gateway) Transport(id string) string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if s := g.sessions[id]; s != nil {
		return s.transport
	}
	return ""
}

func (g *Gateway) ServeTransport(ctx context.Context, id string, c *websocket.Conn, transport string) {
	c.SetReadLimit(32 << 20)
	s := &session{conn: c, pending: map[string]chan Frame{}, done: make(chan struct{}), transport: transport}
	s.revision.Store(g.revisions.Add(1))
	s.generation = s.revision.Load()
	g.mu.Lock()
	old := g.sessions[id]
	g.sessions[id] = s
	g.mu.Unlock()
	if old != nil {
		old.conn.CloseNow()
	}
	// Bound detection of lost USB/network routes, even when no RPC is in flight.
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				pingCtx, cancel := context.WithTimeout(heartbeatCtx, 5*time.Second)
				err := c.Ping(pingCtx)
				cancel()
				if err != nil {
					c.CloseNow()
					return
				}
			}
		}
	}()
	defer func() {
		close(s.done)
		c.CloseNow()
		g.mu.Lock()
		if g.sessions[id] == s {
			delete(g.sessions, id)
		}
		g.mu.Unlock()
	}()
	for {
		var f Frame
		if err := wsjson.Read(ctx, c, &f); err != nil {
			return
		}
		if f.ID == "" {
			s.revision.Store(g.revisions.Add(1))
			continue
		}
		s.mu.Lock()
		ch := s.pending[f.ID]
		s.mu.Unlock()
		if ch != nil {
			select {
			case ch <- f:
			default:
			}
		}
	}
}
func (g *Gateway) Call(ctx context.Context, id, method string, params any) (json.RawMessage, error) {
	return g.CallAtRevision(ctx, id, method, params, 0)
}

// Bind an action to the observed session; a replacement never receives stale actions.
func (g *Gateway) CallAtRevision(ctx context.Context, id, method string, params any, revision uint64) (json.RawMessage, error) {
	g.mu.RLock()
	s := g.sessions[id]
	g.mu.RUnlock()
	if s == nil {
		return nil, domain.ErrOffline
	}
	if revision != 0 && s.revision.Load() != revision {
		return nil, domain.ErrScreenChanged
	}
	reqID := uuid.NewString()
	b, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	ch := make(chan Frame, 1)
	s.mu.Lock()
	s.pending[reqID] = ch
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, reqID); s.mu.Unlock() }()
	if err = wsjson.Write(ctx, s.conn, Frame{ID: reqID, Method: method, Params: b}); err != nil {
		return nil, domain.ErrOffline
	}
	select {
	case <-s.done:
		return nil, domain.ErrOffline
	case <-ctx.Done():
		// CLI connector can cancel the process. Portal has no command cancellation guarantee.
		if method == "model/next" {
			go func() {
				cancelCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				_ = wsjson.Write(cancelCtx, s.conn, Frame{Method: "model/cancel", Params: json.RawMessage(`{"id":"` + reqID + `"}`)})
			}()
		}
		return nil, ctx.Err()
	case f := <-ch:
		if f.Status != "success" {
			// Only known model error codes are translated; never forward messages.
			if strings.HasPrefix(method, "model/") {
				var diagnostic struct {
					Code string `json:"code"`
				}
				if json.Unmarshal(f.Error, &diagnostic) == nil {
					if diagnostic.Code == "workbuddy_login_required" {
						return nil, domain.ErrWorkBuddyLoginRequired
					}
					if modelErr := domain.ModelErrorForCode(diagnostic.Code); modelErr != nil {
						return nil, modelErr
					}
				}
			}
			// CLI diagnostics can contain credentials; keep them local. Portal
			// RPC errors are needed to distinguish permission and capture failures.
			if !strings.HasPrefix(method, "model/") {
				var message string
				if json.Unmarshal(f.Error, &message) == nil && message != "" {
					message = strings.Map(func(r rune) rune {
						if r < 32 {
							return ' '
						}
						return r
					}, message)
					if len(message) > 256 {
						message = message[:256]
					}
					return nil, &RemoteError{Method: method, Message: message}
				}
			}
			return nil, errors.New("remote_call_failed")
		}
		return f.Result, nil
	}
}
