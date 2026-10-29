package main

import (
	"go/ast"
	"go/types"
)

// Integer widths in bytes. int, uint and uintptr are 4 bytes on 386,
// arm, mips and TinyGo's 32-bit targets and 8 on 64-bit ones, so a
// conversion is judged at its worst case: the source at its widest, the
// target at its narrowest.
const (
	platformWide   = 8
	platformNarrow = 4
)

// vet15Fields: a machine's fields and its event payload hold no integer
// narrower than 64 bits unless it is a closed enum. A uint8 counter or
// generation wraps silently, and a wrapped value can match an old one.
func (c *checker) vet15Fields(m *machine) {
	if c == nil {
		return
	}
	if m == nil {
		c.flt.fault("vet15Fields needs a machine")
		return
	}
	if m.pkg != c.pkgTypes {
		c.flt.fault("vet15Fields runs in the machine's package")
		return
	}
	c.narrowFields(m.fields, m.named.Obj().Name())
	ev := m.eventStruct()
	if ev == nil || ev.Obj().Pkg() != c.pkgTypes {
		return
	}
	st, ok := ev.Underlying().(*types.Struct)
	if !ok {
		c.flt.fault("eventStruct returns a struct type")
		return
	}
	c.narrowFields(st, ev.Obj().Name())
}

// narrowFields reports each field of st that holds a narrow integer.
func (c *checker) narrowFields(st *types.Struct, owner string) {
	if c == nil {
		return
	}
	if st == nil {
		c.flt.fault("narrowFields needs a struct")
		return
	}
	if owner == "" {
		c.flt.fault("narrowFields needs the owner's name")
		return
	}
	for i := range st.NumFields() {
		f := st.Field(i)
		if t := c.md.narrowIn(f.Type()); t != nil {
			c.addPos(f.Pos(), "VET-15", "field "+f.Name()+" of "+owner+" holds "+types.TypeString(t, nil)+
				", which wraps silently on some target; use int64 or uint64, or a closed enum")
		}
	}
}

// narrowIn returns the first narrow integer type within t, looking through
// arrays and nested structs but not into closed enums, or nil.
func (md *model) narrowIn(t types.Type) types.Type {
	if md == nil {
		return nil
	}
	if t == nil {
		md.flt.fault("narrowIn needs a type")
		return nil
	}
	stack, ok := md.push(md.stack[:0], t)
	for i := 0; i < maxTypeWalk && ok && len(stack) > 0; i++ {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if md.closedEnum(x) {
			continue
		}
		if u, isBasic := x.Underlying().(*types.Basic); isBasic && narrowInt(md.flt, u) {
			return x
		}
		stack, ok = md.pushParts(stack, x.Underlying())
	}
	if ok && len(stack) != 0 {
		md.flt.fault("type deeper than maxTypeWalk")
	}
	return nil
}

// narrowInt reports whether b is an integer that can wrap at a size a
// protocol reaches on some target: anything but int64 and uint64. int and
// uint are 32 bits on 386, arm and TinyGo's 32-bit targets, so they count
// as narrow.
func narrowInt(flt *faults, b *types.Basic) bool {
	if flt == nil {
		return false
	}
	if b == nil {
		flt.fault("narrowInt needs a basic type")
		return false
	}
	if held := b.Kind() != types.Invalid; !held {
		flt.fault("narrowInt needs a valid type")
		return false
	}
	if b.Info()&types.IsInteger == 0 {
		return false
	}
	switch b.Kind() {
	case types.Int64, types.Uint64, types.UntypedInt:
		return false
	default:
		return true
	}
}

// closedEnum reports whether t is a named integer type with at least one
// constant of its own type declared beside it: a set of values, not a
// quantity.
func (md *model) closedEnum(t types.Type) bool {
	if md == nil {
		return false
	}
	if t == nil {
		md.flt.fault("closedEnum needs a type")
		return false
	}
	named := namedOf(md.flt, t)
	if named == nil || !isEnum(md.flt, named) {
		return false
	}
	return len(md.constsOf(named.Obj())) > 0
}

