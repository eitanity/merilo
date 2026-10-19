package main

import (
	"go/ast"
	"go/constant"
	"go/types"
	"sort"
	"strconv"
	"strings"
)

// vet02: a machine's fields hold no pointers, interfaces, channels, funcs,
// slices or maps, through nested structs and arrays.
func (c *checker) vet02(m *machine) {
	assert(m != nil, "vet02 needs a machine")
	assert(m.pkg == c.p.types, "vet02 runs in the machine's package")
	for i := range m.fields.NumFields() {
		f := m.fields.Field(i)
		if kind := badKind(f.Type()); kind != "" {
			c.addPos(f.Pos(), "VET-02", "field "+f.Name()+" of "+m.named.Obj().Name()+" contains a "+kind)
		}
	}
}

// vet02Enums applies VET-02 to the enum types this package declares.
func (c *checker) vet02Enums() {
	assert(c.p != nil, "vet02Enums needs a package")
	scope := c.p.types.Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || c.md.roles[tn] == roleNone {
			continue
		}
		if kind := badKind(tn.Type()); kind != "" {
			c.addPos(tn.Pos(), "VET-02", "enum type "+name+" contains a "+kind)
		}
	}
	assert(scope.Len() >= 0, "scope size is never negative")
}

// vet03: every method on a machine type has a value receiver.
func (c *checker) vet03(m *machine) {
	assert(m != nil, "vet03 needs a machine")
	assert(m.pkg == c.p.types, "vet03 runs in the machine's package")
	for i := range m.named.NumMethods() {
		fn := m.named.Method(i)
		sig, ok := fn.Type().(*types.Signature)
		assert(ok, "a method's type is a signature")
		if _, ptr := sig.Recv().Type().(*types.Pointer); ptr {
			c.addPos(fn.Pos(), "VET-03", "method "+fn.Name()+" on "+m.named.Obj().Name()+" has a pointer receiver")
		}
	}
}

// vet15: every field of a machine type is unexported.
func (c *checker) vet15(m *machine) {
	assert(m != nil, "vet15 needs a machine")
	assert(m.pkg == c.p.types, "vet15 runs in the machine's package")
	for i := range m.fields.NumFields() {
		if f := m.fields.Field(i); f.Exported() {
			c.addPos(f.Pos(), "VET-15", "field "+f.Name()+" of "+m.named.Obj().Name()+" is exported")
		}
	}
}

// vet08Zero: the zero constant of every enum type declared here is an
// unexported sentinel.
func (c *checker) vet08Zero() {
	assert(c.p != nil, "vet08Zero needs a package")
	scope := c.p.types.Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || c.md.roles[tn] == roleNone {
			continue
		}
		zero := zeroConst(tn)
		if zero == nil {
			c.addPos(tn.Pos(), "VET-08", "enum type "+name+" has no zero-valued sentinel constant")
		} else if zero.Exported() {
			c.addPos(zero.Pos(), "VET-08", "zero value "+zero.Name()+" of "+name+" must be an unexported sentinel")
		}
	}
	assert(scope.Len() >= 0, "scope size is never negative")
}

// zeroConst returns the constant of tn's type whose value is zero, or nil.
func zeroConst(tn *types.TypeName) *types.Const {
	assert(tn != nil && tn.Pkg() != nil, "zeroConst needs a declared type")
	scope := tn.Pkg().Scope()
	assert(scope != nil, "a package has a scope")
	for _, name := range scope.Names() {
		k, ok := scope.Lookup(name).(*types.Const)
		if ok && types.Identical(k.Type(), tn.Type()) && isZero(k.Val()) {
			return k
		}
	}
	return nil
}

func isZero(v constant.Value) bool {
	assert(v != nil, "isZero needs a value")
	assert(v.Kind() != constant.Unknown, "enum constants have known values")
	return v.Kind() == constant.Int && constant.Sign(v) == 0
}

