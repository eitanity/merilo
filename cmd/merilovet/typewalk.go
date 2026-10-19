package main

import "go/types"

// bearing reports whether t is machine-bearing: a machine type, or a struct
// or array that contains one at any depth.
func (md *model) bearing(t types.Type) bool {
	assert(md != nil, "bearing needs a model")
	if t == nil {
		return false
	}
	stack := []types.Type{t}
	seen := map[types.Type]bool{}
	for i := 0; i < maxTypeWalk && len(stack) > 0; i++ {
		top := types.Unalias(stack[len(stack)-1])
		stack = stack[:len(stack)-1]
		if seen[top] {
			continue
		}
		seen[top] = true
		if md.machineFor(top) != nil {
			return true
		}
		switch u := top.Underlying().(type) {
		case *types.Struct:
			for f := range u.NumFields() {
				stack = append(stack, u.Field(f).Type())
			}
		case *types.Array:
			stack = append(stack, u.Elem())
		}
	}
	assert(len(stack) == 0, "type nesting deeper than maxTypeWalk")
	return false
}

// refers reports whether t mentions a machine or enum type anywhere,
// including through pointers, slices, maps and channels.
func (md *model) refers(t types.Type) bool {
	assert(md != nil, "refers needs a model")
	if t == nil {
		return false
	}
	stack := []types.Type{t}
	seen := map[types.Type]bool{}
	for i := 0; i < maxTypeWalk && len(stack) > 0; i++ {
		top := types.Unalias(stack[len(stack)-1])
		stack = stack[:len(stack)-1]
		if seen[top] {
			continue
		}
		seen[top] = true
		if md.machineFor(top) != nil || md.roleOf(top) != roleNone {
			return true
		}
		stack = appendElems(stack, top.Underlying())
	}
	assert(len(stack) == 0, "type nesting deeper than maxTypeWalk")
	return false
}

// appendElems pushes the types a composite type is built from.
func appendElems(stack []types.Type, u types.Type) []types.Type {
	assert(u != nil, "appendElems needs a type")
	assert(len(stack) < maxTypeWalk, "type stack overflow")
	switch x := u.(type) {
	case *types.Struct:
		for f := range x.NumFields() {
			stack = append(stack, x.Field(f).Type())
		}
	case *types.Array:
		stack = append(stack, x.Elem())
	case *types.Slice:
		stack = append(stack, x.Elem())
	case *types.Pointer:
		stack = append(stack, x.Elem())
	case *types.Map:
		stack = append(stack, x.Key(), x.Elem())
	case *types.Chan:
		stack = append(stack, x.Elem())
	}
	return stack
}

// badKind returns the first pointer, interface, channel, func, slice or map
// kind found in t, through nested structs and arrays, or "".
func badKind(t types.Type) string {
	assert(t != nil, "badKind needs a type")
	stack := []types.Type{t}
	seen := map[types.Type]bool{}
	for i := 0; i < maxTypeWalk && len(stack) > 0; i++ {
		top := types.Unalias(stack[len(stack)-1])
		stack = stack[:len(stack)-1]
		if seen[top] {
			continue
		}
		seen[top] = true
		switch u := top.Underlying().(type) {
		case *types.Pointer:
			return "pointer"
		case *types.Interface:
			return "interface"
		case *types.Chan:
			return "channel"
		case *types.Signature:
			return "func"
		case *types.Slice:
			return "slice"
		case *types.Map:
			return "map"
		case *types.Struct:
			for f := range u.NumFields() {
				stack = append(stack, u.Field(f).Type())
			}
		case *types.Array:
			stack = append(stack, u.Elem())
		}
	}
	assert(len(stack) == 0, "type nesting deeper than maxTypeWalk")
	return ""
}

// isHolder reports whether a non-machine package refers to a machine or
// one of its enum types.
func (md *model) isHolder(p *pkg) bool {
	assert(md != nil, "isHolder needs a model")
	assert(p != nil && p.info != nil, "isHolder needs a checked package")
	if md.machinePkgs[p.types] {
		return false
	}
	for _, obj := range p.info.Uses {
		if tn, ok := obj.(*types.TypeName); ok && md.refers(tn.Type()) {
			return true
		}
	}
	for _, tv := range p.info.Types {
		if md.refers(tv.Type) {
			return true
		}
	}
	return false
}
