package main

import (
	"errors"
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"strings"
)

// node is one package of the main module, the code merilovet checks.
type node struct {
	path    string
	dir     string
	files   []string // absolute paths, chosen by build constraints for the target
	tests   []string // _test.go files of the package itself
	xtests  []string // _test.go files of its external test package
	std     []string // standard-library imports, tests' included, as written
	imports []string // imports of other main-module packages
	opaque  bool     // imports something outside the module and the standard library
}

// loader finds the main module's packages without running the go command
// and without looking at dependencies: it walks the module's directories.
type loader struct {
	flt    *faults
	module string // mod.module
	root   string
	mod    *modFile
	ctx    build.Context
	byPath map[string]*node
	queue  []*node
	tested []*node // packages with test files, test-only ones included (VET-17)
	// The standard library, which merilovet type-checks itself from
	// GOROOT/src for the loader's target.
	std      map[string]*node     // by canonical import path
	stdQueue []*node              // in the order found
	vendored map[[2]string]string // importing directory and path as written, to canonical path
}

func newLoader(flt *faults, root string, mod *modFile, tags []string) *loader {
	if flt == nil {
		return nil
	}
	if root == "" || mod == nil {
		flt.fault("newLoader needs a module")
		return nil
	}
	ctx := build.Default
	ctx.CgoEnabled = false
	ctx.BuildTags = tags
	// go/build reads GOOS and GOARCH once, when the process starts; read
	// them again so each run checks the target its environment names.
	if v := os.Getenv("GOOS"); v != "" {
		ctx.GOOS = v
	}
	if v := os.Getenv("GOARCH"); v != "" {
		ctx.GOARCH = v
	}
	if ctx.GOOS == "" || ctx.GOARCH == "" {
		flt.fault("go/build knows the target")
		return nil
	}
	return &loader{flt: flt, module: mod.module, root: root, mod: mod, ctx: ctx, byPath: map[string]*node{},
		std: map[string]*node{}, vendored: map[[2]string]string{}}
}

// inMain reports whether path lies in the main module.
func (l *loader) inMain(path string) bool {
	if l == nil {
		return false
	}
	if l.module == "" {
		l.flt.fault("the main module has a path")
		return false
	}
	if path == "" {
		l.flt.fault("inMain needs a path")
		return false
	}
	return path == l.module || strings.HasPrefix(path, l.module+"/")
}

// isStd reports whether path is a standard-library package: outside the
// main module and present under GOROOT/src. A module path need not hold a
// dot, so the directory decides, not the spelling.
func (l *loader) isStd(path string) bool {
	if l == nil {
		return false
	}
	if path == "" {
		l.flt.fault("isStd needs a path")
		return false
	}
	if path == "C" || l.inMain(path) || l.ctx.GOROOT == "" {
		return false
	}
	first, _, _ := strings.Cut(path, "/")
	if first == "" {
		l.flt.fault("an import path has a first element")
		return false
	}
	return !strings.Contains(first, ".") && isDir(l.flt, filepath.Join(l.ctx.GOROOT, "src", filepath.FromSlash(path)))
}

// discover walks the main module, adding a node per package directory.
// It skips testdata, vendor, hidden and underscore directories, and
// directories that are modules of their own.
func (l *loader) discover() error {
	if l == nil {
		return internalError("discover needs its receiver")
	}
	stack := []string{l.root}
	for i := 0; i < maxListed && len(stack) > 0; i++ {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		subs, err := l.subdirs(dir)
		if err != nil {
			return err
		}
		stack = append(stack, subs...)
		if err := l.addDir(dir); err != nil {
			return err
		}
	}
	if len(stack) > 0 {
		return fmt.Errorf("module has more than %d directories", maxListed)
	}
	if len(l.queue) != len(l.byPath) {
		return internalError("every package is queued once")
	}
	return nil
}