// vet15Convert: in a machine package, a non-constant integer that is not
// a closed enum is never converted to a narrower integer type. The
// conversion truncates without a word, as uint8(len(x)) does.
func (c *checker) vet15Convert(i int) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	if c.info == nil {
		c.flt.fault("vet15Convert needs type information")
		return
	}
	call, ok := c.fl[i].node.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return
	}
	fun, ok := c.tvs[call.Fun]
	if !ok || !fun.IsType() {
		return
	}
	arg, ok := c.tvs[call.Args[0]]
	if !ok || arg.Value != nil || c.md.closedEnum(arg.Type) || c.md.closedEnum(fun.Type) {
		return
	}
	if narrowing(c.flt, arg.Type, fun.Type) {
		c.add(call, "VET-15", "conversion of "+types.TypeString(arg.Type, nil)+" to "+types.TypeString(fun.Type, nil)+
			" truncates silently; keep the wider type, or encode with binary.AppendVarint")
	}
}

// narrowing reports whether converting an integer of type from to type to
// can lose bits.
func narrowing(flt *faults, from, to types.Type) bool {
	if flt == nil {
		return false
	}
	if from == nil || to == nil {
		flt.fault("narrowing needs two types")
		return false
	}
	fb, ok := from.Underlying().(*types.Basic)
	if !ok || fb.Info()&types.IsInteger == 0 {
		return false
	}
	tb, ok := to.Underlying().(*types.Basic)
	if !ok || tb.Info()&types.IsInteger == 0 {
		return false
	}
	narrowest, widest := byteWidth(flt, tb, false), byteWidth(flt, tb, true)
	if narrowest > widest {
		flt.fault("a type's narrowest width is at most its widest")
		return false
	}
	return narrowest < byteWidth(flt, fb, true)
}

// byteWidth is b's size in bytes; for the platform-sized integers it is
// the widest size when widest is set, else the narrowest.
func byteWidth(flt *faults, b *types.Basic, widest bool) int {
	if flt == nil {
		return 0
	}
	if b == nil {
		flt.fault("byteWidth needs a basic type")
		return 0
	}
	if held := b.Info()&types.IsInteger != 0; !held {
		flt.fault("byteWidth needs an integer")
		return 0
	}
	switch b.Kind() {
	case types.Int, types.Uint, types.Uintptr:
		if widest {
			return platformWide
		}
		return platformNarrow
	case types.Int8, types.Uint8:
		return 1
	case types.Int16, types.Uint16:
		return 2
	case types.Int32, types.Uint32:
		return 4
	default:
		return platformWide
	}
}

// vet16: in a machine package, arithmetic that changes a machine field,
// x++, x--, or x op= y for + - * <<, comes after a comparison of x, and
// of a non-constant y, against a constant earlier in the same function.
// Go wraps silently, so the bound has to be checked by hand; this is the
// syntactic part of that check, not a proof.
func (c *checker) vet16(i int) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	if c.info == nil {
		c.flt.fault("vet16 needs type information")
		return
	}
	target, operand := c.arithmetic(c.fl[i].node)
	if target == nil || c.fl[i].fn < 0 || !c.machineField(target) {
		return
	}
	if !c.comparedBefore(i, target) {
		c.add(c.fl[i].node, "VET-16", "arithmetic on a machine field with no earlier bound check on it; compare it with a constant first")
		return
	}
	if operand != nil && c.tvs[operand].Value == nil && !c.comparedBefore(i, operand) {
		c.add(c.fl[i].node, "VET-16", "arithmetic on a machine field by a value with no earlier bound check; compare the value with a constant first")
	}
}

