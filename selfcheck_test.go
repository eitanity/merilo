package merilo

import (
	"fmt"
	"go/ast"
	"go/constant"
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

// TestSelfCheck checks merilo's own non-test code against its code
// safety rules. Rule 5's density is "of the code", so it averages over all
// three packages together.
func TestSelfCheck(t *testing.T) {
	var d density
	for _, dir := range []string{".", "fsm", filepath.Join("cmd", "merilovet")} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		for _, f := range selfCheck(t, dir, false, &d) {
			t.Error(f)
		}
	}
	if d.funcs == 0 || d.asserts < 2*d.funcs {
		t.Errorf("Rule 5: %d assertions in %d functions; the average must be at least two", d.asserts, d.funcs)
	}
}

// density is Rule 5's count over the code: assertions and functions.
type density struct{ asserts, funcs int }

// TestSelfCheckFixture shows the self-check fires on each rule.
func TestSelfCheckFixture(t *testing.T) {
	got := strings.Join(selfCheck(t, filepath.Join("testdata", "selfcheck"), true, nil), "\n")
	for _, want := range []string{
		"Rule 1: recursion through recurse",
		"Rule 1: call to panic",
		"Rule 1: recursion through Visit",
		"Rule 2: loop with no condition",
		"Rule 2: loop bound n changes in the loop body",
		"Rule 2: range over a channel",
		"Rule 7: parameter ch of drain is not checked",
		"Rule 4: long is 64 lines",
		"Rule 5: 1 assertions in 17 functions",
		"Rule 5: unchecked has an assertion whose condition calls fail, which may have side effects",
		"Rule 6: package-level variable",
		"Rule 6: n is declared wider than its uses",
		"Rule 7: error result dropped",
		"Rule 7: result of count dropped",
		"Rule 7: parameter p of unchecked is not checked",
		"Rule 7: parameter i of index is not checked",
		"Rule 8: //go:generate",
		"Rule 9: function literal",
		"Rule 9: a.next.v dereferences 2 pointers",
		"Rule 9: named pointer type ptr",
		"Rule 9: type parameters on generic",
		"Overflow: total changes with no comparison of it before",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fixture finding %q missing; findings:\n%s", want, got)
		}
	}
	if clean := selfCheck(t, filepath.Join("testdata", "selfcheckok"), false, nil); len(clean) != 0 {
		t.Errorf("compliant fixture has findings:\n%s", strings.Join(clean, "\n"))
	}
}

type checker struct {
	decls    map[*types.Func]*ast.FuncDecl
	purity   map[*types.Func]int // 0 unknown, 1 being decided, 2 pure, 3 impure
	external map[string]bool     // external callees an assertion called, pure or not
	fset     *token.FileSet
	info     *types.Info
	pkg      *types.Package
	calls    map[*types.Func][]*types.Func
	asserts  int
	funcs    int
	findings []string
}

// selfCheck checks the package in dir. With d set, Rule 5's counts are added to
// it; without, the package's own average is checked.
func selfCheck(t *testing.T, dir string, fixture bool, d *density) []string {
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
		Selections: map[*ast.SelectorExpr]*types.Selection{}, Scopes: map[ast.Node]*types.Scope{},
	}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check(dir, fset, files, info)
	if err != nil {
		t.Fatalf("%s: type-check: %v", dir, err)
	}
	c := &checker{fset: fset, info: info, pkg: pkg, calls: map[*types.Func][]*types.Func{},
		decls: map[*types.Func]*ast.FuncDecl{}, purity: map[*types.Func]int{}, external: map[string]bool{}}
	for _, f := range files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				if fn, ok := info.Defs[fd.Name].(*types.Func); ok {
					c.decls[fn] = fd
				}
			}
		}
	}
	c.run(files)
	if d != nil {
		d.asserts += c.asserts
		d.funcs += c.funcs
	} else if c.funcs > 0 && c.asserts < 2*c.funcs {
		c.report(files[0].Package, 5, "%d assertions in %d functions; the average must be at least two", c.asserts, c.funcs)
	}
	sort.Strings(c.findings)
	return c.findings
}

// report records a finding against one of the numbered rules, or rule 0 for
// merilo's own overflow rule.
func (c *checker) report(pos token.Pos, rule int, format string, args ...any) {
	p := c.fset.Position(pos)
	name := fmt.Sprintf("Rule %d", rule)
	if rule == 0 {
		name = "Overflow"
	}
	c.findings = append(c.findings, fmt.Sprintf("%s:%d: %s: %s", filepath.Base(p.Filename), p.Line, name, fmt.Sprintf(format, args...)))
}

func (c *checker) run(files []*ast.File) {
	for _, f := range files {
		c.directives(f)
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				c.genDecl(d)
			case *ast.FuncDecl:
				c.funcDecl(d)
			}
		}
		c.body(f)
	}
	c.recursion()
	for id, inst := range c.info.Instances {
		c.report(id.Pos(), 9, "generic instantiation of %s (%s)", id.Name, inst.Type)
	}
}