// subdirs lists dir's subdirectories that belong to the main module.
func (l *loader) subdirs(dir string) ([]string, error) {
	if l == nil {
		return nil, internalError("subdirs needs its receiver")
	}
	if dir == "" {
		return nil, internalError("subdirs needs a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		sub := filepath.Join(dir, name)
		if _, err := os.Stat(filepath.Join(sub, "go.mod")); err == nil {
			continue
		}
		out = append(out, sub)
	}
	if len(out) > len(entries) {
		return nil, internalError("more subdirectories than entries")
	}
	return out, nil
}

// addDir records the package in dir, if it holds Go files for the target.
func (l *loader) addDir(dir string) error {
	if l == nil {
		return internalError("addDir needs its receiver")
	}
	if held := strings.HasPrefix(dir, l.root); !held {
		return internalError("a main package lies in the module")
	}
	rel, err := filepath.Rel(l.root, dir)
	if err != nil {
		return fmt.Errorf("locating %s: %w", dir, err)
	}
	path := l.module
	if rel != "." {
		path += "/" + filepath.ToSlash(rel)
	}
	bp, err := l.ctx.ImportDir(dir, 0)
	var none *build.NoGoError
	testOnly := errors.As(err, &none)
	if err != nil && !testOnly {
		return fmt.Errorf("%s: %w", path, err)
	}
	if bp == nil {
		return internalError("go/build returns a package with its error")
	}
	n := l.newNode(path, bp)
	if len(n.tests)+len(n.xtests) > 0 {
		l.tested = append(l.tested, n)
	}
	if testOnly || len(n.files) == 0 {
		return nil
	}
	for _, imp := range bp.Imports {
		switch {
		case l.inMain(imp):
			n.imports = append(n.imports, imp)
		case imp == fsmPath:
			// carried by merilovet, so not opaque
		case !l.isStd(imp):
			n.opaque = true
		}
	}
	l.byPath[path] = n
	l.queue = append(l.queue, n)
	if len(n.files) == 0 {
		return internalError("a package has files")
	}
	return nil
}

// newNode records the files of the package go/build found at path, and
// its standard-library imports, its tests' included.
func (l *loader) newNode(path string, bp *build.Package) *node {
	if l == nil {
		return nil
	}
	if bp == nil {
		l.flt.fault("newNode needs a package")
		return &node{path: path}
	}
	n := &node{path: path, dir: bp.Dir}
	for _, f := range bp.GoFiles {
		n.files = append(n.files, filepath.Join(bp.Dir, f))
	}
	for _, f := range bp.TestGoFiles {
		n.tests = append(n.tests, filepath.Join(bp.Dir, f))
	}
	for _, f := range bp.XTestGoFiles {
		n.xtests = append(n.xtests, filepath.Join(bp.Dir, f))
	}
	for _, list := range [3][]string{bp.Imports, bp.TestImports, bp.XTestImports} {
		for _, imp := range list {
			if std := l.isStd(imp); std {
				n.std = append(n.std, imp)
			}
		}
	}
	return n
}

func isDir(flt *faults, path string) bool {
	if flt == nil {
		return false
	}
	if path == "" {
		flt.fault("isDir needs a path")
		return false
	}
	fi, err := os.Stat(path)
	if err == nil && fi == nil {
		flt.fault("a successful stat has file info")
		return false
	}
	return err == nil && fi.IsDir()
}

// ordered returns the packages with every package after the main-module
// packages it imports, so type-checking never recurses into an import.
func (l *loader) ordered() ([]*node, error) {
	if l == nil {
		return nil, internalError("ordered needs its receiver")
	}
	waiting := map[*node]int{}
	users := map[*node][]*node{}
	for _, n := range l.queue {
		for _, imp := range n.imports {
			dep, ok := l.byPath[imp]
			if !ok {
				return nil, fmt.Errorf("%s imports %s, which has no Go files for %s/%s", n.path, imp, l.ctx.GOOS, l.ctx.GOARCH)
			}
			users[dep] = append(users[dep], n)
		}
		waiting[n] = len(n.imports)
	}
	out, err := kahn(l.queue, waiting, users)
	if err != nil {
		return nil, err
	}
	if len(waiting) != len(out) {
		return nil, internalError("every package was counted")
	}
	return out, nil
}
