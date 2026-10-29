package main

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strconv"
	"strings"
)

// vet01: a machine's fields hold no pointers, interfaces, channels, funcs,
// slices or maps, through nested structs and arrays.
func (c *checker) vet01(m *machine) {
	if c == nil {
		return
	}
	if m == nil {
		c.flt.fault("vet01 needs a machine")
		return
	}
	if m.pkg != c.pkgTypes {
		c.flt.fault("vet01 runs in the machine's package")
		return
	}
	for i := range m.fields.NumFields() {
		f := m.fields.Field(i)
		if kind := c.md.badKind(f.Type(), false); kind != "" {
			c.addPos(f.Pos(), "VET-01", "field "+f.Name()+" of "+m.named.Obj().Name()+" contains a "+kind)
		}
	}
}

// vet01Event: a struct event's fields are bounded values, so an event is a
// fixed-size value the explorer can enumerate and the key can encode.
func (c *checker) vet01Event(m *machine) {
	if c == nil {
		return
	}
	if m == nil {
		c.flt.fault("vet01Event needs a machine")
		return
	}
	if m.pkg != c.pkgTypes {
		c.flt.fault("vet01Event runs in the machine's package")
		return
	}
	ev := m.eventStruct()
	if ev == nil || ev.Obj().Pkg() != c.pkgTypes {
		return
	}
	st, ok := ev.Underlying().(*types.Struct)
	if !ok {
		c.flt.fault("eventStruct returns a struct type")
		return
	}
	if st.NumFields() == 0 || c.md.roleOf(st.Field(0).Type()) != roleEvent || !isEnum(c.flt, st.Field(0).Type()) {
		c.addPos(ev.Obj().Pos(), "VET-01", "event struct "+ev.Obj().Name()+" must start with its event kind, an integer enum")
	}
	for i := range st.NumFields() {
		f := st.Field(i)
		if kind := c.md.badKind(f.Type(), true); kind != "" {
			c.addPos(f.Pos(), "VET-01", "field "+f.Name()+" of event "+ev.Obj().Name()+" contains a "+kind)
		}
	}
}

// isEnum reports whether t is a named type over an integer.
func isEnum(flt *faults, t types.Type) bool {
	if flt == nil {
		return false
	}
	if t == nil {
		flt.fault("isEnum needs a type")
		return false
	}
	named := namedOf(flt, t)
	if named == nil {
		return false
	}
	b, ok := named.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsInteger != 0
}

// vet01Enums applies VET-01 to the enum types this package declares.
func (c *checker) vet01Enums() {
	if c == nil {
		return
	}
	if c.p == nil {
		c.flt.fault("vet01Enums needs a package")
		return
	}
	scope := c.pkgTypes.Scope()
	for _, name := range c.md.namesOf(c.pkgTypes) {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || c.md.roleOf(tn.Type()) == roleNone {
			continue
		}
		if kind := c.md.badKind(tn.Type(), false); kind != "" {
			c.addPos(tn.Pos(), "VET-01", "enum type "+name+" contains a "+kind)
		}
	}
}

// vet02: every method on a machine type has a value receiver.
func (c *checker) vet02(m *machine) {
	if c == nil {
		return
	}
	if m == nil {
		c.flt.fault("vet02 needs a machine")
		return
	}
	if m.pkg != c.pkgTypes {
		c.flt.fault("vet02 runs in the machine's package")
		return
	}
	for i := range m.named.NumMethods() {
		fn := m.named.Method(i)
		sig, ok := fn.Type().(*types.Signature)
		if !ok {
			c.flt.fault("a method's type is a signature")
			return
		}
		if _, ptr := sig.Recv().Type().(*types.Pointer); ptr {
			c.addPos(fn.Pos(), "VET-02", "method "+fn.Name()+" on "+m.named.Obj().Name()+" has a pointer receiver")
		}
	}
}