// arithmetic returns the location an increment, a compound assignment or
// an assignment of x op y to x changes, and its operand when it has one.
func (c *checker) arithmetic(n ast.Node) (target, operand ast.Expr) {
	if c == nil {
		return nil, nil
	}
	if n == nil {
		c.flt.fault("arithmetic needs a node")
		return nil, nil
	}
	switch s := n.(type) {
	case *ast.IncDecStmt:
		if held := (s.Tok.String() == "++" || s.Tok.String() == "--"); !held {
			c.flt.fault("an inc-dec statement increments or decrements")
			return nil, nil
		}
		return s.X, nil
	case *ast.AssignStmt:
		if len(s.Lhs) != len(s.Rhs) {
			return nil, nil
		}
		switch s.Tok.String() {
		case "+=", "-=", "*=", "<<=":
			if len(s.Lhs) == 1 {
				return s.Lhs[0], s.Rhs[0]
			}
		case "=":
			return c.selfArithmetic(s)
		}
	}
	return nil, nil
}

// selfArithmetic finds x = x op y, or x = y op x, among the pairs of an
// assignment, and returns x and y.
func (c *checker) selfArithmetic(s *ast.AssignStmt) (target, operand ast.Expr) {
	if c == nil {
		return nil, nil
	}
	if s == nil || len(s.Lhs) != len(s.Rhs) {
		c.flt.fault("selfArithmetic needs paired sides")
		return nil, nil
	}
	for k := range s.Lhs {
		be, ok := ast.Unparen(s.Rhs[k]).(*ast.BinaryExpr)
		if !ok {
			continue
		}
		switch be.Op.String() {
		case "+", "-", "*", "<<":
		default:
			continue
		}
		switch {
		case c.sameExpr(be.X, s.Lhs[k]):
			return s.Lhs[k], be.Y
		case c.sameExpr(be.Y, s.Lhs[k]):
			return s.Lhs[k], be.X
		}
	}
	if len(s.Lhs) == 0 {
		c.flt.fault("an assignment has a left side")
		return nil, nil
	}
	return nil, nil
}

// machineField reports whether e selects a field of one of this package's
// machine types.
func (c *checker) machineField(e ast.Expr) bool {
	if c == nil {
		return false
	}
	if e == nil {
		c.flt.fault("machineField needs an expression")
		return false
	}
	sel, ok := ast.Unparen(e).(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if c.sels == nil {
		c.flt.fault("type information records selections")
		return false
	}
	s, ok := c.sels[sel]
	if !ok || s.Kind() != types.FieldVal {
		return false
	}
	md := c.md
	for _, m := range md.machines {
		if m.pkg != c.pkgTypes {
			continue
		}
		for j := range m.fields.NumFields() {
			if m.fields.Field(j) == s.Obj() {
				return true
			}
		}
	}
	return false
}

// comparedBefore reports whether, in the function holding entry i and
// before it, e is compared with a constant.
func (c *checker) comparedBefore(i int, e ast.Expr) bool {
	if e == nil {
		return false
	}
	if c == nil {
		return false
	}
	fn := c.fl[i].fn
	if fn < 0 || fn >= i {
		c.flt.fault("comparedBefore needs an enclosing function")
		return false
	}
	for x := fn + 1; x < i; x++ {
		be, ok := c.fl[x].node.(*ast.BinaryExpr)
		if !ok || !comparison(c.flt, be.Op.String()) {
			continue
		}
		if (c.sameExpr(be.X, e) && c.constant(be.Y)) || (c.sameExpr(be.Y, e) && c.constant(be.X)) {
			return true
		}
	}
	if fn >= len(c.fl) {
		c.flt.fault("function entry in range")
		return false
	}
	return false
}

func comparison(flt *faults, op string) bool {
	if flt == nil {
		return false
	}
	if op == "" {
		flt.fault("comparison needs an operator")
		return false
	}
	switch op {
	case "<", "<=", ">", ">=", "==", "!=":
		return true
	default:
		return false
	}
}

// constant reports whether e has a constant value.
func (c *checker) constant(e ast.Expr) bool {
	if c == nil {
		return false
	}
	if e == nil {
		c.flt.fault("constant needs an expression")
		return false
	}
	tv, ok := c.tvs[e]
	if ok && tv.Type == nil {
		c.flt.fault("a typed expression has a type")
		return false
	}
	return ok && tv.Value != nil
}
