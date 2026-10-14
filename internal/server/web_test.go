package server

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebRoutes(t *testing.T) {
	h := New(log.New(io.Discard, "", 0)).WebHandler()
	for path, want := range map[string]int{
		"/r/ABCDEFGHIJKLMNOP": http.StatusOK,       // a link's page
		"/r/sw.js":            http.StatusOK,       // the service worker
		"/r/dl/0123abcd":      http.StatusNotFound, // a retried download must not get the page
		"/r/nothing.js":       http.StatusNotFound,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
	}
}
