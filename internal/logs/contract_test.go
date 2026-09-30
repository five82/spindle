package logs

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestLogCallsFollowFieldContract enforces the AGENTS.md logging contract on
// every slog call in Spindle and Reel whose keys are literal: WARN carries
// event_type/error_hint/impact, ERROR carries event_type/error_hint/error, a
// decision_type carries decision_result/decision_reason and is never DEBUG,
// and Spindle decision types come from this package's constants so the
// vocabulary the audit tooling keys on stays in one place. Calls that spread
// a built attribute slice are not checked.
func TestLogCallsFollowFieldContract(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var violations []string
	for _, dir := range []string{"internal", "cmd", "reel"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "scripts" || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			spindle := !strings.HasPrefix(rel, "reel"+string(filepath.Separator))
			ast.Inspect(file, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if problem := checkLogCall(call, spindle); problem != "" {
						violations = append(violations, fmt.Sprintf("%s:%d: %s", rel, fset.Position(call.Pos()).Line, problem))
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range violations {
		t.Error(v)
	}
}

var requiredLogKeys = map[string][]string{
	"Warn":  {"event_type", "error_hint", "impact"},
	"Error": {"event_type", "error_hint", "error"},
}

func checkLogCall(call *ast.CallExpr, spindle bool) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	level := strings.TrimSuffix(sel.Sel.Name, "Context")
	switch level {
	case "Debug", "Info", "Warn", "Error":
	default:
		return ""
	}
	if receiver := strings.ToLower(exprName(sel.X)); !strings.Contains(receiver, "log") {
		return ""
	}
	args := call.Args
	if sel.Sel.Name != level {
		if len(args) == 0 {
			return ""
		}
		args = args[1:] // context
	}
	if len(args) == 0 || call.Ellipsis != token.NoPos {
		return ""
	}
	keys := map[string]ast.Expr{}
	for i := 1; i+1 < len(args); i += 2 {
		lit, ok := args[i].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return "" // non-literal key: not statically checkable
		}
		key, _ := strconv.Unquote(lit.Value)
		keys[key] = args[i+1]
	}
	required := requiredLogKeys[level]
	if value, ok := keys["decision_type"]; ok {
		if level == "Debug" {
			return "decision logged at DEBUG; decisions are INFO, DEBUG is raw data"
		}
		if spindle && !isDecisionConstant(value) {
			return "decision_type must be a logs.Decision* constant"
		}
		required = append(required, "decision_result", "decision_reason")
	}
	var missing []string
	for _, key := range required {
		if _, ok := keys[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Sprintf("%s log missing %s", strings.ToUpper(level), strings.Join(missing, ", "))
	}
	return ""
}

func isDecisionConstant(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.SelectorExpr:
		pkg, ok := v.X.(*ast.Ident)
		return ok && pkg.Name == "logs" && strings.HasPrefix(v.Sel.Name, "Decision")
	case *ast.Ident:
		return strings.HasPrefix(v.Name, "Decision")
	}
	return false
}

func exprName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprName(v.X) + "." + v.Sel.Name
	case *ast.CallExpr:
		return exprName(v.Fun)
	}
	return ""
}
