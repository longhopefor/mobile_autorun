package modelconnector

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"log/slog"
	"mobile_autorun/internal/device"
	"mobile_autorun/internal/domain"
	"mobile_autorun/internal/model"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

func pidString(p int) string { return strconv.Itoa(p) }
func Serve(ctx context.Context, cfg Config) error {
	u, e := url.Parse(cfg.ServerURL)
	if e != nil || u.Host == "" || (u.Scheme != "wss" && !(u.Scheme == "ws" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return errors.New("wss_or_loopback_ws_required")
	}
	if cfg.Token == "" {
		return errors.New("connector_token_required")
	}
	for {
		e = serveOnce(ctx, cfg)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("model connector disconnected; retrying", "error", e)
		timer := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func serveOnce(parent context.Context, cfg Config) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+cfg.Token)
	conn, _, e := websocket.Dial(ctx, cfg.ServerURL, &websocket.DialOptions{HTTPHeader: headers})
	if e != nil {
		return e
	}
	defer conn.CloseNow()
	conn.SetReadLimit(32 << 20)
	cli := &CLI{Config: cfg}
	var mu sync.Mutex
	active := map[string]context.CancelFunc{}
	semaphore := make(chan struct{}, 1)
	defer func() {
		mu.Lock()
		for _, c := range active {
			c()
		}
		mu.Unlock()
	}()
	for {
		var f device.Frame
		if e = wsjson.Read(ctx, conn, &f); e != nil {
			return e
		}
		if f.Method == "model/cancel" {
			var v struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(f.Params, &v)
			mu.Lock()
			c := active[v.ID]
			mu.Unlock()
			if c != nil {
				c()
			}
			continue
		}
		if f.Method != "model/next" || f.ID == "" {
			continue
		}
		var q model.Request
		if e = json.Unmarshal(f.Params, &q); e != nil {
			return errors.New("invalid_model_request")
		}
		deadline, e := time.Parse(time.RFC3339Nano, q.Deadline)
		if e != nil {
			return errors.New("invalid_request_deadline")
		}
		if deadline.After(time.Now().Add(120 * time.Second)) {
			deadline = time.Now().Add(120 * time.Second)
		}
		jobCtx, stop := context.WithDeadline(ctx, deadline)
		mu.Lock()
		if _, exists := active[f.ID]; exists {
			mu.Unlock()
			stop()
			continue
		}
		active[f.ID] = stop
		mu.Unlock()
		go func(id string, q model.Request) {
			defer stop()
			defer func() { mu.Lock(); delete(active, id); mu.Unlock() }()
			var result model.Result
			var err error
			select {
			case semaphore <- struct{}{}:
				result, err = cli.Next(jobCtx, q)
				<-semaphore
			case <-jobCtx.Done():
				err = jobCtx.Err()
			}
			frame := device.Frame{ID: id, Status: "success"}
			if err != nil {
				frame.Status = "error"
				code := domain.ModelErrorCode(err)
				if errors.Is(err, context.DeadlineExceeded) {
					code = "model_timeout"
				}
				if errors.Is(err, domain.ErrWorkBuddyLoginRequired) {
					code = "workbuddy_login_required"
				}
				frame.Error, _ = json.Marshal(map[string]string{"code": code})
				slog.Warn("model call failed", "request_id", id, "provider", q.Profile.Provider, "code", code)
			} else {
				frame.Result, _ = json.Marshal(result)
			}
			writeCtx, done := context.WithTimeout(ctx, 5*time.Second)
			defer done()
			_ = wsjson.Write(writeCtx, conn, frame)
		}(f.ID, q)
	}
}
