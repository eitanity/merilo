package main

import (
	"go/ast"
	"go/token"
	"go/types"
)

// finding is one rule violation at one position.
type finding struct {
	pos token.Position
	id  string
	msg string
}

// checker applies the rules to one package, then one file, at a time.
type checker struct {
	flt      *faults
	prog     *program
	order    byPos // sorts findings in place, so sorting boxes nothing
	fset     *token.FileSet
	md       *model
	p        *pkg
	info     *types.Info // p.info, so rules read type information through one pointer
	tvs      map[ast.Expr]types.TypeAndValue
	uses     map[*ast.Ident]types.Object
	defs     map[*ast.Ident]types.Object
	sels     map[*ast.SelectorExpr]*types.Selection
	pkgTypes *types.Package // p.types
	files    []*ast.File    // p.files
	flat     [][]entry      // p.flat
	fl       []entry
	findings []finding
}

// check runs every rule that applies to each package and returns the
// findings in file and line order.
func check(flt *faults, prog *program, md *model) []finding {
	if flt == nil {
		return nil
	}
	if prog == nil || md == nil {
		flt.fault("check needs a program and its model")
		return nil
	}
	c := newChecker(flt, prog, md)
	if c == nil {
		return nil
	}
	return c.run()
}

// newChecker is the rule phase's setup. Rule 3 allows no allocation after
// it: the rules read the model's tables, and only a finding's text is
// formatted, when there is a finding to report.
func newChecker(flt *faults, prog *program, md *model) *checker {
	if flt == nil {
		return nil
	}
	if prog == nil {
		flt.fault("check needs a program")
		return nil
	}
	if md == nil {
		flt.fault("check needs a model")
		return nil
	}
	return &checker{flt: flt, fset: prog.fset, md: md, prog: prog}
}

// run applies the rules to every package, then VET-17 to the test files.
func (c *checker) run() []finding {
	if c == nil || c.prog == nil || c.md == nil {
		return nil
	}
	c.findings = c.findings[:0]
	md, prog := c.md, c.prog
	for _, p := range prog.pkgs {
		c.use(p)
		c.vet11()
		c.vet14()
		machinePkg := md.machinePkgs[p.types]
		if !machinePkg && !md.holderPkgs[p.types] {
			continue
		}
		if machinePkg {
			c.machineRules()
		}
		for _, fl := range p.flat {
			c.fl = fl
			c.fileRules(machinePkg)
		}
	}
	for _, p := range prog.tests {
		c.use(p)
		for _, fl := range p.flat {
			c.fl = fl
			c.vet17()
		}
	}
	c.sortFindings()
	return c.findings
}

// use points the checker at package p.
func (c *checker) use(p *pkg) {
	if c == nil {
		return
	}
	if p == nil || p.info == nil {
		c.flt.fault("use needs a checked package")
		return
	}
	c.p, c.info, c.pkgTypes, c.files, c.flat = p, p.info, p.types, p.files, p.flat
	info := p.info
	c.tvs, c.uses, c.defs, c.sels = info.Types, info.Uses, info.Defs, info.Selections
}

// machineRules checks the declarations of the machines this package owns.
func (c *checker) machineRules() {
	if c == nil {
		return
	}
	if c.p == nil {
		c.flt.fault("machineRules needs a package")
		return
	}
	md := c.md
	if !md.machinePkgs[c.pkgTypes] {
		c.flt.fault("machineRules runs on machine packages only")
		return
	}
	for _, m := range md.machines {
		if m.pkg != c.pkgTypes {
			continue
		}
		c.vet01(m)
		c.vet01Event(m)
		c.vet02(m)
		c.vet09(m)
		c.vet15Fields(m)
	}
	c.vet01Enums()
	c.vet03Zero()
}

// fileRules visits every node of the current file once per rule.
func (c *checker) fileRules(machinePkg bool) {
	if c == nil {
		return
	}
	if c.p == nil {
		c.flt.fault("fileRules needs a package")
		return
	}
	if len(c.fl) == 0 {
		c.flt.fault("fileRules needs a flattened file")
		return
	}
	for i := range c.fl {
		c.vet03Switch(i)
		c.vet03Chain(i)
		c.vet04(i)
		c.vet05(i)
		c.vet07(i)
		c.vet08(i)
		c.vet10(i)
		c.vet12(i)
		c.vet13(i)
		if machinePkg {
			c.vet06Writes(i)
			c.vet15Convert(i)
			c.vet16(i)
		}
	}
	if machinePkg {
		c.vet06Exports()
	}
}

func (c *checker) add(n ast.Node, id, msg string) {
	if c == nil {
		return
	}
	if n == nil {
		c.flt.fault("a finding needs a node")
		return
	}
	if id == "" {
		c.flt.fault("a finding needs a rule id")
		return
	}
	c.addPos(n.Pos(), id, msg)
}

func (c *checker) addPos(pos token.Pos, id, msg string) {
	if c == nil {
		return
	}
	if held := pos.IsValid(); !held {
		c.flt.fault("a finding needs a valid position")
		return
	}
	if msg == "" {
		c.flt.fault("a finding needs a message")
		return
	}
	c.findings = append(c.findings, finding{pos: c.fset.Position(pos), id: id, msg: msg})
}

