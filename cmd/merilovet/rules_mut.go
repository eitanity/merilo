package main

import (
	"go/ast"
	"go/token"
	"go/types"
)

// vet12Writes: machine fields are written only in New and in methods on
// the machine type, and machine literals appear only there.
func (c *checker) vet12Writes(i int) {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	assert(c.md.machinePkgs[c.p.types], "vet12 runs in machine packages")
	switch x := c.fl[i].node.(type) {
	case *ast.AssignStmt:
		for _, lhs := range x.Lhs {
			c.vet12Field(i, lhs)
		}
	case *ast.IncDecStmt:
		c.vet12Field(i, x.X)
	case *ast.CompositeLit:
		if m := c.md.machineFor(c.p.info.TypeOf(x)); m != nil && !c.inMachineCode(i, m) {
			c.add(x, "VET-12", m.named.Obj().Name()+" literal outside New and its methods")
		}
	}
}

func (c *checker) vet12Field(i int, e ast.Expr) {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	assert(e != nil, "vet12Field needs an expression")
	sel, ok := ast.Unparen(e).(*ast.SelectorExpr)
	if !ok {
		return
	}
	s, ok := c.p.info.Selections[sel]
	if !ok || s.Kind() != types.FieldVal {
		return
	}
	m := c.md.fieldOwner(s.Obj())
	if m == nil || c.inMachineCode(i, m) {
		return
	}
	c.add(sel, "VET-12", "field "+s.Obj().Name()+" of "+m.named.Obj().Name()+" written outside New and its methods")
}

// inMachineCode reports whether entry i lies in New or a method of m.
func (c *checker) inMachineCode(i int, m *machine) bool {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	assert(m != nil, "inMachineCode needs a machine")
	fd := c.funcDecl(i)
	if fd == nil {
		return false
	}
	if fd.Recv == nil {
		return fd.Name.Name == "New"
	}
	return c.recvMachine(fd) == m
}

// vet12Exports: the only exported functions or methods returning a machine
// type are New and Fire.
func (c *checker) vet12Exports() {
	assert(c.p != nil, "vet12Exports needs a package")
	assert(len(c.fl) > 0, "vet12Exports needs a file")
	for _, d := range c.fl[0].node.(*ast.File).Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || !fd.Name.IsExported() {
			continue
		}
		if fd.Recv == nil && fd.Name.Name == "New" {
			continue
		}
		if fd.Recv != nil && fd.Name.Name == "Fire" && c.recvMachine(fd) != nil {
			continue
		}
		fn, ok := c.p.info.Defs[fd.Name].(*types.Func)
		assert(ok, "a function declaration defines a function")
		sig, ok := fn.Type().(*types.Signature)
		assert(ok, "a function's type is a signature")
		for r := range sig.Results().Len() {
			if c.md.machineFor(sig.Results().At(r).Type()) != nil {
				c.add(fd.Name, "VET-12", "exported "+fd.Name.Name+" returns a machine; only New and Fire may")
				break
			}
		}
	}
}

// vet13: a Fire result replaces its receiver in place, as
// loc, acts, out = loc.Fire(e) with identical, side-effect-free loc.
func (c *checker) vet13(i int) {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	call, sel, ok := c.fireCall(c.fl[i].node)
	if !ok || c.inMachineMethod(i) {
		return
	}
	assert(c.fl[i].parent >= 0, "a call always has a parent")
	as, ok := c.fl[c.fl[i].parent].node.(*ast.AssignStmt)
	if ok && as.Tok == token.ASSIGN && len(as.Lhs) == 3 && len(as.Rhs) == 1 && as.Rhs[0] == call &&
		c.pureLoc(as.Lhs[0]) && c.sameExpr(as.Lhs[0], sel.X) {
		return
	}
	c.add(call, "VET-13", "Fire must advance its receiver in place: loc, acts, out = loc.Fire(e)")
}

