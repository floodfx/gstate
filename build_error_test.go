package gstate

import (
	"context"
	"strings"
	"testing"
)

func TestBuildWithErrorValidation(t *testing.T) {
	type B = StateBuilder[StateID, EventID, Context]
	type MB = MachineBuilder[StateID, EventID, Context]
	tests := []struct {
		name      string
		configure func(*MB)
		want      string
	}{
		{"empty", func(*MB) {}, "initial state is required"},
		{"initial", func(b *MB) { b.Initial("missing").State("a", func(*B) {}) }, "invalid initial state"},
		{"compound", func(b *MB) { b.Initial("a").State("a", func(s *B) { s.State("child", func(*B) {}) }) }, "compound initial state is required"},
		{"event", func(b *MB) { b.Initial("a").State("a", func(s *B) { s.On("GO").GoTo("missing") }) }, "invalid transition"},
		{"always", func(b *MB) { b.Initial("a").State("a", func(s *B) { s.Always().GoTo("missing") }) }, "invalid Always"},
		{"delayed", func(b *MB) { b.Initial("a").State("a", func(s *B) { s.After(0).GoTo("missing") }) }, "invalid Delayed"},
		{"invoke", func(b *MB) {
			b.Initial("a").State("a", func(s *B) {
				s.Invoke(func(context.Context, Context, func(func(Context) Context)) error { return nil }, "missing", "")
			})
		}, "invalid Invoke"},
		{"cross-region", func(b *MB) {
			b.Initial("p").State("p", func(s *B) {
				s.Type(Parallel)
				s.State("a", func(s *B) { s.On("GO").GoTo("b") })
				s.State("b", func(*B) {})
			})
		}, "cross-region"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := New[StateID, EventID, Context]("checked")
			tc.configure(b)
			if err := b.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate = %v, want %q", err, tc.want)
			}
			m, err := b.BuildWithError()
			if m != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("BuildWithError = %v, %v", m, err)
			}
			assertPanic(t, func() { b.Build() }, tc.want)
		})
	}
}

func TestBuildWithErrorValidAndRepair(t *testing.T) {
	type B = StateBuilder[StateID, EventID, Context]
	b := New[StateID, EventID, Context]("repair").Initial("parent").State("parent", func(s *B) { s.Initial("child"); s.State("child", func(s *B) { s.On("GO").GoTo("next") }) })
	if m, err := b.BuildWithError(); m != nil || err == nil {
		t.Fatalf("missing target: %v, %v", m, err)
	}
	b.State("next", func(*B) {})
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	m, err := b.BuildWithError()
	if err != nil {
		t.Fatal(err)
	}
	if b.Build() != m {
		t.Fatal("Build changed machine identity")
	}
	a := Start(m, Context{})
	defer a.Stop()
	if !a.Matches("parent") || !a.Matches("child") {
		t.Fatalf("metadata did not initialize hierarchy: %v", a.States())
	}
}

func TestBuildPreservesPanicValueTypes(t *testing.T) {
	for _, kind := range []string{"structure", "target"} {
		t.Run(kind, func(t *testing.T) {
			defer func() {
				value := recover()
				if kind == "structure" {
					if message, ok := value.(string); !ok || message != "gstate: machine initial state is required" {
						t.Fatalf("structural panic = %T %v, want original string", value, value)
					}
				} else if _, ok := value.(error); !ok {
					t.Fatalf("target panic = %T %v, want error", value, value)
				}
			}()
			b := New[StateID, EventID, Context]("panic-compat")
			if kind == "target" {
				b.Initial("missing").State("a", func(*StateBuilder[StateID, EventID, Context]) {})
			}
			b.Build()
		})
	}
}
