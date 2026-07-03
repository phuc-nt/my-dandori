package contexthub

import (
	"strings"
	"testing"
)

func TestDiffHTMLShowsAddRemove(t *testing.T) {
	html := string(DiffHTML("dòng 1\ndòng 2\n", "dòng 1\ndòng 2 sửa\n", "v1", "v2"))
	if !strings.Contains(html, "green") || !strings.Contains(html, "red") {
		t.Errorf("diff missing add/remove styling: %s", html)
	}
}

// Diff lines are user content — must be HTML-escaped, never rendered raw.
func TestDiffHTMLEscapesXSS(t *testing.T) {
	html := string(DiffHTML("an toàn\n", "<script>alert(1)</script>\n", "v1", "v2"))
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Errorf("unescaped script in diff: %s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("script not escaped: %s", html)
	}
}

func TestDiffHTMLNoChange(t *testing.T) {
	html := string(DiffHTML("giống nhau\n", "giống nhau\n", "v1", "v2"))
	if !strings.Contains(html, "không có thay đổi") {
		t.Errorf("identical versions should say no change: %s", html)
	}
}
