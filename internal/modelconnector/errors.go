package modelconnector

import (
	"encoding/json"
	"mobile_autorun/internal/domain"
	"strings"
)

// Inspect diagnostics locally; only the allowlisted code crosses the connection.
func classifyCLIError(stdout, stderr []byte) *domain.ModelError {
	var diagnostics []string
	_ = eachJSON(stdout, func(v map[string]json.RawMessage) error {
		var typ string
		_ = json.Unmarshal(v["type"], &typ)
		if typ == "error" || typ == "turn.failed" {
			diagnostics = append(diagnostics, string(v["message"]), string(v["error"]))
		}
		return nil
	})
	diagnostics = append(diagnostics, string(stderr))
	s := strings.ToLower(strings.Join(diagnostics, "\n"))
	code := "model_cli_failed"
	switch {
	case containsAny(s, "authentication required", "authentication failed", "unauthorized", "invalid api key", "401 unauthorized", "token has expired", "refresh_token_reused"):
		code = "model_auth_required"
	case containsAny(s, "insufficient_quota", "usage limit", "exceeded your current quota", "quota exceeded", "credit balance is too low"):
		code = "model_quota_exhausted"
	case containsAny(s, "rate_limit_exceeded", "rate limit reached", "too many requests", "429 too many"):
		code = "model_rate_limited"
	case containsAny(s, "stream disconnected", "connection reset", "connection refused", "error sending request", "network is unreachable", "temporary failure in name resolution", "timed out", "502 bad gateway", "503 service unavailable", "504 gateway timeout", "server_error"):
		code = "model_network_error"
	}
	return domain.ModelErrorForCode(code)
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
