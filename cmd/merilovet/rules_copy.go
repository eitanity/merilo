package main

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
)

// vet17: Fire's actions and outcome go to local variables, and each is read
// later in the same block before it is assigned again.
func (c *checker) vet17(i int) {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	call, _, ok := c.fireCall(c.fl[i].node)
	if !ok || c.inMachineMethod(i) {
		return
	}
	parent := c.fl[i].parent
	assert(parent >= 0, "a call always has a parent")
	as, ok := c.fl[parent].node.(*ast.AssignStmt)
	if !ok || len(as.Lhs) != 3 || len(as.Rhs) != 1 || as.Rhs[0] != call {
		c.add(call, "VET-17", "the actions and outcome of Fire must go to local variables")
		return
	}
	for _, lhs := range as.Lhs[1:] {
		c.vet17Result(parent, lhs)
	}
}

// vet17Result checks one result variable of the Fire assignment at stmt.
func (c *checker) vet17Result(stmt int, lhs ast.Expr) {
	assert(stmt >= 0 && stmt < len(c.fl), "statement index out of range")
	assert(lhs != nil, "vet17Result needs an expression")
	id, ok := ast.Unparen(lhs).(*ast.Ident)
	if !ok || id.Name == "_" || !c.isLocal(c.p.info.ObjectOf(id)) {
		c.add(lhs, "VET-17", "a Fire result must go to a local variable")
		return
	}
	obj := c.p.info.ObjectOf(id)
	block := c.fl[stmt].parent
	assert(block >= 0, "a statement always has a parent")
	stop := c.fl[block].end
	for x := c.fl[stmt].end; x < stop; x++ {
		if _, lit := c.fl[x].node.(*ast.FuncLit); lit {
			x = c.fl[x].end - 1
			continue
		}
		ref, ok := c.fl[x].node.(*ast.Ident)
		if ok && c.p.info.Uses[ref] == obj {
			c.vet17Use(x, id)
			return
		}
	}
	c.add(id, "VET-17", id.Name+" from Fire is never read in its block")
}

// vet17Use judges the first mention after the Fire assignment: a plain
// read passes, a reassignment or a discard with _ = x does not.
func (c *checker) vet17Use(x int, id *ast.Ident) {
	assert(x >= 0 && x < len(c.fl), "entry index out of range")
	assert(id != nil, "vet17Use needs the assigned identifier")
	ref, ok := c.fl[x].node.(*ast.Ident)
	assert(ok, "vet17Use is called on an identifier")
	as, ok := c.fl[c.fl[x].parent].node.(*ast.AssignStmt)
	if !ok {
		return
	}
	if len(as.Lhs) == 1 && isBlank(as.Lhs[0]) && len(as.Rhs) == 1 && ast.Unparen(as.Rhs[0]) == ref {
		c.add(as, "VET-17", id.Name+" from Fire is discarded with _ =")
		return
	}
	if containsExpr(as.Lhs, ref) {
		c.add(id, "VET-17", id.Name+" from Fire is assigned again before it is read")
	}
}

// vet21: no existing machine-bearing value is copied, and a fresh one never
// overwrites a live location except by map insert.
func (c *checker) vet21(i int) {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	assert(c.p.info != nil, "vet21 needs type information")
	switch x := c.fl[i].node.(type) {
	case *ast.AssignStmt:
		c.vet21Overwrite(i, x)
	case *ast.RangeStmt:
		c.vet21Range(i, x)
	case *ast.FuncDecl:
		c.vet21Receiver(x)
	case ast.Expr:
		c.vet21Copy(i, x)
	}
}

func (c *checker) vet21Copy(i int, e ast.Expr) {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	assert(e != nil, "vet21Copy needs an expression")
	if !c.existing(e) {
		return
	}
	t := c.p.info.TypeOf(e)
	if !c.md.bearing(t) || c.exempt(i, t) || !c.copyContext(c.fl[i].parent, e) {
		return
	}
	c.add(e, "VET-21", "copy of an existing "+types.TypeString(t, types.RelativeTo(c.p.types)))
}