// vet09: every field of a machine type is unexported.
func (c *checker) vet09(m *machine) {
	if c == nil {
		return
	}
	if m == nil {
		c.flt.fault("vet09 needs a machine")
		return
	}
	if m.pkg != c.pkgTypes {
		c.flt.fault("vet09 runs in the machine's package")
		return
	}
	for i := range m.fields.NumFields() {
		if f := m.fields.Field(i); f.Exported() {
			c.addPos(f.Pos(), "VET-09", "field "+f.Name()+" of "+m.named.Obj().Name()+" is exported")
		}
	}
}

// vet03Zero: the zero constant of every enum type declared here is an
// unexported sentinel.
func (c *checker) vet03Zero() {
	if c == nil {
		return
	}
	if c.p == nil {
		c.flt.fault("vet03Zero needs a package")
		return
	}
	scope := c.pkgTypes.Scope()
	for _, name := range c.md.namesOf(c.pkgTypes) {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || c.md.roleOf(tn.Type()) == roleNone {
			continue
		}
		zero := c.md.zeroConst(tn)
		if zero == nil {
			c.addPos(tn.Pos(), "VET-03", "enum type "+name+" has no zero-valued sentinel constant")
		} else if zero.Exported() {
			c.addPos(zero.Pos(), "VET-03", "zero value "+zero.Name()+" of "+name+" must be an unexported sentinel")
		}
		c.vet03Num(tn)
	}
}

// vet03Num: a num count constant, if any, is its type's largest constant,
// so excluding it from switches never hides a real value.
func (c *checker) vet03Num(tn *types.TypeName) {
	if c == nil {
		return
	}
	if held := tn != nil && tn.Pkg() == c.pkgTypes; !held {
		c.flt.fault("vet03Num runs where the type is declared")
		return
	}
	consts := c.md.constsOf(tn)
	var largest *types.Const
	for _, k := range consts {
		if largest == nil || constant.Compare(k.Val(), token.GTR, largest.Val()) {
			largest = k
		}
	}
	for _, k := range consts {
		if name := k.Name(); strings.HasPrefix(name, "num") && constant.Compare(k.Val(), token.LSS, largest.Val()) {
			c.addPos(k.Pos(), "VET-03", "count constant "+name+" must be the largest constant of "+tn.Name())
		}
	}
}

// zeroConst returns the constant of tn's type whose value is zero, or nil.
func (md *model) zeroConst(tn *types.TypeName) *types.Const {
	if md == nil {
		return nil
	}
	if tn == nil {
		md.flt.fault("zeroConst needs a declared type")
		return nil
	}
	for _, k := range md.constsOf(tn) {
		if isZero(md.flt, k.Val()) {
			return k
		}
	}
	return nil
}

func isZero(flt *faults, v constant.Value) bool {
	if flt == nil {
		return false
	}
	if v == nil {
		flt.fault("isZero needs a value")
		return false
	}
	if held := v.Kind() != constant.Unknown; !held {
		flt.fault("enum constants have known values")
		return false
	}
	return v.Kind() == constant.Int && constant.Sign(v) == 0
}

// vet03Switch: a switch on an enum lists every constant except the zero
// and num sentinels, ends in a default that returns an outcome, and never
// panics: detecting an impossible value is the machine working correctly.
// listedIn reports whether a case of sw lists the value of constant k.
func (c *checker) listedIn(sw *ast.SwitchStmt, k *types.Const) bool {
	if c == nil {
		return false
	}
	if sw == nil || k == nil {
		c.flt.fault("listedIn needs a switch and a constant")
		return false
	}
	body := sw.Body
	for _, st := range body.List {
		cc, ok := st.(*ast.CaseClause)
		if !ok {
			c.flt.fault("a switch body holds case clauses")
			return false
		}
		for _, e := range cc.List {
			if tv, ok := c.tvs[e]; ok && tv.Value != nil && constant.Compare(tv.Value, token.EQL, k.Val()) {
				return true
			}
		}
	}
	return false
}

