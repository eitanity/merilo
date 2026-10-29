package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// maxModLines bounds a go.mod file; no real one is this long.
const maxModLines = 1 << 16

// modFile is all merilovet reads from go.mod: the module path, which says
// which import paths are the module's own, and the Go version, which sets
// the language rules. merilovet checks the module's code, not its
// dependencies, so require, replace and the module cache never matter.
type modFile struct {
	module    string
	goVersion string
}

// readModFile reads the module and go lines of dir/go.mod.
func readModFile(flt *faults, dir string) (*modFile, error) {
	if flt == nil {
		return nil, internalError("readModFile needs its fault recorder")
	}
	if dir == "" {
		return nil, internalError("readModFile needs a directory")
	}
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("reading go.mod: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > maxModLines {
		return nil, fmt.Errorf("go.mod has more than %d lines", maxModLines)
	}
	mf := &modFile{}
	for i, raw := range lines {
		f := strings.Fields(stripComment(flt, raw))
		if len(f) != 2 {
			continue
		}
		switch f[0] {
		case "module":
			p, err := unquote(flt, f[1])
			if err != nil {
				return nil, fmt.Errorf("go.mod:%d: %w", i+1, err)
			}
			mf.module = p
		case "go":
			mf.goVersion = f[1]
		}
	}
	if mf.module == "" {
		return nil, fmt.Errorf("go.mod has no module line")
	}
	if held := !strings.ContainsAny(mf.module, " \t"); !held {
		return nil, internalError("a module path holds no space")
	}
	return mf, nil
}

func stripComment(flt *faults, line string) string {
	if flt == nil {
		return ""
	}
	i := strings.Index(line, "//")
	if i < 0 {
		return line
	}
	if held := strings.HasPrefix(line[i:], "//"); !held {
		flt.fault("the comment starts at its index")
		return ""
	}
	return line[:i]
}

func unquote(flt *faults, s string) (string, error) {
	if flt == nil {
		return "", internalError("unquote needs its fault recorder")
	}
	if s == "" {
		return "", internalError("unquote needs a field")
	}
	if s[0] != '"' && s[0] != '`' {
		return s, nil
	}
	u, err := strconv.Unquote(s)
	if err != nil {
		return "", fmt.Errorf("bad quoted path %s: %w", s, err)
	}
	if len(u) >= len(s) {
		return "", internalError("unquoting removes the quotes")
	}
	return u, nil
}
