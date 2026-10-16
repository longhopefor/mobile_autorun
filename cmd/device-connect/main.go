package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"mobile_autorun/internal/deviceconnector"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	path := flag.String("config", "configs/device.local.json", "private Portal connection configuration")
	once := flag.Bool("once", false, "configure once instead of maintaining the USB tunnel")
	flag.Parse()
	b, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot read device configuration")
		os.Exit(1)
	}
	var cfg deviceconnector.Config
	if json.Unmarshal(b, &cfg) != nil {
		fmt.Fprintln(os.Stderr, "invalid device configuration")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	m := deviceconnector.Manager{Config: cfg}
	if err = m.Serve(ctx, *once, func(s string) { fmt.Println(s) }); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