// required reports whether a switch here must list constant k: it is not
// the zero sentinel or a num count, and this package can name it.
func (c *checker) required(k *types.Const) bool {
	if c == nil {
		return false
	}
	if k == nil {
		c.flt.fault("required needs a constant")
		return false
	}
	if zero := isZero(c.flt, k.Val()); zero || strings.HasPrefix(k.Name(), "num") {
		return false
	}
	return k.Exported() || k.Pkg() == c.pkgTypes
}

// missing lists the constants sw must list and does not, for a finding.
func (c *checker) missing(sw *ast.SwitchStmt, consts []*types.Const) string {
	if c == nil {
		return ""
	}
	if sw == nil {
		c.flt.fault("missing needs a switch")
		return ""
	}
	var names []string
	for _, k := range consts {
		if c.required(k) && !c.listedIn(sw, k) {
			names = append(names, k.Name())
		}
	}
	return strings.Join(names, ", ")
}

func (c *checker) vet03Switch(i int) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	sw, ok := c.fl[i].node.(*ast.SwitchStmt)
	if !ok || sw.Tag == nil {
		return
	}
	named := namedOf(c.flt, c.info.TypeOf(sw.Tag))
	if named == nil || c.md.roleOf(named) == roleNone {
		return
	}
	consts := c.md.constsOf(named.Obj())
	complete := true
	for _, k := range consts {
		if c.required(k) && !c.listedIn(sw, k) {
			complete = false
		}
	}
	typeName := named.Obj().Name()
	if !complete {
		c.add(sw, "VET-03", "switch on "+typeName+" misses "+c.missing(sw, consts))
	}
	if !endsInReturn(c.flt, sw) {
		c.add(sw, "VET-03", "switch on "+typeName+" must end in a default that returns an outcome, not panic")
	}
	if c.panicsWithin(i) {
		c.add(sw, "VET-03", "switch on "+typeName+" must not panic; return an outcome")
	}
}

// endsInReturn reports whether the switch's last clause is a default whose
// last statement is a return.
func endsInReturn(flt *faults, sw *ast.SwitchStmt) bool {
	if flt == nil {
		return false
	}
	if sw == nil || sw.Body == nil {
		flt.fault("endsInReturn needs a switch")
		return false
	}
	body := sw.Body
	n := len(body.List)
	if n == 0 {
		return false
	}
	cc, ok := body.List[n-1].(*ast.CaseClause)
	if !ok {
		flt.fault("a switch body holds case clauses")
		return false
	}
	if cc.List != nil || len(cc.Body) == 0 {
		return false
	}
	_, ok = cc.Body[len(cc.Body)-1].(*ast.ReturnStmt)
	return ok
}

// panicsWithin reports whether the switch at entry i calls panic anywhere
// in its clauses.
func (c *checker) panicsWithin(i int) bool {
	if c == nil {
		return false
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return false
	}
	if c.fl[i].end > len(c.fl) {
		c.flt.fault("a subtree ends inside its file")
		return false
	}
	end := c.fl[i].end
	for x := i + 1; x < end; x++ {
		if call, ok := c.fl[x].node.(*ast.CallExpr); ok && c.isBuiltin(call.Fun, "panic") {
			return true
		}
	}
	return false
}

// vet04: no struct field has a mode type.
func (c *checker) vet04(i int) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	st, ok := c.fl[i].node.(*ast.StructType)
	if !ok || st.Fields == nil {
		return
	}
	fields := st.Fields
	for _, f := range fields.List {
		if c.md.roleOf(c.info.TypeOf(f.Type)) == roleMode {
			c.add(f, "VET-04", "struct field holds a mode; derive it from state instead")
		}
	}
}

