package main

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
)

// vet10: Fire's outcome, and its actions when the machine has them, go to
// local variables, and each is read later in the same block before it is
// assigned again.
func (c *checker) vet10(i int) {
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
	parent := c.fl[i].parent
	if parent < 0 {
		c.flt.fault("a call always has a parent")
		return
	}
	as, ok := c.fl[parent].node.(*ast.AssignStmt)
	if !ok || len(as.Lhs) != c.fireResults(sel) || len(as.Rhs) != 1 || as.Rhs[0] != call {
		c.add(call, "VET-10", "the results of Fire must go to local variables")
		return
	}
	for _, lhs := range as.Lhs[1:] {
		c.vet10Result(parent, lhs)
	}
}

// vet10Result checks one result variable of the Fire assignment at stmt.
func (c *checker) vet10Result(stmt int, lhs ast.Expr) {
	if c == nil {
		return
	}
	if stmt < 0 || stmt >= len(c.fl) {
		c.flt.fault("statement index out of range")
		return
	}
	if lhs == nil {
		c.flt.fault("vet10Result needs an expression")
		return
	}
	id, ok := ast.Unparen(lhs).(*ast.Ident)
	if !ok || id.Name == "_" || !c.isLocal(c.info.ObjectOf(id)) {
		c.add(lhs, "VET-10", "a Fire result must go to a local variable")
		return
	}
	obj := c.info.ObjectOf(id)
	block := c.fl[stmt].parent
	if block < 0 {
		c.flt.fault("a statement always has a parent")
		return
	}
	stop := c.fl[block].end
	skip := 0 // entries below skip lie inside a function literal
	for x := c.fl[stmt].end; x < stop; x++ {
		if x < skip {
			continue
		}
		if _, lit := c.fl[x].node.(*ast.FuncLit); lit {
			skip = c.fl[x].end
			continue
		}
		ref, ok := c.fl[x].node.(*ast.Ident)
		if ok && c.uses[ref] == obj {
			c.vet10Use(x, id)
			return
		}
	}
	c.add(id, "VET-10", id.Name+" from Fire is never read in its block")
}

// vet10Use judges the first mention after the Fire assignment: a plain
// read passes, a reassignment or a discard with _ = x does not.
func (c *checker) vet10Use(x int, id *ast.Ident) {
	if c == nil {
		return
	}
	if x < 0 || x >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	if id == nil {
		c.flt.fault("vet10Use needs the assigned identifier")
		return
	}
	ref, ok := c.fl[x].node.(*ast.Ident)
	if !ok {
		c.flt.fault("vet10Use is called on an identifier")
		return
	}
	as, ok := c.fl[c.fl[x].parent].node.(*ast.AssignStmt)
	if !ok {
		return
	}
	if len(as.Lhs) == 1 && isBlank(c.flt, as.Lhs[0]) && len(as.Rhs) == 1 && ast.Unparen(as.Rhs[0]) == ref {
		c.add(as, "VET-10", id.Name+" from Fire is discarded with _ =")
		return
	}
	if containsExpr(c.flt, as.Lhs, ref) {
		c.add(id, "VET-10", id.Name+" from Fire is assigned again before it is read")
	}
}

// vet12: no existing machine-bearing value is copied, and a fresh one never
// overwrites a live location except by map insert.
func (c *checker) vet12(i int) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	if c.info == nil {
		c.flt.fault("vet12 needs type information")
		return
	}
	switch x := c.fl[i].node.(type) {
	case *ast.AssignStmt:
		c.vet12Overwrite(i, x)
	case *ast.RangeStmt:
		c.vet12Range(i, x)
	case *ast.FuncDecl:
		c.vet12Receiver(x)
	case ast.Expr:
		c.vet12Copy(i, x)
	}
}

func (c *checker) vet12Copy(i int, e ast.Expr) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	if e == nil {
		c.flt.fault("vet12Copy needs an expression")
		return
	}
	if !c.existing(e) {
		return
	}
	t := c.info.TypeOf(e)
	if !c.md.bearing(t) || c.exempt(i, t) || !c.copyContext(c.fl[i].parent, e) {
		return
	}
	c.add(e, "VET-12", "copy of an existing "+types.TypeString(t, types.RelativeTo(c.pkgTypes)))
}

