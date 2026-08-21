package safety

import "time"

// StaleDetector 检测消息是否过期。
// 用于过滤网关重投递的陈旧消息（如 WebSocket 重连后的历史回放）。
type StaleDetector struct {
	window time.Duration
}

// NewStaleDetector 创建过期检测器。
// window: 消息有效期窗口，超过此时长的消息被视为过期（默认 30 分钟）。
func NewStaleDetector(window time.Duration) *StaleDetector {
	if window <= 0 {
		window = 30 * time.Minute // 默认窗口
	}
	return &StaleDetector{window: window}
}

// IsStale 判断消息是否过期。
// createAtMs: 消息创建时间戳（毫秒），为 0 时认为时间未知，不过滤。
func (s *StaleDetector) IsStale(createAtMs int64) bool {
	if createAtMs <= 0 {
		return false // 时间未知，保守放行
	}
	createTime := time.UnixMilli(createAtMs)
	age := time.Since(createTime)
	return age > s.window
}
