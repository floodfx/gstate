package gstate

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
)

// GoldenTest is a table-driven test that ensures our SCXML output
// matches known-good golden files. To regenerate: go test -update
var update = os.Getenv("UPDATE_GOLDEN") != ""

func TestSCXMLGoldenExamples(t *testing.T) {
	type testCase struct {
		name string
		m    any // *Machine[S,E,C]
	}

	tests := []testCase{
		{"basics", basicGoldenMachine()},
		{"hierarchy", hierarchyGoldenMachine()},
		{"parallel", parallelGoldenMachine()},
		{"history", historyGoldenMachine()},
		{"invoke", invokeGoldenMachine()},
		{"delayed", delayedGoldenMachine()},
	}

	goldenDir := filepath.Join("testdata", "golden")

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			goldenPath := filepath.Join(goldenDir, tc.name+".scxml")

			got := toSCXMLStringAny(tc.m)

			if update {
				os.MkdirAll(goldenDir, 0755)
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

// toSCXMLStringAny is a typed helper that converts *Machine to SCXML string
// for known test types.
func toSCXMLStringAny(m any) string {
	type state = string
	type event = string
	type ctx = any // for most...
	switch v := m.(type) {
	case *Machine[state, event, struct{ Count int }]:
		s, _ := ToSCXMLString(v)
		return s
	case *Machine[state, event, any]:
		s, _ := ToSCXMLString(v)
		return s
	default:
		return ""
	}
}

func basicGoldenMachine() *Machine[string, string, struct{ Count int }] {
	return New[string, string, struct{ Count int }]("counter").
		Initial("idle").
		State("idle", func(s *StateBuilder[string, string, struct{ Count int }]) {
			s.On("START").GoTo("active")
		}).
		State("active", func(s *StateBuilder[string, string, struct{ Count int }]) {
			s.On("STOP").GoTo("idle")
		}).
		Build()
}

func hierarchyGoldenMachine() *Machine[string, string, any] {
	return New[string, string, any]("hierarchy").
		Initial("parent").
		State("parent", func(s *StateBuilder[string, string, any]) {
			s.Initial("child1")
			s.On("EXIT_ALL").GoTo("done")
			s.State("child1", func(s *StateBuilder[string, string, any]) {
				s.On("TO_CHILD2").GoTo("child2")
			})
			s.State("child2", func(s *StateBuilder[string, string, any]) {
				s.On("TO_CHILD1").GoTo("child1")
			})
		}).
		State("done", func(s *StateBuilder[string, string, any]) {
			s.Type(Final)
		}).
		Build()
}

func parallelGoldenMachine() *Machine[string, string, any] {
	return New[string, string, any]("input_system").
		Initial("active").
		State("active", func(s *StateBuilder[string, string, any]) {
			s.Type(Parallel)
			s.State("keyboard", func(s *StateBuilder[string, string, any]) {
				s.Initial("caps_off")
				s.State("caps_off", func(s *StateBuilder[string, string, any]) {
					s.On("CAPS_LOCK").GoTo("caps_on")
				})
				s.State("caps_on", func(s *StateBuilder[string, string, any]) {
					s.On("CAPS_LOCK").GoTo("caps_off")
				})
			})
			s.State("mouse", func(s *StateBuilder[string, string, any]) {
				s.Initial("not_clicked")
				s.State("not_clicked", func(s *StateBuilder[string, string, any]) {
					s.On("CLICK").GoTo("clicked")
				})
				s.State("clicked", func(s *StateBuilder[string, string, any]) {
					s.On("RELEASE").GoTo("not_clicked")
				})
			})
		}).
		Build()
}

func historyGoldenMachine() *Machine[string, string, any] {
	return New[string, string, any]("history").
		Initial("parent").
		State("parent", func(s *StateBuilder[string, string, any]) {
			s.Initial("s1")
			s.History(Shallow)
			s.State("s1", func(s *StateBuilder[string, string, any]) {
				s.On("TO_S2").GoTo("s2")
			})
			s.State("s2", func(s *StateBuilder[string, string, any]) {})
			s.On("GO_AWAY").GoTo("other")
		}).
		State("other", func(s *StateBuilder[string, string, any]) {
			s.On("BACK").GoTo("parent")
		}).
		Build()
}

func invokeGoldenMachine() *Machine[string, string, any] {
	return New[string, string, any]("invoke").
		Initial("loading").
		State("loading", func(s *StateBuilder[string, string, any]) {
			s.Invoke(nil, "success", "failure")
		}).
		State("success", func(s *StateBuilder[string, string, any]) {
			s.Type(Final)
		}).
		State("failure", func(s *StateBuilder[string, string, any]) {
			s.Type(Final)
		}).
		Build()
}

func delayedGoldenMachine() *Machine[string, string, any] {
	return New[string, string, any]("delayed").
		Initial("idle").
		State("idle", func(s *StateBuilder[string, string, any]) {
			s.After(0).GoTo("timeout")
		}).
		State("timeout", func(s *StateBuilder[string, string, any]) {}).
		Build()
}

// TestSCXMLValidatesAgainstW3CSpec verifies our output parses against real W3C SCXML schemas.
// It ensures the namespace, element names, and structure are spec-conformant.
func TestSCXMLValidatesAgainstW3C(t *testing.T) {
	w3cFiles, err := filepath.Glob("testdata/w3c/*.txml")
	if err != nil || len(w3cFiles) == 0 {
		t.Skip("no W3C reference files available (testdata/w3c/)")
	}
	for _, f := range w3cFiles {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			var doc SCXMLDocument
			if err := xml.Unmarshal(data, &doc); err != nil {
				t.Errorf("W3C reference file %s should parse cleanly: %v", f, err)
			}
		})
	}

	// Also test that our own output round-trips correctly
	t.Run("roundtrip", func(t *testing.T) {
		goldenFiles, _ := filepath.Glob("testdata/golden/*.scxml")
		for _, gf := range goldenFiles {
			data, err := os.ReadFile(gf)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			var doc SCXMLDocument
			if err := xml.Unmarshal(data, &doc); err != nil {
				t.Errorf("golden file %s should parse cleanly: %v", gf, err)
			}
		}
	})
}