// directives reports Rule 8: no compiler directive of any kind, and no cgo.
func (c *checker) directives(f *ast.File) {
	for _, g := range f.Comments {
		for _, cm := range g.List {
			if strings.HasPrefix(cm.Text, "//go:") || strings.HasPrefix(cm.Text, "//line ") {
				c.report(cm.Pos(), 8, "%s", strings.Fields(cm.Text)[0])
			}
		}
	}
	for _, imp := range f.Imports {
		if imp.Path.Value == `"C"` {
			c.report(imp.Pos(), 8, "cgo")
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
		if _, ok := ts.Type.(*ast.StarExpr); ok {
			c.report(ts.Pos(), 9, "named pointer type %s hides a dereference", ts.Name.Name)
		}
	}
}

func (c *checker) funcDecl(d *ast.FuncDecl) {
	start, end := c.fset.Position(d.Pos()).Line, c.fset.Position(d.End()).Line
	if n := end - start + 1; n > 60 {
		c.report(d.Pos(), 4, "%s is %d lines", d.Name.Name, n)
	}
	if d.Type.TypeParams != nil {
		c.report(d.Pos(), 9, "type parameters on %s", d.Name.Name)
	}
	if d.Body == nil {
		return
	}
	c.funcs++
	self, _ := c.info.Defs[d.Name].(*types.Func)
	checked := map[types.Object]bool{}
	ast.Inspect(d.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if self != nil {
				c.calls[self] = append(c.calls[self], c.callees(n)...)
			}
		case *ast.IfStmt:
			c.assertion(d, withInit(n), n.Body.List, checked)
		case *ast.SwitchStmt:
			if n.Tag == nil {
				for _, st := range n.Body.List {
					if cc, ok := st.(*ast.CaseClause); ok && len(cc.List) == 1 {
						c.assertion(d, cc.List[0], cc.Body, checked)
					}
				}
			}
		}
		return true
	})
	c.params(d, checked)
	c.locals(d)
	c.overflow(d)
}

// overflow reports an integer that grows or shrinks with no comparison of
// it earlier in the function: x++, x--, x op= y and x = x op y for +, -,
// * and <<. A loop's post statement is bounded by Rule 2. This is
// merilovet's rule for machines, applied to merilo's own code; Go wraps
// integers silently, so a value that is never compared can wrap.
func (c *checker) overflow(d *ast.FuncDecl) {
	posts := map[ast.Stmt]bool{}
	var compared []string // each operand of a comparison, with its position
	var at []token.Pos
	ast.Inspect(d.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ForStmt:
			if x.Post != nil {
				posts[x.Post] = true
			}
		case *ast.BinaryExpr:
			if comparison(x.Op) {
				for _, e := range []ast.Expr{x.X, x.Y} {
					compared = append(compared, types.ExprString(ast.Unparen(e)))
					at = append(at, x.Pos())
				}
			}
		}
		return true
	})
	guarded := func(e ast.Expr, before token.Pos) bool {
		want := types.ExprString(ast.Unparen(e))
		for i, s := range compared {
			if s == want && at[i] < before {
				return true
			}
		}
		return false
	}
	isInt := func(e ast.Expr) bool {
		b, ok := c.info.TypeOf(e).Underlying().(*types.Basic)
		return ok && b.Info()&types.IsInteger != 0
	}
	ast.Inspect(d.Body, func(n ast.Node) bool {
		st, ok := n.(ast.Stmt)
		if !ok || posts[st] {
			return true
		}
		var target ast.Expr
		switch x := st.(type) {
		case *ast.IncDecStmt:
			target = x.X
		case *ast.AssignStmt:
			target = grows(x)
		}
		if target != nil && isInt(target) && !guarded(target, st.Pos()) {
			c.report(st.Pos(), 0, "%s changes with no comparison of it before; it could wrap", types.ExprString(target))
		}
		return true
	})
}

// assertion counts cond guarding body as a Rule 5 assertion when it is one:
// a Boolean test that is not a compile-time constant, so it can both hold
// and fail, free of side effects, whose body takes an explicit recovery
// action. A nil test of a parameter whose body returns is the Rule 7
// parameter check, and its recovery is the function's refusal value.
func (c *checker) assertion(d *ast.FuncDecl, cond ast.Expr, body []ast.Stmt, checked map[types.Object]bool) {
	if tv, ok := c.info.Types[cond]; ok && tv.Value != nil {
		return
	}
	recovers, returns := false, false
	for _, st := range body {
		recovers = recovers || c.recovers(st) || c.refusesOK(d, st)
		_, ret := st.(*ast.ReturnStmt)
		returns = returns || ret
	}
	if !recovers && (!returns || !c.nilParamTest(d, cond)) {
		return
	}
	if name := c.impure(cond); name != "" {
		c.report(cond.Pos(), 5, "%s has an assertion whose condition calls %s, which may have side effects", d.Name.Name, name)
		return
	}
	c.asserts++
	ast.Inspect(cond, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			if obj := c.info.Uses[id]; obj != nil {
				checked[obj] = true
			}
		}
		return true
	})
}

