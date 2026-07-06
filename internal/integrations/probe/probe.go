// Package probe runs read-only connection tests for the console's settings
// page. It sits in its own package (not internal/integrations) because
// integrations/report.go imports internal/integrations, which would create an
// import cycle if probe lived there.
package probe

import (
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"time"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/integrations/confluence"
	"github.com/phuc-nt/dandori/internal/integrations/jira"
	"github.com/phuc-nt/dandori/internal/integrations/slack"
)

// ProbeResult is the outcome of a read-only connection test.
type ProbeResult struct {
	OK   bool
	Note string
}

// siteLabel restricts an Atlassian site to a single DNS label so a saved
// ATLASSIAN_SITE_NAME cannot redirect the probe at an arbitrary host (SSRF).
var siteLabel = regexp.MustCompile(`^[a-z0-9-]+$`)

// Probe runs a read-only connection test for one integration. It never mutates
// remote state. Errors are trimmed to a short note that never echoes
// credentials.
func Probe(name string, cfg *config.Config) ProbeResult {
	i := cfg.Integrations
	switch name {
	case "jira":
		if !siteLabel.MatchString(i.AtlassianSite) {
			return fail("tên site không hợp lệ (chỉ chữ thường, số, gạch ngang)")
		}
		if err := jira.New(i.AtlassianSite, i.AtlassianEmail, i.AtlassianToken).Myself(); err != nil {
			return fail(short(err))
		}
		return ok("kết nối Jira thành công")
	case "confluence":
		if !siteLabel.MatchString(i.AtlassianSite) {
			return fail("tên site không hợp lệ")
		}
		if i.ConfluenceSpaceID == "" {
			return fail("chưa cấu hình Confluence space id")
		}
		if err := confluence.New(i.AtlassianSite, i.AtlassianEmail, i.AtlassianToken).GetSpace(i.ConfluenceSpaceID); err != nil {
			return fail(short(err))
		}
		return ok("kết nối Confluence thành công")
	case "slack":
		if err := slack.New(i.SlackXoxc, i.SlackXoxd).AuthTest(); err != nil {
			return fail(short(err))
		}
		return ok("kết nối Slack thành công")
	case "openrouter":
		return probeOpenRouter(cfg.OpenRouterKey)
	case "github":
		return probeCLI("gh", "auth", "status")
	case "gws":
		return probeCLI("gws", "auth", "status")
	default:
		return fail("integration không xác định")
	}
}

// probeCLI runs a keyring-backed CLI auth check (gh/gws). A non-zero exit means
// not authenticated.
func probeCLI(bin string, args ...string) ProbeResult {
	cmd := exec.Command(bin, args...)
	if err := cmd.Run(); err != nil {
		return fail(bin + " chưa đăng nhập (chạy `" + bin + " auth login`)")
	}
	return ok(bin + " đã đăng nhập")
}

// probeOpenRouter does a read-only GET /models with the key to prove it works.
func probeOpenRouter(key string) ProbeResult {
	if key == "" {
		return fail("chưa cấu hình OpenRouter API key")
	}
	req, err := http.NewRequest("GET", "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		return fail(short(err))
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fail(short(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fail(fmt.Sprintf("OpenRouter HTTP %d", resp.StatusCode))
	}
	return ok("kết nối OpenRouter thành công")
}

func ok(note string) ProbeResult   { return ProbeResult{OK: true, Note: note} }
func fail(note string) ProbeResult { return ProbeResult{OK: false, Note: note} }

// short trims an error to a safe, credential-free note.
func short(err error) string {
	s := err.Error()
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
