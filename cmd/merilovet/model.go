package main

import "go/types"

// maxTypeWalk bounds every walk over a type's structure.
const maxTypeWalk = 1 << 16

// role is what an enum type means to a machine.
type role int

const (
	roleNone role = iota
	roleState
	roleEvent
	roleMode
	roleOutcome
)

// machine is a type recognised by shape: a named struct with methods
// Fire(E) (T, A, O) or Fire(E) (T, O) returning itself, and State() S, on
// either receiver.
type machine struct {
	flt     *faults
	named   *types.Named
	pkg     *types.Package
	fire    *types.Func
	fields  *types.Struct
	results int        // Fire's result count: 3 with actions, 2 without
	event   types.Type // Fire's parameter: an enum, or a struct led by one
}

// model is what merilovet learned about the module's machines.
type model struct {
	flt         *faults
	machines    []*machine
	nearMisses  []*types.TypeName // types with Fire but not the machine shape (VET-14)
	roles       map[*types.TypeName]role
	machinePkgs map[*types.Package]bool
	holderPkgs  map[*types.Package]bool // decided once, when the model is built
	// Built once, so the rules allocate nothing (Rule 3): each package's
	// names in sorted order, each named type's constants in name order,
	// and the stack the type walks share.
	names  map[*types.Package][]string
	consts map[*types.TypeName][]*types.Const
	stack  []types.Type
}

// buildModel finds every machine type in the program and its enum types.
func buildModel(flt *faults, prog *program) *model {
	if flt == nil {
		return nil
	}
	if prog == nil {
		flt.fault("buildModel needs a program")
		return nil
	}
	md := &model{flt: flt, roles: map[*types.TypeName]role{}, machinePkgs: map[*types.Package]bool{},
		holderPkgs: map[*types.Package]bool{}, names: map[*types.Package][]string{},
		consts: map[*types.TypeName][]*types.Const{}, stack: make([]types.Type, 0, maxTypeWalk)}
	if prog.fsm != nil {
		md.addScope(prog.fsm)
	}
	for _, p := range prog.pkgs {
		md.addNames(p)
		md.addMachines(p)
	}
	if len(md.machinePkgs) > len(md.machines) {
		flt.fault("every machine package declares a machine")
		return nil
	}
	for _, p := range prog.pkgs {
		if holder := md.isHolder(p); holder {
			md.holderPkgs[p.types] = true
		}
	}
	return md
}

// addMachines records the machine types declared in p, in name order.
func (md *model) addMachines(p *pkg) {
	if md == nil {
		return
	}
	if p == nil || p.types == nil {
		md.flt.fault("addMachines needs a checked package")
		return
	}
	scope := p.types.Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() {
			continue
		}
		m := machineOf(md.flt, tn)
		if m == nil {
			if reservedMethod(md.flt, tn) {
				md.nearMisses = append(md.nearMisses, tn)
			}
			continue
		}
		md.machines = append(md.machines, m)
		md.machinePkgs[p.types] = true
		md.addRoles(m)
	}
}

// reservedMethod reports whether tn declares a method named Fire, which
// only machine types may have (VET-14).
func reservedMethod(flt *faults, tn *types.TypeName) bool {
	if flt == nil {
		return false
	}
	if tn == nil {
		flt.fault("reservedMethod needs a type name")
		return false
	}
	named, ok := tn.Type().(*types.Named)
	if !ok {
		return false
	}
	if held := named.Obj() == tn; !held {
		flt.fault("a named type's object is its name")
		return false
	}
	return method(flt, named, "Fire") != nil
}

// machineOf reports tn as a machine when its shape matches, else nil.
func machineOf(flt *faults, tn *types.TypeName) *machine {
	if flt == nil {
		return nil
	}
	if tn == nil {
		flt.fault("machineOf needs a type name")
		return nil
	}
	named, ok := tn.Type().(*types.Named)
	if !ok {
		return nil
	}
	st, ok := named.Underlying().(*types.Struct)
	if !ok {
		return nil
	}
	fire := method(flt, named, "Fire")
	if fire == nil || resultOf(flt, method(flt, named, "State")) == nil {
		return nil
	}
	fs, ok := fire.Type().(*types.Signature)
	if !ok {
		flt.fault("a method's type is a signature")
		return nil
	}
	n := fs.Results().Len()
	if fs.Params().Len() != 1 || (n != 2 && n != 3) || !types.Identical(fs.Results().At(0).Type(), named) {
		return nil
	}
	return &machine{flt: flt, named: named, pkg: tn.Pkg(), fire: fire, fields: st,
		results: n, event: fs.Params().At(0).Type()}
}

