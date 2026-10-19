package main

import "go/ast"

// maxNodes bounds every walk; no source file has this many syntax nodes.
const maxNodes = 1 << 24

// entry is one node of a file in pre-order. A node's descendants are the
// entries from its index+1 up to end, so a subtree is a contiguous range.
type entry struct {
	node   ast.Node
	parent int // index of the parent entry, -1 for the file
	end    int // index just past the last descendant
	fn     int // index of the enclosing FuncDecl entry, -1 outside one
	depth  int
}

type pending struct {
	node   ast.Node
	parent int
	depth  int
}

// flatten lists every node of f in pre-order, using an explicit stack.
func flatten(f *ast.File) []entry {
	assert(f != nil, "flatten needs a file")
	entries := make([]entry, 0, 256)
	stack := []pending{{node: f, parent: -1}}
	var kids []ast.Node
	for i := 0; i < maxNodes && len(stack) > 0; i++ {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		idx := len(entries)
		fn := -1
		if top.parent >= 0 {
			fn = entries[top.parent].fn
		}
		if _, ok := top.node.(*ast.FuncDecl); ok {
			fn = idx
		}
		entries = append(entries, entry{node: top.node, parent: top.parent, fn: fn, depth: top.depth})
		kids = children(top.node, kids[:0])
		for k := len(kids) - 1; k >= 0; k-- {
			stack = append(stack, pending{node: kids[k], parent: idx, depth: top.depth + 1})
		}
	}
	assert(len(stack) == 0, "file has more than maxNodes nodes")
	closeEnds(entries)
	return entries
}

// closeEnds sets each entry's end from the depths of the pre-order list.
func closeEnds(entries []entry) {
	assert(len(entries) > 0, "closeEnds needs entries")
	open := make([]int, 0, 64)
	for j := range entries {
		for k := len(open) - 1; k >= 0; k-- {
			if entries[open[k]].depth < entries[j].depth {
				break
			}
			entries[open[k]].end = j
			open = open[:k]
		}
		open = append(open, j)
	}
	for _, k := range open {
		entries[k].end = len(entries)
	}
	assert(entries[0].end == len(entries), "the file entry must span every node")
}

// children appends n's direct children to buf in source order, except that
// an assignment lists its right-hand side first, the order it is evaluated.
func children(n ast.Node, buf []ast.Node) []ast.Node {
	assert(n != nil, "children of a nil node")
	if out, ok := exprChildren(n, buf); ok {
		return out
	}
	if out, ok := typeChildren(n, buf); ok {
		return out
	}
	if out, ok := stmtChildren(n, buf); ok {
		return out
	}
	if out, ok := flowChildren(n, buf); ok {
		return out
	}
	out, ok := declChildren(n, buf)
	assert(ok, "unknown syntax node")
	return out
}

func exprChildren(n ast.Node, buf []ast.Node) ([]ast.Node, bool) {
	assert(n != nil, "exprChildren of a nil node")
	assert(len(buf) == 0, "child buffer must start empty")
	switch x := n.(type) {
	case *ast.Ellipsis:
		return addExpr(buf, x.Elt), true
	case *ast.FuncLit:
		return addBlock(addFuncType(buf, x.Type), x.Body), true
	case *ast.CompositeLit:
		return addExprs(addExpr(buf, x.Type), x.Elts), true
	case *ast.ParenExpr:
		return addExpr(buf, x.X), true
	case *ast.SelectorExpr:
		return addIdent(addExpr(buf, x.X), x.Sel), true
	case *ast.IndexExpr:
		return addExpr(addExpr(buf, x.X), x.Index), true
	case *ast.IndexListExpr:
		return addExprs(addExpr(buf, x.X), x.Indices), true
	case *ast.SliceExpr:
		return addExpr(addExpr(addExpr(addExpr(buf, x.X), x.Low), x.High), x.Max), true
	case *ast.TypeAssertExpr:
		return addExpr(addExpr(buf, x.X), x.Type), true
	case *ast.CallExpr:
		return addExprs(addExpr(buf, x.Fun), x.Args), true
	case *ast.StarExpr:
		return addExpr(buf, x.X), true
	case *ast.UnaryExpr:
		return addExpr(buf, x.X), true
	case *ast.BinaryExpr:
		return addExpr(addExpr(buf, x.X), x.Y), true
	case *ast.KeyValueExpr:
		return addExpr(addExpr(buf, x.Key), x.Value), true
	}
	return buf, false
}

