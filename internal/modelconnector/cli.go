package modelconnector

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"mobile_autorun/internal/domain"
	"mobile_autorun/internal/model"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	ServerURL      string   `json:"server_url"`
	Token          string   `json:"token"`
	CodexBinary    string   `json:"codex_binary"`
	WorkBuddyNode  string   `json:"workbuddy_node"`
	WorkBuddyEntry string   `json:"workbuddy_entry"`
	AllowedModels  []string `json:"allowed_models"`
}
type CLI struct{ Config Config }
type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, errors.New("cli_output_limit")
	}
	return b.Buffer.Write(p)
}

// WorkBuddy can emit an assistant authentication notice without a terminal result
// and keep its stream open. Stop its process group immediately on that notice.
// Only fixed error codes leave this machine; raw CLI diagnostics stay local.
type workBuddyOutput struct {
	buffer  *limitedBuffer
	pending []byte
	failure error
	cancel  context.CancelFunc
}

func (w *workBuddyOutput) Write(p []byte) (int, error) {
	n, err := w.buffer.Write(p)
	if err != nil {
		return n, err
	}
	w.pending = append(w.pending, p...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		line := w.pending[:i]
		w.pending = w.pending[i+1:]
		var event struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &event) != nil || event.Type != "assistant" {
			continue
		}
		for _, block := range event.Message.Content {
			text := strings.TrimSpace(block.Text)
			if block.Type == "text" && (strings.HasPrefix(text, "Authentication required. Please use /login") || strings.HasPrefix(text, "Authentication failed. Please use /login")) {
				w.failure = domain.ErrWorkBuddyLoginRequired
				w.cancel()
			}
		}
	}
	return n, nil
}

