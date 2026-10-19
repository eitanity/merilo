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
	fset     *token.FileSet
	md       *model
	p        *pkg
	fl       []entry
	findings []finding
}

// check runs every rule that applies to each package and returns the
// findings in file and line order.
func check(prog *program, md *model) []finding {
	assert(prog != nil, "check needs a program")
	assert(md != nil, "check needs a model")
	c := &checker{fset: prog.fset, md: md}
	for _, p := range prog.pkgs {
		c.p = p
		c.vet20()
		machinePkg := md.machinePkgs[p.types]
		if !machinePkg && !md.isHolder(p) {
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
	sortFindings(c.findings)
	return c.findings
}

// machineRules checks the declarations of the machines this package owns.
func (c *checker) machineRules() {
	assert(c.p != nil, "machineRules needs a package")
	assert(c.md.machinePkgs[c.p.types], "machineRules runs on machine packages only")
	for _, m := range c.md.machines {
		if m.pkg != c.p.types {
			continue
		}
		c.vet02(m)
		c.vet03(m)
		c.vet15(m)
	}
	c.vet02Enums()
	c.vet08Zero()
}

// fileRules visits every node of the current file once per rule.
func (c *checker) fileRules(machinePkg bool) {
	assert(c.p != nil, "fileRules needs a package")
	assert(len(c.fl) > 0, "fileRules needs a flattened file")
	for i := range c.fl {
		c.vet08Switch(i)
		c.vet10(i)
		c.vet11(i)
		c.vet13(i)
		c.vet14(i)
		c.vet17(i)
		c.vet21(i)
		c.vet22(i)
		if machinePkg {
			c.vet12Writes(i)
			c.vet16(i)
		}
	}
	if machinePkg {
		c.vet12Exports()
	}
}

func (c *checker) add(n ast.Node, id, msg string) {
	assert(n != nil, "a finding needs a node")
	assert(id != "", "a finding needs a rule id")
	c.addPos(n.Pos(), id, msg)
}

func (c *checker) addPos(pos token.Pos, id, msg string) {
	assert(pos.IsValid(), "a finding needs a valid position")
	assert(msg != "", "a finding needs a message")
	c.findings = append(c.findings, finding{pos: c.fset.Position(pos), id: id, msg: msg})
}

// funcDecl returns the function declaration enclosing entry i, or nil.
func (c *checker) funcDecl(i int) *ast.FuncDecl {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	fn := c.fl[i].fn
	if fn < 0 {
		return nil
	}
	fd, ok := c.fl[fn].node.(*ast.FuncDecl)
	assert(ok, "fn always indexes a function declaration")
	return fd
}

// recvMachine returns the machine fd is a method of, or nil.
func (c *checker) recvMachine(fd *ast.FuncDecl) *machine {
	assert(fd != nil, "recvMachine needs a declaration")
	assert(c.p != nil && c.p.info != nil, "recvMachine needs type information")
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return nil
	}
	t := c.p.info.TypeOf(fd.Recv.List[0].Type)
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	return c.md.machineFor(t)
}

// inMachineMethod reports whether entry i lies in a method of a machine.
func (c *checker) inMachineMethod(i int) bool {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	assert(c.fl[i].node != nil, "entries hold nodes")
	fd := c.funcDecl(i)
	return fd != nil && c.recvMachine(fd) != nil
}

// exempt reports whether entry i lies in a method of the machine type t,
// where Fire builds its result from the receiver.
func (c *checker) exempt(i int, t types.Type) bool {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	assert(c.fl[i].node != nil, "entries hold nodes")
	fd := c.funcDecl(i)
	if fd == nil || t == nil {
		return false
	}
	m := c.recvMachine(fd)
	return m != nil && types.Identical(t, m.named)
}

// fireCall reports n as a call of some machine's Fire method.
func (c *checker) fireCall(n ast.Node) (*ast.CallExpr, *ast.SelectorExpr, bool) {
	assert(n != nil, "fireCall needs a node")
	assert(c.p.info != nil, "fireCall needs type information")
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return nil, nil, false
	}
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return nil, nil, false
	}
	s, ok := c.p.info.Selections[sel]
	if !ok || s.Kind() != types.MethodVal {
		return nil, nil, false
	}
	fn, ok := s.Obj().(*types.Func)
	if !ok || !c.md.isFire(fn) {
		return nil, nil, false
	}
	return call, sel, true
}

// isBuiltin reports whether fun names the builtin called name.
func (c *checker) isBuiltin(fun ast.Expr, name string) bool {
	assert(fun != nil, "isBuiltin needs an expression")
	assert(name != "", "isBuiltin needs a name")
	id, ok := ast.Unparen(fun).(*ast.Ident)
	if !ok {
		return false
	}
	b, ok := c.p.info.Uses[id].(*types.Builtin)
	return ok && b.Name() == name
}

// isLocal reports whether obj is a function-local variable.
func (c *checker) isLocal(obj types.Object) bool {
	assert(c.p.types != nil, "isLocal needs a checked package")
	assert(c.p.info != nil, "isLocal needs type information")
	v, ok := obj.(*types.Var)
	if !ok || v.IsField() {
		return false
	}
	return v.Parent() != nil && v.Parent() != c.p.types.Scope()
}

func isBlank(e ast.Expr) bool {
	assert(e != nil, "isBlank needs an expression")
	id, ok := ast.Unparen(e).(*ast.Ident)
	return ok && id.Name == "_"
}

// containsExpr reports whether list holds exactly the node e.
func containsExpr(list []ast.Expr, e ast.Expr) bool {
	assert(e != nil, "containsExpr needs an expression")
	assert(len(list) < maxNodes, "expression list longer than maxNodes")
	for _, x := range list {
		if x == e {
			return true
		}
	}
	return false
}
