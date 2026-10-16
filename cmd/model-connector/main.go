package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"mobile_autorun/internal/modelconnector"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	path := flag.String("config", "configs/connector.local.json", "local config; contains pairing token, keep private")
	flag.Parse()
	b, e := os.ReadFile(*path)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	var cfg modelconnector.Config
	if e = json.Unmarshal(b, &cfg); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	ctx, done := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer done()
	fmt.Println("ModelConnector connecting; local CLI mode requires this process to stay online")
	if e = modelconnector.Serve(ctx, cfg); e != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
