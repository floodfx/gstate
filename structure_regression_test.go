package gstate

import (
	"context"
	"slices"
	"testing"
)

func TestInvalidStructures(t *testing.T) {
	type B = StateBuilder[string, string, Context]
	tests := []struct {
		name  string
		build func()
	}{
		{"empty", func() { New[string, string, Context]("x").Build() }},
		{"missing initial", func() { New[string, string, Context]("x").State("a", func(*B) {}).Build() }},
		{"empty ID", func() { New[string, string, Context]("x").State("", func(*B) {}).Build() }},
		{"duplicate", func() {
			New[string, string, Context]("x").Initial("a").State("a", func(*B) {}).State("a", func(*B) {}).Build()
		}},
		{"nested duplicate", func() {
			New[string, string, Context]("x").Initial("p").State("p", func(s *B) { s.Initial("a"); s.State("a", func(*B) {}) }).State("q", func(s *B) { s.Initial("a"); s.State("a", func(*B) {}) }).Build()
		}},
		{"nested machine initial", func() {
			New[string, string, Context]("x").Initial("a").State("p", func(s *B) { s.Initial("a"); s.State("a", func(*B) {}) }).Build()
		}},
		{"missing compound initial", func() {
			New[string, string, Context]("x").Initial("p").State("p", func(s *B) { s.State("a", func(*B) {}) }).Build()
		}},
		{"unknown kind", func() {
			New[string, string, Context]("x").Initial("a").State("a", func(s *B) { s.Type(StateType(42)) }).Build()
		}},
		{"empty compound", func() {
			New[string, string, Context]("x").Initial("a").State("a", func(s *B) { s.Type(Compound) }).Build()
		}},
		{"empty parallel", func() {
			New[string, string, Context]("x").Initial("a").State("a", func(s *B) { s.Type(Parallel) }).Build()
		}},
		{"final outgoing", func() {
			New[string, string, Context]("x").Initial("a").State("a", func(s *B) { s.Type(Final); s.On("GO").GoTo("a") }).Build()
		}},
		{"atomic children", func() {
			New[string, string, Context]("x").Initial("p").State("p", func(s *B) { s.State("a", func(*B) {}); s.Type(Atomic) }).Build()
		}},
		{"final children", func() {
			New[string, string, Context]("x").Initial("p").State("p", func(s *B) { s.Type(Final); s.State("a", func(*B) {}) }).Build()
		}},
		{"atomic initial", func() {
			New[string, string, Context]("x").Initial("a").State("a", func(s *B) { s.Initial("a") }).Build()
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { assertPanic(t, tc.build, "gstate:") })
	}
}

func TestCrossRegionTransitionsRejected(t *testing.T) {
	for _, kind := range []string{"event", "always", "timer", "invoke"} {
		t.Run(kind, func(t *testing.T) {
			assertPanic(t, func() {
				New[string, string, Context]("cross").Initial("p").State("p", func(s *StateBuilder[string, string, Context]) {
					s.Type(Parallel)
					s.State("a", func(s *StateBuilder[string, string, Context]) {
						switch kind {
						case "event":
							s.On("GO").GoTo("b")
						case "always":
							s.Always().GoTo("b")
						case "timer":
							s.After(1).GoTo("b")
						case "invoke":
							s.Invoke(func(context.Context, Context, func(func(Context) Context)) error { return nil }, "b", "")
						}
					})
					s.State("b", func(*StateBuilder[string, string, Context]) {})
				}).Build()
			}, "cross-region")
		})
	}
}

func TestHydrateRepairsConfiguration(t *testing.T) {
	m := fuzzMachine()
	for _, tc := range []struct {
		name    string
		active  []StateID
		history map[StateID]StateID
		want    []StateID
	}{
		{"empty", nil, nil, []StateID{"idle"}},
		{"unknown", []StateID{"bogus"}, nil, []StateID{"idle"}},
		{"compound", []StateID{"working"}, nil, []StateID{"working", "loading"}},
		{"parallel", []StateID{"r1b"}, nil, []StateID{"parallel", "region1", "region2", "r1b", "r2a"}},
		{"conflict", []StateID{"working", "ready", "loading"}, nil, []StateID{"working", "loading"}},
		{"bad history", []StateID{"working"}, map[StateID]StateID{"working": "parallel"}, []StateID{"working", "loading"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := Hydrate(m, Snapshot[StateID, Context]{Active: tc.active, History: tc.history})
			defer a.Stop()
			if got := a.States(); !slices.Equal(got, tc.want) {
				t.Fatalf("states=%v, want %v", got, tc.want)
			}
		})
	}
}