// existing reports whether e denotes a value already stored somewhere, as
// opposed to a fresh literal or call result.
func (c *checker) existing(e ast.Expr) bool {
	if c == nil {
		return false
	}
	if e == nil {
		c.flt.fault("existing needs an expression")
		return false
	}
	if c.info == nil {
		c.flt.fault("existing needs type information")
		return false
	}
	if tv, ok := c.tvs[e]; ok && !tv.IsValue() {
		return false
	}
	switch x := ast.Unparen(e).(type) {
	case *ast.Ident:
		_, ok := c.uses[x].(*types.Var)
		return ok
	case *ast.SelectorExpr:
		if s, ok := c.sels[x]; ok {
			return s.Kind() == types.FieldVal
		}
		_, ok := c.uses[x.Sel].(*types.Var)
		return ok
	case *ast.IndexExpr, *ast.StarExpr, *ast.TypeAssertExpr:
		return true
	case *ast.UnaryExpr:
		return x.Op == token.ARROW
	}
	return false
}

// copyContext reports whether parent copies child: assignment and
// declaration values, arguments, results, literal elements and sends.
func (c *checker) copyContext(parent int, child ast.Expr) bool {
	if c == nil {
		return false
	}
	if parent < 0 || parent >= len(c.fl) {
		c.flt.fault("parent index out of range")
		return false
	}
	if child == nil {
		c.flt.fault("copyContext needs an expression")
		return false
	}
	switch p := c.fl[parent].node.(type) {
	case *ast.AssignStmt:
		return containsExpr(c.flt, p.Rhs, child)
	case *ast.ValueSpec:
		return containsExpr(c.flt, p.Values, child)
	case *ast.ReturnStmt:
		return containsExpr(c.flt, p.Results, child)
	case *ast.CallExpr:
		return containsExpr(c.flt, p.Args, child) && !c.isBuiltin(p.Fun, "len") && !c.isBuiltin(p.Fun, "cap")
	case *ast.CompositeLit:
		return containsExpr(c.flt, p.Elts, child)
	case *ast.KeyValueExpr:
		return p.Value == child
	case *ast.SendStmt:
		return p.Value == child
	}
	return false
}

// vet12Overwrite flags a fresh value assigned over a live machine-bearing
// location; a reset is a transition, and only a map insert is allowed.
func (c *checker) vet12Overwrite(i int, as *ast.AssignStmt) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	if as == nil || len(as.Rhs) == 0 {
		c.flt.fault("vet12Overwrite needs an assignment")
		return
	}
	if as.Tok != token.ASSIGN {
		return
	}
	for k, lhs := range as.Lhs {
		t := c.info.TypeOf(lhs)
		if isBlank(c.flt, lhs) || !c.md.bearing(t) || c.exempt(i, t) || c.isMapIndex(lhs) {
			continue
		}
		rhs := as.Rhs[0]
		if len(as.Rhs) == len(as.Lhs) {
			rhs = as.Rhs[k]
		}
		if _, _, fire := c.fireCall(ast.Unparen(rhs)); fire {
			continue
		}
		if c.fresh(rhs) {
			c.add(lhs, "VET-12", "fresh value overwrites a live machine; a reset is a transition")
		}
	}
}

// fresh reports whether e makes a new value: a literal or a function call.
func (c *checker) fresh(e ast.Expr) bool {
	if c == nil {
		return false
	}
	if e == nil {
		c.flt.fault("fresh needs an expression")
		return false
	}
	if c.info == nil {
		c.flt.fault("fresh needs type information")
		return false
	}
	switch x := ast.Unparen(e).(type) {
	case *ast.CompositeLit:
		return true
	case *ast.CallExpr:
		fun, ok := c.tvs[x.Fun]
		return !ok || !fun.IsType()
	}
	return false
}

func (c *checker) isMapIndex(e ast.Expr) bool {
	if c == nil {
		return false
	}
	if e == nil {
		c.flt.fault("isMapIndex needs an expression")
		return false
	}
	if c.info == nil {
		c.flt.fault("isMapIndex needs type information")
		return false
	}
	ix, ok := ast.Unparen(e).(*ast.IndexExpr)
	if !ok {
		return false
	}
	_, isMap := c.info.TypeOf(ix.X).Underlying().(*types.Map)
	return isMap
}

// vet12Range flags a range value variable that copies machine-bearing elements.
func (c *checker) vet12Range(i int, rs *ast.RangeStmt) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	if rs == nil {
		c.flt.fault("vet12Range needs a range statement")
		return
	}
	if rs.Value == nil || isBlank(c.flt, rs.Value) {
		return
	}
	t := c.info.TypeOf(rs.Value)
	if c.md.bearing(t) && !c.exempt(i, t) {
		c.add(rs.Value, "VET-12", "range value variable copies each machine-bearing element; loop by index")
	}
}