// withInit returns an if statement's condition, with any expression its
// init computes folded in: moving a call into the header does not take it
// out of the assertion. An init that is just a call stays out; the
// condition then checks that call's result, as Rule 7 asks.
func withInit(n *ast.IfStmt) ast.Expr {
	as, ok := n.Init.(*ast.AssignStmt)
	if !ok || as.Tok != token.DEFINE {
		return n.Cond
	}
	e := n.Cond
	for _, r := range as.Rhs {
		if _, isCall := ast.Unparen(r).(*ast.CallExpr); isCall {
			continue // the call whose result the condition checks (Rule 7)
		}
		e = &ast.BinaryExpr{X: e, OpPos: n.Cond.Pos(), Op: token.LAND, Y: r}
	}
	return e
}

// impure names the first call in cond that may have a side effect, or
// returns "": every call must be to a builtin that only reads, a
// conversion, a function of this package proved pure, or a read-only
// function of the standard library.
func (c *checker) impure(cond ast.Expr) string {
	name := ""
	ast.Inspect(cond, func(n ast.Node) bool {
		if call, isCall := n.(*ast.CallExpr); isCall && name == "" && !c.pureCall(call) {
			name = types.ExprString(call.Fun)
			if id, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok {
				if fn, ok := c.info.Uses[id.Sel].(*types.Func); ok {
					name = fn.FullName()
				}
			}
		}
		return name == ""
	})
	return name
}

func (c *checker) pureCall(call *ast.CallExpr) bool {
	if c.info.Types[call.Fun].IsType() {
		return true
	}
	var id *ast.Ident
	switch f := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	default:
		return false
	}
	switch obj := c.info.Uses[id].(type) {
	case *types.Builtin:
		switch obj.Name() {
		case "len", "cap", "min", "max", "real", "imag", "complex":
			return true
		}
		return false
	case *types.Func:
		return c.pureFunc(obj)
	}
	return false
}

// pureFunc decides whether fn has no side effects. A function of this
// package is pure when it assigns only its own local variables, sends,
// starts and defers nothing, and calls only pure functions; Rule 1 keeps
// the call graph acyclic, so the decision terminates.
func (c *checker) pureFunc(fn *types.Func) bool {
	if fn.Pkg() != c.pkg {
		name := fn.FullName()
		c.external[name] = true
		return readOnly[name]
	}
	switch c.purity[fn] {
	case 1:
		return false
	case 2:
		return true
	case 3:
		return false
	}
	c.purity[fn] = 1
	d := c.decls[fn]
	ok := d != nil && d.Body != nil
	if ok {
		ast.Inspect(d.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignStmt:
				for _, l := range x.Lhs {
					ok = ok && c.localWrite(d, l)
				}
			case *ast.IncDecStmt:
				ok = ok && c.localWrite(d, x.X)
			case *ast.SendStmt, *ast.GoStmt, *ast.DeferStmt:
				ok = false
			case *ast.CallExpr:
				ok = ok && c.pureCall(x)
			}
			return ok
		})
	}
	c.purity[fn] = 3
	if ok {
		c.purity[fn] = 2
	}
	return ok
}

// localWrite reports whether assigning to l changes only a variable
// declared inside d's body: not a parameter, a receiver, a field or an
// element reached through one.
func (c *checker) localWrite(d *ast.FuncDecl, l ast.Expr) bool {
	id, ok := ast.Unparen(l).(*ast.Ident)
	if !ok {
		return false
	}
	if id.Name == "_" {
		return true
	}
	obj := c.info.ObjectOf(id)
	return obj != nil && obj.Pos() > d.Body.Pos() && obj.Pos() < d.Body.End()
}

// readOnly lists the standard-library functions an assertion may call:
// each only reads its arguments and receiver.
var readOnly = map[string]bool{
	"(*go/types.Basic).Info":       true,
	"(*go/types.Basic).Kind":       true,
	"(*go/types.Named).Obj":        true,
	"(*go/types.Package).Complete": true,
	"(*go/types.Scope).Len":        true,
	"(*go/types.Signature).Recv":   true,
	"(*go/types.Struct).Field":     true,
	"(go/constant.Value).Kind":     true,
	"(*go/types.object).Pkg":       true,
	"(*go/types.object).Type":      true,
	"(go/token.Pos).IsValid":       true,
	"(go/token.Token).String":      true,
	"strings.ContainsAny":          true,
	"strings.HasPrefix":            true,
	"strings.TrimSpace":            true,
}

// nilParamTest reports whether cond tests a parameter or the receiver
// against nil.
func (c *checker) nilParamTest(d *ast.FuncDecl, cond ast.Expr) bool {
	params := c.paramObjects(d)
	found := false
	ast.Inspect(cond, func(n ast.Node) bool {
		be, ok := n.(*ast.BinaryExpr)
		if !ok || be.Op != token.EQL {
			return true
		}
		for _, pair := range [2][2]ast.Expr{{be.X, be.Y}, {be.Y, be.X}} {
			id, isID := ast.Unparen(pair[0]).(*ast.Ident)
			nl, isNil := ast.Unparen(pair[1]).(*ast.Ident)
			if isID && isNil && nl.Name == "nil" && params[c.info.Uses[id]] {
				found = true
			}
		}
		return true
	})
	return found
}

