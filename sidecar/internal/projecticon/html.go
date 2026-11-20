package projecticon

import (
	"html"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tdewolff/parse/v2"
	htmlparse "github.com/tdewolff/parse/v2/html"
)

// Read declarations from conventional HTML entry files without starting a server.
func htmlIcons(root, base string) []string {
	var result []string
	for _, file := range []string{"index.html", "public/index.html", "src/index.html"} {
		document := filepath.Join(base, filepath.FromSlash(file))
		for _, href := range iconLinks(readLocal(root, document, 128<<10)) {
			// Support common public-path placeholders in source HTML.
			for _, prefix := range []string{"%PUBLIC_URL%", "%BASE_URL%"} {
				if strings.HasPrefix(href, prefix) {
					href = "/" + strings.TrimPrefix(strings.TrimPrefix(href, prefix), "/")
					break
				}
			}
			ref, err := url.Parse(href)
			if err != nil || ref.Scheme != "" || ref.Host != "" || strings.HasPrefix(ref.Path, "//") || strings.Contains(ref.Path, `\`) {
				continue
			}
			if path, absolute := strings.CutPrefix(ref.Path, "/"); absolute {
				for _, dir := range []string{filepath.Join(base, "public"), base, filepath.Dir(document)} {
					if candidate := relativeIcon(dir, path); candidate != "" {
						result = append(result, candidate)
					}
				}
			} else if candidate := relativeIcon(filepath.Dir(document), ref.Path); candidate != "" {
				result = append(result, candidate)
			}
		}
	}
	return result
}

func iconLinks(data []byte) []string {
	var links []string
	lexer := htmlparse.NewLexer(parse.NewInputBytes(data))
	var inLink bool
	var rel, href string
	for {
		kind, _ := lexer.Next()
		switch kind {
		case htmlparse.ErrorToken:
			return links
		case htmlparse.StartTagToken:
			inLink = string(lexer.Text()) == "link"
			rel, href = "", ""
		case htmlparse.AttributeToken:
			if !inLink {
				continue
			}
			value := string(lexer.AttrVal())
			if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
				value = value[1 : len(value)-1]
			}
			switch string(lexer.AttrKey()) {
			case "rel":
				rel = html.UnescapeString(value)
			case "href":
				href = strings.TrimSpace(html.UnescapeString(value))
			}
		case htmlparse.StartTagCloseToken, htmlparse.StartTagVoidToken:
			if inLink && href != "" && slices.Contains(strings.Fields(strings.ToLower(rel)), "icon") {
				links = append(links, href)
			}
		}
	}
}
