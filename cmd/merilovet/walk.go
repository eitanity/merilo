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
func flatten(flt *faults, f *ast.File) []entry {
	if flt == nil {
		return nil
	}
	if f == nil {
		flt.fault("flatten needs a file")
		return nil
	}
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
		kids = children(flt, top.node, kids[:0])
		for k := len(kids) - 1; k >= 0; k-- {
			stack = append(stack, pending{node: kids[k], parent: idx, depth: top.depth + 1})
		}
	}
	if len(stack) != 0 {
		flt.fault("file has more than maxNodes nodes")
		return nil
	}
	closeEnds(flt, entries)
	return entries
}

// closeEnds sets each entry's end from the depths of the pre-order list.
func closeEnds(flt *faults, entries []entry) {
	if flt == nil {
		return
	}
	if len(entries) == 0 {
		flt.fault("closeEnds needs entries")
		return
	}
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
	if entries[0].end != len(entries) {
		flt.fault("the file entry must span every node")
		return
	}
}

// children appends n's direct children to buf in source order, except that
// an assignment lists its right-hand side first, the order it is evaluated.
func children(flt *faults, n ast.Node, buf []ast.Node) []ast.Node {
	if flt == nil {
		return nil
	}
	if n == nil {
		flt.fault("children of a nil node")
		return nil
	}
	if out, ok := exprChildren(flt, n, buf); ok {
		return out
	}
	if out, ok := typeChildren(flt, n, buf); ok {
		return out
	}
	if out, ok := stmtChildren(flt, n, buf); ok {
		return out
	}
	if out, ok := flowChildren(flt, n, buf); ok {
		return out
	}
	out, ok := declChildren(flt, n, buf)
	if !ok {
		flt.fault("unknown syntax node")
		return nil
	}
	return out
}

func exprChildren(flt *faults, n ast.Node, buf []ast.Node) ([]ast.Node, bool) {
	if flt == nil {
		return nil, false
	}
	if n == nil {
		flt.fault("exprChildren of a nil node")
		return nil, false
	}
	if len(buf) != 0 {
		flt.fault("child buffer must start empty")
		return nil, false
	}
	switch x := n.(type) {
	case *ast.Ellipsis:
		return addExpr(flt, buf, x.Elt), true
	case *ast.FuncLit:
		return addBlock(flt, addFuncType(flt, buf, x.Type), x.Body), true
	case *ast.CompositeLit:
		return addExprs(flt, addExpr(flt, buf, x.Type), x.Elts), true
	case *ast.ParenExpr:
		return addExpr(flt, buf, x.X), true
	case *ast.SelectorExpr:
		return addIdent(flt, addExpr(flt, buf, x.X), x.Sel), true
	case *ast.IndexExpr:
		return addExpr(flt, addExpr(flt, buf, x.X), x.Index), true
	case *ast.IndexListExpr:
		return addExprs(flt, addExpr(flt, buf, x.X), x.Indices), true
	case *ast.SliceExpr:
		return addExpr(flt, addExpr(flt, addExpr(flt, addExpr(flt, buf, x.X), x.Low), x.High), x.Max), true
	case *ast.TypeAssertExpr:
		return addExpr(flt, addExpr(flt, buf, x.X), x.Type), true
	case *ast.CallExpr:
		return addExprs(flt, addExpr(flt, buf, x.Fun), x.Args), true
	case *ast.StarExpr:
		return addExpr(flt, buf, x.X), true
	case *ast.UnaryExpr:
		return addExpr(flt, buf, x.X), true
	case *ast.BinaryExpr:
		return addExpr(flt, addExpr(flt, buf, x.X), x.Y), true
	case *ast.KeyValueExpr:
		return addExpr(flt, addExpr(flt, buf, x.Key), x.Value), true
	}
	return buf, false
}

func typeChildren(flt *faults, n ast.Node, buf []ast.Node) ([]ast.Node, bool) {
	if flt == nil {
		return nil, false
	}
	if n == nil {
		flt.fault("typeChildren of a nil node")
		return nil, false
	}
	if len(buf) != 0 {
		flt.fault("child buffer must start empty")
		return nil, false
	}
	switch x := n.(type) {
	case *ast.ArrayType:
		return addExpr(flt, addExpr(flt, buf, x.Len), x.Elt), true
	case *ast.StructType:
		return addFields(flt, buf, x.Fields), true
	case *ast.FuncType:
		return addFields(flt, addFields(flt, addFields(flt, buf, x.TypeParams), x.Params), x.Results), true
	case *ast.InterfaceType:
		return addFields(flt, buf, x.Methods), true
	case *ast.MapType:
		return addExpr(flt, addExpr(flt, buf, x.Key), x.Value), true
	case *ast.ChanType:
		return addExpr(flt, buf, x.Value), true
	case *ast.Field:
		return addLit(flt, addExpr(flt, addIdents(flt, buf, x.Names), x.Type), x.Tag), true
	case *ast.FieldList:
		for _, f := range x.List {
			buf = append(buf, f)
		}
		return buf, true
	}
	return buf, false
}

