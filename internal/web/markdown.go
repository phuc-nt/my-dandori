package web

import (
	"bytes"
	"html/template"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// mdRenderer converts CommonMark + GFM tables to HTML. The chat assistant
// replies in markdown (headings, tables, bold), which was previously shown raw.
var mdRenderer = goldmark.New(goldmark.WithExtensions(extension.GFM))

// mdPolicy sanitizes the goldmark output. The chat answer is LLM-generated —
// untrusted — so we render markdown then strip anything but a safe subset of
// tags (no script/style/iframe, no event handlers, no javascript: URLs).
var mdPolicy = bluemonday.UGCPolicy()

// renderMarkdown turns untrusted markdown into sanitized, safe HTML for
// injection into a template via template.HTML. XSS-safe: goldmark escapes raw
// HTML in the source, and bluemonday whitelists the resulting tag set.
func renderMarkdown(src string) template.HTML {
	var buf bytes.Buffer
	if err := mdRenderer.Convert([]byte(src), &buf); err != nil {
		// Fall back to escaped plain text on any conversion error — never
		// emit unsanitized bytes.
		return template.HTML(template.HTMLEscapeString(src))
	}
	return template.HTML(mdPolicy.SanitizeBytes(buf.Bytes()))
}
