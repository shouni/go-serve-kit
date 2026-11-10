package respond_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/shouni/go-serve-kit/respond"
)

// A route whose URL is shared by a screen and an API. Only the representation is chosen by Accept.
func ExampleWantsJSON() {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if respond.WantsJSON(w, r) { // Vary: Accept is set here too
			respond.JSON(w, r, http.StatusOK, map[string]string{"title": "first"})
			return
		}
		_, _ = fmt.Fprintln(w, "<h1>first</h1>")
	})

	for _, accept := range []string{"application/json", "text/html"} {
		req := httptest.NewRequest(http.MethodGet, "/comics", nil)
		req.Header.Set("Accept", accept)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		fmt.Printf("%s -> %s / Vary: %s / %s", accept, rec.Header().Get("Content-Type"), rec.Header().Get("Vary"), rec.Body.String())
	}
	// Output:
	// application/json -> application/json; charset=utf-8 / Vary: Accept / {"title":"first"}
	// text/html -> text/html; charset=utf-8 / Vary: Accept / <h1>first</h1>
}

// Use Error on routes whose URL is shared by a screen and an API.
func ExampleError() {
	for _, accept := range []string{"application/json", "text/html"} {
		req := httptest.NewRequest(http.MethodGet, "/comics", nil)
		req.Header.Set("Accept", accept)
		rec := httptest.NewRecorder()
		respond.Error(rec, req, http.StatusNotFound, "not found")
		fmt.Printf("%s -> %d %s", accept, rec.Code, rec.Body.String())
	}
	// Output:
	// application/json -> 404 {"error":"not found"}
	// text/html -> 404 not found
}

// Use ErrorJSON on routes that return only JSON. Callers that send no Accept also get the
// error in the same shape as on success.
func ExampleErrorJSON() {
	req := httptest.NewRequest(http.MethodPost, "/comics", nil) // no Accept
	rec := httptest.NewRecorder()
	respond.ErrorJSON(rec, req, http.StatusBadRequest, "invalid request")
	fmt.Printf("%d %s", rec.Code, rec.Body.String())
	// Output:
	// 400 {"error":"invalid request"}
}
