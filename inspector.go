package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"log"
	"strings"
)

type inspector struct {
	typeName      string // type name to generate documentation for, could be empty
	all           bool   // generate documentation for all types in the file
	execLine      int    // line number of the go:generate directive
	useFieldNames bool   // use field names if tag is not specified
	log           *log.Logger
}

func newInspector(typeName string, all bool, execLine int, useFieldNames bool) *inspector {
	return &inspector{
		typeName:      typeName,
		all:           all,
		execLine:      execLine,
		useFieldNames: useFieldNames,
		log:           logger(),
	}
}

func (i *inspector) inspectFile(fileName string) ([]*EnvScope, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, fileName, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse file: %w", err)
	}
	docResolver, err := newASTTypeDocResolver(fileSet, file)
	if err != nil {
		return nil, fmt.Errorf("new ast type doc resolver: %w", err)
	}
	var commentsHandler astCommentsHandler
	if i.all {
		commentsHandler = astCommentDummyHandler
	} else {
		commentsHandler = newASTCommentTargetLineHandler(i.execLine, fileSet.File(file.Pos()).Lines())
	}
	visitor := newAstVisitor(commentsHandler, docResolver)
	visitor.Walk(file)
	targetName := i.typeName
	if targetName == "" {
		targetName = visitor.targetName
	}
	return i.traverseAST(visitor.currentNode, targetName), nil
}

func (i *inspector) traverseAST(root *visitorNode, targetName string) []*EnvScope {
	scopes := make([]*EnvScope, 0, len(root.children))
	logger := logger()
	for _, child := range root.children {
		if child.kind != nodeType && child.kind != nodeStruct {
			panic(fmt.Sprintf("expected type node root child, got %v", child.kind))
		}

		if !i.all && targetName != child.typeName {
			logger.Printf("inspector: (traverse) skipping node: %v", child.typeName)
			continue
		}
		logger.Printf("inspector: (traverse) process node: %v", child.typeName)

		if scope := newScope(child, i.useFieldNames); scope != nil {
			scopes = append(scopes, scope)
		}
	}
	return scopes
}

func newScope(node *visitorNode, useFieldNames bool) *EnvScope {
	if len(node.names) != 1 {
		panic("type node must have exactly one name")
	}

	logger := logger()
	logger.Printf("inspecctor: (scope) got node: %v", node.names)

	scope := &EnvScope{
		Name: node.names[0],
		Doc:  node.doc,
	}
	for _, child := range node.children {
		if items := newDocItems(child, useFieldNames, ""); len(items) > 0 {
			logger.Printf("inspector: (scope) add items: %d", len(items))
			scope.Vars = append(scope.Vars, items...)
		} else {
			logger.Printf("inspector: (scope) no items")
		}
	}
	if len(scope.Vars) == 0 {
		return nil
	}
	return scope
}

func newDocItems(node *visitorNode, useFieldNames bool, envPrefix string) []*EnvDocItem {
	logger := logger()
	builder := new(envDocItemsBuilder).apply(
		withEnvDocItemEnvPrefix(envPrefix),
		withEnvDocItemDoc(node.doc),
	)
	logger.Printf("inspector: (items) process node: %v, envPrefix=%q", node.names, envPrefix)
	if node.kind == nodeField && node.typeRef != nil {
		if tags := getTagValues(node.tag, "envPrefix"); len(tags) > 0 {
			envPrefix = strConcat(envPrefix, tags[0])
		}
		logger.Printf("inspector: (items) get subitem fields for typeref: %q, envPrefix=%q", node.typeRef.names, envPrefix)
		typeRef := node.typeRef
		builder.apply(withEnvDocItemDoc(typeRef.doc), withEnvDocEmptyNames)
		for _, subItem := range node.typeRef.children {
			logger.Printf("inspector: (items) add subitem for typeref %q: %q", node.typeRef.names, subItem.names)
			if items := newDocItems(subItem, useFieldNames, envPrefix); len(items) > 0 {
				builder.apply(withEnvDocItemAddChildren(items))
			}
		}
		debugBuilder(logger, "inspector: (items) typeref builder: ", builder)
		return builder.items()
	}

	if node.tag == "" && !useFieldNames {
		logger.Printf("inspector: (items) no tag and no field names, skip node: %q", node.names)
		return nil
	}

	tagName, opts := parseEnvTag(node.tag)
	if tagName != "" {
		logger.Printf("inspector: (items) tag name: %q", tagName)
		builder.apply(withEnvDocItemNames(tagName))
	} else if useFieldNames {
		logger.Printf("inspector: (items) field names: %q", node.names)
		names := make([]string, len(node.names))
		for i, name := range node.names {
			names[i] = camelToSnake(name)
		}
		builder.apply(withEnvDocItemNames(names...))
	} else {
		logger.Printf("inspector: (items) no tag name and not using field names")
		return nil
	}

	// Check if the field type is a slice or array, then use default separator
	if node.isArray && opts.Separator == "" {
		opts.Separator = ","
	}

	builder.apply(withEnvDocItemOpts(opts))

	debugBuilder(logger, "inspector: (items) builder: ", builder)
	return builder.items()
}

func parseEnvTag(tag string) (string, EnvVarOptions) {
	tagValues := getTagValues(tag, "env")
	var tagName string
	if len(tagValues) > 0 {
		tagName = tagValues[0]
	}

	var opts EnvVarOptions
	if len(tagValues) > 1 {
		for _, tagValue := range tagValues[1:] {
			switch tagValue {
			case "required":
				opts.Required = true
			case "expand":
				opts.Expand = true
			case "notEmpty":
				opts.Required = true
				opts.NonEmpty = true
			case "file":
				opts.FromFile = true
			}
		}
	}

	envDefault := getTagValues(tag, "envDefault")
	if len(envDefault) > 0 {
		opts.Default = strings.Join(envDefault, ",")
	}

	envSeparator := getTagValues(tag, "envSeparator")
	if len(envSeparator) > 0 {
		opts.Separator = envSeparator[0]
	}

	return tagName, opts
}

func getTagValues(tag, tagName string) []string {
	tagPrefix := tagName + ":"
	if !strings.Contains(tag, tagPrefix) {
		return nil
	}
	tagValue := strings.Split(tag, tagPrefix)[1]
	leftQ := strings.Index(tagValue, `"`)
	if leftQ == -1 || leftQ == len(tagValue)-1 {
		return nil
	}
	rightQ := strings.Index(tagValue[leftQ+1:], `"`)
	if rightQ == -1 {
		return nil
	}
	tagValue = tagValue[leftQ+1 : leftQ+rightQ+1]
	return strings.Split(tagValue, ",")
}
