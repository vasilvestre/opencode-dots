package jira

import (
	"encoding/json"
	"strings"
)

const maxDepth = 32

type node struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Attrs   json.RawMessage `json:"attrs"`
	Content []node          `json:"content"`
}

// FlattenText turns a Jira text field into plain text. The field is a JSON string
// (API v2, webhooks) or an Atlassian document (API v3); null or anything else gives "".
// Only text is kept: marks, links and media are dropped, and nothing is executed or
// resolved. The result is data.
func FlattenText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var n node
	if json.Unmarshal(raw, &n) != nil || n.Type != "doc" {
		return ""
	}
	var b strings.Builder
	walk(&b, n, 0)
	return strings.TrimSpace(b.String())
}

func walk(b *strings.Builder, n node, depth int) {
	if depth > maxDepth {
		return
	}
	switch n.Type {
	case "text":
		b.WriteString(n.Text)
		return
	case "hardBreak":
		b.WriteByte('\n')
		return
	case "mention":
		var a struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(n.Attrs, &a)
		b.WriteString(a.Text)
		return
	}
	for _, c := range n.Content {
		walk(b, c, depth+1)
	}
	switch n.Type {
	case "paragraph", "heading", "listItem", "codeBlock", "blockquote", "panel", "tableRow":
		b.WriteByte('\n')
	}
}