// sameExpr compares two location expressions token by token, identifiers
// by the object they denote.
func (c *checker) sameExpr(a, b ast.Expr) bool {
	assert(a != nil && b != nil, "sameExpr needs two expressions")
	left, right := []ast.Expr{a}, []ast.Expr{b}
	for i := 0; i < maxNodes && len(left) > 0; i++ {
		x, y := ast.Unparen(left[len(left)-1]), ast.Unparen(right[len(right)-1])
		left, right = left[:len(left)-1], right[:len(right)-1]
		switch xv := x.(type) {
		case *ast.Ident:
			yv, ok := y.(*ast.Ident)
			if !ok || xv.Name != yv.Name || c.p.info.ObjectOf(xv) != c.p.info.ObjectOf(yv) {
				return false
			}
		case *ast.SelectorExpr:
			yv, ok := y.(*ast.SelectorExpr)
			if !ok || xv.Sel.Name != yv.Sel.Name {
				return false
			}
			left, right = append(left, xv.X), append(right, yv.X)
		case *ast.StarExpr:
			yv, ok := y.(*ast.StarExpr)
			if !ok {
				return false
			}
			left, right = append(left, xv.X), append(right, yv.X)
		case *ast.IndexExpr:
			yv, ok := y.(*ast.IndexExpr)
			if !ok {
				return false
			}
			left, right = append(left, xv.X, xv.Index), append(right, yv.X, yv.Index)
		case *ast.BasicLit:
			yv, ok := y.(*ast.BasicLit)
			if !ok || xv.Kind != yv.Kind || xv.Value != yv.Value {
				return false
			}
		default:
			return false
		}
	}
	assert(len(left) == 0 && len(right) == 0, "location deeper than maxNodes")
	return true
}

// pureLoc reports whether e is a location without side effects:
// identifiers, field selectors, dereferences, and indexes by an
// identifier or constant.
func (c *checker) pureLoc(e ast.Expr) bool {
	assert(e != nil, "pureLoc needs an expression")
	stack := []ast.Expr{e}
	for i := 0; i < maxNodes && len(stack) > 0; i++ {
		x := ast.Unparen(stack[len(stack)-1])
		stack = stack[:len(stack)-1]
		switch xv := x.(type) {
		case *ast.Ident:
		case *ast.SelectorExpr:
			if s, ok := c.p.info.Selections[xv]; ok && s.Kind() != types.FieldVal {
				return false
			}
			stack = append(stack, xv.X)
		case *ast.StarExpr:
			stack = append(stack, xv.X)
		case *ast.IndexExpr:
			if !c.simpleIndex(xv.Index) {
				return false
			}
			stack = append(stack, xv.X)
		default:
			return false
		}
	}
	assert(len(stack) == 0, "location deeper than maxNodes")
	return true
}

// simpleIndex reports whether an index is an identifier or a constant.
func (c *checker) simpleIndex(e ast.Expr) bool {
	assert(e != nil, "simpleIndex needs an expression")
	assert(c.p.info != nil, "simpleIndex needs type information")
	if _, ok := ast.Unparen(e).(*ast.Ident); ok {
		return true
	}
	tv, ok := c.p.info.Types[e]
	return ok && tv.Value != nil
}

// vet14: no address-of on machine values or their fields.
func (c *checker) vet14(i int) {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	assert(c.p.info != nil, "vet14 needs type information")
	u, ok := c.fl[i].node.(*ast.UnaryExpr)
	if !ok || u.Op != token.AND {
		return
	}
	if c.md.machineFor(c.p.info.TypeOf(u.X)) != nil {
		c.add(u, "VET-14", "address of a machine")
		return
	}
	sel, ok := ast.Unparen(u.X).(*ast.SelectorExpr)
	if !ok {
		return
	}
	if s, found := c.p.info.Selections[sel]; found && s.Kind() == types.FieldVal && c.md.fieldOwner(s.Obj()) != nil {
		c.add(u, "VET-14", "address of a machine field")
	}
}
