package domain

import "errors"

// ModelError exposes only fixed diagnostics. Never place raw CLI output here.
type ModelError struct {
	Code      string
	Message   string
	Retryable bool
}

func (e *ModelError) Error() string { return e.Code + ": " + e.Message }

func ModelErrorForCode(code string) *ModelError {
	message, retry := "", false
	switch code {
	case "model_network_error":
		message, retry = "模型服务网络暂时失败，本轮没有下发手机动作", true
	case "model_rate_limited":
		message, retry = "模型服务暂时限流，本轮没有下发手机动作", true
	case "model_auth_required":
		message = "模型 CLI 登录已失效，请在本机重新登录后继续"
	case "model_quota_exhausted":
		message = "模型服务额度不足，请检查账号额度后继续"
	case "model_invalid_decision":
		message, retry = "模型返回了无效决策，本轮没有下发手机动作", true
	case "model_output_missing":
		message, retry = "模型未返回完整决策，本轮没有下发手机动作", true
	case "model_host_tool_rejected":
		message = "模型尝试调用本机工具，已拦截；请检查模型配置"
	case "model_cli_unavailable":
		message = "本机模型 CLI 无法启动，请检查连接器的 CLI 路径"
	case "model_timeout":
		message = "模型调用超时，本轮没有下发手机动作，可继续执行"
	case "model_cli_failed", "cli_call_failed":
		code, message = "model_cli_failed", "本机模型 CLI 调用失败，本轮没有下发手机动作；请检查连接器日志后继续"
	default:
		return nil
	}
	return &ModelError{Code: code, Message: message, Retryable: retry}
}

func RetryableModelError(err error) bool {
	var e *ModelError
	return errors.As(err, &e) && e.Retryable
}

func ModelErrorCode(err error) string {
	var e *ModelError
	if errors.As(err, &e) {
		return e.Code
	}
	return "model_cli_failed"
}
