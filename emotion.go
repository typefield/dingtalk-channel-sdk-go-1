package channel

import (
	"context"
	"net/http"
)

// 消息表情回应（"🤔思考中" 状态章）——与 openclaw connector addEmotionReply/recallEmotionReply 同款。
// 状态章不是卡片：是钉在用户消息上的 text emotion；思考期间甚至可以没有卡片。
// 生命周期：开始处理时贴（MarkThinking）→ 回复落地后撤回（RecallThinking，connector 同款）。
const (
	EmotionThinking = "🤔思考中"
	EmotionDone     = "🥳完成"

	emotionFrameGapHint = 0
)

// sendEmotion 在用户消息上添加（或撤回）text reaction。
// 失败只记录不打断——reaction 是装饰，永远不值得为它失败整条回复（dws 同款原则）。
// 注意：仅支持"人发的消息"（机器人自己的消息会 500）。
func (c *Channel) sendEmotion(ctx context.Context, conversationID, msgID, name string, recall bool) error {
	if conversationID == "" || msgID == "" {
		return &channelError{"emotion needs openConversationId and openMsgId"}
	}
	path := "/v1.0/robot/emotion/reply"
	if recall {
		path = "/v1.0/robot/emotion/recall"
	}
	return c.cards.call(ctx, http.MethodPost, path, map[string]any{
		"robotCode":          c.cfg.ClientID,
		"openConversationId": conversationID,
		"openMsgId":          msgID,
		"emotionType":        2,
		"emotionName":        name,
		"textEmotion": map[string]any{
			"emotionId":    "2659900",
			"emotionName":  name,
			"text":         name,
			"backgroundId": "im_bg_1",
		},
	}, nil)
}

// MarkThinking 在用户消息上贴"🤔思考中"状态章（收到消息、开始处理时，connector 同款）。
// 仅支持人发的消息（机器人自己的消息会 500）。
func (c *Channel) MarkThinking(ctx context.Context, conversationID, msgID string) error {
	return c.sendEmotion(ctx, conversationID, msgID, EmotionThinking, false)
}

// RecallThinking 撤回"🤔思考中"状态章（回复落地后；openclaw connector 的完成动作，best-effort）。
func (c *Channel) RecallThinking(ctx context.Context, conversationID, msgID string) error {
	return c.sendEmotion(ctx, conversationID, msgID, EmotionThinking, true)
}

// MarkDone 把"🤔思考中"换成"🥳完成"（回复落地后；best-effort）。
func (c *Channel) MarkDone(ctx context.Context, conversationID, msgID string) error {
	_ = c.sendEmotion(ctx, conversationID, msgID, EmotionThinking, true)
	return c.sendEmotion(ctx, conversationID, msgID, EmotionDone, false)
}
