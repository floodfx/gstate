package gstate

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Compile the actual documentation blocks, so fixing a separate copy cannot
// hide a README regression. Fragment examples receive only their named context.
func TestREADMEExamplesCompile(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	matches := regexp.MustCompile("(?s)```go\\n(.*?)```").FindAllStringSubmatch(string(raw), -1)
	blocks := make([]string, len(matches))
	for i, m := range matches {
		blocks[i] = m[1]
	}
	find := func(prefix string) string {
		for _, block := range blocks {
			if strings.HasPrefix(block, prefix) {
				return block
			}
		}
		t.Fatalf("missing README block %q", prefix)
		return ""
	}
	imports := "package main\nimport(\"context\";\"fmt\";\"log\";\"github.com/floodfx/gstate\")\nvar _=context.Background\nvar _=fmt.Println\nvar _=log.Printf\nvar _=gstate.Atomic\n"
	types := "type MyState string\ntype MyEvent string\ntype MyData struct{Count int}\nfunc(d MyData) Clone() MyData{return d}\nconst EventGo MyEvent=\"GO\"\n"
	machine := "machine:=gstate.New[MyState,MyEvent,MyData](\"docs\").Initial(\"idle\").State(\"idle\",func(*gstate.StateBuilder[MyState,MyEvent,MyData]){}).Build()\n"
	quick := find("type MyState string")
	cut := strings.Index(quick, "machine :=")
	tests := []struct{ name, source string }{
		{"quickstart", imports + quick[:cut] + "\nfunc main(){\n" + quick[cut:] + "\n_=machine\n}"},
		{"deep clone", imports + find("type MyData struct {\n    Data []int") + "\nfunc main(){}"},
		{"validation", imports + types + "\nfunc main(){\n" + find("// Panics:") + "\n}"},
		{"custom observer", imports + types + find("type loggingObs struct") + "\nfunc main(){}"},
		{"signal observer", imports + types + "\nfunc main(){\n" + machine + find("ready := make(chan struct{}, 1)") + "\nactor.Stop()\n}"},
		{"observer funcs", imports + types + "\nfunc main(){\n" + machine + find("obs := gstate.ObserverFuncs") + "\nactor.Stop()\n}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "main.go")
			if err := os.WriteFile(source, []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "example"), source)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("README example does not compile: %v\n%s", err, output)
			}
		})
	}
}