func stmtChildren(flt *faults, n ast.Node, buf []ast.Node) ([]ast.Node, bool) {
	if flt == nil {
		return nil, false
	}
	if n == nil {
		flt.fault("stmtChildren of a nil node")
		return nil, false
	}
	if len(buf) != 0 {
		flt.fault("child buffer must start empty")
		return nil, false
	}
	switch x := n.(type) {
	case *ast.DeclStmt:
		return append(buf, x.Decl), true
	case *ast.LabeledStmt:
		return addStmt(flt, addIdent(flt, buf, x.Label), x.Stmt), true
	case *ast.ExprStmt:
		return addExpr(flt, buf, x.X), true
	case *ast.SendStmt:
		return addExpr(flt, addExpr(flt, buf, x.Chan), x.Value), true
	case *ast.IncDecStmt:
		return addExpr(flt, buf, x.X), true
	case *ast.AssignStmt:
		return addExprs(flt, addExprs(flt, buf, x.Rhs), x.Lhs), true
	case *ast.GoStmt:
		return addCall(flt, buf, x.Call), true
	case *ast.DeferStmt:
		return addCall(flt, buf, x.Call), true
	case *ast.ReturnStmt:
		return addExprs(flt, buf, x.Results), true
	case *ast.BranchStmt:
		return addIdent(flt, buf, x.Label), true
	case *ast.BlockStmt:
		return addStmts(flt, buf, x.List), true
	}
	return buf, false
}

func flowChildren(flt *faults, n ast.Node, buf []ast.Node) ([]ast.Node, bool) {
	if flt == nil {
		return nil, false
	}
	if n == nil {
		flt.fault("flowChildren of a nil node")
		return nil, false
	}
	if len(buf) != 0 {
		flt.fault("child buffer must start empty")
		return nil, false
	}
	switch x := n.(type) {
	case *ast.IfStmt:
		return addStmt(flt, addBlock(flt, addExpr(flt, addStmt(flt, buf, x.Init), x.Cond), x.Body), x.Else), true
	case *ast.CaseClause:
		return addStmts(flt, addExprs(flt, buf, x.List), x.Body), true
	case *ast.SwitchStmt:
		return addBlock(flt, addExpr(flt, addStmt(flt, buf, x.Init), x.Tag), x.Body), true
	case *ast.TypeSwitchStmt:
		return addBlock(flt, addStmt(flt, addStmt(flt, buf, x.Init), x.Assign), x.Body), true
	case *ast.CommClause:
		return addStmts(flt, addStmt(flt, buf, x.Comm), x.Body), true
	case *ast.SelectStmt:
		return addBlock(flt, buf, x.Body), true
	case *ast.ForStmt:
		return addBlock(flt, addStmt(flt, addExpr(flt, addStmt(flt, buf, x.Init), x.Cond), x.Post), x.Body), true
	case *ast.RangeStmt:
		return addBlock(flt, addExpr(flt, addExpr(flt, addExpr(flt, buf, x.X), x.Key), x.Value), x.Body), true
	}
	return buf, false
}

func declChildren(flt *faults, n ast.Node, buf []ast.Node) ([]ast.Node, bool) {
	if flt == nil {
		return nil, false
	}
	if n == nil {
		flt.fault("declChildren of a nil node")
		return nil, false
	}
	if len(buf) != 0 {
		flt.fault("child buffer must start empty")
		return nil, false
	}
	switch x := n.(type) {
	case *ast.File:
		buf = addIdent(flt, buf, x.Name)
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
		return addLit(flt, addIdent(flt, buf, x.Name), x.Path), true
	case *ast.ValueSpec:
		return addExprs(flt, addExpr(flt, addIdents(flt, buf, x.Names), x.Type), x.Values), true
	case *ast.TypeSpec:
		return addExpr(flt, addFields(flt, addIdent(flt, buf, x.Name), x.TypeParams), x.Type), true
	case *ast.FuncDecl:
		return addBlock(flt, addFuncType(flt, addIdent(flt, addFields(flt, buf, x.Recv), x.Name), x.Type), x.Body), true
	case *ast.Ident, *ast.BasicLit, *ast.BadExpr, *ast.BadStmt, *ast.BadDecl, *ast.EmptyStmt:
		return buf, true
	}
	return buf, false
}

// The add helpers append one child only when it is present. Concrete
// pointer fields need their own helper, because a nil *ast.Ident stored in
// an ast.Node is not a nil interface.

