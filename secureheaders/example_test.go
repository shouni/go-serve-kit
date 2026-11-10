package secureheaders_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/shouni/go-serve-kit/secureheaders"
)

// Add external origins only to the directives that actually cross origins.
func ExampleMiddleware() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	handler := secureheaders.Middleware(secureheaders.Config{
		MediaSources:     []string{"https://media.example.com"}, // when redirecting (302) to signed URLs
		AllowInlineStyle: true,                                  // when using Bootstrap's collapse / tab
	})(mux)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	fmt.Println(rec.Header().Get("Content-Security-Policy"))
	fmt.Println(rec.Header().Get("Strict-Transport-Security"))
	// Output:
	// default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; media-src 'self' https://media.example.com; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'
	// max-age=31536000; includeSubDomains
}

// Passing a keyword to *Sources makes New return an error (Middleware panics).
func ExampleNew() {
	_, err := secureheaders.New(secureheaders.Config{
		ScriptSources: []string{"'unsafe-inline'"},
	})
	fmt.Println(err)
	// Output:
	// secureheaders: Config.ScriptSources[0] = "'unsafe-inline'": CSP keywords are not accepted in *Sources; use AllowInlineStyle for inline style
}
