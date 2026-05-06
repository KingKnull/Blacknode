package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"golang.org/x/tools/go/ast/astutil"
)

func main() {
	files, err := filepath.Glob("*.go")
	if err != nil {
		panic(err)
	}

	for _, file := range files {
		if file == "rewrite.go" || strings.HasSuffix(file, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
		if err != nil {
			fmt.Printf("Error parsing %s: %v\n", file, err)
			continue
		}

		modified := false
		needsContext := false

		for _, decl := range f.Decls {
			funcDecl, ok := decl.(*ast.FuncDecl)
			if !ok || funcDecl.Recv == nil || len(funcDecl.Recv.List) == 0 {
				continue
			}
			// Only modify exported methods
			if !funcDecl.Name.IsExported() {
				continue
			}

			// Check receiver type
			starExpr, ok := funcDecl.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			ident, ok := starExpr.X.(*ast.Ident)
			if !ok || !strings.HasSuffix(ident.Name, "Service") {
				continue
			}

			// Skip if method name is Start, Stop, or Callback (often internal, non-wails)
			if funcDecl.Name.Name == "Start" || funcDecl.Name.Name == "Stop" || funcDecl.Name.Name == "Callback" {
				continue
			}

			// Add ctx context.Context as first param
			hasCtx := false
			if len(funcDecl.Type.Params.List) > 0 {
				firstParam := funcDecl.Type.Params.List[0]
				if len(firstParam.Names) > 0 && firstParam.Names[0].Name == "ctx" {
					hasCtx = true
				}
			}

			if !hasCtx {
				ctxField := &ast.Field{
					Names: []*ast.Ident{ast.NewIdent("ctx")},
					Type: &ast.SelectorExpr{
						X:   ast.NewIdent("context"),
						Sel: ast.NewIdent("Context"),
					},
				}
				funcDecl.Type.Params.List = append([]*ast.Field{ctxField}, funcDecl.Type.Params.List...)
				modified = true
				needsContext = true
			}
		}

		srcBytes, _ := os.ReadFile(file)
		if strings.Contains(string(srcBytes), "ctx,") {
			needsContext = true
		}

		if modified || needsContext {
			astutil.AddImport(fset, f, "context")
			var buf bytes.Buffer
			if err := format.Node(&buf, fset, f); err != nil {
				fmt.Printf("Error formatting %s: %v\n", file, err)
				continue
			}
			os.WriteFile(file, buf.Bytes(), 0644)
		}
	}
}
