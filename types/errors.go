package types

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ErrorCode 结构化错误码。
type ErrorCode string

const (
	ErrCodeTargetRevoked    ErrorCode = "target_revoked"
	ErrCodePermissionDenied ErrorCode = "permission_denied"
	ErrCodeFormatError      ErrorCode = "format_error"
	ErrCodeRateLimited      ErrorCode = "rate_limited"
	ErrCodeSendTimeout      ErrorCode = "send_timeout"
	ErrCodeQpsLimited       ErrorCode = "qps_limited"
	ErrCodeSSRFBlocked      ErrorCode = "ssrf_blocked"
	ErrCodeUnknown          ErrorCode = "unknown"
)

// ChannelError 结构化错误，携带错误码、原始错误和上下文。
type ChannelError struct {
	Code    ErrorCode
	Message string
	Cause   error
}

func (e *ChannelError) Error() string {
	msg := fmt.Sprintf("ChannelError(code=%s): %s", e.Code, e.Message)
	if e.Cause != nil {
		msg += fmt.Sprintf(" | cause: %v", e.Cause)
	}
	return msg
}

func (e *ChannelError) Unwrap() error { return e.Cause }

// apiStatusError 由具体 API 错误类型实现（如钉钉 API 错误），
// 供错误分类读取 HTTP 状态与 QPS 限流标记，避免对本包的反向依赖。
type apiStatusError interface {
	error
	APIStatus() int
	APIIsQpsLimit() bool
}

// ClassifyError 将原始错误分类为结构化 ChannelError。
func ClassifyError(err error) *ChannelError {
	if err == nil {
		return nil
	}
	var ce *ChannelError
	if errors.As(err, &ce) {
		return ce
	}
	code := classifyCode(err)
	return &ChannelError{Code: code, Message: err.Error(), Cause: err}
}

func classifyCode(err error) ErrorCode {
	var ae apiStatusError
	if errors.As(err, &ae) {
		if ae.APIIsQpsLimit() {
			return ErrCodeQpsLimited
		}
		if ae.APIStatus() == 403 {
			return ErrCodePermissionDenied
		}
		if ae.APIStatus() == 400 {
			return ErrCodeFormatError
		}
		if ae.APIStatus() == 404 {
			return ErrCodeTargetRevoked
		}
		if ae.APIStatus() == 429 {
			return ErrCodeRateLimited
		}
	}

	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "status 429") || strings.Contains(msg, "too many requests") {
		return ErrCodeRateLimited
	}
	if strings.Contains(msg, "status 401") || strings.Contains(msg, "status 403") {
		return ErrCodePermissionDenied
	}
	if strings.Contains(msg, "status 400") {
		return ErrCodeFormatError
	}
	if strings.Contains(msg, "status 404") {
		return ErrCodeTargetRevoked
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return ErrCodeSendTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrCodeSendTimeout
	}
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "context deadline exceeded") {
		return ErrCodeSendTimeout
	}

	return ErrCodeUnknown
}

// IsRetryable 判断错误是否可重试。
func IsRetryable(err error) bool {
	var ce *ChannelError
	if errors.As(err, &ce) {
		return ce.Code == ErrCodeRateLimited || ce.Code == ErrCodeQpsLimited || ce.Code == ErrCodeUnknown || ce.Code == ErrCodeSendTimeout
	}
	return true
}

// IsReplyTargetGone 判断是否为回复目标已撤回。
func IsReplyTargetGone(err error) bool {
	var ce *ChannelError
	if errors.As(err, &ce) {
		return ce.Code == ErrCodeTargetRevoked
	}
	return false
}

// IsFormatError 判断是否为格式错误。
func IsFormatError(err error) bool {
	var ce *ChannelError
	if errors.As(err, &ce) {
		return ce.Code == ErrCodeFormatError
	}
	return false
}
