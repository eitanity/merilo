package main

import "go/types"

// bearing reports whether t is machine-bearing: a machine type, or a struct
// or array that contains one at any depth.
func (md *model) bearing(t types.Type) bool {
	if md == nil {
		return false
	}
	if t == nil {
		return false
	}
	// A type cannot hold itself by value, so a walk of struct fields and
	// array elements always ends; it needs no record of what it has seen.
	stack, ok := md.push(md.stack[:0], t)
	for i := 0; i < maxTypeWalk && ok && len(stack) > 0; i++ {
		top := types.Unalias(stack[len(stack)-1])
		stack = stack[:len(stack)-1]
		if md.machineFor(top) != nil {
			return true
		}
		stack, ok = md.pushParts(stack, top.Underlying())
	}
	if ok && len(stack) != 0 {
		md.flt.fault("type nesting deeper than maxTypeWalk")
	}
	return false
}

// refers reports whether t mentions a machine or enum type anywhere,
// including through pointers, slices, maps and channels.
func (md *model) refers(t types.Type) bool {
	if md == nil {
		return false
	}
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
		stack = appendElems(md.flt, stack, top.Underlying())
	}
	if len(stack) != 0 {
		md.flt.fault("type nesting deeper than maxTypeWalk")
		return false
	}
	return false
}

// appendElems pushes the types a composite type is built from.
func appendElems(flt *faults, stack []types.Type, u types.Type) []types.Type {
	if flt == nil {
		return nil
	}
	if u == nil {
		flt.fault("appendElems needs a type")
		return nil
	}
	if len(stack) >= maxTypeWalk {
		flt.fault("type stack overflow")
		return nil
	}
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
// kind found in t, through nested structs and arrays, or "". With strings
// set, a string is unbounded too, as it is in an event payload.
func (md *model) badKind(t types.Type, strings bool) string {
	if md == nil {
		return ""
	}
	if t == nil {
		md.flt.fault("badKind needs a type")
		return ""
	}
	stack, ok := md.push(md.stack[:0], t)
	for i := 0; i < maxTypeWalk && ok && len(stack) > 0; i++ {
		top := types.Unalias(stack[len(stack)-1])
		stack = stack[:len(stack)-1]
		if kind := kindOf(top.Underlying(), strings); kind != "" {
			return kind
		}
		stack, ok = md.pushParts(stack, top.Underlying())
	}
	if ok && len(stack) != 0 {
		md.flt.fault("type nesting deeper than maxTypeWalk")
	}
	return ""
}

// kindOf names u when it is a pointer, interface, channel, func, slice or
// map, or, with strings set, a string; otherwise it returns "".
func kindOf(u types.Type, strings bool) string {
	if u == nil {
		return ""
	}
	switch x := u.(type) {
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
	case *types.Basic:
		if strings && x.Info()&types.IsString != 0 {
			return "string"
		}
	}
	return ""
}

// isHolder reports whether a non-machine package refers to a machine or
// one of its enum types.
func (md *model) isHolder(p *pkg) bool {
	if md == nil {
		return false
	}
	if p == nil || p.info == nil {
		md.flt.fault("isHolder needs a checked package")
		return false
	}
	if md.machinePkgs[p.types] {
		return false
	}
	info := p.info
	for _, obj := range info.Uses {
		if tn, ok := obj.(*types.TypeName); ok && md.refers(tn.Type()) {
			return true
		}
	}
	for _, tv := range info.Types {
		if md.refers(tv.Type) {
			return true
		}
	}
	return false
}

// hasInvalid reports whether t, through nested structs and arrays, holds a
// type go/types could not resolve, such as one from an opaque package.
func (md *model) hasInvalid(t types.Type) bool {
	if md == nil {
		return true
	}
	if t == nil {
		md.flt.fault("hasInvalid needs a type")
		return true
	}
	stack, ok := md.push(md.stack[:0], t)
	for i := 0; i < maxTypeWalk && ok && len(stack) > 0; i++ {
		top := types.Unalias(stack[len(stack)-1])
		stack = stack[:len(stack)-1]
		if b, isBasic := top.Underlying().(*types.Basic); isBasic && b.Kind() == types.Invalid {
			return true
		}
		stack, ok = md.pushParts(stack, top.Underlying())
	}
	if ok && len(stack) != 0 {
		md.flt.fault("type nesting deeper than maxTypeWalk")
	}
	return !ok
}