func (c *checker) paramObjects(d *ast.FuncDecl) map[types.Object]bool {
	out := map[types.Object]bool{}
	for _, fl := range []*ast.FieldList{d.Recv, d.Type.Params} {
		if fl == nil {
			continue
		}
		for _, f := range fl.List {
			for _, n := range f.Names {
				if obj := c.info.Defs[n]; obj != nil {
					out[obj] = true
				}
			}
		}
	}
	return out
}

// params reports Rule 7's second half: a parameter, the receiver included,
// that can hold an invalid value is checked by an assertion. Pointers,
// maps, interfaces and channels can be nil; an integer used as an index,
// a slice bound or a make size can be out of range.
func (c *checker) params(d *ast.FuncDecl, checked map[types.Object]bool) {
	indexed := map[types.Object]bool{}
	mark := func(e ast.Expr) {
		if id, ok := ast.Unparen(e).(*ast.Ident); ok {
			if obj := c.info.Uses[id]; obj != nil {
				indexed[obj] = true
			}
		}
	}
	used := map[types.Object]bool{}
	ast.Inspect(d.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			if obj := c.info.Uses[n]; obj != nil {
				used[obj] = true
			}
		case *ast.IndexExpr:
			if _, isMap := c.info.TypeOf(n.X).Underlying().(*types.Map); !isMap {
				mark(n.Index)
			}
		case *ast.SliceExpr:
			mark(n.Low)
			mark(n.High)
			mark(n.Max)
		case *ast.CallExpr:
			if id, ok := ast.Unparen(n.Fun).(*ast.Ident); ok && id.Name == "make" {
				for _, a := range n.Args[1:] {
					mark(a)
				}
			}
		}
		return true
	})
	for obj := range c.paramObjects(d) {
		if _, isTP := obj.Type().(*types.TypeParam); isTP || obj.Name() == "_" || !used[obj] || checked[obj] {
			continue
		}
		switch u := obj.Type().Underlying().(type) {
		case *types.Pointer, *types.Map, *types.Interface, *types.Chan, *types.Signature:
			c.report(obj.Pos(), 7, "parameter %s of %s is not checked", obj.Name(), d.Name.Name)
		case *types.Basic:
			if u.Info()&types.IsInteger != 0 && indexed[obj] {
				c.report(obj.Pos(), 7, "parameter %s of %s is not checked", obj.Name(), d.Name.Name)
			}
		}
	}
}

// locals reports Rule 6 for declarations in a block: one whose variables
// are all used only inside one narrower block, not across a loop
// iteration, could be declared there.
func (c *checker) locals(d *ast.FuncDecl) {
	nodeOf := map[*types.Scope]ast.Node{}
	for n, s := range c.info.Scopes {
		nodeOf[s] = n
	}
	uses := map[types.Object][]token.Pos{}
	var decls [][]*ast.Ident
	ast.Inspect(d.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if v, ok := c.info.Uses[x].(*types.Var); ok {
				uses[v] = append(uses[v], x.Pos())
			}
		case *ast.AssignStmt:
			if x.Tok == token.DEFINE {
				var ids []*ast.Ident
				for _, l := range x.Lhs {
					if id, ok := l.(*ast.Ident); ok && c.info.Defs[id] != nil {
						ids = append(ids, id)
					}
				}
				decls = append(decls, ids)
			}
		case *ast.ValueSpec:
			decls = append(decls, x.Names)
		}
		return true
	})
	for _, ids := range decls {
		c.narrower(ids, uses, nodeOf)
	}
}

// narrower reports one declaration whose variables could all live in a
// narrower block.
func (c *checker) narrower(ids []*ast.Ident, uses map[types.Object][]token.Pos, nodeOf map[*types.Scope]ast.Node) {
	var decl, lca *types.Scope
	for _, id := range ids {
		obj := c.info.Defs[id]
		if obj == nil || id.Name == "_" {
			continue
		}
		if len(uses[obj]) == 0 {
			return
		}
		decl = obj.Parent()
		for _, p := range uses[obj] {
			if lca == nil {
				lca = decl.Innermost(p)
			} else {
				lca = commonScope(lca, decl.Innermost(p), decl)
			}
		}
	}
	if decl == nil || lca == nil || lca == decl || crossesLoop(lca, decl, nodeOf) {
		return
	}
	switch nodeOf[decl].(type) {
	case *ast.BlockStmt, *ast.FuncType, *ast.CaseClause, *ast.CommClause:
	default:
		return // declared in a statement header, already the statement's scope
	}
	switch nodeOf[lca].(type) {
	case *ast.BlockStmt, *ast.CaseClause, *ast.CommClause:
		c.report(ids[0].Pos(), 6, "%s is declared wider than its uses", ids[0].Name)
	}
}

