package normalize

import (
	"encoding/json"
	"testing"
)

func TestConvertPicture(t *testing.T) {
	text, res, _ := parseContent("picture", json.RawMessage(`{"downloadCode":"dc-1"}`), nil)
	if text != "[图片]" {
		t.Fatalf("text = %q", text)
	}
	if len(res) != 1 || res[0].Type != "image" || res[0].DownloadCode != "dc-1" {
		t.Fatalf("resource = %+v", res)
	}
	// 缺 downloadCode：占位文本仍输出，无资源
	text, res, _ = parseContent("picture", json.RawMessage(`{}`), nil)
	if text != "[图片]" || len(res) != 0 {
		t.Fatalf("missing code: text=%q res=%d", text, len(res))
	}
}

func TestConvertFile(t *testing.T) {
	text, res, _ := parseContent("file", json.RawMessage(`{"downloadCode":"dc-2","fileName":"report.pdf"}`), nil)
	if text != "[文件: report.pdf]" {
		t.Fatalf("text = %q", text)
	}
	if len(res) != 1 || res[0].Type != "file" || res[0].FileName != "report.pdf" {
		t.Fatalf("resource = %+v", res)
	}
	// 文件名缺失回退
	text, _, _ = parseContent("file", json.RawMessage(`{"downloadCode":"dc-3"}`), nil)
	if text != "[文件: 文件]" {
		t.Fatalf("fallback name text = %q", text)
	}
}

func TestConvertAudio(t *testing.T) {
	// 有识别文本：优先透出
	text, res, _ := parseContent("audio", json.RawMessage(`{"downloadCode":"dc-4","recognition":"开会要点"}`), nil)
	if text != "开会要点" {
		t.Fatalf("recognition text = %q", text)
	}
	if len(res) != 1 || res[0].Recognition != "开会要点" {
		t.Fatalf("resource = %+v", res)
	}
	// 无识别文本：占位
	text, _, _ = parseContent("audio", json.RawMessage(`{"downloadCode":"dc-5"}`), nil)
	if text != "[语音消息]" {
		t.Fatalf("placeholder = %q", text)
	}
}

func TestConvertVideo(t *testing.T) {
	text, res, _ := parseContent("video", json.RawMessage(`{"downloadCode":"dc-6","fileName":"clip.mp4"}`), nil)
	if text != "[视频]" {
		t.Fatalf("text = %q", text)
	}
	if len(res) != 1 || res[0].Type != "video" || res[0].FileName != "clip.mp4" {
		t.Fatalf("resource = %+v", res)
	}
}
