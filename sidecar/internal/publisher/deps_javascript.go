package publisher

import (
	"io"
	"strings"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/html"
	"github.com/tdewolff/parse/v2/js"
)

type dependencyJSToken struct {
	kind js.TokenType
	text string
}

// Only inspect executable tokens. In particular, generated source passed to
// writeFileSync is a string, not an import relative to the generator's directory.
// This is a local-reference check, not a replacement for a build/type check.
func localJSImportSpecs(content string) []string {
	tokens := dependencyJSTokens(content)
	specs := []string{}
	add := func(i int) {
		if i >= len(tokens) || tokens[i].kind != js.StringToken {
			return
		}
		text := tokens[i].text
		if len(text) < 2 {
			return
		}
		spec := text[1 : len(text)-1]
		// Escaped/computed specifiers are not certain filesystem paths. Do not
		// manufacture a blocking missing-file finding from their source text.
		if (strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../")) && !strings.Contains(spec, `\`) {
			specs = append(specs, spec)
		}
	}
	for i, token := range tokens {
		if i > 0 && (tokens[i-1].kind == js.DotToken || tokens[i-1].kind == js.OptChainToken) {
			continue // obj.require()/obj.import() are not module imports.
		}
		isImport, isExport := token.kind == js.ImportToken, token.kind == js.ExportToken
		isRequire := token.kind == js.IdentifierToken && token.text == "require"
		if !isImport && !isExport && !isRequire || i+1 >= len(tokens) {
			continue
		}
		next := tokens[i+1].kind
		if (isImport || isRequire) && next == js.OpenParenToken {
			if i+3 < len(tokens) && (tokens[i+3].kind == js.CloseParenToken || (isImport && tokens[i+3].kind == js.CommaToken)) {
				add(i + 2) // Only a literal argument, not './prefix' + variable.
			}
			continue
		}
		if isRequire {
			continue
		}
		if isImport && next == js.StringToken {
			add(i + 1) // Side-effect import.
			continue
		}
		// An import/export clause may contain identifiers, aliases, braces,
		// commas and '*'. Stop at anything else instead of crossing statements
		// (e.g. export default followed by an unrelated "from" string).
		depth := 0
		for j := i + 1; j < len(tokens); j++ {
			t := tokens[j]
			if t.kind == js.FromToken && depth == 0 && j+1 < len(tokens) && tokens[j+1].kind == js.StringToken {
				add(j + 1)
				break
			}
			if t.kind == js.OpenBraceToken {
				depth++
			} else if t.kind == js.CloseBraceToken && depth > 0 {
				depth--
			} else if t.kind != js.CommaToken && t.kind != js.MulToken &&
				!(js.IsIdentifier(t.kind) || (depth > 0 && js.IsIdentifierName(t.kind))) {
				break
			}
		}
	}
	return specs
}

func dependencyJSTokens(content string) []dependencyJSToken {
	input := parse.NewInputString(content)
	lexer := js.NewLexer(input)
	tokens := []dependencyJSToken{}
	expectExpression := true
	previous := js.ErrorToken
	controlParens := []bool{}
	for {
		before := input.Offset()
		kind, raw := lexer.Next()
		if (kind == js.DivToken || kind == js.DivEqToken) && expectExpression {
			kind, raw = lexer.RegExp()
		}
		if kind == js.ErrorToken {
			if lexer.Err() == io.EOF || input.Offset() <= before {
				break
			}
			// Keep a boundary on unsupported syntax; never fall back to matching
			// arbitrary source text (which would reintroduce string false positives).
			tokens = append(tokens, dependencyJSToken{kind: js.SemicolonToken})
			expectExpression = true
			continue
		}
		if kind == js.WhitespaceToken || kind == js.LineTerminatorToken || kind == js.CommentToken || kind == js.CommentLineTerminatorToken {
			continue
		}
		tokens = append(tokens, dependencyJSToken{kind, string(raw)})
		switch kind {
		case js.OpenParenToken:
			control := previous == js.IfToken || previous == js.WhileToken || previous == js.ForToken || previous == js.WithToken || previous == js.SwitchToken || previous == js.CatchToken
			controlParens = append(controlParens, control)
			expectExpression = true
		case js.CloseParenToken:
			expectExpression = false
			if n := len(controlParens); n > 0 {
				expectExpression = controlParens[n-1]
				controlParens = controlParens[:n-1]
			}
		case js.StringToken, js.TemplateToken, js.TemplateEndToken, js.RegExpToken,
			js.CloseBracketToken, js.CloseBraceToken, js.IncrToken, js.DecrToken,
			js.ThisToken, js.SuperToken, js.TrueToken, js.FalseToken, js.NullToken:
			expectExpression = false
		case js.TemplateStartToken, js.TemplateMiddleToken:
			expectExpression = true // ${...} contains real executable references.
		case js.DotToken, js.OptChainToken, js.LtToken:
			expectExpression = false // Also avoid treating a JSX closing tag as a regexp.
		default:
			expectExpression = !js.IsIdentifier(kind) && !js.IsNumeric(kind)
		}
		previous = kind
	}
	return tokens
}

// Vue template/style text can contain import examples. Only script blocks are
// JavaScript/TypeScript; the HTML lexer also skips commented-out script blocks.
func localVueImportSpecs(content string) []string {
	lexer := html.NewLexer(parse.NewInputString(content))
	inScript := false
	specs := []string{}
	for {
		kind, raw := lexer.Next()
		switch kind {
		case html.ErrorToken:
			return specs
		case html.StartTagToken:
			inScript = strings.EqualFold(string(lexer.Text()), "script")
		case html.EndTagToken, html.StartTagVoidToken:
			inScript = false
		case html.TextToken:
			if inScript {
				specs = append(specs, localJSImportSpecs(string(raw))...)
			}
		}
	}
}
