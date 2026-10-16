package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"mobile_autorun/internal/mediaindex"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	path := flag.String("config", "configs/media.local.json", "private USB/media connector config")
	flag.Parse()
	b, e := os.ReadFile(*path)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	var c mediaindex.Config
	if e = json.Unmarshal(b, &c); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Println("MediaConnector connecting; scans only task-owned images on configured USB phone")
	if e = mediaindex.Serve(ctx, c); e != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
