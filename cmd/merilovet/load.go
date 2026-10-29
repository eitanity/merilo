package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
)

// maxListed bounds the packages and directories merilovet loads; no
// module has this many.
const maxListed = 1 << 20

// maxSourceBytes bounds each source file merilovet parses. A file this size
// holds far fewer than maxNodes syntax nodes, so every later walk stays
// within its bound.
const maxSourceBytes = 8 << 20

// pkg is one type-checked package of the main module.
type pkg struct {
	path  string
	files []*ast.File
	flat  [][]entry
	types *types.Package
	info  *types.Info
}

// target is what type-checking needs to know of the build target.
type target struct {
	goVersion string      // the module's language version, as go1.N
	sizes     types.Sizes // the target architecture's sizes
}

type program struct {
	dir   string
	fset  *token.FileSet
	pkgs  []*pkg
	tests []*pkg         // test packages, checked only for VET-17
	fsm   *types.Package // the carried fsm, for a module that imports it from outside
}

// moduleImporter returns the packages merilovet already checked, module
// and standard library alike, so objects are shared across packages, and
// for anything else an empty, opaque package: merilovet checks the
// module's code, not its dependencies.
type moduleImporter struct {
	flt      *faults
	checked  map[string]*types.Package
	opaque   map[string]*types.Package
	vendored map[[2]string]string // the loader's: importing directory and path to canonical path
	ld       *loader              // decides which paths are standard
}

func (im *moduleImporter) Import(path string) (*types.Package, error) {
	if im == nil {
		return nil, internalError("Import needs its receiver")
	}
	if path == "" {
		return nil, internalError("import path must not be empty")
	}
	return im.ImportFrom(path, "", 0)
}

// ImportFrom resolves path as imported from dir, which go/types passes so
// a standard package's own vendored imports find the vendored copy.
func (im *moduleImporter) ImportFrom(path, dir string, _ types.ImportMode) (*types.Package, error) {
	if im == nil {
		return nil, internalError("ImportFrom needs its receiver")
	}
	if path == "" {
		return nil, internalError("import path must not be empty")
	}
	if path == unsafePath {
		return types.Unsafe, nil
	}
	if canonical, ok := im.vendored[[2]string{dir, path}]; ok {
		path = canonical
	}
	if p, ok := im.checked[path]; ok {
		return p, nil
	}
	if in := im.ld.inMain(path); in {
		return nil, fmt.Errorf("importing %s: not loaded; merilovet checks every package after the module packages it imports", path)
	}
	if std := im.ld.isStd(path); std {
		return nil, fmt.Errorf("importing %s: a standard package merilovet did not load", path)
	}
	return im.opaquePackage(path), nil
}

// load parses and type-checks every non-test package of the main module
// rooted at dir, and the packages they import, in dependency order, each
// built with the given build tags. It runs
// no other program: the module's files come from walking dir, a
// dependency's from vendor/, a local replace or the module cache, and the
// standard library's from GOROOT through the source importer.
func load(flt *faults, dir string, tags []string) (*program, error) {
	if flt == nil {
		return nil, internalError("load needs its fault recorder")
	}
	if dir == "" {
		return nil, internalError("load needs a directory")
	}
	mod, err := readModFile(flt, dir)
	if err != nil {
		return nil, err
	}
	ld := newLoader(flt, dir, mod, tags)
	if err := ld.discover(); err != nil {
		return nil, err
	}
	if err := ld.loadStd(); err != nil {
		return nil, err
	}
	nodes, err := ld.ordered()
	if err != nil {
		return nil, err
	}
	if len(nodes) != len(ld.byPath) {
		return nil, internalError("every loaded package is ordered")
	}
	return checkTarget(flt, dir, mod, ld, nodes)
}

// checkTarget parses and type-checks the ordered packages for the loader's
// target, keeping the main module's for the rules.
func checkTarget(flt *faults, dir string, mod *modFile, ld *loader, nodes []*node) (*program, error) {
	if flt == nil {
		return nil, internalError("checkTarget needs its fault recorder")
	}
	if ld == nil {
		return nil, internalError("checkTarget needs the loader")
	}
	if mod == nil {
		return nil, internalError("checkTarget needs the module")
	}
	prog := &program{dir: dir, fset: token.NewFileSet()}
	sizes := types.SizesFor("gc", ld.ctx.GOARCH)
	if sizes == nil {
		return nil, fmt.Errorf("no type sizes for GOARCH %s", ld.ctx.GOARCH)
	}
	imp := &moduleImporter{flt: flt, checked: map[string]*types.Package{}, opaque: map[string]*types.Package{},
		vendored: ld.vendored, ld: ld}
	if err := checkStd(flt, prog.fset, imp, ld, sizes); err != nil {
		return nil, err
	}
	if outside := !ld.inMain(fsmPath); outside {
		fp, err := checkFSM(prog.fset, imp)
		if err != nil {
			return nil, err
		}
		prog.fsm = fp
	}
	tc := target{goVersion: "", sizes: sizes}
	if mod.goVersion != "" {
		tc.goVersion = "go" + mod.goVersion
	}
	for _, n := range nodes {
		p, err := parsePackage(flt, prog.fset, n)
		if err != nil {
			return nil, err
		}
		if err := typeCheck(flt, prog.fset, imp, p, tc, n.opaque); err != nil {
			return nil, err
		}
		imp.checked[p.path] = p.types
		prog.pkgs = append(prog.pkgs, p)
	}
	if len(prog.pkgs) > len(nodes) {
		return nil, internalError("kept more packages than were loaded")
	}
	if err := checkTests(flt, prog, imp, ld, tc); err != nil {
		return nil, err
	}
	return prog, nil
}

