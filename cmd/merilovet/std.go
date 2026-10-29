package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
)

// unsafePath is the one standard package with no source to check:
// go/types provides it.
const unsafePath = "unsafe"

// stdImport is one import of a standard-library package: the path as
// written and the directory of the package that wrote it, which go/build
// needs to find the standard library's own vendored packages.
type stdImport struct {
	path string
	from string
}

// loadStd finds every standard-library package the module's packages and
// their tests import, and every package those import. Each lookup starts
// from a directory inside GOROOT/src, so go/build resolves it in-process
// and never starts the go command.
func (l *loader) loadStd() error {
	if l == nil {
		return internalError("loadStd needs its receiver")
	}
	if l.ctx.GOROOT == "" {
		return errors.New("GOROOT is not set; merilovet type-checks the standard library from its source")
	}
	src := filepath.Join(l.ctx.GOROOT, "src")
	var stack []stdImport
	for _, list := range [2][]*node{l.queue, l.tested} {
		for _, n := range list {
			for _, p := range n.std {
				stack = append(stack, stdImport{path: p, from: src})
			}
		}
	}
	for i := 0; i < maxListed && len(stack) > 0; i++ {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		next, err := l.findStd(top)
		if err != nil {
			return err
		}
		stack = append(stack, next...)
	}
	if len(stack) > 0 {
		return fmt.Errorf("the standard library imports more than %d packages", maxListed)
	}
	return nil
}

// findStd looks up one standard import and, the first time its package is
// seen, records it and returns the imports it makes.
func (l *loader) findStd(imp stdImport) ([]stdImport, error) {
	if l == nil {
		return nil, internalError("findStd needs its receiver")
	}
	if imp.path == "" || imp.from == "" {
		return nil, internalError("findStd needs a path and its importer's directory")
	}
	key := [2]string{imp.from, imp.path}
	if _, done := l.vendored[key]; done || imp.path == unsafePath {
		return nil, nil
	}
	bp, err := l.ctx.Import(imp.path, imp.from, 0)
	if err != nil {
		return nil, fmt.Errorf("finding %s in GOROOT: %w", imp.path, err)
	}
	l.vendored[key] = bp.ImportPath
	if _, seen := l.std[bp.ImportPath]; seen {
		return nil, nil
	}
	n := &node{path: bp.ImportPath, dir: bp.Dir}
	var next []stdImport
	for _, f := range bp.GoFiles {
		n.files = append(n.files, filepath.Join(bp.Dir, f))
	}
	for _, p := range bp.Imports {
		n.std = append(n.std, p)
		next = append(next, stdImport{path: p, from: bp.Dir})
	}
	l.std[bp.ImportPath] = n
	l.stdQueue = append(l.stdQueue, n)
	return next, nil
}

// orderStd returns the standard-library packages with each after every
// package it imports.
func (l *loader) orderStd() ([]*node, error) {
	if l == nil {
		return nil, internalError("orderStd needs its receiver")
	}
	waiting := map[*node]int{}
	users := map[*node][]*node{}
	for _, n := range l.stdQueue {
		for _, p := range n.std {
			if p == unsafePath {
				continue
			}
			dep := l.std[l.vendored[[2]string{n.dir, p}]]
			if dep == nil {
				return nil, internalError("every standard import was found")
			}
			if waiting[n] >= maxListed {
				return nil, internalError("a package imports more than maxListed others")
			}
			waiting[n]++
			users[dep] = append(users[dep], n)
		}
	}
	return kahn(l.stdQueue, waiting, users)
}

// kahn orders queue so each node follows the nodes it waits on: waiting
// counts each node's dependencies, and users lists who depends on each.
func kahn(queue []*node, waiting map[*node]int, users map[*node][]*node) ([]*node, error) {
	if waiting == nil || users == nil {
		return nil, internalError("kahn needs the dependency counts and users")
	}
	var ready, out []*node
	for _, n := range queue {
		if waiting[n] == 0 {
			ready = append(ready, n)
		}
	}
	for i := 0; i < maxListed && i < len(ready); i++ {
		out = append(out, ready[i])
		for _, u := range users[ready[i]] {
			if waiting[u] <= 0 {
				return nil, internalError("a package is released more often than it imports")
			}
			if waiting[u]--; waiting[u] == 0 {
				ready = append(ready, u)
			}
		}
	}
	if len(out) != len(queue) {
		return nil, fmt.Errorf("import cycle among %d packages", len(queue)-len(out))
	}
	return out, nil
}

// checkStd type-checks the standard library, declarations only, in
// dependency order, and makes each package importable.
func checkStd(flt *faults, fset *token.FileSet, imp *moduleImporter, ld *loader, sizes types.Sizes) error {
	if flt == nil {
		return internalError("checkStd needs its fault recorder")
	}
	if fset == nil || imp == nil || ld == nil || sizes == nil {
		return internalError("checkStd needs a file set, the importer, the loader and the target's sizes")
	}
	order, err := ld.orderStd()
	if err != nil {
		return err
	}
	for _, n := range order {
		files, err := parseStd(fset, n)
		if err != nil {
			return err
		}
		// Function bodies are skipped, so an import used only in one is
		// reported as unused: a soft error, which the sink ignores.
		sink := &errorSink{flt: flt}
		conf := types.Config{Importer: imp, Sizes: sizes, IgnoreFuncBodies: true}
		conf.Error = sink.record
		tp, err := conf.Check(n.path, fset, files, nil)
		if err != nil && sink.hard != nil {
			return fmt.Errorf("type-checking %s from GOROOT: %w", n.path, sink.hard)
		}
		if tp == nil {
			return internalError("go/types returns a package even after errors")
		}
		imp.checked[n.path] = tp
	}
	return nil
}

// parseStd parses one standard-library package's files for the target.
func parseStd(fset *token.FileSet, n *node) ([]*ast.File, error) {
	if fset == nil || n == nil {
		return nil, internalError("parseStd needs a file set and a package")
	}
	files := make([]*ast.File, 0, len(n.files))
	for _, path := range n.files {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		files = append(files, f)
	}
	return files, nil
}