// vet12Receiver flags a value receiver on a machine-bearing type other than
// the machine itself.
func (c *checker) vet12Receiver(fd *ast.FuncDecl) {
	if c == nil {
		return
	}
	if fd == nil {
		c.flt.fault("vet12Receiver needs a declaration")
		return
	}
	recv := fd.Recv
	if recv == nil || len(recv.List) == 0 {
		return
	}
	field := recv.List[0]
	t := c.info.TypeOf(field.Type)
	if t == nil {
		c.flt.fault("a receiver has a type")
		return
	}
	if _, ptr := t.(*types.Pointer); ptr {
		return
	}
	if c.md.bearing(t) && c.md.machineFor(t) == nil {
		c.add(field, "VET-12", "value receiver copies a machine-bearing value; use a pointer receiver")
	}
}

// vet13: no machine-bearing value is created implicitly.
func (c *checker) vet13(i int) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	if c.info == nil {
		c.flt.fault("vet13 needs type information")
		return
	}
	switch x := c.fl[i].node.(type) {
	case *ast.ValueSpec:
		c.vet13Var(i, x)
	case *ast.CompositeLit:
		c.vet13Lit(i, x)
	case *ast.CallExpr:
		c.vet13Alloc(i, x)
	}
}

func (c *checker) vet13Var(i int, vs *ast.ValueSpec) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	if vs == nil {
		c.flt.fault("vet13Var needs a value spec")
		return
	}
	if len(vs.Values) > 0 {
		return
	}
	for _, name := range vs.Names {
		obj := c.defs[name]
		if obj == nil {
			continue
		}
		if t := obj.Type(); c.md.bearing(t) && !c.exempt(i, t) {
			c.add(name, "VET-13", "var "+name.Name+" holds a machine not built with New")
		}
	}
}

func (c *checker) vet13Lit(i int, lit *ast.CompositeLit) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	if lit == nil {
		c.flt.fault("vet13Lit needs a literal")
		return
	}
	t := c.info.TypeOf(lit)
	if m := c.md.machineFor(t); m != nil {
		if m.pkg != c.pkgTypes {
			c.add(lit, "VET-13", m.named.Obj().Name()+" literal outside its package; use New")
		}
		return
	}
	if !c.md.bearing(t) || c.exempt(i, t) {
		return
	}
	switch u := t.Underlying().(type) {
	case *types.Struct:
		c.vet13Fields(lit, u)
	case *types.Array:
		if int64(len(lit.Elts)) < u.Len() {
			c.add(lit, "VET-13", "array literal leaves machine-bearing elements zero")
		}
	}
}

// vet13Fields flags a keyed or empty struct literal that omits a
// machine-bearing field; an unkeyed literal must list every field anyway.
func (c *checker) vet13Fields(lit *ast.CompositeLit, st *types.Struct) {
	if c == nil {
		return
	}
	if lit == nil {
		c.flt.fault("vet13Fields needs a literal")
		return
	}
	if st == nil {
		c.flt.fault("vet13Fields needs a struct")
		return
	}
	keys := map[string]bool{}
	for _, e := range lit.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			return
		}
		if id, ok := kv.Key.(*ast.Ident); ok {
			keys[id.Name] = true
		}
	}
	for f := range st.NumFields() {
		if field := st.Field(f); c.md.bearing(field.Type()) && !keys[field.Name()] {
			c.add(lit, "VET-13", "literal omits machine-bearing field "+field.Name())
		}
	}
}

// vet13Alloc flags new(T) and a non-empty make([]T, n) of machine-bearing T.
func (c *checker) vet13Alloc(i int, call *ast.CallExpr) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	if call == nil {
		c.flt.fault("vet13Alloc needs a call")
		return
	}
	if len(call.Args) == 0 {
		return
	}
	t := c.info.TypeOf(call.Args[0])
	if c.isBuiltin(call.Fun, "new") && c.md.bearing(t) && !c.exempt(i, t) {
		c.add(call, "VET-13", "new allocates a machine not built with New")
		return
	}
	if !c.isBuiltin(call.Fun, "make") || len(call.Args) < 2 {
		return
	}
	sl, ok := t.Underlying().(*types.Slice)
	if !ok || !c.md.bearing(sl.Elem()) {
		return
	}
	if tv, ok := c.tvs[call.Args[1]]; ok && tv.Value != nil && constant.Sign(tv.Value) == 0 {
		return
	}
	c.add(call, "VET-13", "make creates machines not built with New")
}
