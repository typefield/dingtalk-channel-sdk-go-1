package normalize

import (
	"encoding/json"
	"testing"
)

func runParse(t *testing.T, msgType string, content string) (string, []json.RawMessage, []string) {
	t.Helper()
	var text string
	parsed, res, mentions := parseContent(msgType, json.RawMessage(content), nil)
	text = parsed
	_ = res
	_ = mentions
	return text, nil, nil
}

func TestConvertText(t *testing.T) {
	text, _, _ := runParse(t, "text", `{"content":"  hello world  "}`)
	if text != "hello world" {
		t.Fatalf("text = %q, want trimmed hello world", text)
	}
	// 缺 content 字段
	text, _, _ = runParse(t, "text", `{}`)
	if text != "" {
		t.Fatalf("empty content should yield empty text, got %q", text)
	}
}

func TestConvertMarkdown(t *testing.T) {
	text, _, _ := runParse(t, "markdown", `{"text":"# 标题\n正文"}`)
	if text != "# 标题\n正文" {
		t.Fatalf("markdown = %q", text)
	}
	text, _, _ = runParse(t, "markdown", `{}`)
	if text != "" {
		t.Fatalf("missing text field should be empty, got %q", text)
	}
}

func TestConvertUnknownTypeFallback(t *testing.T) {
	// 未知类型回退 content 字段
	text, _, _ := runParse(t, "futureType", `{"content":"fallback"}`)
	if text != "fallback" {
		t.Fatalf("unknown type fallback = %q, want fallback", text)
	}
}

func TestParseContentEmptyPayload(t *testing.T) {
	text, res, men := parseContent("text", nil, nil)
	if text != "" || res != nil || men != nil {
		t.Fatal("empty payload should yield zero values")
	}
	// 非法 JSON
	text, _, _ = parseContent("text", json.RawMessage(`{bad`), nil)
	if text != "" {
		t.Fatal("invalid JSON should yield empty text")
	}
}
