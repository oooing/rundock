package projecticon

import (
	"bytes"
	"encoding/xml"
	"io"
	"regexp"
	"strings"
)

var svgTags = wordSet("svg g path rect circle ellipse line polyline polygon defs linearGradient radialGradient stop clipPath title desc")
var svgAttrs = wordSet("viewBox width height x y x1 x2 y1 y2 cx cy r rx ry d points fill fill-rule fill-opacity stroke stroke-width stroke-linecap stroke-linejoin stroke-miterlimit stroke-dasharray stroke-dashoffset stroke-opacity opacity transform id offset stop-color stop-opacity gradientUnits gradientTransform spreadMethod clip-path clip-rule preserveAspectRatio")
var localPaint = regexp.MustCompile(`^url\(#[A-Za-z_][A-Za-z0-9_.-]*\)$`)

func wordSet(words string) map[string]bool {
	m := map[string]bool{}
	for _, word := range strings.Fields(words) {
		m[word] = true
	}
	return m
}

// Re-encode a static subset. No script, CSS, foreignObject, animation, use/image,
// event handlers, external references or XML entities can leave this boundary.
func safeSVG(data []byte) []byte {
	d := xml.NewDecoder(bytes.NewReader(data))
	var out bytes.Buffer
	e := xml.NewEncoder(&out)
	depth, roots, count := 0, 0, 0
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil
		}
		count++
		if count > 10000 {
			return nil
		}
		switch v := token.(type) {
		case xml.StartElement:
			if !svgTags[v.Name.Local] || (v.Name.Space != "" && v.Name.Space != "http://www.w3.org/2000/svg") {
				return nil
			}
			if depth == 0 {
				roots++
				if roots != 1 || v.Name.Local != "svg" {
					return nil
				}
			}
			depth++
			if depth > 32 {
				return nil
			}
			clean := xml.StartElement{Name: xml.Name{Local: v.Name.Local}}
			if depth == 1 {
				clean.Attr = append(clean.Attr, xml.Attr{Name: xml.Name{Local: "xmlns"}, Value: "http://www.w3.org/2000/svg"})
			}
			for _, a := range v.Attr {
				if a.Name.Space != "" || !svgAttrs[a.Name.Local] {
					continue
				}
				value := strings.TrimSpace(a.Value)
				// Reject CSS escapes/imports and all non-fragment paint references.
				lower := strings.ToLower(value)
				if strings.ContainsAny(value, `\<>@`) || strings.Contains(lower, "url") && !localPaint.MatchString(value) {
					return nil
				}
				clean.Attr = append(clean.Attr, xml.Attr{Name: xml.Name{Local: a.Name.Local}, Value: value})
			}
			if e.EncodeToken(clean) != nil {
				return nil
			}
		case xml.EndElement:
			depth--
			if e.EncodeToken(xml.EndElement{Name: xml.Name{Local: v.Name.Local}}) != nil {
				return nil
			}
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(v)) != "" {
				return nil
			}
			if e.EncodeToken(v) != nil {
				return nil
			}
		case xml.Directive:
			return nil
			// XML declaration and comments aren't needed in an image response.
		}
	}
	if roots != 1 || depth != 0 || e.Flush() != nil {
		return nil
	}
	return out.Bytes()
}