// existing reports whether e denotes a value already stored somewhere, as
// opposed to a fresh literal or call result.
func (c *checker) existing(e ast.Expr) bool {
	assert(e != nil, "existing needs an expression")
	assert(c.p.info != nil, "existing needs type information")
	if tv, ok := c.p.info.Types[e]; ok && !tv.IsValue() {
		return false
	}
	switch x := ast.Unparen(e).(type) {
	case *ast.Ident:
		_, ok := c.p.info.Uses[x].(*types.Var)
		return ok
	case *ast.SelectorExpr:
		if s, ok := c.p.info.Selections[x]; ok {
			return s.Kind() == types.FieldVal
		}
		_, ok := c.p.info.Uses[x.Sel].(*types.Var)
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
	assert(parent >= 0 && parent < len(c.fl), "parent index out of range")
	assert(child != nil, "copyContext needs an expression")
	switch p := c.fl[parent].node.(type) {
	case *ast.AssignStmt:
		return containsExpr(p.Rhs, child)
	case *ast.ValueSpec:
		return containsExpr(p.Values, child)
	case *ast.ReturnStmt:
		return containsExpr(p.Results, child)
	case *ast.CallExpr:
		return containsExpr(p.Args, child) && !c.isBuiltin(p.Fun, "len") && !c.isBuiltin(p.Fun, "cap")
	case *ast.CompositeLit:
		return containsExpr(p.Elts, child)
	case *ast.KeyValueExpr:
		return p.Value == child
	case *ast.SendStmt:
		return p.Value == child
	}
	return false
}

// vet21Overwrite flags a fresh value assigned over a live machine-bearing
// location; a reset is a transition, and only a map insert is allowed.
func (c *checker) vet21Overwrite(i int, as *ast.AssignStmt) {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	assert(as != nil && len(as.Rhs) > 0, "vet21Overwrite needs an assignment")
	if as.Tok != token.ASSIGN {
		return
	}
	for k, lhs := range as.Lhs {
		t := c.p.info.TypeOf(lhs)
		if isBlank(lhs) || !c.md.bearing(t) || c.exempt(i, t) || c.isMapIndex(lhs) {
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
			c.add(lhs, "VET-21", "fresh value overwrites a live machine; a reset is a transition")
		}
	}
}

// fresh reports whether e makes a new value: a literal or a function call.
func (c *checker) fresh(e ast.Expr) bool {
	assert(e != nil, "fresh needs an expression")
	assert(c.p.info != nil, "fresh needs type information")
	switch x := ast.Unparen(e).(type) {
	case *ast.CompositeLit:
		return true
	case *ast.CallExpr:
		fun, ok := c.p.info.Types[x.Fun]
		return !ok || !fun.IsType()
	}
	return false
}

func (c *checker) isMapIndex(e ast.Expr) bool {
	assert(e != nil, "isMapIndex needs an expression")
	assert(c.p.info != nil, "isMapIndex needs type information")
	ix, ok := ast.Unparen(e).(*ast.IndexExpr)
	if !ok {
		return false
	}
	_, isMap := c.p.info.TypeOf(ix.X).Underlying().(*types.Map)
	return isMap
}

// vet21Range flags a range value variable that copies machine-bearing elements.
func (c *checker) vet21Range(i int, rs *ast.RangeStmt) {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	assert(rs != nil, "vet21Range needs a range statement")
	if rs.Value == nil || isBlank(rs.Value) {
		return
	}
	t := c.p.info.TypeOf(rs.Value)
	if c.md.bearing(t) && !c.exempt(i, t) {
		c.add(rs.Value, "VET-21", "range value variable copies each machine-bearing element; loop by index")
	}
}

// vet21Receiver flags a value receiver on a machine-bearing type other than
// the machine itself.
func (c *checker) vet21Receiver(fd *ast.FuncDecl) {
	assert(fd != nil, "vet21Receiver needs a declaration")
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return
	}
	field := fd.Recv.List[0]
	t := c.p.info.TypeOf(field.Type)
	assert(t != nil, "a receiver has a type")
	if _, ptr := t.(*types.Pointer); ptr {
		return
	}
	if c.md.bearing(t) && c.md.machineFor(t) == nil {
		c.add(field, "VET-21", "value receiver copies a machine-bearing value; use a pointer receiver")
	}
}

// vet22: no machine-bearing value is created implicitly.
func (c *checker) vet22(i int) {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	assert(c.p.info != nil, "vet22 needs type information")
	switch x := c.fl[i].node.(type) {
	case *ast.ValueSpec:
		c.vet22Var(i, x)
	case *ast.CompositeLit:
		c.vet22Lit(i, x)
	case *ast.CallExpr:
		c.vet22Alloc(i, x)
	}
}

func (c *checker) vet22Var(i int, vs *ast.ValueSpec) {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	assert(vs != nil, "vet22Var needs a value spec")
	if len(vs.Values) > 0 {
		return
	}
	for _, name := range vs.Names {
		obj := c.p.info.Defs[name]
		if obj == nil {
			continue
		}
		if t := obj.Type(); c.md.bearing(t) && !c.exempt(i, t) {
			c.add(name, "VET-22", "var "+name.Name+" holds a machine not built with New")
		}
	}
}

func (c *checker) vet22Lit(i int, lit *ast.CompositeLit) {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	assert(lit != nil, "vet22Lit needs a literal")
	t := c.p.info.TypeOf(lit)
	if m := c.md.machineFor(t); m != nil {
		if m.pkg != c.p.types {
			c.add(lit, "VET-22", m.named.Obj().Name()+" literal outside its package; use New")
		}
		return
	}
	if !c.md.bearing(t) || c.exempt(i, t) {
		return
	}
	switch u := t.Underlying().(type) {
	case *types.Struct:
		c.vet22Fields(lit, u)
	case *types.Array:
		if int64(len(lit.Elts)) < u.Len() {
			c.add(lit, "VET-22", "array literal leaves machine-bearing elements zero")
		}
	}
}

// vet22Fields flags a keyed or empty struct literal that omits a
// machine-bearing field; an unkeyed literal must list every field anyway.
func (c *checker) vet22Fields(lit *ast.CompositeLit, st *types.Struct) {
	assert(lit != nil, "vet22Fields needs a literal")
	assert(st != nil, "vet22Fields needs a struct")
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
			c.add(lit, "VET-22", "literal omits machine-bearing field "+field.Name())
		}
	}
}

// vet22Alloc flags new(T) and a non-empty make([]T, n) of machine-bearing T.
func (c *checker) vet22Alloc(i int, call *ast.CallExpr) {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	assert(call != nil, "vet22Alloc needs a call")
	if len(call.Args) == 0 {
		return
	}
	t := c.p.info.TypeOf(call.Args[0])
	if c.isBuiltin(call.Fun, "new") && c.md.bearing(t) && !c.exempt(i, t) {
		c.add(call, "VET-22", "new allocates a machine not built with New")
		return
	}
	if !c.isBuiltin(call.Fun, "make") || len(call.Args) < 2 {
		return
	}
	sl, ok := t.Underlying().(*types.Slice)
	if !ok || !c.md.bearing(sl.Elem()) {
		return
	}
	if tv, ok := c.p.info.Types[call.Args[1]]; ok && tv.Value != nil && constant.Sign(tv.Value) == 0 {
		return
	}
	c.add(call, "VET-22", "make creates machines not built with New")
}
