package normalize

import "strings"

// convertText 提取文本消息内容。
func convertText(m map[string]interface{}) string {
	t, _ := m["content"].(string)
	return strings.TrimSpace(t)
}

// convertMarkdown 提取 markdown 正文。
func convertMarkdown(m map[string]interface{}) string {
	t, _ := m["text"].(string)
	return t
}
