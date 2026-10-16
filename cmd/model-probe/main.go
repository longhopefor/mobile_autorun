package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"mobile_autorun/internal/domain"
	"mobile_autorun/internal/model"
	"mobile_autorun/internal/modelconnector"
	"os"
	"time"
)

func main() {
	config := flag.String("config", "configs/connector.example.json", "local CLI paths")
	image := flag.String("image", "", "PNG screenshot/fixture")
	provider := flag.String("provider", "codex_cli", "codex_cli or workbuddy_cli")
	modelID := flag.String("model", "", "model override; must be locally allowlisted")
	goal := flag.String("goal", "请指出图片中心的颜色，并选择图片中心位置。只返回一个 phone_action/tap 决策，reason 简短说明所见颜色，不执行任何本机工具。", "one-round vision probe")
	flag.Parse()
	if *image == "" {
		fmt.Fprintln(os.Stderr, "--image is required; this probe calls a real model but never operates a phone")
		os.Exit(1)
	}
	b, e := os.ReadFile(*config)
	if e != nil {
		panic(e)
	}
	var cfg modelconnector.Config
	if e = json.Unmarshal(b, &cfg); e != nil {
		panic(e)
	}
	png, e := os.ReadFile(*image)
	if e != nil {
		panic(e)
	}
	ctx, stop := context.WithTimeout(context.Background(), 120*time.Second)
	defer stop()
	deadline, _ := ctx.Deadline()
	r, e := (&modelconnector.CLI{Config: cfg}).Next(ctx, model.Request{ID: "probe", TaskID: "probe", Profile: domain.Profile{Provider: *provider, Model: *modelID}, Goal: *goal, ObservationID: "probe-image", Image: base64.StdEncoding.EncodeToString(png), State: json.RawMessage(`{}`), Packages: []string{"com.android.settings"}, Deadline: deadline.Format(time.RFC3339Nano)})
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	out, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(out))
}
