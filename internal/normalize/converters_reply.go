package normalize

import "encoding/json"

// convertReply 组装回复消息：正文 + 被引用消息摘要（逐行拼接）。
func convertReply(m map[string]interface{}) string {
	text, _ := m["text"].(string)
	if rm, ok := m["repliedMsg"].(map[string]interface{}); ok {
		quoted := extractQuotedText(rm)
		if quoted != "" {
			if text != "" {
				text += "\n" + quoted
			} else {
				text = quoted
			}
		}
	}
	return text
}

// extractQuotedText 生成被引用消息的摘要；content 可能是 JSON 字符串或对象。
func extractQuotedText(repliedMsg map[string]interface{}) string {
	mt, _ := repliedMsg["msgType"].(string)
	raw, _ := repliedMsg["content"].(string)
	if raw == "" {
		if c, ok := repliedMsg["content"].(map[string]interface{}); ok {
			switch mt {
			case "text":
				if t, ok := c["text"].(string); ok {
					return "[引用] " + t
				}
			default:
				return "[引用] " + mt + "消息"
			}
		}
		return "[引用] " + mt + "消息"
	}
	var c map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return "[引用] " + raw
	}
	switch mt {
	case "text":
		if t, ok := c["text"].(string); ok {
			return "[引用] " + t
		}
	case "picture":
		return "[引用] [图片]"
	case "file":
		fn, _ := c["fileName"].(string)
		return "[引用] [文件: " + fn + "]"
	default:
		return "[引用] " + mt + "消息"
	}
	return "[引用] " + mt + "消息"
}