// checkTests type-checks each package's test files, internal and external,
// after every package they import. They are checked leniently: they import
// the merilo harness, which lies outside an adopter's module, and only
// VET-17 reads them.
func checkTests(flt *faults, prog *program, imp *moduleImporter, ld *loader, tc target) error {
	if flt == nil {
		return internalError("checkTests needs its fault recorder")
	}
	if prog == nil || imp == nil || ld == nil {
		return internalError("checkTests needs the program, its importer and the loader")
	}
	for _, n := range ld.tested {
		for _, t := range [2]node{
			{path: n.path, dir: n.dir, files: append(append([]string(nil), n.files...), n.tests...)},
			{path: n.path + "_test", dir: n.dir, files: n.xtests},
		} {
			if len(t.files) == len(n.files) && t.path == n.path || len(t.files) == 0 {
				continue
			}
			p, err := parsePackage(flt, prog.fset, &t)
			if err != nil {
				return err
			}
			if err := typeCheck(flt, prog.fset, imp, p, tc, true); err != nil {
				return err
			}
			prog.tests = append(prog.tests, p)
		}
	}
	return nil
}

// parsePackage parses the package's non-test Go files and flattens each.
func parsePackage(flt *faults, fset *token.FileSet, n *node) (*pkg, error) {
	if flt == nil {
		return nil, internalError("parsePackage needs its fault recorder")
	}
	if fset == nil {
		return nil, internalError("parsePackage needs a file set")
	}
	if n == nil || n.path == "" {
		return nil, internalError("parsePackage needs a package")
	}
	p := &pkg{path: n.path}
	for _, path := range n.files {
		fi, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if size := fi.Size(); size > maxSourceBytes {
			return nil, fmt.Errorf("%s: larger than %d bytes", path, maxSourceBytes)
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", filepath.Base(path), err)
		}
		p.files = append(p.files, f)
		p.flat = append(p.flat, flatten(flt, f))
	}
	if len(p.files) != len(p.flat) {
		return nil, internalError("every file must be flattened")
	}
	return p, nil
}

// typeCheck checks p with go/types and records the facts the rules need.
// A package that imports an opaque package is checked leniently: its uses
// of that package do not type-check, and those errors are expected. The
// rules need only the module's own types, which stay complete. Every
// other package is checked strictly.
func typeCheck(flt *faults, fset *token.FileSet, imp *moduleImporter, p *pkg, tc target, lenient bool) error {
	if flt == nil {
		return internalError("typeCheck needs its fault recorder")
	}
	if fset == nil {
		return internalError("typeCheck needs a file set")
	}
	if imp == nil || p == nil {
		return internalError("typeCheck needs an importer and a package")
	}
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	conf := types.Config{Importer: imp, GoVersion: tc.goVersion, Sizes: tc.sizes}
	if lenient {
		sink := &errorSink{flt: flt}
		conf.Error = sink.record
	}
	tp, err := conf.Check(p.path, fset, p.files, info)
	if err != nil && !lenient {
		return fmt.Errorf("type-checking %s: %w", p.path, err)
	}
	if tp == nil {
		return internalError("go/types returns a package even after errors")
	}
	p.types = tp
	p.info = info
	return nil
}

// opaquePackage is an empty, complete package standing in for an import
// outside the module and the standard library.
func (im *moduleImporter) opaquePackage(path string) *types.Package {
	if im == nil {
		return nil
	}
	if path == "" {
		im.flt.fault("opaquePackage needs a path")
		return nil
	}
	if p, ok := im.opaque[path]; ok {
		return p
	}
	p := types.NewPackage(path, packageName(im.flt, path))
	p.MarkComplete()
	im.opaque[path] = p
	if held := p.Complete(); !held {
		im.flt.fault("an opaque package is complete")
		return nil
	}
	return p
}

// packageName guesses an import's package name from its path: the last
// element, or the one before a major-version suffix such as /v2.
func packageName(flt *faults, path string) string {
	if flt == nil {
		return ""
	}
	if path == "" {
		flt.fault("packageName needs a path")
		return ""
	}
	parts := strings.Split(path, "/")
	name := parts[len(parts)-1]
	if len(parts) > 1 && len(name) > 1 && name[0] == 'v' && strings.Trim(name[1:], "0123456789") == "" {
		name = parts[len(parts)-2]
	}
	name, _, _ = strings.Cut(name, ".")
	if name == "" && path != "" {
		flt.fault("a package name is not empty")
		return ""
	}
	return strings.ReplaceAll(name, "-", "_")
}

// errorSink counts the type errors of a lenient check. go/types reports
// them through a function, Config.Error, the one function value merilovet
// passes: its API offers no other way to keep checking after an error.
type errorSink struct {
	flt  *faults
	hard error // the first error that is not soft
}

func (e *errorSink) record(err error) {
	if e == nil {
		return
	}
	if err == nil {
		e.flt.fault("go/types reports only errors")
		return
	}
	var te types.Error
	if errors.As(err, &te) && te.Soft {
		return
	}
	if e.hard == nil {
		e.hard = err
	}
}