// commonScope returns the innermost scope containing both a and b, at most
// up to top.
func commonScope(a, b, top *types.Scope) *types.Scope {
	seen := map[*types.Scope]bool{}
	for s := a; s != nil; s = s.Parent() {
		seen[s] = true
		if s == top {
			break
		}
	}
	for s := b; s != nil; s = s.Parent() {
		if seen[s] {
			return s
		}
	}
	return top
}

// crossesLoop reports whether a loop lies between inner and outer, so a
// variable moved into inner would be reset every iteration.
func crossesLoop(inner, outer *types.Scope, nodeOf map[*types.Scope]ast.Node) bool {
	for s := inner; s != nil && s != outer; s = s.Parent() {
		switch nodeOf[s].(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			return true
		}
	}
	return false
}

// recovers reports whether st is a recovery action: returning a non-nil
// error or a violation, recording a violation, reporting through
// testing.TB, or recording an internal fault.
func (c *checker) recovers(st ast.Stmt) bool {
	switch s := st.(type) {
	case *ast.ReturnStmt:
		for _, r := range s.Results {
			if c.isError(r) || c.isViolation(r) {
				return true
			}
		}
	case *ast.AssignStmt:
		for _, r := range s.Rhs {
			if c.isViolation(r) {
				return true
			}
		}
	case *ast.ExprStmt:
		call, ok := s.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		fn, ok := c.info.Uses[sel.Sel].(*types.Func)
		if !ok || fn.Pkg() == nil {
			return false
		}
		switch sel.Sel.Name {
		case "Fatalf", "Fatal", "Errorf", "Error":
			return fn.Pkg().Path() == "testing"
		case "fault":
			return fn.Pkg() == c.pkg
		}
	}
	return false
}

// refusesOK reports whether st is Go's comma-ok refusal: a return of false
// as d's last result, declared "ok bool", as fsm's Counter.Inc returns at
// its limit. That is returning an error condition to the caller.
func (c *checker) refusesOK(d *ast.FuncDecl, st ast.Stmt) bool {
	ret, ok := st.(*ast.ReturnStmt)
	if !ok || d == nil || d.Type.Results == nil || len(ret.Results) < 2 {
		return false
	}
	fields := d.Type.Results.List
	last := fields[len(fields)-1]
	if len(last.Names) == 0 || last.Names[len(last.Names)-1].Name != "ok" {
		return false
	}
	id, isIdent := ret.Results[len(ret.Results)-1].(*ast.Ident)
	return isIdent && c.info.Uses[id] == types.Universe.Lookup("false")
}

// isError reports whether e is an error value other than the nil literal.
func (c *checker) isError(e ast.Expr) bool {
	if id, ok := e.(*ast.Ident); ok && id.Name == "nil" {
		return false
	}
	t := c.info.Types[e].Type
	return t != nil && types.Identical(t, types.Universe.Lookup("error").Type())
}

// isViolation reports whether e is a violation: a composite literal of a
// type with a kind field that sets it, or a value of such a type that was
// just tested.
func (c *checker) isViolation(e ast.Expr) bool {
	named, ok := c.info.Types[e].Type.(*types.Named)
	if !ok || (named.Obj().Name() != "applied" && named.Obj().Name() != "Violation") {
		return false
	}
	lit, ok := e.(*ast.CompositeLit)
	if !ok {
		_, isIdent := e.(*ast.Ident)
		return isIdent
	}
	for _, el := range lit.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if k, ok := kv.Key.(*ast.Ident); ok && (k.Name == "kind" || k.Name == "Kind") {
				return true
			}
		}
	}
	return false
}

// callees resolves a call to the functions of this package it can reach:
// the function or method itself, or, for a call through an interface,
// that method on every type of this package that implements it. The
// compiler checks every implementation, so the call graph stays exact.
func (c *checker) callees(call *ast.CallExpr) []*types.Func {
	var id *ast.Ident
	switch f := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	default:
		return nil
	}
	fn, ok := c.info.Uses[id].(*types.Func)
	if !ok {
		return nil
	}
	sig, _ := fn.Type().(*types.Signature)
	if sig == nil || sig.Recv() == nil {
		if fn.Pkg() == c.pkg {
			return []*types.Func{fn}
		}
		return nil
	}
	iface, isIface := sig.Recv().Type().Underlying().(*types.Interface)
	if !isIface {
		if fn.Pkg() == c.pkg {
			return []*types.Func{fn}
		}
		return nil
	}
	var out []*types.Func
	scope := c.pkg.Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || types.IsInterface(tn.Type()) {
			continue
		}
		for _, t := range []types.Type{tn.Type(), types.NewPointer(tn.Type())} {
			if !types.Implements(t, iface) {
				continue
			}
			if m, ok := types.NewMethodSet(t).Lookup(c.pkg, fn.Name()).Obj().(*types.Func); ok {
				out = append(out, m)
			} else if sel := types.NewMethodSet(t).Lookup(fn.Pkg(), fn.Name()); sel != nil {
				if m, ok := sel.Obj().(*types.Func); ok {
					out = append(out, m)
				}
			}
			break
		}
	}
	return out
}

