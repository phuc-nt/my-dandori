package contexthub

import (
	"html/template"
	"strings"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
)

// DiffHTML renders a line-level unified diff of two context versions as
// coloured HTML. Every line is HTML-escaped BEFORE styling — content is
// user-supplied, so an unescaped diff would be an XSS hole. gotextdiff is
// pure-Go (no CGO); it is a frozen upstream mirror of the gopls diff code,
// fine as a stable dependency at this scale.
func DiffHTML(before, after, fromLabel, toLabel string) template.HTML {
	edits := myers.ComputeEdits(span.URIFromPath("a"), before, after)
	unified := gotextdiff.ToUnified(fromLabel, toLabel, before, edits)

	var b strings.Builder
	b.WriteString(`<pre class="text-xs leading-5 font-mono overflow-x-auto">`)
	for _, hunk := range unified.Hunks {
		for _, l := range hunk.Lines {
			text := template.HTMLEscapeString(strings.TrimRight(l.Content, "\n"))
			switch l.Kind {
			case gotextdiff.Insert:
				b.WriteString(`<div class="bg-green-50 text-green-800">+ ` + text + `</div>`)
			case gotextdiff.Delete:
				b.WriteString(`<div class="bg-red-50 text-red-700">- ` + text + `</div>`)
			default:
				b.WriteString(`<div class="text-gray-500">  ` + text + `</div>`)
			}
		}
	}
	if len(unified.Hunks) == 0 {
		b.WriteString(`<div class="text-gray-400">(không có thay đổi)</div>`)
	}
	b.WriteString(`</pre>`)
	return template.HTML(b.String())
}
