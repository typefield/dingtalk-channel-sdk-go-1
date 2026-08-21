package safety

import (
	"testing"
	"time"
)

func TestStaleDetector_IsStale(t *testing.T) {
	window := 30 * time.Minute
	detector := NewStaleDetector(window)

	now := time.Now()

	tests := []struct {
		name       string
		createAtMs int64
		want       bool
	}{
		{
			name:       "fresh message",
			createAtMs: now.Add(-10 * time.Minute).UnixMilli(),
			want:       false,
		},
		{
			name:       "exactly at window boundary",
			createAtMs: now.Add(-window).UnixMilli(),
			want:       true, // 边界情况：age > window，刚好超过窗口即过期
		},
		{
			name:       "stale message - 31 minutes old",
			createAtMs: now.Add(-31 * time.Minute).UnixMilli(),
			want:       true,
		},
		{
			name:       "very old message - 2 hours",
			createAtMs: now.Add(-2 * time.Hour).UnixMilli(),
			want:       true,
		},
		{
			name:       "zero timestamp - unknown time",
			createAtMs: 0,
			want:       false, // 时间未知，保守放行
		},
		{
			name:       "negative timestamp - invalid",
			createAtMs: -1,
			want:       false,
		},
		{
			name:       "future message - clock skew",
			createAtMs: now.Add(5 * time.Minute).UnixMilli(),
			want:       false, // 未来消息不视为过期
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detector.IsStale(tt.createAtMs)
			if got != tt.want {
				t.Errorf("IsStale(%d) = %v, want %v (age = %v)",
					tt.createAtMs, got, tt.want, time.Since(time.UnixMilli(tt.createAtMs)))
			}
		})
	}
}

func TestStaleDetector_DefaultWindow(t *testing.T) {
	// 测试 0 或负数窗口时使用默认值
	detector := NewStaleDetector(0)
	if detector.window != 30*time.Minute {
		t.Errorf("expected default window 30m, got %v", detector.window)
	}

	detector2 := NewStaleDetector(-5 * time.Minute)
	if detector2.window != 30*time.Minute {
		t.Errorf("expected default window 30m for negative input, got %v", detector2.window)
	}
}

func TestStaleDetector_CustomWindow(t *testing.T) {
	window := 10 * time.Minute
	detector := NewStaleDetector(window)

	now := time.Now()

	// 9 分钟前 - 不过期
	fresh := now.Add(-9 * time.Minute).UnixMilli()
	if detector.IsStale(fresh) {
		t.Errorf("message at 9 minutes should not be stale with 10m window")
	}

	// 11 分钟前 - 过期
	stale := now.Add(-11 * time.Minute).UnixMilli()
	if !detector.IsStale(stale) {
		t.Errorf("message at 11 minutes should be stale with 10m window")
	}
}