// funcDecl returns the function declaration enclosing entry i, or nil.
func (c *checker) funcDecl(i int) *ast.FuncDecl {
	if c == nil {
		return nil
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return nil
	}
	fn := c.fl[i].fn
	if fn < 0 {
		return nil
	}
	fd, ok := c.fl[fn].node.(*ast.FuncDecl)
	if !ok {
		c.flt.fault("fn always indexes a function declaration")
		return nil
	}
	return fd
}

// recvMachine returns the machine fd is a method of, or nil.
func (c *checker) recvMachine(fd *ast.FuncDecl) *machine {
	if c == nil {
		return nil
	}
	if fd == nil {
		c.flt.fault("recvMachine needs a declaration")
		return nil
	}
	if c.p == nil || c.info == nil {
		c.flt.fault("recvMachine needs type information")
		return nil
	}
	recv := fd.Recv
	if recv == nil || len(recv.List) == 0 {
		return nil
	}
	field := recv.List[0]
	t := c.info.TypeOf(field.Type)
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	return c.md.machineFor(t)
}

// inMachineMethod reports whether entry i lies in a method of a machine.
func (c *checker) inMachineMethod(i int) bool {
	if c == nil {
		return false
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return false
	}
	if c.fl[i].node == nil {
		c.flt.fault("entries hold nodes")
		return false
	}
	fd := c.funcDecl(i)
	return fd != nil && c.recvMachine(fd) != nil
}

// exempt reports whether entry i lies in a method of the machine type t,
// where Fire builds its result from the receiver.
func (c *checker) exempt(i int, t types.Type) bool {
	if c == nil {
		return false
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return false
	}
	if c.fl[i].node == nil {
		c.flt.fault("entries hold nodes")
		return false
	}
	fd := c.funcDecl(i)
	if fd == nil || t == nil {
		return false
	}
	m := c.recvMachine(fd)
	return m != nil && types.Identical(t, m.named)
}

// fireCall reports n as a call of some machine's Fire method.
func (c *checker) fireCall(n ast.Node) (*ast.CallExpr, *ast.SelectorExpr, bool) {
	if c == nil {
		return nil, nil, false
	}
	if n == nil {
		c.flt.fault("fireCall needs a node")
		return nil, nil, false
	}
	if c.info == nil {
		c.flt.fault("fireCall needs type information")
		return nil, nil, false
	}
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return nil, nil, false
	}
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return nil, nil, false
	}
	s, ok := c.sels[sel]
	if !ok || s.Kind() != types.MethodVal {
		return nil, nil, false
	}
	fn, ok := s.Obj().(*types.Func)
	if !ok || c.md.fireMachine(fn) == nil {
		return nil, nil, false
	}
	return call, sel, true
}

// fireResults returns how many results the Fire method called through sel
// has: 3 for a machine with actions, 2 without.
func (c *checker) fireResults(sel *ast.SelectorExpr) int {
	if c == nil {
		return 0
	}
	if sel == nil {
		c.flt.fault("fireResults needs a selector")
		return 0
	}
	s, ok := c.sels[sel]
	if !ok {
		c.flt.fault("a Fire call has a selection")
		return 0
	}
	fn, ok := s.Obj().(*types.Func)
	if !ok {
		c.flt.fault("a Fire selection is a method")
		return 0
	}
	m := c.md.fireMachine(fn)
	if m == nil {
		c.flt.fault("fireResults is used on Fire calls")
		return 0
	}
	return m.results
}

// isBuiltin reports whether fun names the builtin called name.
func (c *checker) isBuiltin(fun ast.Expr, name string) bool {
	if c == nil {
		return false
	}
	if fun == nil {
		c.flt.fault("isBuiltin needs an expression")
		return false
	}
	if name == "" {
		c.flt.fault("isBuiltin needs a name")
		return false
	}
	id, ok := ast.Unparen(fun).(*ast.Ident)
	if !ok {
		return false
	}
	b, ok := c.uses[id].(*types.Builtin)
	return ok && b.Name() == name
}

// isLocal reports whether obj is a function-local variable.
func (c *checker) isLocal(obj types.Object) bool {
	if obj == nil {
		return false
	}
	if c == nil {
		return false
	}
	if c.pkgTypes == nil {
		c.flt.fault("isLocal needs a checked package")
		return false
	}
	if c.info == nil {
		c.flt.fault("isLocal needs type information")
		return false
	}
	v, ok := obj.(*types.Var)
	if !ok || v.IsField() {
		return false
	}
	return v.Parent() != nil && v.Parent() != c.pkgTypes.Scope()
}

func isBlank(flt *faults, e ast.Expr) bool {
	if flt == nil {
		return false
	}
	if e == nil {
		flt.fault("isBlank needs an expression")
		return false
	}
	id, ok := ast.Unparen(e).(*ast.Ident)
	return ok && id.Name == "_"
}

// containsExpr reports whether list holds exactly the node e.
func containsExpr(flt *faults, list []ast.Expr, e ast.Expr) bool {
	if flt == nil {
		return false
	}
	if e == nil {
		flt.fault("containsExpr needs an expression")
		return false
	}
	if len(list) >= maxNodes {
		flt.fault("expression list longer than maxNodes")
		return false
	}
	for _, x := range list {
		if x == e {
			return true
		}
	}
	return false
}
