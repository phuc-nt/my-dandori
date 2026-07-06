package probe

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/phuc-nt/dandori/internal/config"
)

var fakeCfg = config.Config{}

func cfgWithSite(site string) *config.Config {
	c := config.Config{}
	c.Integrations.AtlassianSite = site
	c.Integrations.AtlassianEmail = "a@b.c"
	c.Integrations.AtlassianToken = "tok"
	c.Integrations.ConfluenceSpaceID = "123"
	return &c
}

func TestProbeRejectsInvalidSiteLabel(t *testing.T) {
	// A site with a dot/slash would let a saved value redirect the probe host
	// (SSRF). It must be rejected before any network call.
	for _, bad := range []string{"evil.com", "a/b", "host:9000", "UPPER"} {
		cfg := cfgWithSite(bad)
		if r := Probe("jira", cfg); r.OK {
			t.Errorf("jira probe accepted invalid site %q", bad)
		}
		if r := Probe("confluence", cfg); r.OK {
			t.Errorf("confluence probe accepted invalid site %q", bad)
		}
	}
}

func TestProbeCLIReportsAuthFailure(t *testing.T) {
	// Point PATH at a dir with a fake `gh` that exits non-zero → not authed.
	dir := t.TempDir()
	writeFakeBin(t, dir, "gh", "#!/bin/sh\nexit 1\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if r := Probe("github", &fakeCfg); r.OK {
		t.Error("github probe reported OK when gh exits 1")
	}

	// Now a fake gh that exits 0 → authed.
	writeFakeBin(t, dir, "gh", "#!/bin/sh\nexit 0\n")
	if r := Probe("github", &fakeCfg); !r.OK {
		t.Error("github probe reported failure when gh exits 0")
	}
}

func TestProbeUnknownIntegration(t *testing.T) {
	if r := Probe("mystery", &fakeCfg); r.OK {
		t.Error("unknown integration probed OK")
	}
}

func writeFakeBin(t *testing.T, dir, name, script string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
