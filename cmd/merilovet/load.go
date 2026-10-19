package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// maxListed bounds the go list output; no module has this many packages.
const maxListed = 1 << 20

type listModule struct {
	Path string
	Main bool
}

type listError struct {
	Err string
}

type listPackage struct {
	ImportPath string
	Dir        string
	Standard   bool
	GoFiles    []string
	Module     *listModule
	Error      *listError
}

// pkg is one type-checked package of the main module.
type pkg struct {
	path  string
	files []*ast.File
	flat  [][]entry
	types *types.Package
	info  *types.Info
}

type program struct {
	dir  string
	fset *token.FileSet
	pkgs []*pkg
}

// moduleImporter returns packages merilovet already checked, so objects are
// shared across packages, and falls back to the source importer otherwise.
type moduleImporter struct {
	checked map[string]*types.Package
	source  types.ImporterFrom
}

func (im *moduleImporter) Import(path string) (*types.Package, error) {
	assert(im != nil, "importer must exist")
	assert(path != "", "import path must not be empty")
	return im.ImportFrom(path, "", 0)
}

func (im *moduleImporter) ImportFrom(path, dir string, mode types.ImportMode) (*types.Package, error) {
	assert(im != nil, "importer must exist")
	assert(path != "", "import path must not be empty")
	if p, ok := im.checked[path]; ok {
		return p, nil
	}
	return im.source.ImportFrom(path, dir, mode)
}

// goList runs go list -deps -json ./... in dir; dependencies come first.
func goList(dir string) ([]listPackage, error) {
	assert(dir != "", "goList needs a directory")
	cmd := exec.Command("go", "list", "-deps", "-json", "./...")
	cmd.Dir = dir
	assert(cmd.Dir != "", "go list must run inside the module")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %v: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	var listed []listPackage
	for i := 0; i < maxListed; i++ {
		var p listPackage
		err := dec.Decode(&p)
		if errors.Is(err, io.EOF) {
			return listed, nil
		}
		if err != nil {
			return nil, fmt.Errorf("go list output: %v", err)
		}
		listed = append(listed, p)
	}
	panic("merilovet: go list printed more than maxListed packages")
}

// load lists, parses and type-checks every non-test package of the main
// module rooted at dir, in dependency order.
func load(dir string) (*program, error) {
	assert(dir != "", "load needs a directory")
	listed, err := goList(dir)
	if err != nil {
		return nil, err
	}
	prog := &program{dir: dir, fset: token.NewFileSet()}
	// The source importer resolves imports through build.Default, whose go
	// command runs in Dir; point it at the module being checked.
	build.Default.CgoEnabled = false
	build.Default.Dir = dir
	src, ok := importer.ForCompiler(prog.fset, "source", nil).(types.ImporterFrom)
	assert(ok, "the source importer must support ImportFrom")
	imp := &moduleImporter{checked: map[string]*types.Package{}, source: src}
	for i := range listed {
		lp := &listed[i]
		if lp.Standard || lp.Module == nil || !lp.Module.Main {
			continue
		}
		if lp.Error != nil {
			return nil, fmt.Errorf("%s: %s", lp.ImportPath, lp.Error.Err)
		}
		p, err := parsePackage(prog.fset, lp)
		if err != nil {
			return nil, err
		}
		if err := typeCheck(prog.fset, imp, p); err != nil {
			return nil, err
		}
		imp.checked[p.path] = p.types
		prog.pkgs = append(prog.pkgs, p)
	}
	assert(len(prog.pkgs) <= len(listed), "kept more packages than go list printed")
	return prog, nil
}

// parsePackage parses the package's non-test Go files and flattens each.
func parsePackage(fset *token.FileSet, lp *listPackage) (*pkg, error) {
	assert(fset != nil, "parsePackage needs a file set")
	assert(lp != nil && lp.ImportPath != "", "parsePackage needs a listed package")
	p := &pkg{path: lp.ImportPath}
	for _, name := range lp.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(lp.Dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		p.files = append(p.files, f)
		p.flat = append(p.flat, flatten(f))
	}
	assert(len(p.files) == len(p.flat), "every file must be flattened")
	return p, nil
}

// typeCheck checks p with go/types and records the facts the rules need.
func typeCheck(fset *token.FileSet, imp *moduleImporter, p *pkg) error {
	assert(fset != nil, "typeCheck needs a file set")
	assert(imp != nil && p != nil, "typeCheck needs an importer and a package")
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	conf := types.Config{Importer: imp}
	tp, err := conf.Check(p.path, fset, p.files, info)
	if err != nil {
		return fmt.Errorf("type-checking %s: %v", p.path, err)
	}
	p.types = tp
	p.info = info
	return nil
}
