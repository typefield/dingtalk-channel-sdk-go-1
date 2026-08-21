package normalize

import (
	"encoding/json"
	"testing"
)

func TestConvertActionCard(t *testing.T) {
	payload := `{"title":"验收","text":"请确认","actionUrlItemList":[{"actionUrl":"https://a"},{"actionUrl":"https://b"}]}`
	text, _, _ := parseContent("actionCard", json.RawMessage(payload), nil)
	want := "验收\n\n请确认\n\n操作链接：\n- https://a\n- https://b"
	if text != want {
		t.Fatalf("actionCard = %q, want %q", text, want)
	}
	// 单链接
	text, _, _ = parseContent("actionCard", json.RawMessage(`{"text":"t","actionUrlItemList":[{"actionUrl":"https://x"}]}`), nil)
	if text != "t\n\n操作链接：https://x" {
		t.Fatalf("single url = %q", text)
	}
	// 空卡片
	text, _, _ = parseContent("actionCard", json.RawMessage(`{}`), nil)
	if text != "[actionCard消息]" {
		t.Fatalf("empty = %q", text)
	}
}

func TestConvertInteractiveCard(t *testing.T) {
	text, _, _ := parseContent("interactiveCard", json.RawMessage(`{"biz_custom_action_url":"https://jump"}`), nil)
	if text != "收到交互式卡片链接：https://jump" {
		t.Fatalf("text = %q", text)
	}
	text, _, _ = parseContent("interactiveCard", json.RawMessage(`{}`), nil)
	if text != "[interactiveCard消息]" {
		t.Fatalf("fallback = %q", text)
	}
}
