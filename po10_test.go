package merilo

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestPowerOfTen checks merilo's own non-test code against the Power of
// Ten as the spec's Principles table states it.
func TestPowerOfTen(t *testing.T) {
	for _, dir := range []string{".", filepath.Join("cmd", "merilovet")} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		for _, f := range po10(t, dir, map[string]bool{"System": true, "Totality": true}) {
			t.Error(f)
		}
	}
}

// TestPowerOfTenFixture shows the self-check fires on each rule.
func TestPowerOfTenFixture(t *testing.T) {
	got := strings.Join(po10(t, filepath.Join("testdata", "po10"), nil), "\n")
	for _, rule := range []string{"Rule 1", "Rule 2", "Rule 4", "Rule 5", "Rule 6", "Rule 7", "Rule 8", "Rule 9"} {
		if !strings.Contains(got, rule+":") {
			t.Errorf("fixture produced no %s finding; findings:\n%s", rule, got)
		}
	}
}

type checker struct {
	fset     *token.FileSet
	info     *types.Info
	pkg      *types.Package
	allowed  map[string]bool // interface types the package may declare
	findings []string
}

func po10(t *testing.T, dir string, allowed map[string]bool) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatalf("%s: %v", dir, err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("%s: want one package, got %d", dir, len(pkgs))
	}
	var files []*ast.File
	for _, p := range pkgs {
		for _, f := range p.Files {
			files = append(files, f)
		}
	}
	sort.Slice(files, func(i, j int) bool { return fset.File(files[i].Pos()).Name() < fset.File(files[j].Pos()).Name() })
	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{},
		Uses: map[*ast.Ident]types.Object{}, Instances: map[*ast.Ident]types.Instance{},
	}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check(dir, fset, files, info)
	if err != nil {
		t.Fatalf("%s: type-check: %v", dir, err)
	}
	c := &checker{fset: fset, info: info, pkg: pkg, allowed: allowed}
	c.run(files)
	sort.Strings(c.findings)
	return c.findings
}

func (c *checker) report(pos token.Pos, rule int, format string, args ...any) {
	p := c.fset.Position(pos)
	c.findings = append(c.findings, fmt.Sprintf("%s:%d: Rule %d: %s", filepath.Base(p.Filename), p.Line, rule, fmt.Sprintf(format, args...)))
}

func (c *checker) run(files []*ast.File) {
	funcs, asserts := 0, 0
	calls := map[*types.Func][]*types.Func{}
	for _, f := range files {
		c.comments(f)
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				c.genDecl(d)
			case *ast.FuncDecl:
				funcs++
				asserts += c.funcDecl(d, calls)
			}
		}
		c.body(f)
	}
	if funcs > 0 && asserts < 2*funcs {
		c.report(files[0].Package, 5, "%d assertions across %d functions; the average must be at least 2", asserts, funcs)
	}
	c.recursion(calls)
	for id, inst := range c.info.Instances {
		c.report(id.Pos(), 9, "generic instantiation of %s (%s)", id.Name, inst.Type)
	}
}

func (c *checker) comments(f *ast.File) {
	for _, g := range f.Comments {
		for _, cm := range g.List {
			if strings.HasPrefix(cm.Text, "//go:build") || strings.HasPrefix(cm.Text, "//go:generate") {
				c.report(cm.Pos(), 8, "%s", cm.Text)
			}
		}
	}
}

func (c *checker) genDecl(d *ast.GenDecl) {
	if d.Tok == token.VAR {
		c.report(d.Pos(), 6, "package-level variable")
	}
	for _, s := range d.Specs {
		ts, ok := s.(*ast.TypeSpec)
		if !ok {
			continue
		}
		if ts.TypeParams != nil {
			c.report(ts.Pos(), 9, "type parameters on %s", ts.Name.Name)
		}
		if _, ok := ts.Type.(*ast.InterfaceType); ok && !c.allowed[ts.Name.Name] {
			c.report(ts.Pos(), 9, "interface %s outside the permitted boundaries", ts.Name.Name)
		}
	}
}

func (c *checker) funcDecl(d *ast.FuncDecl, calls map[*types.Func][]*types.Func) int {
	start, end := c.fset.Position(d.Pos()).Line, c.fset.Position(d.End()).Line
	if n := end - start + 1; n > 60 {
		c.report(d.Pos(), 4, "%s is %d lines", d.Name.Name, n)
	}
	if d.Type.TypeParams != nil {
		c.report(d.Pos(), 9, "type parameters on %s", d.Name.Name)
	}
	self, _ := c.info.Defs[d.Name].(*types.Func)
	asserts := 0
	if d.Body == nil {
		return 0
	}
	ast.Inspect(d.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "assert" {
				asserts++
			}
			if fn := c.callee(n); fn != nil && self != nil {
				calls[self] = append(calls[self], fn)
			}
		case *ast.IfStmt:
			if len(n.Body.List) == 1 && isPanic(n.Body.List[0]) {
				asserts++
			}
		}
		return true
	})
	return asserts
}

func isPanic(s ast.Stmt) bool {
	es, ok := s.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := call.Fun.(*ast.Ident)
	return ok && id.Name == "panic"
}

