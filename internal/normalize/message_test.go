package normalize

import (
	"encoding/json"
	"testing"
)

func fullPayload() []byte {
	return []byte(`{
		"conversationId": "cid-1",
		"conversationType": "2",
		"conversationTitle": "项目群",
		"msgId": "m-1",
		"msgtype": "text",
		"text": {"content": "@bot 你好"},
		"senderStaffId": "staff-1",
		"senderNick": "John",
		"senderCorpId": "corp-1",
		"senderId": "sender-1",
		"sessionWebhook": "https://hook",
		"sessionWebhookExpiredTime": 1717171717,
		"createAt": 1717171717000,
		"isInAtList": true,
		"atUsers": [{"dingtalkId":"dt-1","staffId":"staff-2"}]
	}`)
}

func TestNormalizeIncomingEnvelope(t *testing.T) {
	msg, err := NormalizeIncoming(fullPayload())
	if err != nil {
		t.Fatal(err)
	}
	if msg.ConversationID != "cid-1" || msg.ConversationType != "group" || msg.ConversationTitle != "项目群" {
		t.Fatalf("conversation fields wrong: %+v", msg)
	}
	if msg.SenderID != "sender-1" || msg.SenderStaffID != "staff-1" || msg.SenderNick != "John" || msg.SenderCorpID != "corp-1" {
		t.Fatalf("sender fields wrong: %+v", msg)
	}
	if msg.MsgID != "m-1" || msg.MsgType != "text" || msg.CreateAt != 1717171717000 {
		t.Fatalf("msg fields wrong: %+v", msg)
	}
	if msg.SessionWebhook != "https://hook" || msg.WebhookExpiredAt != 1717171717 {
		t.Fatalf("webhook fields wrong: %+v", msg)
	}
	if !msg.IsInAtList || msg.Text != "你好" {
		t.Fatalf("group @-prefix not stripped: text=%q isInAtList=%v", msg.Text, msg.IsInAtList)
	}
	if len(msg.AtUsers) != 1 || msg.AtUsers[0].StaffID != "staff-2" {
		t.Fatalf("atUsers = %+v", msg.AtUsers)
	}
	// mentions 合并 atUsers
	found := false
	for _, m := range msg.Mentions {
		if m.UserID == "staff-2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("atUsers not merged into mentions: %+v", msg.Mentions)
	}
}

func TestNormalizeConversationType(t *testing.T) {
	data := fullPayload()
	var m map[string]interface{}
	_ = json.Unmarshal(data, &m)
	m["conversationType"] = "1"
	b, _ := json.Marshal(m)
	msg, err := NormalizeIncoming(b)
	if err != nil {
		t.Fatal(err)
	}
	if msg.ConversationType != "dm" {
		t.Fatalf("conversationType 1 should map to dm, got %q", msg.ConversationType)
	}
	// 单聊不剥 @ 前缀
	m["text"] = map[string]string{"content": "@bot hi"}
	b, _ = json.Marshal(m)
	msg, _ = NormalizeIncoming(b)
	if msg.Text != "@bot hi" {
		t.Fatalf("dm should keep @ prefix, got %q", msg.Text)
	}
	// 未知值 → group
	m["conversationType"] = "9"
	b, _ = json.Marshal(m)
	msg, _ = NormalizeIncoming(b)
	if msg.ConversationType != "group" {
		t.Fatalf("unknown type should default to group, got %q", msg.ConversationType)
	}
}

func TestNormalizeMentionAll(t *testing.T) {
	data := fullPayload()
	var m map[string]interface{}
	_ = json.Unmarshal(data, &m)
	m["atUsers"] = []map[string]string{{"staffId": "all"}}
	b, _ := json.Marshal(m)
	msg, _ := NormalizeIncoming(b)
	if !msg.MentionAll {
		t.Fatal("atUsers staffId=all should set MentionAll")
	}
}

func TestNormalizeInvalidJSON(t *testing.T) {
	if _, err := NormalizeIncoming(json.RawMessage(`{bad`)); err == nil {
		t.Fatal("invalid JSON should error")
	}
}
