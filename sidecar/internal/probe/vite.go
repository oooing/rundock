package probe

import (
	"mime"
	"strings"
)

// A 200 catch-all page or an arbitrary JS file is not proof of a Vite server.
// Require a JavaScript response with the client runtime and HMR protocol.
func isViteClient(contentType, body string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || (mediaType != "text/javascript" && mediaType != "application/javascript") {
		return false
	}
	return strings.Contains(body, "createHotContext") &&
		strings.Contains(body, "vite-hmr") && strings.Contains(body, "[vite]")
}