// callee resolves a call to a function or method of this package.
func (c *checker) callee(call *ast.CallExpr) *types.Func {
	var id *ast.Ident
	switch f := call.Fun.(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	default:
		return nil
	}
	fn, ok := c.info.Uses[id].(*types.Func)
	if !ok || fn.Pkg() != c.pkg {
		return nil
	}
	return fn
}

// body checks control flow, loops, closures, function values, pointers and
// dropped errors in one walk of a file.
func (c *checker) body(f *ast.File) {
	parents := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		if len(stack) > 0 {
			parents[n] = stack[len(stack)-1]
		}
		stack = append(stack, n)
		c.node(n, parents)
		return true
	})
	for id, obj := range c.info.Defs {
		if obj == nil || c.fset.File(id.Pos()) != c.fset.File(f.Pos()) {
			continue
		}
		if _, isFunc := obj.(*types.Func); isFunc {
			continue
		}
		if _, ok := obj.Type().Underlying().(*types.Signature); ok {
			c.report(id.Pos(), 9, "%s has a function type", id.Name)
		}
		if p, ok := obj.Type().Underlying().(*types.Pointer); ok {
			if _, ok := p.Elem().Underlying().(*types.Pointer); ok {
				c.report(id.Pos(), 9, "%s is a pointer to a pointer", id.Name)
			}
		}
	}
}

func (c *checker) node(n ast.Node, parents map[ast.Node]ast.Node) {
	switch n := n.(type) {
	case *ast.BranchStmt:
		if n.Tok == token.GOTO {
			c.report(n.Pos(), 1, "goto")
		}
	case *ast.ForStmt:
		if n.Cond == nil {
			c.report(n.Pos(), 2, "loop with no condition")
		} else if n.Init == nil && n.Post == nil {
			c.report(n.Pos(), 2, "condition-only loop")
		}
	case *ast.FuncLit:
		if !c.fuzzArg(n, parents) {
			c.report(n.Pos(), 9, "function literal")
		}
	case *ast.Ident:
		if fn, ok := c.info.Uses[n].(*types.Func); ok && !c.called(n, parents) && fn.Name() != "_" {
			c.report(n.Pos(), 9, "function value %s", n.Name)
		}
	case *ast.ExprStmt:
		if call, ok := n.X.(*ast.CallExpr); ok && c.returnsError(call) {
			c.report(n.Pos(), 7, "error result dropped")
		}
	case *ast.AssignStmt:
		c.blankErrors(n)
	}
}

// fuzzArg reports whether lit is the argument of (*testing.F).Fuzz.
func (c *checker) fuzzArg(lit *ast.FuncLit, parents map[ast.Node]ast.Node) bool {
	call, ok := parents[lit].(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Fuzz" {
		return false
	}
	fn, ok := c.info.Uses[sel.Sel].(*types.Func)
	return ok && fn.Pkg() != nil && fn.Pkg().Path() == "testing"
}

// called reports whether the function named by id is being called, or is
// only declared, rather than used as a value.
func (c *checker) called(id *ast.Ident, parents map[ast.Node]ast.Node) bool {
	var e ast.Node = id
	if sel, ok := parents[id].(*ast.SelectorExpr); ok && sel.Sel == id {
		e = sel
	}
	call, ok := parents[e].(*ast.CallExpr)
	return ok && call.Fun == e
}

func (c *checker) returnsError(call *ast.CallExpr) bool {
	tv, ok := c.info.Types[call]
	if !ok || tv.Type == nil {
		return false
	}
	errType := types.Universe.Lookup("error").Type()
	if tup, ok := tv.Type.(*types.Tuple); ok {
		for i := 0; i < tup.Len(); i++ {
			if types.Identical(tup.At(i).Type(), errType) {
				return true
			}
		}
		return false
	}
	return types.Identical(tv.Type, errType)
}

func (c *checker) blankErrors(a *ast.AssignStmt) {
	errType := types.Universe.Lookup("error").Type()
	for i, lhs := range a.Lhs {
		id, ok := lhs.(*ast.Ident)
		if !ok || id.Name != "_" {
			continue
		}
		var t types.Type
		if len(a.Rhs) == len(a.Lhs) {
			t = c.info.Types[a.Rhs[i]].Type
		} else if tup, ok := c.info.Types[a.Rhs[0]].Type.(*types.Tuple); ok && i < tup.Len() {
			t = tup.At(i).Type()
		}
		if t != nil && types.Identical(t, errType) {
			c.report(id.Pos(), 7, "error assigned to _")
		}
	}
}

func (c *checker) recursion(calls map[*types.Func][]*types.Func) {
	const (
		white = iota
		grey
		black
	)
	color := map[*types.Func]int{}
	var visit func(fn *types.Func)
	visit = func(fn *types.Func) {
		color[fn] = grey
		for _, g := range calls[fn] {
			switch color[g] {
			case grey:
				c.report(g.Pos(), 1, "recursion through %s", g.Name())
			case white:
				visit(g)
			}
		}
		color[fn] = black
	}
	fns := make([]*types.Func, 0, len(calls))
	for fn := range calls {
		fns = append(fns, fn)
	}
	sort.Slice(fns, func(i, j int) bool { return fns[i].Pos() < fns[j].Pos() })
	for _, fn := range fns {
		if color[fn] == white {
			visit(fn)
		}
	}
}
