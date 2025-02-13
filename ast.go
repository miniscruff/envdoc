package main

import (
	"fmt"
	"go/ast"
	"go/doc"
	"go/token"
	"log"
	"strings"
)

// visitor nodes types
const (
	nodeUnknown int = iota
	nodeType
	nodeRoot
	nodeStruct
	nodeField
)

type visitorNode struct {
	kind     int
	typeName string         // type name if node is a type or field type name if node is a field
	names    []string       // it's possible that a field has multiple names
	doc      string         // field or type documentation or comment if doc is empty
	children []*visitorNode // optional children nodes for structs
	typeRef  *visitorNode   // type reference if field is a struct
	tag      string         // field tag
	isArray  bool           // true if field is an array
}

type (
	astCommentsHandler func(*ast.Comment) bool
	astTypeDocResolver func(*ast.TypeSpec) string
)

type astVisitor struct {
	commentHandler  astCommentsHandler
	typeDocResolver astTypeDocResolver
	logger          *log.Logger

	currentNode *visitorNode
	pendingType bool   // true if the next type is a target type
	targetName  string // name of the type we are looking for
	depth       int    // current depth in the AST (used for debugging, 1 based)
}

func newAstVisitor(commentsHandler astCommentsHandler, typeDocsResolver astTypeDocResolver) *astVisitor {
	return &astVisitor{
		commentHandler:  commentsHandler,
		typeDocResolver: typeDocsResolver,
		logger:          logger(),
		depth:           1,
	}
}

func (v *astVisitor) push(node *visitorNode, appendChild bool) *astVisitor {
	if appendChild {
		v.currentNode.children = append(v.currentNode.children, node)
	}
	return &astVisitor{
		commentHandler:  v.commentHandler,
		typeDocResolver: v.typeDocResolver,
		logger:          v.logger,
		pendingType:     v.pendingType,
		currentNode:     node,
		depth:           v.depth + 1,
	}
}

func (v *astVisitor) Walk(n ast.Node) {
	ast.Walk(v, n)
	v.resolveFieldTypes()
}

func (v *astVisitor) Visit(n ast.Node) ast.Visitor {
	if v.currentNode == nil {
		v.currentNode = &visitorNode{kind: nodeRoot}
	}

	switch t := n.(type) {
	case *ast.Comment:
		v.logger.Printf("ast(%d): visit comment", v.depth)
		if !v.pendingType {
			v.pendingType = v.commentHandler(t)
		}
		return v
	case *ast.TypeSpec:
		v.logger.Printf("ast(%d): visit type: %q", v.depth, t.Name.Name)
		doc := v.typeDocResolver(t)
		name := t.Name.Name
		if v.pendingType {
			v.targetName = name
			v.pendingType = false
			v.logger.Printf("ast(%d): detect target type: %q", v.depth, name)
		}
		typeNode := &visitorNode{
			names:    []string{name},
			typeName: name,
			kind:     nodeType,
			doc:      doc,
		}
		return v.push(typeNode, true)
	case *ast.StructType:
		v.logger.Printf("ast(%d): found struct", v.depth)
		switch v.currentNode.kind {
		case nodeType:
			v.currentNode.kind = nodeStruct
			return v
		case nodeField:
			structNode := &visitorNode{
				kind: nodeStruct,
				doc:  v.currentNode.doc,
			}
			v.currentNode.typeRef = structNode
			return v.push(structNode, false)
		default:
			panic(fmt.Sprintf("unexpected node kind: %d", v.currentNode.kind))
		}
	case *ast.Field:
		names := fieldNamesToStr(t)
		v.logger.Printf("ast(%d): visit field (%v)", v.depth, names)
		doc := getFieldDoc(t)
		var (
			tag     string
			isArray bool
		)
		if t.Tag != nil {
			tag = t.Tag.Value
		}
		if _, ok := t.Type.(*ast.ArrayType); ok {
			isArray = true
		}
		fieldNode := &visitorNode{
			kind:    nodeField,
			names:   names,
			doc:     doc,
			tag:     tag,
			isArray: isArray,
		}
		if expr, ok := t.Type.(*ast.Ident); ok {
			fieldNode.typeName = expr.Name
		}
		return v.push(fieldNode, true)
	}
	return v
}

func (v *astVisitor) resolveFieldTypes() {
	unresolved := getAllNodes(v.currentNode, func(n *visitorNode) bool {
		return n.kind == nodeField && n.typeRef == nil
	})
	structs := getAllNodes(v.currentNode, func(n *visitorNode) bool {
		return n.kind == nodeStruct
	})
	structsByName := make(map[string]*visitorNode, len(structs))
	for _, s := range structs {
		structsByName[s.typeName] = s
	}
	for _, f := range unresolved {
		if s, ok := structsByName[f.typeName]; ok {
			f.typeRef = s
			v.logger.Printf("ast: resolve field type %q to struct %q", f.names, s.typeName)
		}
	}
}

func getAllNodes(root *visitorNode, filter func(*visitorNode) bool) []*visitorNode {
	var result []*visitorNode
	if filter(root) {
		result = append(result, root)
	}
	for _, c := range root.children {
		result = append(result, getAllNodes(c, filter)...)
	}
	return result
}

func getFieldDoc(f *ast.Field) string {
	doc := f.Doc.Text()
	if doc == "" {
		doc = f.Comment.Text()
	}
	return strings.TrimSpace(doc)
}

func fieldNamesToStr(f *ast.Field) []string {
	names := make([]string, len(f.Names))
	for i, n := range f.Names {
		names[i] = n.Name
	}
	return names
}

func newASTTypeDocResolver(fileSet *token.FileSet, astFile *ast.File) (func(t *ast.TypeSpec) string, error) {
	docs, err := doc.NewFromFiles(fileSet, []*ast.File{astFile}, "./", doc.PreserveAST)
	if err != nil {
		return nil, fmt.Errorf("extract package docs: %w", err)
	}
	return func(t *ast.TypeSpec) string {
		typeName := t.Name.String()
		docStr := strings.TrimSpace(t.Doc.Text())
		if docStr == "" {
			for _, t := range docs.Types {
				if t.Name == typeName {
					docStr = strings.TrimSpace(t.Doc)
					break
				}
			}
		}
		return docStr
	}, nil
}

var astCommentDummyHandler = func(*ast.Comment) bool {
	return false
}

func newASTCommentTargetLineHandler(goGenLine int, linePositions []int) func(*ast.Comment) bool {
	l := logger()
	return func(c *ast.Comment) bool {
		// if type name is not specified we should process the next type
		// declaration after the comment with go:generate
		// which causes this command to be executed.
		var line int
		for l, pos := range linePositions {
			if token.Pos(pos) > c.Pos() {
				break
			}
			// $GOLINE env var is 1-based.
			line = l + 1
		}
		if line != goGenLine {
			return false
		}

		l.Printf("found go:generate comment at line %d", line)
		return true
	}
}