// vet05: no conversion to a state, event or mode type from a non-constant.
func (c *checker) vet05(i int) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	if c.info == nil {
		c.flt.fault("vet05 needs type information")
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
	r := c.md.roleOf(fun.Type)
	if r != roleState && r != roleEvent && r != roleMode {
		return
	}
	if arg, ok := c.tvs[call.Args[0]]; ok && arg.Value == nil {
		c.add(call, "VET-05", "conversion to "+types.TypeString(fun.Type, nil)+" from a non-constant; decode with a switch")
	}
}

// vet11: no non-test file imports the merilo harness.
func (c *checker) vet11() {
	if c == nil {
		return
	}
	if c.p == nil {
		c.flt.fault("vet11 needs a package")
		return
	}
	for _, f := range c.files {
		for _, imp := range f.Imports {
			lit := imp.Path
			path, err := strconv.Unquote(lit.Value)
			if err != nil {
				c.flt.fault("an import path is a quoted string")
				return
			}
			if path == harnessPath {
				c.add(imp, "VET-11", "non-test file imports the merilo harness")
			}
		}
	}
	if len(c.files) != len(c.flat) {
		c.flt.fault("files and flattened files correspond")
		return
	}
}

// vet03Chain: an if/else if chain that compares one enum value with two or
// more of its constants is a switch instead, so the totality check sees it.
func (c *checker) vet03Chain(i int) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	ifs, ok := c.fl[i].node.(*ast.IfStmt)
	if !ok || c.chainContinues(i) {
		return
	}
	var x ast.Expr
	cur := ifs
	for n := 0; n < maxNodes && cur != nil && x == nil; n++ {
		if subject := c.enumComparison(cur.Cond); subject != nil {
			x = c.comparedAgain(cur, subject)
		}
		cur, _ = cur.Else.(*ast.IfStmt)
	}
	if cur != nil && x == nil {
		c.flt.fault("if chain longer than maxNodes")
		return
	}
	if x != nil {
		named := namedOf(c.flt, c.info.TypeOf(x))
		if named == nil {
			c.flt.fault("an enum comparison has a named type")
			return
		}
		c.add(ifs, "VET-03", "if chain compares one "+named.Obj().Name()+" with several constants; use a switch")
	}
}

// chainContinues reports whether the if statement at i is the else branch
// of another, so only the head of a chain is judged.
func (c *checker) chainContinues(i int) bool {
	if c == nil {
		return false
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return false
	}
	parent := c.fl[i].parent
	if parent < 0 {
		return false
	}
	outer, ok := c.fl[parent].node.(*ast.IfStmt)
	return ok && outer.Else == c.fl[i].node
}

// enumComparison returns x when cond is x == C or C == x, with C a constant
// of x's enum type and x not constant, else nil.
func (c *checker) enumComparison(cond ast.Expr) ast.Expr {
	if c == nil {
		return nil
	}
	if cond == nil {
		c.flt.fault("enumComparison needs a condition")
		return nil
	}
	if c.info == nil {
		c.flt.fault("enumComparison needs type information")
		return nil
	}
	bin, ok := ast.Unparen(cond).(*ast.BinaryExpr)
	if !ok || bin.Op != token.EQL {
		return nil
	}
	sides := [2]ast.Expr{bin.X, bin.Y}
	for k, x := range sides {
		tx, okx := c.tvs[x]
		ty, oky := c.tvs[sides[1-k]]
		if okx && oky && tx.Value == nil && ty.Value != nil && c.md.roleOf(tx.Type) != roleNone &&
			types.Identical(tx.Type, ty.Type) {
			return x
		}
	}
	return nil
}

// comparedAgain returns subject when a later if in the chain after from
// compares the same value with a constant, else nil.
func (c *checker) comparedAgain(from *ast.IfStmt, subject ast.Expr) ast.Expr {
	if c == nil {
		return nil
	}
	if from == nil || subject == nil {
		c.flt.fault("comparedAgain needs an if and its subject")
		return nil
	}
	cur, _ := from.Else.(*ast.IfStmt)
	for n := 0; n < maxNodes && cur != nil; n++ {
		if y := c.enumComparison(cur.Cond); y != nil && c.sameExpr(subject, y) {
			return subject
		}
		cur, _ = cur.Else.(*ast.IfStmt)
	}
	return nil
}

