//go:build mermaid_truth

package gstate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// mermaidCliVersion is pinned for reproducible SVG snapshots. Bumps are
// explicit PRs that regenerate testdata/golden/mermaid/svg/*.svg.
const mermaidCliVersion = "11.15.0"

// TestMermaidParsesWithMermaidCli runs every golden .mmd through the real
// mermaid-js parser+renderer via mermaid-cli. Exit code 0 from mmdc means
// the real Mermaid engine accepted our output and produced an SVG.
//
// Build-tagged because it needs Node on PATH; skipped if npx isn't found.
// Gated out of `just ci`; runs via `just mermaid-verify` and the dedicated
// CI workflow.
func TestMermaidParsesWithMermaidCli(t *testing.T) {
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not available; skipping truth tier")
	}
	files, err := filepath.Glob(filepath.Join("testdata", "golden", "mermaid", "*.mmd"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Skip("no golden .mmd files; run with UPDATE_GOLDEN=1 first")
	}

	puppeteerCfg := filepath.Join("testdata", "puppeteer-config.json")
	out := t.TempDir()
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			svg := filepath.Join(out, filepath.Base(f)+".svg")
			cmd := exec.Command("npx", "-y",
				"@mermaid-js/mermaid-cli@"+mermaidCliVersion,
				"--quiet",
				"-p", puppeteerCfg,
				"-i", f, "-o", svg)
			stderr, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("mmdc rejected %s: %v\n%s", filepath.Base(f), err, string(stderr))
			}
		})
	}
}

// TestMermaidSvgsExist confirms a rendered SVG is checked in for every
// golden .mmd. The SVGs are reference artifacts for human visual review
// at PR time — they are not byte-compared because mermaid-cli's layout
// step produces non-deterministic coordinates (typical for graph layout).
// The parse-and-render assertion in TestMermaidParsesWithMermaidCli is the
// behavioral check; the SVG files document what we last blessed visually.
func TestMermaidSvgsExist(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "golden", "mermaid", "*.mmd"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Skip("no golden .mmd files")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			name := filepath.Base(f)
			svg := filepath.Join("testdata", "golden", "mermaid", "svg",
				name[:len(name)-len(filepath.Ext(name))]+".svg")
			info, err := os.Stat(svg)
			if err != nil {
				t.Fatalf("missing SVG %s: %v (run `just mermaid-verify` to regenerate)", svg, err)
			}
			if info.Size() == 0 {
				t.Fatalf("SVG %s is empty (run `just mermaid-verify` to regenerate)", svg)
			}
		})
	}
}
