// Package types 定义 Channel 各层共享的数据结构：
// 入站消息归一化模型、策略配置、批处理/队列配置、出站配置与去重配置。
// 根包 channel 通过类型别名透出同名符号，保持既有 API 不变。
package types

import (
	"context"
	"encoding/json"
	"time"
)

// 会话类型（钉钉原始值 "1"/"2"）。
const (
	ConversationTypeDM    = "dm"
	ConversationTypeGroup = "group"
)

// DefaultDedupTTL 默认去重缓存 TTL。
const DefaultDedupTTL = 5 * time.Minute

// AtUser 被 @ 的用户。
type AtUser struct {
	DingtalkID string `json:"dingtalkId"`
	StaffID    string `json:"staffId"`
}

// Resource 表示消息中携带的媒体资源。
type Resource struct {
	Type         string // image | file | audio | video
	DownloadCode string // 钉钉下载码
	FileName     string // 文件名（file/audio/video 有）
	Recognition  string // 语音识别文本（audio 有）
}

// Mention 表示消息中的 @ 提及。
type Mention struct {
	Key    string // richText 中的占位 key
	UserID string // staffId 或 dingtalkId
	Name   string // 显示名
	IsBot  bool
	IsAll  bool // @所有人
}

// IncomingMessage 归一化后的机器人消息事件（SPEC §3.1）。
type IncomingMessage struct {
	ConversationID    string          `json:"conversationId"`
	ConversationType  string          `json:"conversationType"` // dm | group
	ConversationTitle string          `json:"conversationTitle"`
	SenderID          string          `json:"senderId"`
	SenderStaffID     string          `json:"senderStaffId"`
	SenderNick        string          `json:"senderNick"`
	SenderCorpID      string          `json:"senderCorpId"`
	Text              string          `json:"text"` // 已去 @机器人 前缀并 trim
	MsgType           string          `json:"msgType"`
	Content           json.RawMessage `json:"content,omitempty"` // rich 内容原样透出
	Resources         []Resource      `json:"resources,omitempty"`
	Mentions          []Mention       `json:"mentions,omitempty"`
	MentionAll        bool            `json:"mentionAll"`
	AtUsers           []AtUser        `json:"atUsers,omitempty"`
	SessionWebhook    string          `json:"-"` // 不序列化泄露
	WebhookExpiredAt  int64           `json:"webhookExpiredAt"`
	MsgID             string          `json:"msgId"`
	CreateAt          int64           `json:"createAt"`
	IsAdmin           bool            `json:"isAdmin"`
	IsInAtList        bool            `json:"isInAtList"`
	// BatchedSources 媒体/文本批次合并前的原始消息列表（单条时为 nil）。
	BatchedSources []*IncomingMessage `json:"-"`
	Raw            json.RawMessage    `json:"raw,omitempty"`
}

// CardAction 归一化后的卡片交互回调（E7）。DataContent 为卡片回调业务数据。
type CardAction struct {
	OutTrackID  string
	UserID      string
	DataContent json.RawMessage
	Raw         json.RawMessage
}

// PolicyConfig 控制消息准入策略。
type PolicyConfig struct {
	// GroupAllowlist 群聊白名单（空 = 允许所有群）
	GroupAllowlist []string
	// GroupBlocklist 群聊黑名单
	GroupBlocklist []string
	// RequireMention 群聊是否需要 @机器人（默认 true）
	RequireMention *bool
	// RespondToMentionAll 是否响应 @所有人（默认 false）
	RespondToMentionAll *bool
	// DMMode 单聊模式："open" | "disabled" | "allowlist" | "blocklist"
	DMMode string
	// DMAllowlist 单聊白名单（DMMode="allowlist" 时生效）
	DMAllowlist []string
	// DMBlocklist 单聊黑名单（DMMode="blocklist" 时生效）
	DMBlocklist []string
	// GroupOverrides 按群（conversationId）覆盖策略。零值字段沿用全局；
	// 显式条目可在白名单模式下放行该群，黑名单永不例外。
	GroupOverrides map[string]GroupOverride
	
	// 全局发送者控制
	// AllowFrom 全局发送者白名单（staffId 列表）- 设置后仅名单内发送者可通过
	AllowFrom []string
	// DenyFrom 全局发送者黑名单（staffId 列表）- 优先于 AllowFrom
	DenyFrom []string
	// Admins 管理员列表（staffId）- 绕过所有策略限制
	Admins []string
}

// GroupOverride 单群策略覆盖。
type GroupOverride struct {
	// Enabled 显式禁用该群（false = 拒绝该群所有消息）。
	Enabled *bool
	// RequireMention 覆盖该群的 @机器人 要求。
	RequireMention *bool
	// RespondToMentionAll 覆盖该群的 @all 响应。
	RespondToMentionAll *bool
	// AllowFrom 该群内发送者白名单（设置后仅名单内 sender 可通过）。
	AllowFrom []string
	// BlockFrom 该群内发送者黑名单（先于 AllowFrom 检查，命中即拒绝）。
	BlockFrom []string
}