// body checks control flow, loops, closures, function values, pointers and
// dropped results in one walk of a file.
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
		if why := c.forBound(n); why != "" {
			c.report(n.Pos(), 2, "%s", why)
		}
	case *ast.RangeStmt:
		c.rangeBound(n)
	case *ast.CallExpr:
		if id, ok := n.Fun.(*ast.Ident); ok {
			if b, ok := c.info.Uses[id].(*types.Builtin); ok && (b.Name() == "panic" || b.Name() == "recover") {
				c.report(n.Pos(), 1, "call to %s, a non-local jump; detect and return an outcome instead", b.Name())
			}
		}
	case *ast.FuncLit:
		if !c.fuzzArg(n, parents) {
			c.report(n.Pos(), 9, "function literal")
		}
	case *ast.Ident:
		if fn, ok := c.info.Uses[n].(*types.Func); ok && !c.called(n, parents) && !c.configError(n, parents) && fn.Name() != "_" {
			c.report(n.Pos(), 9, "function value %s", n.Name)
		}
	case *ast.ExprStmt:
		if call, ok := n.X.(*ast.CallExpr); ok {
			c.dropped(call)
		}
	case *ast.AssignStmt:
		c.blankErrors(n)
	}
	if e, ok := n.(ast.Expr); ok && isChain(e) && !continuesChain(e, parents[n]) {
		if k := c.derefs(e); k > 1 {
			c.report(e.Pos(), 9, "%s dereferences %d pointers", types.ExprString(e), k)
		}
	}
}

// dropped reports Rule 7's first half: the result of a call is used.
func (c *checker) dropped(call *ast.CallExpr) {
	tv, ok := c.info.Types[call]
	if !ok || tv.IsVoid() || tv.Type == nil {
		return
	}
	if c.returnsError(call) {
		c.report(call.Pos(), 7, "error result dropped")
		return
	}
	c.report(call.Pos(), 7, "result of %s dropped", types.ExprString(call.Fun))
}

func isChain(e ast.Expr) bool {
	switch e.(type) {
	case *ast.SelectorExpr, *ast.StarExpr, *ast.IndexExpr, *ast.SliceExpr, *ast.ParenExpr:
		return true
	}
	return false
}

// continuesChain reports whether parent extends the chain e is the base of.
func continuesChain(e ast.Expr, parent ast.Node) bool {
	switch p := parent.(type) {
	case *ast.SelectorExpr:
		return p.X == e
	case *ast.StarExpr:
		return p.X == e
	case *ast.IndexExpr:
		return p.X == e
	case *ast.SliceExpr:
		return p.X == e
	case *ast.ParenExpr:
		return p.X == e
	}
	return false
}

// derefs counts the pointer dereferences in one selector, index and star
// chain; a call starts a new chain.
func (c *checker) derefs(e ast.Expr) int {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return c.derefs(x.X)
	case *ast.StarExpr:
		if c.info.Types[x].IsType() {
			return 0
		}
		return 1 + c.derefs(x.X)
	case *ast.SelectorExpr:
		sel, ok := c.info.Selections[x]
		if !ok {
			return 0
		}
		return c.selDerefs(sel, x.X) + c.derefs(x.X)
	case *ast.IndexExpr:
		return c.ptrBase(x.X) + c.derefs(x.X)
	case *ast.SliceExpr:
		return c.ptrBase(x.X) + c.derefs(x.X)
	}
	return 0
}

// selDerefs counts the dereference one selection makes. go/types reports
// an indirection for a pointer-receiver method called through a pointer,
// which reads nothing; a method dereferences only when its receiver is a
// value and x is a pointer.
func (c *checker) selDerefs(sel *types.Selection, x ast.Expr) int {
	if sel.Kind() == types.FieldVal {
		if sel.Indirect() {
			return 1
		}
		return 0
	}
	fn, ok := sel.Obj().(*types.Func)
	if !ok {
		return 0
	}
	// Walk the embedding path: reaching a method through an embedded
	// pointer field reads that pointer; through an embedded value it only
	// computes an address.
	n, t := 0, sel.Recv()
	for _, i := range sel.Index()[:len(sel.Index())-1] {
		if p, isPtr := t.Underlying().(*types.Pointer); isPtr {
			t = p.Elem()
		}
		st, isStruct := t.Underlying().(*types.Struct)
		if !isStruct {
			return n
		}
		t = st.Field(i).Type()
		if _, isPtr := t.Underlying().(*types.Pointer); isPtr {
			n++
		}
	}
	recv := fn.Type().(*types.Signature).Recv()
	_, ptrRecv := recv.Type().Underlying().(*types.Pointer)
	_, onPtr := t.Underlying().(*types.Pointer)
	if !ptrRecv && (onPtr || (len(sel.Index()) == 1 && c.ptrBase(x) == 1)) {
		n++
	}
	return n
}