func typeChildren(n ast.Node, buf []ast.Node) ([]ast.Node, bool) {
	assert(n != nil, "typeChildren of a nil node")
	assert(len(buf) == 0, "child buffer must start empty")
	switch x := n.(type) {
	case *ast.ArrayType:
		return addExpr(addExpr(buf, x.Len), x.Elt), true
	case *ast.StructType:
		return addFields(buf, x.Fields), true
	case *ast.FuncType:
		return addFields(addFields(addFields(buf, x.TypeParams), x.Params), x.Results), true
	case *ast.InterfaceType:
		return addFields(buf, x.Methods), true
	case *ast.MapType:
		return addExpr(addExpr(buf, x.Key), x.Value), true
	case *ast.ChanType:
		return addExpr(buf, x.Value), true
	case *ast.Field:
		return addLit(addExpr(addIdents(buf, x.Names), x.Type), x.Tag), true
	case *ast.FieldList:
		for _, f := range x.List {
			buf = append(buf, f)
		}
		return buf, true
	}
	return buf, false
}

func stmtChildren(n ast.Node, buf []ast.Node) ([]ast.Node, bool) {
	assert(n != nil, "stmtChildren of a nil node")
	assert(len(buf) == 0, "child buffer must start empty")
	switch x := n.(type) {
	case *ast.DeclStmt:
		return append(buf, x.Decl), true
	case *ast.LabeledStmt:
		return addStmt(addIdent(buf, x.Label), x.Stmt), true
	case *ast.ExprStmt:
		return addExpr(buf, x.X), true
	case *ast.SendStmt:
		return addExpr(addExpr(buf, x.Chan), x.Value), true
	case *ast.IncDecStmt:
		return addExpr(buf, x.X), true
	case *ast.AssignStmt:
		return addExprs(addExprs(buf, x.Rhs), x.Lhs), true
	case *ast.GoStmt:
		return addCall(buf, x.Call), true
	case *ast.DeferStmt:
		return addCall(buf, x.Call), true
	case *ast.ReturnStmt:
		return addExprs(buf, x.Results), true
	case *ast.BranchStmt:
		return addIdent(buf, x.Label), true
	case *ast.BlockStmt:
		return addStmts(buf, x.List), true
	}
	return buf, false
}

func flowChildren(n ast.Node, buf []ast.Node) ([]ast.Node, bool) {
	assert(n != nil, "flowChildren of a nil node")
	assert(len(buf) == 0, "child buffer must start empty")
	switch x := n.(type) {
	case *ast.IfStmt:
		return addStmt(addBlock(addExpr(addStmt(buf, x.Init), x.Cond), x.Body), x.Else), true
	case *ast.CaseClause:
		return addStmts(addExprs(buf, x.List), x.Body), true
	case *ast.SwitchStmt:
		return addBlock(addExpr(addStmt(buf, x.Init), x.Tag), x.Body), true
	case *ast.TypeSwitchStmt:
		return addBlock(addStmt(addStmt(buf, x.Init), x.Assign), x.Body), true
	case *ast.CommClause:
		return addStmts(addStmt(buf, x.Comm), x.Body), true
	case *ast.SelectStmt:
		return addBlock(buf, x.Body), true
	case *ast.ForStmt:
		return addBlock(addStmt(addExpr(addStmt(buf, x.Init), x.Cond), x.Post), x.Body), true
	case *ast.RangeStmt:
		return addBlock(addExpr(addExpr(addExpr(buf, x.X), x.Key), x.Value), x.Body), true
	}
	return buf, false
}

