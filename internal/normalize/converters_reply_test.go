package normalize

import (
	"encoding/json"
	"testing"
)

func TestConvertReplyTextQuote(t *testing.T) {
	payload := `{"text":"这是我的回复","repliedMsg":{"msgType":"text","content":"{\"text\":\"被引用的话\"}"}}`
	text, _, _ := parseContent("reply", json.RawMessage(payload), nil)
	if text != "这是我的回复\n[引用] 被引用的话" {
		t.Fatalf("reply = %q", text)
	}
}

func TestConvertReplyObjectContent(t *testing.T) {
	// content 为对象形态
	payload := `{"repliedMsg":{"msgType":"text","content":{"text":"对象引用"}}}`
	text, _, _ := parseContent("reply", json.RawMessage(payload), nil)
	if text != "[引用] 对象引用" {
		t.Fatalf("object content = %q", text)
	}
}

func TestConvertReplyMediaQuote(t *testing.T) {
	payload := `{"repliedMsg":{"msgType":"picture","content":"{}"}}`
	text, _, _ := parseContent("reply", json.RawMessage(payload), nil)
	if text != "[引用] [图片]" {
		t.Fatalf("picture quote = %q", text)
	}
	payload = `{"repliedMsg":{"msgType":"file","content":"{\"fileName\":\"a.zip\"}"}}`
	text, _, _ = parseContent("reply", json.RawMessage(payload), nil)
	if text != "[引用] [文件: a.zip]" {
		t.Fatalf("file quote = %q", text)
	}
	// 非法 JSON content → 原样引用
	payload = `{"repliedMsg":{"msgType":"text","content":"不是json"}}`
	text, _, _ = parseContent("reply", json.RawMessage(payload), nil)
	if text != "[引用] 不是json" {
		t.Fatalf("raw fallback = %q", text)
	}
	// 未知引用类型
	payload = `{"repliedMsg":{"msgType":"superMsg","content":"{}"}}`
	text, _, _ = parseContent("reply", json.RawMessage(payload), nil)
	if text != "[引用] superMsg消息" {
		t.Fatalf("unknown quote = %q", text)
	}
}