func (c *checker) ptrBase(e ast.Expr) int {
	if _, ok := c.info.TypeOf(e).Underlying().(*types.Pointer); ok {
		return 1
	}
	return 0
}

// forBound reports why a three-clause loop's bound is not provable: the
// post statement steps a counter the body never assigns, and the
// condition compares that counter with a bound the body never changes.
func (c *checker) forBound(n *ast.ForStmt) string {
	if n.Cond == nil {
		return "loop with no condition"
	}
	counter, up := c.counter(n.Post)
	if counter == nil {
		return "loop has no counter stepped by its post statement"
	}
	assigned := c.assignedIn(n.Body)
	if assigned[counter] {
		return "loop counter " + counter.Name() + " changes in the loop body"
	}
	why := ""
	for _, conj := range conjuncts(n.Cond) {
		be, ok := ast.Unparen(conj).(*ast.BinaryExpr)
		if !ok {
			continue
		}
		bound := c.boundOf(be, counter, up)
		if bound == nil {
			continue
		}
		w := c.fixed(bound, assigned)
		if w == "" {
			return "" // one fixed bound is enough; the other conjuncts only stop earlier
		}
		if why == "" {
			why = w
		}
	}
	if why != "" {
		return why
	}
	return "loop condition does not compare counter " + counter.Name() + " with a bound"
}

// counter returns the variable post steps and whether it counts up.
func (c *checker) counter(post ast.Stmt) (*types.Var, bool) {
	switch p := post.(type) {
	case *ast.IncDecStmt:
		if id, ok := p.X.(*ast.Ident); ok {
			v, _ := c.info.Uses[id].(*types.Var)
			return v, p.Tok == token.INC
		}
	case *ast.AssignStmt:
		if len(p.Lhs) != 1 || len(p.Rhs) != 1 || (p.Tok != token.ADD_ASSIGN && p.Tok != token.SUB_ASSIGN) {
			return nil, false
		}
		id, ok := p.Lhs[0].(*ast.Ident)
		tv := c.info.Types[p.Rhs[0]]
		if !ok || tv.Value == nil || constant.Sign(tv.Value) <= 0 {
			return nil, false
		}
		v, _ := c.info.Uses[id].(*types.Var)
		return v, p.Tok == token.ADD_ASSIGN
	}
	return nil, false
}

// boundOf returns the side of be the counter is compared against, when be
// stops the counter in the direction it moves.
func (c *checker) boundOf(be *ast.BinaryExpr, counter *types.Var, up bool) ast.Expr {
	is := func(e ast.Expr) bool {
		id, ok := ast.Unparen(e).(*ast.Ident)
		return ok && c.info.Uses[id] == counter
	}
	switch {
	case is(be.X) && up && (be.Op == token.LSS || be.Op == token.LEQ),
		is(be.X) && !up && (be.Op == token.GTR || be.Op == token.GEQ):
		return be.Y
	case is(be.Y) && up && (be.Op == token.GTR || be.Op == token.GEQ),
		is(be.Y) && !up && (be.Op == token.LSS || be.Op == token.LEQ):
		return be.X
	}
	return nil
}

// fixed reports why bound may change while the loop runs: it may use only
// constants, and variables and their len or cap that the body never
// assigns, takes the address of or reaches through a pointer.
func (c *checker) fixed(bound ast.Expr, assigned map[types.Object]bool) string {
	why := ""
	ast.Inspect(bound, func(n ast.Node) bool {
		if why != "" {
			return false
		}
		switch x := n.(type) {
		case *ast.CallExpr:
			id, ok := ast.Unparen(x.Fun).(*ast.Ident)
			b, isB := c.info.Uses[id].(*types.Builtin)
			if !ok || !isB || (b.Name() != "len" && b.Name() != "cap") {
				if !c.info.Types[x.Fun].IsType() {
					why = "loop bound " + types.ExprString(bound) + " calls a function"
				}
			}
		case *ast.SelectorExpr:
			if _, isSel := c.info.Selections[x]; isSel {
				why = "loop bound " + types.ExprString(bound) + " reads a field; copy it to a local first"
				return false
			}
		case *ast.Ident:
			if v, ok := c.info.Uses[x].(*types.Var); ok && assigned[v] {
				why = "loop bound " + v.Name() + " changes in the loop body"
			}
		}
		return true
	})
	return why
}

// assignedIn returns the variables body assigns, steps or takes the
// address of.
func (c *checker) assignedIn(body ast.Node) map[types.Object]bool {
	out := map[types.Object]bool{}
	root := func(e ast.Expr) {
		for {
			switch x := ast.Unparen(e).(type) {
			case *ast.SelectorExpr:
				e = x.X
				continue
			case *ast.IndexExpr:
				e = x.X
				continue
			case *ast.StarExpr:
				e = x.X
				continue
			case *ast.Ident:
				if obj := c.info.ObjectOf(x); obj != nil {
					out[obj] = true
				}
			}
			return
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for _, l := range x.Lhs {
				root(l)
			}
		case *ast.IncDecStmt:
			root(x.X)
		case *ast.UnaryExpr:
			if x.Op == token.AND {
				root(x.X)
			}
		case *ast.RangeStmt:
			if x.Key != nil {
				root(x.Key)
			}
			if x.Value != nil {
				root(x.Value)
			}
		}
		return true
	})
	return out
}