func declChildren(n ast.Node, buf []ast.Node) ([]ast.Node, bool) {
	assert(n != nil, "declChildren of a nil node")
	assert(len(buf) == 0, "child buffer must start empty")
	switch x := n.(type) {
	case *ast.File:
		buf = addIdent(buf, x.Name)
		for _, d := range x.Decls {
			buf = append(buf, d)
		}
		return buf, true
	case *ast.GenDecl:
		for _, s := range x.Specs {
			buf = append(buf, s)
		}
		return buf, true
	case *ast.ImportSpec:
		return addLit(addIdent(buf, x.Name), x.Path), true
	case *ast.ValueSpec:
		return addExprs(addExpr(addIdents(buf, x.Names), x.Type), x.Values), true
	case *ast.TypeSpec:
		return addExpr(addFields(addIdent(buf, x.Name), x.TypeParams), x.Type), true
	case *ast.FuncDecl:
		return addBlock(addFuncType(addIdent(addFields(buf, x.Recv), x.Name), x.Type), x.Body), true
	case *ast.Ident, *ast.BasicLit, *ast.BadExpr, *ast.BadStmt, *ast.BadDecl, *ast.EmptyStmt:
		return buf, true
	}
	return buf, false
}

// The add helpers append one child only when it is present. Concrete
// pointer fields need their own helper, because a nil *ast.Ident stored in
// an ast.Node is not a nil interface.

func addExpr(buf []ast.Node, e ast.Expr) []ast.Node {
	assert(len(buf) < maxNodes, "child buffer overflow")
	assert(len(buf) == 0 || buf[len(buf)-1] != ast.Node(e), "a child is added once")
	if e != nil {
		buf = append(buf, e)
	}
	return buf
}

func addExprs(buf []ast.Node, es []ast.Expr) []ast.Node {
	assert(len(buf) < maxNodes, "child buffer overflow")
	for _, e := range es {
		assert(e != nil, "expression lists hold no nil entries")
		buf = append(buf, e)
	}
	return buf
}

func addStmt(buf []ast.Node, s ast.Stmt) []ast.Node {
	assert(len(buf) < maxNodes, "child buffer overflow")
	assert(len(buf) == 0 || buf[len(buf)-1] != ast.Node(s), "a child is added once")
	if s != nil {
		buf = append(buf, s)
	}
	return buf
}

func addStmts(buf []ast.Node, ss []ast.Stmt) []ast.Node {
	assert(len(buf) < maxNodes, "child buffer overflow")
	for _, s := range ss {
		assert(s != nil, "statement lists hold no nil entries")
		buf = append(buf, s)
	}
	return buf
}

func addIdent(buf []ast.Node, id *ast.Ident) []ast.Node {
	assert(len(buf) < maxNodes, "child buffer overflow")
	assert(len(buf) == 0 || buf[len(buf)-1] != ast.Node(id), "a child is added once")
	if id != nil {
		buf = append(buf, id)
	}
	return buf
}

func addIdents(buf []ast.Node, ids []*ast.Ident) []ast.Node {
	assert(len(buf) < maxNodes, "child buffer overflow")
	for _, id := range ids {
		assert(id != nil, "identifier lists hold no nil entries")
		buf = append(buf, id)
	}
	return buf
}

func addBlock(buf []ast.Node, b *ast.BlockStmt) []ast.Node {
	assert(len(buf) < maxNodes, "child buffer overflow")
	assert(len(buf) == 0 || buf[len(buf)-1] != ast.Node(b), "a child is added once")
	if b != nil {
		buf = append(buf, b)
	}
	return buf
}

func addFields(buf []ast.Node, fl *ast.FieldList) []ast.Node {
	assert(len(buf) < maxNodes, "child buffer overflow")
	assert(len(buf) == 0 || buf[len(buf)-1] != ast.Node(fl), "a child is added once")
	if fl != nil {
		buf = append(buf, fl)
	}
	return buf
}

func addFuncType(buf []ast.Node, ft *ast.FuncType) []ast.Node {
	assert(len(buf) < maxNodes, "child buffer overflow")
	assert(len(buf) == 0 || buf[len(buf)-1] != ast.Node(ft), "a child is added once")
	if ft != nil {
		buf = append(buf, ft)
	}
	return buf
}

func addLit(buf []ast.Node, lit *ast.BasicLit) []ast.Node {
	assert(len(buf) < maxNodes, "child buffer overflow")
	assert(len(buf) == 0 || buf[len(buf)-1] != ast.Node(lit), "a child is added once")
	if lit != nil {
		buf = append(buf, lit)
	}
	return buf
}

func addCall(buf []ast.Node, call *ast.CallExpr) []ast.Node {
	assert(len(buf) < maxNodes, "child buffer overflow")
	assert(call != nil, "go and defer statements always hold a call")
	return append(buf, call)
}