func addExpr(flt *faults, buf []ast.Node, e ast.Expr) []ast.Node {
	if flt == nil {
		return nil
	}
	if len(buf) >= maxNodes {
		flt.fault("child buffer overflow")
		return nil
	}
	if len(buf) != 0 && buf[len(buf)-1] == ast.Node(e) {
		flt.fault("a child is added once")
		return nil
	}
	if e != nil {
		buf = append(buf, e)
	}
	return buf
}

func addExprs(flt *faults, buf []ast.Node, es []ast.Expr) []ast.Node {
	if flt == nil {
		return nil
	}
	if len(buf) >= maxNodes {
		flt.fault("child buffer overflow")
		return nil
	}
	for _, e := range es {
		if e == nil {
			flt.fault("expression lists hold no nil entries")
			return nil
		}
		buf = append(buf, e)
	}
	return buf
}

func addStmt(flt *faults, buf []ast.Node, s ast.Stmt) []ast.Node {
	if flt == nil {
		return nil
	}
	if len(buf) >= maxNodes {
		flt.fault("child buffer overflow")
		return nil
	}
	if len(buf) != 0 && buf[len(buf)-1] == ast.Node(s) {
		flt.fault("a child is added once")
		return nil
	}
	if s != nil {
		buf = append(buf, s)
	}
	return buf
}

func addStmts(flt *faults, buf []ast.Node, ss []ast.Stmt) []ast.Node {
	if flt == nil {
		return nil
	}
	if len(buf) >= maxNodes {
		flt.fault("child buffer overflow")
		return nil
	}
	for _, s := range ss {
		if s == nil {
			flt.fault("statement lists hold no nil entries")
			return nil
		}
		buf = append(buf, s)
	}
	return buf
}

func addIdent(flt *faults, buf []ast.Node, id *ast.Ident) []ast.Node {
	if flt == nil {
		return nil
	}
	if len(buf) >= maxNodes {
		flt.fault("child buffer overflow")
		return nil
	}
	if len(buf) != 0 && buf[len(buf)-1] == ast.Node(id) {
		flt.fault("a child is added once")
		return nil
	}
	if id != nil {
		buf = append(buf, id)
	}
	return buf
}

func addIdents(flt *faults, buf []ast.Node, ids []*ast.Ident) []ast.Node {
	if flt == nil {
		return nil
	}
	if len(buf) >= maxNodes {
		flt.fault("child buffer overflow")
		return nil
	}
	for _, id := range ids {
		if id == nil {
			flt.fault("identifier lists hold no nil entries")
			return nil
		}
		buf = append(buf, id)
	}
	return buf
}

func addBlock(flt *faults, buf []ast.Node, b *ast.BlockStmt) []ast.Node {
	if flt == nil {
		return nil
	}
	if len(buf) >= maxNodes {
		flt.fault("child buffer overflow")
		return nil
	}
	if len(buf) != 0 && buf[len(buf)-1] == ast.Node(b) {
		flt.fault("a child is added once")
		return nil
	}
	if b != nil {
		buf = append(buf, b)
	}
	return buf
}

func addFields(flt *faults, buf []ast.Node, fl *ast.FieldList) []ast.Node {
	if flt == nil {
		return nil
	}
	if len(buf) >= maxNodes {
		flt.fault("child buffer overflow")
		return nil
	}
	if len(buf) != 0 && buf[len(buf)-1] == ast.Node(fl) {
		flt.fault("a child is added once")
		return nil
	}
	if fl != nil {
		buf = append(buf, fl)
	}
	return buf
}

func addFuncType(flt *faults, buf []ast.Node, ft *ast.FuncType) []ast.Node {
	if flt == nil {
		return nil
	}
	if len(buf) >= maxNodes {
		flt.fault("child buffer overflow")
		return nil
	}
	if len(buf) != 0 && buf[len(buf)-1] == ast.Node(ft) {
		flt.fault("a child is added once")
		return nil
	}
	if ft != nil {
		buf = append(buf, ft)
	}
	return buf
}

func addLit(flt *faults, buf []ast.Node, lit *ast.BasicLit) []ast.Node {
	if flt == nil {
		return nil
	}
	if len(buf) >= maxNodes {
		flt.fault("child buffer overflow")
		return nil
	}
	if len(buf) != 0 && buf[len(buf)-1] == ast.Node(lit) {
		flt.fault("a child is added once")
		return nil
	}
	if lit != nil {
		buf = append(buf, lit)
	}
	return buf
}

func addCall(flt *faults, buf []ast.Node, call *ast.CallExpr) []ast.Node {
	if flt == nil {
		return nil
	}
	if len(buf) >= maxNodes {
		flt.fault("child buffer overflow")
		return nil
	}
	if call == nil {
		flt.fault("go and defer statements always hold a call")
		return nil
	}
	return append(buf, call)
}
