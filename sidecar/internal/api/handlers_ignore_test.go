package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReleaseIgnoreRequestBoundaries(t *testing.T) {
	router, _, closeDB := newReleaseConfigAPIFixture(t)
	defer closeDB()
	for _, tc := range []struct {
		method, origin, contentType, body string
		status                            int
	}{
		{"GET", "", "", "", http.StatusMethodNotAllowed},
		{"POST", "https://example.invalid", "application/json", `{}`, http.StatusForbidden},
		{"POST", "http://127.0.0.1:17656", "text/plain", `{}`, http.StatusForbidden},
		{"POST", "http://localhost:17656", "application/json", `{`, http.StatusBadRequest},
		{"POST", "http://127.0.0.1:17656", "application/json", `{"path":"../outside.txt"}`, http.StatusBadRequest},
	} {
		r := httptest.NewRequest(tc.method, "/api/apps/app1/release/ignore-file", strings.NewReader(tc.body))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", tc.contentType)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%+v: got %d %s", tc, w.Code, w.Body.String())
		}
	}
}