// method finds a method of named with either receiver. Classification uses
// the pointer method set so that a pointer-receiver Fire still makes the
// type a machine, which VET-02 then reports, instead of hiding it from
// every rule.
func method(flt *faults, named *types.Named, name string) *types.Func {
	if flt == nil {
		return nil
	}
	if named == nil {
		flt.fault("valueMethod needs a type")
		return nil
	}
	if name == "" {
		flt.fault("valueMethod needs a name")
		return nil
	}
	ms := types.NewMethodSet(types.NewPointer(named))
	for i := range ms.Len() {
		if fn, ok := ms.At(i).Obj().(*types.Func); ok && fn.Name() == name {
			return fn
		}
	}
	return nil
}

// addRoles records m's event kind, outcome, state and, when present, mode
// types.
func (md *model) addRoles(m *machine) {
	if md == nil {
		return
	}
	if m == nil {
		md.flt.fault("addRoles needs a machine")
		return
	}
	sig, ok := m.fire.Type().(*types.Signature)
	if !ok {
		md.flt.fault("Fire's type is a signature")
		return
	}
	md.setRole(eventKind(md.flt, m.event), roleEvent)
	md.setRole(sig.Results().At(m.results-1).Type(), roleOutcome)
	md.setRole(resultOf(md.flt, method(md.flt, m.named, "State")), roleState)
	md.setRole(resultOf(md.flt, method(md.flt, m.named, "Mode")), roleMode)
}

// eventKind returns the event kind type: the event type itself, or the type
// of the first field when the event is a struct.
func eventKind(flt *faults, event types.Type) types.Type {
	if flt == nil {
		return nil
	}
	if event == nil {
		flt.fault("eventKind needs a type")
		return nil
	}
	st, ok := event.Underlying().(*types.Struct)
	if !ok {
		return event
	}
	if st.NumFields() == 0 {
		return nil
	}
	if held := st.Field(0).Type() != nil; !held {
		flt.fault("a struct field has a type")
		return nil
	}
	return st.Field(0).Type()
}

// eventStruct returns m's event type when it is a struct, else nil.
func (m *machine) eventStruct() *types.Named {
	if m == nil {
		return nil
	}
	if m.event == nil {
		m.flt.fault("eventStruct needs a machine")
		return nil
	}
	named := namedOf(m.flt, m.event)
	if named == nil {
		return nil
	}
	if _, ok := named.Underlying().(*types.Struct); !ok {
		return nil
	}
	return named
}

// fireMachine returns the machine whose Fire method is fn, or nil.
func (md *model) fireMachine(fn *types.Func) *machine {
	if md == nil {
		return nil
	}
	if fn == nil {
		md.flt.fault("fireMachine needs a function")
		return nil
	}
	for _, m := range md.machines {
		if m.fire == fn {
			return m
		}
	}
	return nil
}

// resultOf returns the single result of a no-argument method, else nil.
func resultOf(flt *faults, fn *types.Func) types.Type {
	if flt == nil {
		return nil
	}
	if fn == nil {
		return nil
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		flt.fault("a method's type is a signature")
		return nil
	}
	if held := sig.Recv() != nil; !held {
		flt.fault("resultOf is used on methods")
		return nil
	}
	if sig.Params().Len() != 0 || sig.Results().Len() != 1 {
		return nil
	}
	return sig.Results().At(0).Type()
}

func (md *model) setRole(t types.Type, r role) {
	if t == nil {
		return
	}
	if md == nil {
		return
	}

	if r == roleNone {
		md.flt.fault("setRole needs a role")
		return
	}
	if named := namedOf(md.flt, t); named != nil {
		md.roles[named.Obj()] = r
	}
}

