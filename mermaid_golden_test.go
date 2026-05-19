package gstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	mc "github.com/sammcj/mermaid-check"
)

// TestMermaidGoldens checks that ToMermaid output matches known-good golden
// .mmd files. To regenerate: UPDATE_GOLDEN=1 go test -run TestMermaidGoldens
func TestMermaidGoldens(t *testing.T) {
	tests := []struct {
		name string
		m    any
	}{
		{"basics", basicGoldenMachine()},
		{"hierarchy", hierarchyGoldenMachine()},
		{"parallel", parallelGoldenMachine()},
		{"history", historyGoldenMachine()},
		{"invoke", invokeGoldenMachine()},
		{"delayed", delayedGoldenMachine()},
		{"labels", labelsGoldenMachine()},
	}

	goldenDir := filepath.Join("testdata", "golden", "mermaid")

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			goldenPath := filepath.Join(goldenDir, tc.name+".mmd")

			got := toMermaidStringAny(tc.m)

			if update {
				if err := os.MkdirAll(goldenDir, 0755); err != nil {
					t.Fatalf("mkdir golden: %v", err)
				}
				if err := os.WriteFile(goldenPath, []byte(got), 0644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v (run with UPDATE_GOLDEN=1)", goldenPath, err)
			}
			if got != string(want) {
				t.Errorf("golden mismatch (-want +got):\nwant:\n%s\ngot:\n%s", string(want), got)
			}
		})
	}
}

// TestMermaidGoldensParseAsFlowchart confirms each golden file is accepted
// by the pure-Go mermaid-check parser (the in-Go correctness tier).
//
// mermaid-check v0.0.4 doesn't yet recognize YAML frontmatter, so we strip
// it before parsing. The truth tier (mermaid-cli) validates frontmatter
// end-to-end.
func TestMermaidGoldensParseAsFlowchart(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "golden", "mermaid", "*.mmd"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Skip("no golden .mmd files; run with UPDATE_GOLDEN=1 first")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			body := stripFrontmatter(string(data))
			if _, err := mc.ParseFlowchart(body); err != nil {
				t.Errorf("mermaid-check rejected golden:\n  err: %v\n  body:\n%s", err, body)
			}
		})
	}
}

// stripFrontmatter removes a leading YAML frontmatter block from a Mermaid
// source string. Mermaid's real parser handles frontmatter; mermaid-check
// v0.0.4 does not. Used by parse-tier tests.
func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return s
	}
	rest := s[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return s
	}
	return rest[end+len("\n---\n"):]
}

// toMermaidStringAny mirrors toSCXMLStringAny for the Mermaid output path.
func toMermaidStringAny(m any) string {
	type state = string
	type event = string
	switch v := m.(type) {
	case *Machine[state, event, struct{ Count int }]:
		return ToMermaid(v)
	case *Machine[state, event, any]:
		return ToMermaid(v)
	default:
		return ""
	}
}

// labelsGoldenMachine exercises EntryLabel/ExitLabel/InvokeLabel together
// with a labeled invoke that has both OnDone and OnError targets — the
// signature combination Issue #37 is about.
func labelsGoldenMachine() *Machine[string, string, any] {
	return New[string, string, any]("labeled_machine").
		Initial("idle").
		State("idle", func(s *StateBuilder[string, string, any]) {
			s.Entry(func(c any) any { return c })
			s.EntryLabel("startEngine")
			s.Exit(func(c any) any { return c })
			s.ExitLabel("stopEngine")
			s.On("BEGIN").GoTo("calling_llm")
		}).
		State("calling_llm", func(s *StateBuilder[string, string, any]) {
			s.Invoke(nil, "checking_response", "failed")
			s.InvokeLabel("call_llm")
		}).
		State("checking_response", func(s *StateBuilder[string, string, any]) {
			s.On("OK").GoTo("done")
		}).
		State("failed", func(s *StateBuilder[string, string, any]) {
			s.Type(Final)
		}).
		State("done", func(s *StateBuilder[string, string, any]) {
			s.Type(Final)
		}).
		Build()
}
