package main

import (
	"go/ast"
	"go/token"
	"go/types"
)

// vet06Writes: machine fields are written only in New and in methods on
// the machine type, and machine literals appear only there.
func (c *checker) vet06Writes(i int) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	md := c.md
	if !md.machinePkgs[c.pkgTypes] {
		c.flt.fault("vet06 runs in machine packages")
		return
	}
	switch x := c.fl[i].node.(type) {
	case *ast.AssignStmt:
		for _, lhs := range x.Lhs {
			c.vet06Field(i, lhs)
		}
	case *ast.IncDecStmt:
		c.vet06Field(i, x.X)
	case *ast.CompositeLit:
		if m := c.md.machineFor(c.info.TypeOf(x)); m != nil && !c.inMachineCode(i, m) {
			c.add(x, "VET-06", m.named.Obj().Name()+" literal outside New and its methods")
		}
	}
}

func (c *checker) vet06Field(i int, e ast.Expr) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	if e == nil {
		c.flt.fault("vet06Field needs an expression")
		return
	}
	sel, ok := ast.Unparen(e).(*ast.SelectorExpr)
	if !ok {
		return
	}
	s, ok := c.sels[sel]
	if !ok || s.Kind() != types.FieldVal {
		return
	}
	m := c.md.fieldOwner(s.Obj())
	if m == nil || c.inMachineCode(i, m) {
		return
	}
	c.add(sel, "VET-06", "field "+s.Obj().Name()+" of "+m.named.Obj().Name()+" written outside New and its methods")
}

// inMachineCode reports whether entry i lies in New or a method of m.
func (c *checker) inMachineCode(i int, m *machine) bool {
	if c == nil {
		return false
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return false
	}
	if m == nil {
		c.flt.fault("inMachineCode needs a machine")
		return false
	}
	fd := c.funcDecl(i)
	if fd == nil {
		return false
	}
	if fd.Recv == nil {
		name := fd.Name
		return name.Name == "New"
	}
	return c.recvMachine(fd) == m
}

// vet06Exports: the only exported functions or methods returning a machine
// type are New and Fire.
func (c *checker) vet06Exports() {
	if c == nil {
		return
	}
	if c.p == nil {
		c.flt.fault("vet06Exports needs a package")
		return
	}
	if len(c.fl) == 0 {
		c.flt.fault("vet06Exports needs a file")
		return
	}
	file, ok := c.fl[0].node.(*ast.File)
	if !ok {
		c.flt.fault("the first entry of a file is the file")
		return
	}
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.IsExported() {
			c.vet06Export(fd)
		}
	}
}

// vet06Export reports the exported function fd when it returns a machine
// and is neither New nor a machine's Fire.
func (c *checker) vet06Export(fd *ast.FuncDecl) {
	if c == nil {
		return
	}
	if fd == nil {
		c.flt.fault("vet06Export needs a declaration")
		return
	}
	name := fd.Name
	if fd.Recv == nil && name.Name == "New" {
		return
	}
	if fd.Recv != nil && name.Name == "Fire" && c.recvMachine(fd) != nil {
		return
	}
	fn, ok := c.defs[fd.Name].(*types.Func)
	if !ok {
		c.flt.fault("a function declaration defines a function")
		return
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		c.flt.fault("a function's type is a signature")
		return
	}
	for r := range sig.Results().Len() {
		if c.md.machineFor(sig.Results().At(r).Type()) != nil {
			c.add(name, "VET-06", "exported "+name.Name+" returns a machine; only New and Fire may")
			return
		}
	}
}

// vet07: a Fire result replaces its receiver in place, as
// loc, acts, out = loc.Fire(e), or loc, out = loc.Fire(e) for a machine
// without actions, with identical, side-effect-free loc.
func (c *checker) vet07(i int) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	call, sel, ok := c.fireCall(c.fl[i].node)
	if !ok || c.inMachineMethod(i) {
		return
	}
	if c.fl[i].parent < 0 {
		c.flt.fault("a call always has a parent")
		return
	}
	as, ok := c.fl[c.fl[i].parent].node.(*ast.AssignStmt)
	if ok && as.Tok == token.ASSIGN && len(as.Lhs) == c.fireResults(sel) && len(as.Rhs) == 1 && as.Rhs[0] == call &&
		c.pureLoc(as.Lhs[0]) && c.sameExpr(as.Lhs[0], sel.X) {
		return
	}
	form := "loc, acts, out = loc.Fire(e)"
	if c.fireResults(sel) == 2 {
		form = "loc, out = loc.Fire(e)"
	}
	c.add(call, "VET-07", "Fire must advance its receiver in place: "+form)
}