func conjuncts(e ast.Expr) []ast.Expr {
	be, ok := ast.Unparen(e).(*ast.BinaryExpr)
	if !ok || be.Op != token.LAND {
		return []ast.Expr{e}
	}
	return append(conjuncts(be.X), conjuncts(be.Y)...)
}

// rangeBound reports a range whose length is not fixed when it starts: a
// channel, a function iterator, or a map the body changes.
func (c *checker) rangeBound(n *ast.RangeStmt) {
	switch u := c.info.TypeOf(n.X).Underlying().(type) {
	case *types.Chan:
		c.report(n.Pos(), 2, "range over a channel")
	case *types.Signature:
		c.report(n.Pos(), 2, "range over a function iterator")
	case *types.Map:
		x := types.ExprString(n.X)
		ast.Inspect(n.Body, func(m ast.Node) bool {
			switch s := m.(type) {
			case *ast.AssignStmt:
				for _, l := range s.Lhs {
					if ix, ok := l.(*ast.IndexExpr); ok && types.ExprString(ix.X) == x {
						c.report(n.Pos(), 2, "range over map %s, which the body adds to", x)
					}
				}
			case *ast.CallExpr:
				if id, ok := s.Fun.(*ast.Ident); ok && id.Name == "delete" && types.ExprString(s.Args[0]) == x {
					c.report(n.Pos(), 2, "range over map %s, which the body deletes from", x)
				}
			}
			return true
		})
		_ = u
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

// configError reports whether id is the method value assigned to a
// go/types Config's Error field, the one function value go/types requires.
func (c *checker) configError(id *ast.Ident, parents map[ast.Node]ast.Node) bool {
	sel, ok := parents[id].(*ast.SelectorExpr)
	if !ok || sel.Sel != id {
		return false
	}
	as, ok := parents[sel].(*ast.AssignStmt)
	if !ok || len(as.Lhs) != 1 || as.Rhs[0] != sel {
		return false
	}
	lhs, ok := as.Lhs[0].(*ast.SelectorExpr)
	if !ok || lhs.Sel.Name != "Error" {
		return false
	}
	field, ok := c.info.Uses[lhs.Sel].(*types.Var)
	return ok && field.Pkg() != nil && field.Pkg().Path() == "go/types"
}

// called reports whether the function named by id is being called, or is
// only declared, rather than used as a value.
func (c *checker) called(id *ast.Ident, parents map[ast.Node]ast.Node) bool {
	var e ast.Node = id
	if sel, ok := parents[id].(*ast.SelectorExpr); ok && sel.Sel == id {
		e = sel
	}
	call, ok := parents[e].(*ast.CallExpr)
	return ok && ast.Unparen(call.Fun) == e
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

// recursion reports Rule 1 for any cycle in the call graph, interface
// calls included.
func (c *checker) recursion() {
	const (
		white = iota
		grey
		black
	)
	color := map[*types.Func]int{}
	var visit func(fn *types.Func)
	visit = func(fn *types.Func) {
		color[fn] = grey
		for _, g := range c.calls[fn] {
			switch color[g] {
			case grey:
				c.report(g.Pos(), 1, "recursion through %s", g.Name())
			case white:
				visit(g)
			}
		}
		color[fn] = black
	}
	fns := make([]*types.Func, 0, len(c.calls))
	for fn := range c.calls {
		fns = append(fns, fn)
	}
	sort.Slice(fns, func(i, j int) bool { return fns[i].Pos() < fns[j].Pos() })
	for _, fn := range fns {
		if color[fn] == white {
			visit(fn)
		}
	}
}

func comparison(op token.Token) bool {
	return op == token.LSS || op == token.LEQ || op == token.GTR || op == token.GEQ || op == token.EQL || op == token.NEQ
}

func growing(op token.Token) bool {
	return op == token.ADD || op == token.SUB || op == token.MUL || op == token.SHL
}

// grows returns what x op= y, or x = x op y, changes, or nil.
func grows(a *ast.AssignStmt) ast.Expr {
	if len(a.Lhs) != 1 || len(a.Rhs) != 1 {
		return nil
	}
	if a.Tok == token.ADD_ASSIGN || a.Tok == token.SUB_ASSIGN || a.Tok == token.MUL_ASSIGN || a.Tok == token.SHL_ASSIGN {
		return a.Lhs[0]
	}
	be, ok := ast.Unparen(a.Rhs[0]).(*ast.BinaryExpr)
	if (a.Tok == token.ASSIGN || a.Tok == token.DEFINE) && ok && growing(be.Op) &&
		types.ExprString(ast.Unparen(be.X)) == types.ExprString(a.Lhs[0]) {
		return a.Lhs[0]
	}
	return nil
}
