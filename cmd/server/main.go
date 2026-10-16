package main

import (
	"context"
	"errors"
	"log/slog"
	"mobile_autorun/internal/app"
	"mobile_autorun/internal/httpapi"
	"mobile_autorun/internal/store"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func env(k, f string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return f
}
func main() {
	if e := run(); e != nil {
		slog.Error("server stopped", "error", e)
		os.Exit(1)
	}
}
func run() error {
	ctx, done := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer done()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is required; start PostgreSQL first")
	}
	s, e := store.New(ctx, url, env("DATA_DIR", ".data"))
	if e != nil {
		return e
	}
	defer s.Pool.Close()
	if e = s.Migrate(ctx); e != nil {
		return e
	}
	if e = s.Bootstrap(ctx, env("ADMIN_USERNAME", "admin"), os.Getenv("ADMIN_PASSWORD")); e != nil {
		return e
	}
	a, e := app.New(ctx, s)
	if e != nil {
		return e
	}
	if e = s.Recover(ctx); e != nil {
		return e
	}
	a.StartCampaigns(ctx)
	if e = a.Queue.Start(ctx); e != nil {
		return e
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = a.Queue.Stop(c)
	}()
	handler := (&httpapi.Server{Service: a, Origin: env("WEB_ORIGIN", "http://localhost:5173")}).Routes()
	srv := &http.Server{Addr: env("LISTEN_ADDR", "127.0.0.1:8080"), Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	errCh := make(chan error, 1)
	go func() { slog.Info("phone workbench listening", "address", srv.Addr); errCh <- srv.ListenAndServe() }()
	select {
	case e := <-errCh:
		if !errors.Is(e, http.ErrServerClosed) {
			return e
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}