// PolicyDecision 策略评估结果
type PolicyDecision struct {
	Allowed bool
	Reason  RejectReason
}

// RejectReason 拒绝原因
type RejectReason string

const (
	RejectReasonGroupNotAllowed  RejectReason = "group_not_allowed"
	RejectReasonGroupBlocked     RejectReason = "group_blocked"
	RejectReasonGroupDisabled    RejectReason = "group_disabled"
	RejectReasonNoMention        RejectReason = "no_mention"
	RejectReasonMentionAll       RejectReason = "mention_all_blocked"
	RejectReasonDMDisabled       RejectReason = "dm_disabled"
	RejectReasonDMNotAllowed     RejectReason = "dm_not_allowed"
	RejectReasonDMBlocked        RejectReason = "dm_blocked"
	RejectReasonSenderNotAllowed RejectReason = "sender_not_allowed"
	RejectReasonSenderBlocked    RejectReason = "sender_blocked"
)

// RejectEvent 消息被策略拒绝时触发的事件
type RejectEvent struct {
	MessageID string
	ChatID    string
	SenderID  string
	Reason    RejectReason
}

// BatchConfig 配置消息批处理行为。
type BatchConfig struct {
	// DelayMs 批处理延迟（默认 600ms）
	DelayMs time.Duration
	// LongThresholdChars 长消息阈值（字符数）
	LongThresholdChars int
	// LongDelayMs 长消息延迟（默认 2s）
	LongDelayMs time.Duration
	// MaxMessages 最大批处理消息数（默认 8）
	MaxMessages int
	// MaxChars 最大批处理字符数（默认 4000）
	MaxChars int
}

// DefaultBatchConfig 返回默认批处理配置。
func DefaultBatchConfig() BatchConfig {
	return BatchConfig{
		DelayMs:            600 * time.Millisecond,
		LongThresholdChars: 1000,
		LongDelayMs:        2 * time.Second,
		MaxMessages:        8,
		MaxChars:           4000,
	}
}

// BatchedMessage 批处理后的消息。
type BatchedMessage struct {
	// Message 合并后的消息（最后一条为基础，内容合并）
	Message *IncomingMessage
	// SourceIDs 源消息 ID 列表
	SourceIDs []string
}

// BatchHandler 批处理回调。
type BatchHandler func(ctx context.Context, batch *BatchedMessage) error

// ChatQueueConfig 配置 per-chat 串行队列。
type ChatQueueConfig struct {
	// Enabled 是否启用 per-chat 串行队列（默认 true）。
	// 关闭后消息可能并发处理，造成回复乱序。
	Enabled bool
}

// DefaultChatQueueConfig 返回默认的 ChatQueueConfig。
func DefaultChatQueueConfig() ChatQueueConfig {
	return ChatQueueConfig{
		Enabled: true,
	}
}

// FlushHandler 批处理刷新时的回调函数。
type FlushHandler func(context.Context, *BatchedDispatch) error

// BatchedDispatch 批处理后的分发单元。
type BatchedDispatch struct {
	Message   *IncomingMessage // 合并后的消息
	SourceIDs []string         // 原始消息 ID 列表
}

// MediaBatchConfig 媒体消息批处理（默认关闭）。
type MediaBatchConfig struct {
	Enabled  bool
	DelayMs  int64 // 媒体合并窗口毫秒（默认 800）
	MaxItems int   // 单批最大媒体数（默认 9）
}

// RetryConfig 出站重试参数（指数退避）。
type RetryConfig struct {
	MaxAttempts int
	BaseDelayMs int64
}

// BeforeSendHook 发送前钩子：返回非 nil 时替换 payload。kind: "reply" | "send"。
type BeforeSendHook func(kind, target string, payload any) any

// AfterSendHook 发送后钩子（含失败；errStr 成功时为空）。
type AfterSendHook func(kind, target string, ok bool, errStr string)

// OutboundConfig 出站配置。
type OutboundConfig struct {
	Retry      RetryConfig
	BeforeSend BeforeSendHook
	AfterSend  AfterSendHook
	// Footer 统一页脚：追加到每条文本/Markdown 消息末尾（如免责声明）。
	Footer string
}

// BotIdentity 机器人身份信息。
type BotIdentity struct {
	// RobotCode 机器人代码（对应 ClientID）
	RobotCode string
	// RobotName 机器人名称
	RobotName string
	// Avatar 机器人头像 URL
	Avatar string
}

// DedupConfig 去重缓存配置。
type DedupConfig struct {
	TTL           time.Duration
	MaxEntries    int
	SweepInterval time.Duration
}
