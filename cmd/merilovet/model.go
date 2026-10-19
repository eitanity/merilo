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

// machine is a type recognised by shape: a named struct whose value method
// set has Fire(E) (T, A, O) returning itself, and AppendKey([]byte) []byte.
type machine struct {
	named     *types.Named
	pkg       *types.Package
	fire      *types.Func
	appendKey *types.Func
	fields    *types.Struct
}

// model is what merilovet learned about the module's machines.
type model struct {
	machines    []*machine
	roles       map[*types.TypeName]role
	machinePkgs map[*types.Package]bool
}

// buildModel finds every machine type in the program and its enum types.
func buildModel(prog *program) *model {
	assert(prog != nil, "buildModel needs a program")
	md := &model{roles: map[*types.TypeName]role{}, machinePkgs: map[*types.Package]bool{}}
	for _, p := range prog.pkgs {
		md.addMachines(p)
	}
	assert(len(md.machinePkgs) <= len(md.machines), "every machine package declares a machine")
	return md
}

// addMachines records the machine types declared in p, in name order.
func (md *model) addMachines(p *pkg) {
	assert(md != nil, "addMachines needs a model")
	assert(p != nil && p.types != nil, "addMachines needs a checked package")
	scope := p.types.Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() {
			continue
		}
		m := machineOf(tn)
		if m == nil {
			continue
		}
		md.machines = append(md.machines, m)
		md.machinePkgs[p.types] = true
		md.addRoles(m)
	}
}

// machineOf reports tn as a machine when its shape matches, else nil.
func machineOf(tn *types.TypeName) *machine {
	assert(tn != nil, "machineOf needs a type name")
	named, ok := tn.Type().(*types.Named)
	if !ok {
		return nil
	}
	st, ok := named.Underlying().(*types.Struct)
	if !ok {
		return nil
	}
	fire := valueMethod(named, "Fire")
	key := valueMethod(named, "AppendKey")
	if fire == nil || key == nil {
		return nil
	}
	fs, ok := fire.Type().(*types.Signature)
	assert(ok, "a method's type is a signature")
	if fs.Params().Len() != 1 || fs.Results().Len() != 3 || !types.Identical(fs.Results().At(0).Type(), named) {
		return nil
	}
	ks, ok := key.Type().(*types.Signature)
	assert(ok, "a method's type is a signature")
	bytesType := types.NewSlice(types.Typ[types.Byte])
	if ks.Params().Len() != 1 || ks.Results().Len() != 1 ||
		!types.Identical(ks.Params().At(0).Type(), bytesType) ||
		!types.Identical(ks.Results().At(0).Type(), bytesType) {
		return nil
	}
	return &machine{named: named, pkg: tn.Pkg(), fire: fire, appendKey: key, fields: st}
}

// valueMethod finds a method in the value method set of named, so pointer
// receiver methods never count.
func valueMethod(named *types.Named, name string) *types.Func {
	assert(named != nil, "valueMethod needs a type")
	assert(name != "", "valueMethod needs a name")
	ms := types.NewMethodSet(named)
	for i := range ms.Len() {
		if fn, ok := ms.At(i).Obj().(*types.Func); ok && fn.Name() == name {
			return fn
		}
	}
	return nil
}

// addRoles records m's event, outcome, state and mode types.
func (md *model) addRoles(m *machine) {
	assert(md != nil, "addRoles needs a model")
	assert(m != nil, "addRoles needs a machine")
	sig, ok := m.fire.Type().(*types.Signature)
	assert(ok, "Fire's type is a signature")
	md.setRole(sig.Params().At(0).Type(), roleEvent)
	md.setRole(sig.Results().At(2).Type(), roleOutcome)
	md.setRole(resultOf(valueMethod(m.named, "State")), roleState)
	md.setRole(resultOf(valueMethod(m.named, "Mode")), roleMode)
}

// resultOf returns the single result of a no-argument method, else nil.
func resultOf(fn *types.Func) types.Type {
	if fn == nil {
		return nil
	}
	sig, ok := fn.Type().(*types.Signature)
	assert(ok, "a method's type is a signature")
	assert(sig.Recv() != nil, "resultOf is used on methods")
	if sig.Params().Len() != 0 || sig.Results().Len() != 1 {
		return nil
	}
	return sig.Results().At(0).Type()
}

func (md *model) setRole(t types.Type, r role) {
	assert(md != nil, "setRole needs a model")
	assert(r != roleNone, "setRole needs a role")
	if named := namedOf(t); named != nil {
		md.roles[named.Obj()] = r
	}
}

// namedOf returns t's named type, looking through aliases, or nil.
func namedOf(t types.Type) *types.Named {
	if t == nil {
		return nil
	}
	named, _ := types.Unalias(t).(*types.Named)
	return named
}

// machineFor returns the machine whose type is t, or nil.
func (md *model) machineFor(t types.Type) *machine {
	assert(md != nil, "machineFor needs a model")
	assert(md.roles != nil, "model maps exist")
	named := namedOf(t)
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
	assert(md != nil, "roleOf needs a model")
	assert(md.roles != nil, "model maps exist")
	named := namedOf(t)
	if named == nil {
		return roleNone
	}
	return md.roles[named.Obj()]
}

// isFire reports whether fn is some machine's Fire method.
func (md *model) isFire(fn *types.Func) bool {
	assert(md != nil, "isFire needs a model")
	assert(fn != nil, "isFire needs a function")
	for _, m := range md.machines {
		if m.fire == fn {
			return true
		}
	}
	return false
}

// fieldOwner returns the machine that declares field v, or nil.
func (md *model) fieldOwner(v types.Object) *machine {
	assert(md != nil, "fieldOwner needs a model")
	assert(v != nil, "fieldOwner needs a field")
	for _, m := range md.machines {
		for i := range m.fields.NumFields() {
			if m.fields.Field(i) == v {
				return m
			}
		}
	}
	return nil
}