// vet08Switch: a switch on an enum lists every constant except the zero
// and num sentinels and ends in default: panic.
func (c *checker) vet08Switch(i int) {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	sw, ok := c.fl[i].node.(*ast.SwitchStmt)
	if !ok || sw.Tag == nil {
		return
	}
	named := namedOf(c.p.info.TypeOf(sw.Tag))
	if named == nil || c.md.roleOf(named) == roleNone {
		return
	}
	got := map[string]bool{}
	for _, st := range sw.Body.List {
		cc, ok := st.(*ast.CaseClause)
		assert(ok, "a switch body holds case clauses")
		for _, e := range cc.List {
			if tv, ok := c.p.info.Types[e]; ok && tv.Value != nil {
				got[tv.Value.ExactString()] = true
			}
		}
	}
	var missing []string
	for value, name := range c.enumConsts(named) {
		if !got[value] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	typeName := named.Obj().Name()
	if len(missing) > 0 {
		c.add(sw, "VET-08", "switch on "+typeName+" misses "+strings.Join(missing, ", "))
	}
	if !c.endsInPanic(sw) {
		c.add(sw, "VET-08", "switch on "+typeName+" must end in default: panic")
	}
}

// enumConsts maps each constant of named a switch here must list, by exact
// value, to its name: not zero, not a num sentinel, and nameable here.
func (c *checker) enumConsts(named *types.Named) map[string]string {
	assert(named != nil && named.Obj().Pkg() != nil, "enumConsts needs a declared type")
	out := map[string]string{}
	scope := named.Obj().Pkg().Scope()
	for _, name := range scope.Names() {
		k, ok := scope.Lookup(name).(*types.Const)
		if !ok || !types.Identical(k.Type(), named) || isZero(k.Val()) || strings.HasPrefix(name, "num") {
			continue
		}
		if !k.Exported() && k.Pkg() != c.p.types {
			continue
		}
		out[k.Val().ExactString()] = name
	}
	assert(len(out) <= scope.Len(), "more constants than scope entries")
	return out
}

// endsInPanic reports whether the switch's last clause is default: panic(...).
func (c *checker) endsInPanic(sw *ast.SwitchStmt) bool {
	assert(sw != nil && sw.Body != nil, "endsInPanic needs a switch")
	n := len(sw.Body.List)
	if n == 0 {
		return false
	}
	cc, ok := sw.Body.List[n-1].(*ast.CaseClause)
	assert(ok, "a switch body holds case clauses")
	if cc.List != nil || len(cc.Body) != 1 {
		return false
	}
	es, ok := cc.Body[0].(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := es.X.(*ast.CallExpr)
	return ok && c.isBuiltin(call.Fun, "panic")
}

// vet10: no struct field has a mode type.
func (c *checker) vet10(i int) {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	st, ok := c.fl[i].node.(*ast.StructType)
	if !ok || st.Fields == nil {
		return
	}
	for _, f := range st.Fields.List {
		if c.md.roleOf(c.p.info.TypeOf(f.Type)) == roleMode {
			c.add(f, "VET-10", "struct field holds a mode; derive it from state instead")
		}
	}
	assert(st.Fields.NumFields() >= 0, "field count is never negative")
}

// vet11: no conversion to a state, event or mode type from a non-constant.
func (c *checker) vet11(i int) {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	assert(c.p.info != nil, "vet11 needs type information")
	call, ok := c.fl[i].node.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return
	}
	fun, ok := c.p.info.Types[call.Fun]
	if !ok || !fun.IsType() {
		return
	}
	r := c.md.roleOf(fun.Type)
	if r != roleState && r != roleEvent && r != roleMode {
		return
	}
	if arg, ok := c.p.info.Types[call.Args[0]]; ok && arg.Value == nil {
		c.add(call, "VET-11", "conversion to "+types.TypeString(fun.Type, nil)+" from a non-constant; decode with a switch")
	}
}

// vet16: AppendKey reads every field of its machine through the receiver.
func (c *checker) vet16(i int) {
	assert(i >= 0 && i < len(c.fl), "entry index out of range")
	assert(c.p.info != nil, "vet16 needs type information")
	fd, ok := c.fl[i].node.(*ast.FuncDecl)
	if !ok || fd.Body == nil {
		return
	}
	m := c.recvMachine(fd)
	if m == nil || c.p.info.Defs[fd.Name] != m.appendKey {
		return
	}
	var recv types.Object
	if names := fd.Recv.List[0].Names; len(names) == 1 {
		recv = c.p.info.Defs[names[0]]
	}
	read := map[types.Object]bool{}
	for x := i + 1; x < c.fl[i].end; x++ {
		sel, ok := c.fl[x].node.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		id, ok := ast.Unparen(sel.X).(*ast.Ident)
		if s, found := c.p.info.Selections[sel]; ok && found && recv != nil && c.p.info.Uses[id] == recv {
			read[s.Obj()] = true
		}
	}
	for f := range m.fields.NumFields() {
		if field := m.fields.Field(f); !read[field] {
			c.add(fd.Name, "VET-16", "AppendKey does not read field "+field.Name())
		}
	}
}

// vet20: no non-test file imports the merilo harness.
func (c *checker) vet20() {
	assert(c.p != nil, "vet20 needs a package")
	for _, f := range c.p.files {
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			assert(err == nil, "an import path is a quoted string")
			if path == harnessPath {
				c.add(imp, "VET-20", "non-test file imports the merilo harness")
			}
		}
	}
	assert(len(c.p.files) == len(c.p.flat), "files and flattened files correspond")
}
