package normalize

import "strings"

// convertActionCard 组装 actionCard 的标题/正文/操作链接为可读文本。
func convertActionCard(m map[string]interface{}) string {
	title, _ := m["title"].(string)
	body, _ := m["text"].(string)
	var parts []string
	if title != "" {
		parts = append(parts, title)
	}
	if body != "" {
		parts = append(parts, body)
	}
	if urls, ok := m["actionUrlItemList"].([]interface{}); ok {
		var actionUrls []string
		for _, u := range urls {
			if um, ok := u.(map[string]interface{}); ok {
				if url, ok := um["actionUrl"].(string); ok && url != "" {
					actionUrls = append(actionUrls, url)
				}
			}
		}
		if len(actionUrls) > 0 {
			if len(actionUrls) == 1 {
				parts = append(parts, "操作链接："+actionUrls[0])
			} else {
				parts = append(parts, "操作链接：\n- "+strings.Join(actionUrls, "\n- "))
			}
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n\n")
	}
	return "[actionCard消息]"
}

// convertInteractiveCard 提取交互卡片的自定义跳转链接。
func convertInteractiveCard(m map[string]interface{}) string {
	url, _ := m["biz_custom_action_url"].(string)
	if url != "" {
		return "收到交互式卡片链接：" + url
	}
	return "[interactiveCard消息]"
}
