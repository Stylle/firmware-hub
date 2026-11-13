// notices packages dependency copyright/license texts without local build paths.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		panic("usage: notices output-directory")
	}
	out := os.Args[1]
	must(os.MkdirAll(out, 0755))
	raw, e := exec.Command("go", "list", "-m", "-json", "all").Output()
	must(e)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var index strings.Builder
	for {
		var m struct {
			Path, Version, Dir string
			Main               bool
		}
		e = decoder.Decode(&m)
		if e == io.EOF {
			break
		}
		must(e)
		if m.Main || m.Dir == "" {
			continue
		}
		files, e := os.ReadDir(m.Dir)
		must(e)
		name := strings.ReplaceAll(m.Path, "/", "_") + "@" + m.Version
		for _, f := range files {
			upper := strings.ToUpper(f.Name())
			if f.IsDir() || !(strings.HasPrefix(upper, "LICENSE") || strings.HasPrefix(upper, "LICENCE") || strings.HasPrefix(upper, "COPYING") || strings.HasPrefix(upper, "NOTICE") || upper == "EDL-V10") {
				continue
			}
			dir := filepath.Join(out, name)
			must(os.MkdirAll(dir, 0755))
			b, e := os.ReadFile(filepath.Join(m.Dir, f.Name()))
			must(e)
			must(os.WriteFile(filepath.Join(dir, f.Name()), b, 0644))
		}
		fmt.Fprintln(&index, m.Path, m.Version)
	}
	b, e := os.ReadFile(filepath.Join(runtime.GOROOT(), "LICENSE"))
	must(e)
	must(os.WriteFile(filepath.Join(out, "Go-LICENSE"), b, 0644))
	must(os.WriteFile(filepath.Join(out, "MODULES.txt"), []byte(index.String()), 0644))
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
