package respond_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/shouni/go-serve-kit/respond"
)

// 画面と API が同じ URL を共有するルートです。表現だけを Accept で選びます。
func ExampleWantsJSON() {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if respond.WantsJSON(w, r) { // Vary: Accept もここで立ちます
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

// 画面と API が同じ URL を共有するルートでは Error を使います。
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

// JSON しか返さないルートでは ErrorJSON を使います。Accept を送らない呼び出し元にも
// 成功時と同じ形でエラーが返ります。
func ExampleErrorJSON() {
	req := httptest.NewRequest(http.MethodPost, "/comics", nil) // Accept なし
	rec := httptest.NewRecorder()
	respond.ErrorJSON(rec, req, http.StatusBadRequest, "invalid request")
	fmt.Printf("%d %s", rec.Code, rec.Body.String())
	// Output:
	// 400 {"error":"invalid request"}
}
