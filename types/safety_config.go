package types

import "time"

// SafetyConfig 统一安全配置。
// 整合去重、策略、批处理、过期检测等所有安全机制的配置。
type SafetyConfig struct {
	Dedup        DedupConfig      // 去重配置
	Policy       PolicyConfig     // 策略门控配置
	TextBatch    BatchConfig      // 文本批处理配置
	MediaBatch   MediaBatchConfig // 媒体批处理配置
	ChatQueue    ChatQueueConfig  // 队列配置
	StaleWindow  time.Duration    // 过期消息窗口（默认 30 分钟）
	LockTTL      time.Duration    // 处理锁 TTL（默认 5 分钟）
	DropSelfSent bool             // 是否丢弃机器人自己发送的消息（默认 true）
	// MarkAfterHandler 去重标记时机（默认 false = 入口即标记）。
	// true 时采用"处理成功后标记"语义：入口只查不写，handler 成功返回后
	// 才写入 seen —— 处理失败的消息可被重投重试；代价是同消息并发重投会撞
	// 处理锁（RejectReasonLockContention）而非直接判重。
	MarkAfterHandler bool
}

// DefaultSafetyConfig 返回默认安全配置。
func DefaultSafetyConfig() SafetyConfig {
	return SafetyConfig{
		Dedup:        DefaultDedupConfigExtended(),
		Policy:       DefaultPolicyConfigExtended(),
		TextBatch:    DefaultBatchConfig(),
		MediaBatch:   DefaultMediaBatchConfig(),
		ChatQueue:    DefaultChatQueueConfig(),
		StaleWindow:  30 * time.Minute,
		LockTTL:      5 * time.Minute,
		DropSelfSent: true,
	}
}

// DefaultDedupConfigExtended 返回增强的默认去重配置。
func DefaultDedupConfigExtended() DedupConfig {
	return DedupConfig{
		TTL:           12 * time.Hour,
		MaxEntries:    5000,
		SweepInterval: 5 * time.Minute,
	}
}

// DefaultPolicyConfigExtended 返回增强的默认策略配置。
func DefaultPolicyConfigExtended() PolicyConfig {
	return PolicyConfig{
		DMMode: "open", // DM 默认开放
	}
}

// DefaultMediaBatchConfig 返回默认媒体批处理配置。
func DefaultMediaBatchConfig() MediaBatchConfig {
	return MediaBatchConfig{
		Enabled:  false,
		DelayMs:  800,
		MaxItems: 9,
	}
}

// 新增 RejectReason 常量（扩展现有定义）
const (
	RejectReasonStale          RejectReason = "stale"           // 消息过期
	RejectReasonDuplicate      RejectReason = "duplicate"       // 消息重复
	RejectReasonSelfSent       RejectReason = "self_sent"       // 机器人自己发送的消息
	RejectReasonLockContention RejectReason = "lock_contention" // 处理锁竞争
	RejectReasonGroupDisabledByOverride RejectReason = "group_disabled_by_override" // 群组被覆盖配置禁用
	RejectReasonSenderDenied   RejectReason = "sender_denied"   // 发送者在黑名单（全局级别）
)