// namedOf returns t's named type, looking through aliases, or nil.
func namedOf(flt *faults, t types.Type) *types.Named {
	if flt == nil {
		return nil
	}
	if t == nil {
		return nil
	}
	named, _ := types.Unalias(t).(*types.Named)
	if held := (named == nil || named.Obj() != nil); !held {
		flt.fault("a named type has a name")
		return nil
	}
	return named
}

// machineFor returns the machine whose type is t, or nil.
func (md *model) machineFor(t types.Type) *machine {
	if t == nil {
		return nil
	}
	if md == nil {
		return nil
	}

	if md.roles == nil {
		md.flt.fault("model maps exist")
		return nil
	}
	named := namedOf(md.flt, t)
	if named == nil {
		return nil
	}
	for _, m := range md.machines {
		if m.named == named {
			return m
		}
	}
	return nil
}

// roleOf returns the enum role of t, or roleNone.
func (md *model) roleOf(t types.Type) role {
	if t == nil {
		return roleNone
	}
	if md == nil {
		return 0
	}

	if md.roles == nil {
		md.flt.fault("model maps exist")
		return 0
	}
	named := namedOf(md.flt, t)
	if named == nil {
		return roleNone
	}
	return md.roles[named.Obj()]
}

// fieldOwner returns the machine that declares field v, or nil.
func (md *model) fieldOwner(v types.Object) *machine {
	if md == nil {
		return nil
	}
	if v == nil {
		md.flt.fault("fieldOwner needs a field")
		return nil
	}
	for _, m := range md.machines {
		for i := range m.fields.NumFields() {
			if m.fields.Field(i) == v {
				return m
			}
		}
	}
	return nil
}

// addNames records p's names, sorted, and the constants of each named
// type it declares.
func (md *model) addNames(p *pkg) {
	if md == nil {
		return
	}
	if p == nil || p.types == nil {
		md.flt.fault("addNames needs a checked package")
		return
	}
	md.addScope(p.types)
}

// addScope records tp's names in sorted order and each named type's
// constants.
func (md *model) addScope(tp *types.Package) {
	if md == nil {
		return
	}
	if tp == nil {
		md.flt.fault("addScope needs a package")
		return
	}
	scope := tp.Scope()
	names := scope.Names()
	md.names[tp] = names
	for _, name := range names {
		k, ok := scope.Lookup(name).(*types.Const)
		if !ok {
			continue
		}
		if named := namedOf(md.flt, k.Type()); named != nil {
			md.consts[named.Obj()] = append(md.consts[named.Obj()], k)
		}
	}
}

// namesOf returns p's names in sorted order.
func (md *model) namesOf(p *types.Package) []string {
	if md == nil || p == nil {
		return nil
	}
	return md.names[p]
}

// constsOf returns the constants of tn's type in its package, by name.
func (md *model) constsOf(tn *types.TypeName) []*types.Const {
	if md == nil || tn == nil {
		return nil
	}
	return md.consts[tn]
}

// push adds t to a walk's stack, which never grows past the buffer the
// model allocated for it.
func (md *model) push(stack []types.Type, t types.Type) ([]types.Type, bool) {
	if md == nil {
		return stack, false
	}
	if t == nil {
		md.flt.fault("push needs a type")
		return stack, false
	}
	if len(stack) >= cap(stack) {
		md.flt.fault("type nesting deeper than maxTypeWalk")
		return stack, false
	}
	return append(stack, t), true
}

// pushParts adds the types a struct or array is built from.
func (md *model) pushParts(stack []types.Type, u types.Type) ([]types.Type, bool) {
	if md == nil {
		return stack, false
	}
	if u == nil {
		md.flt.fault("pushParts needs a type")
		return stack, false
	}
	ok := true
	switch x := u.(type) {
	case *types.Struct:
		n := x.NumFields()
		for f := 0; f < n && ok; f++ {
			stack, ok = md.push(stack, x.Field(f).Type())
		}
	case *types.Array:
		stack, ok = md.push(stack, x.Elem())
	}
	return stack, ok
}
