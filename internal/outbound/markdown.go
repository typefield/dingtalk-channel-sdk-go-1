package outbound

import (
	"regexp"
	"strings"
)

// 钉钉 AI 卡片渲染器的 Markdown 归一化（SPEC §7 / E10），移植自官方 connector。
// 规则：代码块外单 \n → <br>；代码块内保留 \n；块语法行（列表/表格/标题/分隔线）
// 前保留 \n；连续引用行合并为 <br> 连接；表格分隔行前补空行。

var (
	tableDividerRe = regexp.MustCompile(`^\s*\|?\s*:?-+:?\s*(\|?\s*:?-+:?\s*)+\|?\s*$`)
	tableRowRe     = regexp.MustCompile(`^\s*\|?.*\|.*\|?\s*$`)
	blockStartRe   = regexp.MustCompile(`^(\s{0,3}(?:[-*+]|\d+[.)])[ ])|(\s{0,3}\|)|(\s{0,3}#{1,6}\s)|(\s{0,3}(?:[-*_])\s*(?:[-*_])\s*(?:[-*_]))`)
	fenceRe        = regexp.MustCompile(`^\s{0,3}` + "```")
	quoteRe        = regexp.MustCompile(`^\s{0,3}>\s?`)
	crlfRe         = regexp.MustCompile(`\r\n?`)
)

func NormalizeForCard(content string) string {
	return fixNewlines(ensureTableBlankLines(content))
}

// ensureTableBlankLines 表格前无空行则插入（否则钉钉不渲染表格）。
func ensureTableBlankLines(text string) string {
	lines := strings.Split(crlfRe.ReplaceAllString(text, "\n"), "\n")
	out := make([]string, 0, len(lines)+4)
	for i, cur := range lines {
		next := ""
		if i+1 < len(lines) {
			next = lines[i+1]
		}
		if i > 0 && tableRowRe.MatchString(cur) && isDivider(next) && strings.TrimSpace(lines[i-1]) != "" && !tableRowRe.MatchString(lines[i-1]) {
			out = append(out, "")
		}
		out = append(out, cur)
	}
	return strings.Join(out, "\n")
}

func isDivider(line string) bool {
	return line != "" && strings.Contains(line, "|") && tableDividerRe.MatchString(line)
}

// fixNewlines 单 \n → <br>，按钉钉卡片渲染器约定处理代码块/引用/块语法行。
func fixNewlines(text string) string {
	lines := strings.Split(crlfRe.ReplaceAllString(text, "\n"), "\n")

	// 1. 合并连续引用行：去掉续行 > 前缀，用 <br> 连接（代码块外）。
	merged := make([]string, 0, len(lines))
	var pending []string
	inCode := false
	flush := func() {
		if len(pending) > 0 {
			merged = append(merged, strings.Join(pending, "<br>"))
			pending = pending[:0]
		}
	}
	for _, line := range lines {
		isFence := fenceRe.MatchString(line)
		if inCode {
			flush()
			merged = append(merged, line)
			if isFence {
				inCode = false
			}
			continue
		}
		if isFence {
			flush()
			merged = append(merged, line)
			inCode = true
			continue
		}
		if quoteRe.MatchString(line) {
			if len(pending) == 0 {
				pending = append(pending, line)
			} else {
				pending = append(pending, quoteRe.ReplaceAllString(line, ""))
			}
		} else {
			flush()
			merged = append(merged, line)
		}
	}
	flush()

	// 2. 逐行决定分隔符：代码块内/块语法行前保留 \n，其余 <br>。
	var sb strings.Builder
	inCode = false
	for i, cur := range merged {
		nextInCode := inCode
		if fenceRe.MatchString(cur) {
			nextInCode = !inCode
		}
		if i < len(merged)-1 {
			next := merged[i+1]
			keepNL := nextInCode || cur == "" || next == "" || fenceRe.MatchString(next) || blockStartRe.MatchString(next)
			sb.WriteString(cur)
			if keepNL {
				sb.WriteByte('\n')
			} else {
				sb.WriteString("<br>")
			}
		} else {
			sb.WriteString(cur)
		}
		inCode = nextInCode
	}
	return sb.String()
}