// vet14: a type with a method named Fire has the full machine shape. Otherwise a machine with one wrong signature or receiver would
// fall outside every rule, unreported.
func (c *checker) vet14() {
	if c == nil {
		return
	}
	if c.p == nil {
		c.flt.fault("vet14 needs a package")
		return
	}
	if c.md == nil {
		c.flt.fault("vet14 needs a model")
		return
	}
	md := c.md
	for _, tn := range md.nearMisses {
		if tn.Pkg() == c.pkgTypes {
			c.addPos(tn.Pos(), "VET-14", "type "+tn.Name()+" has a Fire method but not the machine shape: "+
				"Fire(E) (T, A, O) or Fire(E) (T, O) returning itself, and State(), on a struct")
		}
	}
}

// vet17: a World method returns a strictly comparable value. The harness
// keeps worlds in a map and compares them with ==, so a world holding a
// pointer or a channel would compare by identity, and one holding an
// interface, func, slice or map would panic.
func (c *checker) vet17() {
	if c == nil {
		return
	}
	if c.info == nil {
		c.flt.fault("vet17 needs type information")
		return
	}
	for i := range c.fl {
		fd, ok := c.fl[i].node.(*ast.FuncDecl)
		if !ok || fd.Recv == nil || fd.Body == nil {
			continue
		}
		if name := fd.Name; name.Name == "World" && c.worldMethod(fd) {
			c.vet17Returns(i)
		}
	}
}

// worldMethod reports whether fd is World() any, the System method.
func (c *checker) worldMethod(fd *ast.FuncDecl) bool {
	if c == nil {
		return false
	}
	if fd == nil {
		c.flt.fault("worldMethod needs a declaration")
		return false
	}
	fn, ok := c.defs[fd.Name].(*types.Func)
	if !ok {
		return false
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		c.flt.fault("a method's type is a signature")
		return false
	}
	if sig.Params().Len() != 0 || sig.Results().Len() != 1 {
		return false
	}
	iface, ok := sig.Results().At(0).Type().Underlying().(*types.Interface)
	return ok && iface.Empty()
}

// vet17Returns checks every value the World method at entry i returns,
// outside any function literal in it.
func (c *checker) vet17Returns(i int) {
	if c == nil {
		return
	}
	if i < 0 || i >= len(c.fl) {
		c.flt.fault("entry index out of range")
		return
	}
	end, skip := c.fl[i].end, 0
	if end > len(c.fl) {
		c.flt.fault("a subtree ends inside its file")
		return
	}
	for x := i + 1; x < end; x++ {
		if x < skip {
			continue
		}
		switch n := c.fl[x].node.(type) {
		case *ast.FuncLit:
			skip = c.fl[x].end
		case *ast.ReturnStmt:
			if len(n.Results) == 1 {
				c.vet17Value(n.Results[0])
			}
		}
	}
}

// vet17Value reports e when its static type is not strictly comparable.
func (c *checker) vet17Value(e ast.Expr) {
	if c == nil {
		return
	}
	if e == nil {
		c.flt.fault("vet17Value needs an expression")
		return
	}
	t := c.info.TypeOf(e)
	switch {
	case t == nil || c.md.hasInvalid(t):
		c.add(e, "VET-17", "World returns a value whose type merilovet cannot see; declare the world in this module")
	case types.IsInterface(t):
		c.add(e, "VET-17", "World returns an interface value; return the world itself, a struct of comparable fields")
	default:
		if kind := c.md.badKind(t, false); kind != "" {
			c.add(e, "VET-17", "World returns "+types.TypeString(t, types.RelativeTo(c.pkgTypes))+
				", which holds a "+kind+"; == on it would compare identity or panic")
		}
	}
}