func (c *CLI) Next(ctx context.Context, q model.Request) (model.Result, error) {
	var r model.Result
	var prepErr error
	q, prepErr = model.PrepareVision(q)
	if prepErr != nil {
		return r, prepErr
	}
	if len(q.Image) > 28<<20 {
		return r, errors.New("image_too_large")
	}
	raw, e := base64.StdEncoding.DecodeString(q.Image)
	if e != nil {
		return r, errors.New("invalid_image")
	}
	cfg, e := png.DecodeConfig(bytes.NewReader(raw))
	if e != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 12000 || cfg.Height > 12000 {
		return r, errors.New("invalid_png")
	}
	allowed := q.Profile.Model == ""
	for _, id := range c.Config.AllowedModels {
		if id == q.Profile.Model {
			allowed = true
		}
	}
	if !allowed {
		return r, errors.New("model_not_allowed_locally")
	}
	dir, e := os.MkdirTemp("", "mobile-autorun-model-")
	if e != nil {
		return r, e
	}
	defer os.RemoveAll(dir)
	img := filepath.Join(dir, "observation.png")
	schemaFile := filepath.Join(dir, "decision-schema.json")
	resultFile := filepath.Join(dir, "decision.json")
	if e = os.WriteFile(img, raw, 0600); e != nil {
		return r, e
	}
	if len(q.References) > 9 {
		return r, errors.New("reference_image_limit")
	}
	refPaths := []string{}
	for i, ref := range q.References {
		if len(ref.Image) > 2<<20 {
			return r, errors.New("reference_image_too_large")
		}
		b, err := base64.StdEncoding.DecodeString(ref.Image)
		if err != nil {
			return r, errors.New("invalid_reference_image")
		}
		conf, err := png.DecodeConfig(bytes.NewReader(b))
		if err != nil || conf.Width < 1 || conf.Height < 1 || conf.Width > 1024 || conf.Height > 1024 {
			return r, errors.New("invalid_reference_png")
		}
		path := filepath.Join(dir, fmt.Sprintf("reference-%02d.png", i+1))
		if err = os.WriteFile(path, b, 0600); err != nil {
			return r, err
		}
		refPaths = append(refPaths, path)
	}
	schemaBytes, _ := json.Marshal(model.RequestSchema(q))
	if e = os.WriteFile(schemaFile, schemaBytes, 0600); e != nil {
		return r, e
	}
	processCtx, stopProcess := context.WithCancel(ctx)
	defer stopProcess()
	var cmd *exec.Cmd
	var stdin []byte
	switch q.Profile.Provider {
	case "codex_cli":
		bin := c.Config.CodexBinary
		if bin == "" {
			bin = "codex"
		}
		args := []string{"exec", "--ignore-user-config", "--ephemeral", "--skip-git-repo-check", "--sandbox", "read-only", "--json", "--output-schema", schemaFile, "--output-last-message", resultFile, "--image", img, "-c", "features.shell_tool=false", "-c", "features.apply_patch_freeform=false", "-c", "web_search=\"disabled\""}
		if q.Profile.Model != "" {
			args = append(args, "--model", q.Profile.Model)
		}
		for _, path := range refPaths {
			args = append(args, "--image", path)
		}
		args = append(args, "-")
		cmd = exec.CommandContext(processCtx, bin, args...)
		stdin = []byte(model.Prompt(q))
	case "workbuddy_cli":
		if c.Config.WorkBuddyNode == "" || c.Config.WorkBuddyEntry == "" {
			return r, errors.New("workbuddy_node_and_entry_required")
		}
		args := []string{c.Config.WorkBuddyEntry, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--json-schema", string(schemaBytes), "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--permission-mode", "dontAsk", "--no-session-persistence", "--max-turns", "2"}
		if q.Profile.Model != "" {
			args = append(args, "--model", q.Profile.Model)
		}
		cmd = exec.CommandContext(processCtx, c.Config.WorkBuddyNode, args...)
		parts := []any{map[string]any{"type": "text", "text": model.Prompt(q)}, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": q.Image}}}
		for _, ref := range q.References {
			parts = append(parts, map[string]any{"type": "text", "text": "目标素材参考图：" + ref.Name}, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": ref.Image}})
		}
		stdin, _ = json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": parts}})
		stdin = append(stdin, '\n')
	default:
		return r, errors.New("unsupported_cli_provider")
	}
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdin)
	out := &limitedBuffer{max: 4 << 20}
	stderr := &limitedBuffer{max: 1 << 20}
	cmd.Stdout = out
	var guard *workBuddyOutput
	if q.Profile.Provider == "workbuddy_cli" {
		guard = &workBuddyOutput{buffer: out, cancel: func() {
			// Kill the group even if the CLI leader exited while descendants
			// still hold its stdout/stderr open (CommandContext's watcher may
			// already have stopped when that happens).
			_ = cmd.Cancel()
			stopProcess()
		}}
		cmd.Stdout = guard
	}
	cmd.Stderr = stderr
	cmd.WaitDelay = 3 * time.Second
	configureProcess(cmd)
	e = cmd.Run()
	if ctx.Err() != nil {
		return r, ctx.Err()
	}
	// Wait has joined stdout copying before reading the guard's failure.
	if guard != nil && guard.failure != nil {
		return r, guard.failure
	}
	if e != nil {
		var launch *exec.Error
		var path *os.PathError
		if errors.As(e, &launch) || errors.As(e, &path) {
			return r, domain.ModelErrorForCode("model_cli_unavailable")
		}
		return r, classifyCLIError(out.Bytes(), stderr.Bytes())
	}
	if q.Profile.Provider == "codex_cli" {
		if e = ValidateCodexEvents(out.Bytes()); e != nil {
			return r, e
		}
		b, err := os.ReadFile(resultFile)
		if err != nil {
			return r, domain.ModelErrorForCode("model_output_missing")
		}
		if len(b) > 64<<10 {
			return r, errors.New("decision_too_large")
		}
		e = domain.DecodeStrict(b, &r.Decision)
		r.Usage = CodexUsage(out.Bytes())
	} else {
		r, e = ParseWorkBuddy(out.Bytes())
	}
	if e != nil {
		return r, domain.ModelErrorForCode("model_invalid_decision")
	}
	if e = model.ValidateResult(q, r); e != nil {
		return r, domain.ModelErrorForCode("model_invalid_decision")
	}
	r.Input = q.Input
	return r, nil
}
func eachJSON(data []byte, fn func(map[string]json.RawMessage) error) error {
	d := json.NewDecoder(bytes.NewReader(data))
	for {
		var v map[string]json.RawMessage
		e := d.Decode(&v)
		if errors.Is(e, io.EOF) {
			return nil
		}
		if e != nil {
			return errors.New("invalid_cli_event_stream")
		}
		if e = fn(v); e != nil {
			return e
		}
	}
}
func ValidateCodexEvents(data []byte) error {
	completed := false
	e := eachJSON(data, func(v map[string]json.RawMessage) error {
		var typ string
		_ = json.Unmarshal(v["type"], &typ)
		if typ == "turn.failed" || typ == "error" {
			return classifyCLIError(data, nil)
		}
		if typ == "turn.completed" {
			completed = true
		}
		if typ == "item.started" || typ == "item.completed" {
			var item struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(v["item"], &item)
			if item.Type == "command_execution" || item.Type == "file_change" || item.Type == "mcp_tool_call" || item.Type == "web_search" {
				return domain.ModelErrorForCode("model_host_tool_rejected")
			}
		}
		return nil
	})
	if e != nil {
		return e
	}
	if !completed {
		return domain.ModelErrorForCode("model_output_missing")
	}
	return nil
}
func CodexUsage(data []byte) json.RawMessage {
	var usage json.RawMessage
	_ = eachJSON(data, func(v map[string]json.RawMessage) error {
		var t string
		_ = json.Unmarshal(v["type"], &t)
		if t == "turn.completed" {
			usage = v["usage"]
		}
		return nil
	})
	return usage
}
func ParseWorkBuddy(data []byte) (model.Result, error) {
	var r model.Result
	seen := false
	e := eachJSON(data, func(v map[string]json.RawMessage) error {
		var typ string
		_ = json.Unmarshal(v["type"], &typ)
		if typ != "result" {
			return nil
		}
		if seen {
			return errors.New("multiple_cli_results")
		}
		seen = true
		var failed bool
		_ = json.Unmarshal(v["is_error"], &failed)
		var subtype string
		_ = json.Unmarshal(v["subtype"], &subtype)
		if failed || strings.HasPrefix(subtype, "error") {
			return errors.New("workbuddy_failed_result")
		}
		if len(v["structured_output"]) == 0 {
			return errors.New("structured_output_missing")
		}
		r.Usage = v["usage"]
		return domain.DecodeStrict(v["structured_output"], &r.Decision)
	})
	if e != nil {
		return r, e
	}
	if !seen {
		return r, errors.New("workbuddy_final_result_missing")
	}
	return r, nil
}
