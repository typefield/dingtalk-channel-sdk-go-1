package outbound

import (
	"regexp"
	"strings"
)

var (
	fenceRegex   = regexp.MustCompile("^" + "```" + `(\w*)$`)
	headingRegex = regexp.MustCompile(`^#{1,6}\s`)
)

// SplitWithCodeFences splits a long markdown string into chunks under limit chars.
// Preserves code block integrity — closes and reopens fenced code blocks at chunk boundaries 。
func SplitWithCodeFences(text string, limit int) []string {
	if limit <= 0 || len(text) <= limit {
		return []string{text}
	}
	lines := strings.Split(text, "\n")
	var out []string
	var buf []string
	var bufLen int
	var fenceLang *string

	flush := func() {
		if len(buf) == 0 {
			return
		}
		chunk := strings.Join(buf, "\n")
		if fenceLang != nil {
			chunk += "\n```"
		}
		out = append(out, chunk)
		buf = nil
		bufLen = 0
		if fenceLang != nil {
			reopen := "```" + *fenceLang
			buf = append(buf, reopen)
			bufLen = len(reopen)
		}
	}

	for _, line := range lines {
		m := fenceRegex.FindStringSubmatch(line)
		lineLen := len(line)
		if len(buf) > 0 {
			lineLen++
		}
		isHeading := headingRegex.MatchString(line)
		nearFull := float64(bufLen) > float64(limit)*0.75

		if bufLen+lineLen > limit || (isHeading && nearFull && len(buf) > 0) {
			flush()
		}

		buf = append(buf, line)
		bufLen += lineLen

		if len(m) > 0 {
			if fenceLang == nil {
				lang := m[1]
				fenceLang = &lang
			} else {
				fenceLang = nil
			}
		}
	}
	flush()
	return out
}
