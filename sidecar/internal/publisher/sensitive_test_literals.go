package publisher

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// Only parsed Go test literals qualify. Comments, malformed source, unknown
// expressions and production files continue through the normal detector.
type testLiteralRange struct {
	start, end     int
	placeholderKey bool
}
type testLiteralEvidence []testLiteralRange

func (ranges testLiteralEvidence) contains(offset int, privateKey bool) bool {
	for _, r := range ranges {
		if offset >= r.start && offset < r.end && (!privateKey || r.placeholderKey) {
			return true
		}
	}
	return false
}

func knownFixtureGitHubToken(value string) bool {
	// Exact deterministic examples, not a prefix or entropy exemption.
	return value == "ghp_"+"abcdefghijklmnopqrstuvwxyz0123456789" ||
		value == "ghp_"+"abcdefghijklmnopqrstuvwxyz1234567890"
}

func testConstantString(expr ast.Expr, depth int) (string, bool) {
	if depth > 16 {
		return "", false
	}
	switch x := expr.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			v, err := strconv.Unquote(x.Value)
			return v, err == nil
		}
	case *ast.ParenExpr:
		return testConstantString(x.X, depth+1)
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			a, okA := testConstantString(x.X, depth+1)
			b, okB := testConstantString(x.Y, depth+1)
			if len(a)+len(b) <= 1<<20 {
				return a + b, okA && okB
			}
		}
	case *ast.Ident:
		if x.Obj != nil && x.Obj.Kind == ast.Con {
			if spec, ok := x.Obj.Decl.(*ast.ValueSpec); ok && len(spec.Names) == 1 && len(spec.Values) == 1 {
				return testConstantString(spec.Values[0], depth+1)
			}
		}
	}
	return "", false
}

func inspectTestLiterals(rel string, content []byte) testLiteralEvidence {
	if !strings.HasSuffix(strings.ToLower(rel), "_test.go") {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, content, 0)
	if err != nil {
		return nil
	}
	var ranges testLiteralEvidence
	var visitExpr func(ast.Expr)
	visitExpr = func(expr ast.Expr) {
		// A test comparing a line with the header alone contains no key body.
		if comparison, ok := expr.(*ast.BinaryExpr); ok && (comparison.Op == token.EQL || comparison.Op == token.NEQ) {
			for _, operand := range []ast.Expr{comparison.X, comparison.Y} {
				if lit, ok := operand.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					value, err := strconv.Unquote(lit.Value)
					if err == nil && pem.FindString(value) == value && value != "" {
						ranges = append(ranges, testLiteralRange{fset.Position(lit.Pos()).Offset, fset.Position(lit.End()).Offset, true})
					}
				}
			}
		}
		// Join only adjacent statically known operands. Never infer variable values
		// or execute fixture builders. Unknown operands break the evidence segment.
		var operands []ast.Expr
		var flatten func(ast.Expr)
		flatten = func(e ast.Expr) {
			if b, ok := e.(*ast.BinaryExpr); ok && b.Op == token.ADD {
				flatten(b.X)
				flatten(b.Y)
			} else {
				operands = append(operands, e)
			}
		}
		flatten(expr)
		var literals []*ast.BasicLit
		segment := ""
		flush := func() {
			placeholder := placeholderPrivateKeySegment(segment)
			for _, lit := range literals {
				ranges = append(ranges, testLiteralRange{fset.Position(lit.Pos()).Offset, fset.Position(lit.End()).Offset, placeholder})
			}
			segment = ""
			literals = nil
		}
		for _, operand := range operands {
			value, known := testConstantString(operand, 0)
			if !known || len(segment)+len(value) > 1<<20 {
				flush()
				ast.Inspect(operand, func(n ast.Node) bool {
					if n == operand {
						return true
					}
					if e, ok := n.(ast.Expr); ok {
						visitExpr(e)
						return false
					}
					return true
				})
				continue
			}
			segment += value
			ast.Inspect(operand, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					literals = append(literals, lit)
				}
				return true
			})
		}
		flush()
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if expr, ok := n.(ast.Expr); ok {
			visitExpr(expr)
			return false
		}
		return true
	})
	return ranges
}

func placeholderPrivateKeySegment(value string) bool {
	locations := pem.FindAllStringIndex(value, -1)
	if len(locations) == 0 {
		return false
	}
	for _, loc := range locations {
		tail := value[loc[1]:]
		if end := strings.Index(tail, "-----END "); end >= 0 {
			tail = tail[:end]
		}
		lines := strings.Split(strings.TrimSpace(tail), "\n")
		body := strings.TrimSpace(lines[0])
		// Deliberately tiny allowlist: a real-looking body after a TEST-ONLY
		// comment is not sufficient. Unknown/truncated/encoded data stays blocked.
		if body != "abc" && body != "example" && body != "TEST-ONLY-NOT-A-REAL-KEY" && body != "TEST-ONLY-CONTEXT-NOT-A-REAL-SECRET" {
			return false
		}
		for _, line := range lines[1:] {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "# ") {
				return false
			}
		}
	}
	return true
}
