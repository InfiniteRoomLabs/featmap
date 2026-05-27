package main

// Static source analysis guarding the MCP tool surface. No database required
// (run under SKIP_DB_TESTS=1 in the lint CI job; TestMain still skips the
// postgres container in that mode).
//
// The MCP SDK's mcp.Server exposes no way to enumerate registered tools at
// runtime (only AddTool/RemoveTools), so completeness is checked by parsing the
// package source instead:
//
//   (a) handler set   -- every package-level func named ^mcp[A-Z] whose
//                         signature is (context.Context, Service, T) (U, error).
//   (b) registered set -- every handler identifier passed as the final argument
//                         to an add(srv, ...) call in mcp.go.
//
// Orphan guard: (a) MINUS (b) must be empty -- a handler that exists but was
// never add()-ed is unreachable from the wire (this is how bulk_add_comment
// shipped dead once). We also assert no duplicate tool-name string literals.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// parsePackageFiles parses every non-test .go file in the package directory.
func parsePackageFiles(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("no non-test .go files parsed")
	}
	return fset, files
}

// isToolHandlerSig reports whether fn has the exact tool-handler signature
// (context.Context, Service, T) (U, error). This cleanly excludes helpers like
// mcpHTTPHandler (returns http.Handler, single value) and applyFeaturePatch
// (not mcp*-prefixed).
func isToolHandlerSig(fn *ast.FuncDecl) bool {
	if fn.Recv != nil {
		return false // methods are not tool handlers
	}
	ft := fn.Type
	// Exactly 3 params: ctx context.Context, s Service, a T.
	if ft.Params == nil || numFields(ft.Params) != 3 {
		return false
	}
	// Exactly 2 results: (U, error).
	if ft.Results == nil || numFields(ft.Results) != 2 {
		return false
	}
	params := flattenFields(ft.Params)
	if exprString(params[0]) != "context.Context" {
		return false
	}
	if exprString(params[1]) != "Service" {
		return false
	}
	results := flattenFields(ft.Results)
	if exprString(results[1]) != "error" {
		return false
	}
	return true
}

// numFields counts individual fields, expanding grouped params like (a, b T).
func numFields(fl *ast.FieldList) int {
	return len(flattenFields(fl))
}

// flattenFields returns one type expr per logical field, expanding grouped
// declarations (a, b T -> two entries of T).
func flattenFields(fl *ast.FieldList) []ast.Expr {
	var out []ast.Expr
	for _, f := range fl.List {
		n := len(f.Names)
		if n == 0 {
			n = 1 // unnamed result like the trailing error
		}
		for i := 0; i < n; i++ {
			out = append(out, f.Type)
		}
	}
	return out
}

// exprString renders a type expression to its source-ish string for matching.
func exprString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return exprString(t.X) + "." + t.Sel.Name
	case *ast.StarExpr:
		return "*" + exprString(t.X)
	case *ast.ArrayType:
		return "[]" + exprString(t.Elt)
	default:
		return ""
	}
}

// collectHandlers returns the set of package-level func names matching the
// tool-handler signature and the ^mcp[A-Z] naming convention.
func collectHandlers(files []*ast.File) map[string]bool {
	handlers := map[string]bool{}
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			name := fn.Name.Name
			if !strings.HasPrefix(name, "mcp") || len(name) < 4 {
				continue
			}
			// ^mcp[A-Z]
			c := name[3]
			if c < 'A' || c > 'Z' {
				continue
			}
			if isToolHandlerSig(fn) {
				handlers[name] = true
			}
		}
	}
	return handlers
}

// addCall is one parsed add(srv, "name", "desc", resolveWS, handler) call.
type addCall struct {
	toolName    string // string literal arg, "" if not a literal
	handlerName string // final-arg identifier, "" if not a bare ident
}

// collectAddCalls finds every add(srv, ...) call in the package and extracts
// the tool-name literal and the final-argument handler identifier.
func collectAddCalls(t *testing.T, files []*ast.File) []addCall {
	t.Helper()
	var calls []addCall
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || ident.Name != "add" {
				return true
			}
			// add(srv, name, desc, resolveWS, handler) -- 5 args.
			if len(call.Args) != 5 {
				t.Errorf("add() call at %v has %d args, expected 5", call.Pos(), len(call.Args))
				return true
			}
			ac := addCall{}
			if lit, ok := call.Args[1].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				// Unquote-ish: strip surrounding quotes.
				ac.toolName = strings.Trim(lit.Value, "\"")
			}
			final := call.Args[len(call.Args)-1]
			if hid, ok := final.(*ast.Ident); ok {
				ac.handlerName = hid.Name
			} else {
				// Not a bare identifier (func literal, selector, etc). Record a
				// sentinel so the orphan check can still flag a missing handler,
				// and surface it loudly -- the current source only uses bare
				// idents, so this means the registration shape changed.
				t.Errorf("add() call at %v: final arg is not a bare handler identifier (%T); update the registration test", call.Pos(), final)
			}
			calls = append(calls, ac)
			return true
		})
	}
	return calls
}

// TestMCPRegistrationCompleteness asserts every tool-handler func is wired into
// the MCP server via an add() call. A handler that exists but is never
// registered is an orphan (unreachable from the wire) and fails the test.
func TestMCPRegistrationCompleteness(t *testing.T) {
	_, files := parsePackageFiles(t)

	handlers := collectHandlers(files)
	if len(handlers) == 0 {
		t.Fatal("no MCP tool handlers found -- signature filter likely broke")
	}

	calls := collectAddCalls(t, files)
	registered := map[string]bool{}
	for _, c := range calls {
		if c.handlerName != "" {
			registered[c.handlerName] = true
		}
	}

	var orphans []string
	for h := range handlers {
		if !registered[h] {
			orphans = append(orphans, h)
		}
	}
	sort.Strings(orphans)

	if len(orphans) > 0 {
		t.Fatalf("found %d unregistered MCP tool handler(s) -- each must be wired via add() in mcp.go:\n  %s\n\n(handlers found: %d, registered: %d)",
			len(orphans), strings.Join(orphans, "\n  "), len(handlers), len(registered))
	}

	t.Logf("OK: %d tool handlers, all registered via add() (%d registrations)", len(handlers), len(registered))
}

// TestMCPNoDuplicateToolNames asserts no two add() calls register the same tool
// name string literal -- a duplicate would silently shadow a tool.
func TestMCPNoDuplicateToolNames(t *testing.T) {
	_, files := parsePackageFiles(t)
	calls := collectAddCalls(t, files)

	seen := map[string]int{}
	for _, c := range calls {
		if c.toolName == "" {
			continue
		}
		seen[c.toolName]++
	}

	var dupes []string
	for name, n := range seen {
		if n > 1 {
			dupes = append(dupes, name)
		}
	}
	sort.Strings(dupes)

	if len(dupes) > 0 {
		t.Fatalf("duplicate tool name(s) registered via add():\n  %s", strings.Join(dupes, "\n  "))
	}

	t.Logf("OK: %d distinct tool names registered", len(seen))
}