// sameExpr compares two location expressions token by token, identifiers
// by the object they denote.
func (c *checker) sameExpr(a, b ast.Expr) bool {
	if c == nil {
		return false
	}
	if a == nil || b == nil {
		c.flt.fault("sameExpr needs two expressions")
		return false
	}
	left, right := []ast.Expr{a}, []ast.Expr{b}
	for i := 0; i < maxNodes && len(left) > 0; i++ {
		x, y := ast.Unparen(left[len(left)-1]), ast.Unparen(right[len(right)-1])
		left, right = left[:len(left)-1], right[:len(right)-1]
		var ok bool
		if left, right, ok = c.matchNode(x, y, left, right); !ok {
			return false
		}
	}
	if len(left) != 0 || len(right) != 0 {
		c.flt.fault("location deeper than maxNodes")
		return false
	}
	return true
}

// matchNode compares one pair of nodes and pushes their children onto the
// two stacks. It reports false when the nodes differ.
func (c *checker) matchNode(x, y ast.Expr, left, right []ast.Expr) ([]ast.Expr, []ast.Expr, bool) {
	if c == nil {
		return nil, nil, false
	}
	if x == nil || y == nil {
		c.flt.fault("matchNode needs two nodes")
		return nil, nil, false
	}
	if len(left) != len(right) {
		c.flt.fault("matchNode stacks out of step")
		return nil, nil, false
	}
	switch xv := x.(type) {
	case *ast.Ident:
		yv, ok := y.(*ast.Ident)
		return left, right, ok && xv.Name == yv.Name && c.info.ObjectOf(xv) == c.info.ObjectOf(yv)
	case *ast.SelectorExpr:
		yv, ok := y.(*ast.SelectorExpr)
		if !ok {
			return left, right, false
		}
		xs, ys := xv.Sel, yv.Sel
		if xs.Name != ys.Name {
			return left, right, false
		}
		return append(left, xv.X), append(right, yv.X), true
	case *ast.StarExpr:
		yv, ok := y.(*ast.StarExpr)
		if !ok {
			return left, right, false
		}
		return append(left, xv.X), append(right, yv.X), true
	case *ast.IndexExpr:
		yv, ok := y.(*ast.IndexExpr)
		if !ok {
			return left, right, false
		}
		return append(left, xv.X, xv.Index), append(right, yv.X, yv.Index), true
	case *ast.BasicLit:
		yv, ok := y.(*ast.BasicLit)
		return left, right, ok && xv.Kind == yv.Kind && xv.Value == yv.Value
	default:
		return left, right, false
	}
}

// pureLoc reports whether e is a location without side effects:
// identifiers, field selectors, dereferences, and indexes by an
// identifier or constant.
func (c *checker) pureLoc(e ast.Expr) bool {
	if c == nil {
		return false
	}
	if e == nil {
		c.flt.fault("pureLoc needs an expression")
		return false
	}
	stack := []ast.Expr{e}
	for i := 0; i < maxNodes && len(stack) > 0; i++ {
		x := ast.Unparen(stack[len(stack)-1])
		stack = stack[:len(stack)-1]
		switch xv := x.(type) {
		case *ast.Ident:
		case *ast.SelectorExpr:
			if s, ok := c.sels[xv]; ok && s.Kind() != types.FieldVal {
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
	if len(stack) != 0 {
		c.flt.fault("location deeper than maxNodes")
		return false
	}
	return true
}

// simpleIndex reports whether an index is an identifier or a constant.
func (c *checker) simpleIndex(e ast.Expr) bool {
	if c == nil {
		return false
	}
	if e == nil {
		c.flt.fault("simpleIndex needs an expression")
		return false
	}
	if c.info == nil {
		c.flt.fault("simpleIndex needs type information")
		return false
	}
	if _, ok := ast.Unparen(e).(*ast.Ident); ok {
		return true
	}
	tv, ok := c.tvs[e]
	return ok && tv.Value != nil
}

// vet08: no address-of on machine values or their fields.
func (c *checker) vet08(i int) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	if c.info == nil {
		c.flt.fault("vet08 needs type information")
		return
	}
	u, ok := c.fl[i].node.(*ast.UnaryExpr)
	if !ok || u.Op != token.AND {
		return
	}
	if c.md.machineFor(c.info.TypeOf(u.X)) != nil {
		c.add(u, "VET-08", "address of a machine")
		return
	}
	sel, ok := ast.Unparen(u.X).(*ast.SelectorExpr)
	if !ok {
		return
	}
	if s, found := c.sels[sel]; found && s.Kind() == types.FieldVal && c.md.fieldOwner(s.Obj()) != nil {
		c.add(u, "VET-08", "address of a machine field")
	}
}
