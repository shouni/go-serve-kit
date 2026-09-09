package secureheaders_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/shouni/go-serve-kit/secureheaders"
)

// 外部オリジンを足すのは、実際に越境するディレクティブだけです。
func ExampleMiddleware() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	handler := secureheaders.Middleware(secureheaders.Config{
		MediaSources:     []string{"https://media.example.com"}, // 署名付き URL へ 302 する場合
		AllowInlineStyle: true,                                  // Bootstrap の collapse / tab を使う場合
	})(mux)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	fmt.Println(rec.Header().Get("Content-Security-Policy"))
	fmt.Println(rec.Header().Get("Strict-Transport-Security"))
	// Output:
	// default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; media-src 'self' https://media.example.com; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'
	// max-age=31536000; includeSubDomains
}

// *Sources にキーワードを渡すと New はエラーを返します（Middleware は panic します）。
func ExampleNew() {
	_, err := secureheaders.New(secureheaders.Config{
		ScriptSources: []string{"'unsafe-inline'"},
	})
	fmt.Println(err)
	// Output:
	// secureheaders: Config.ScriptSources[0] = "'unsafe-inline'": CSP keywords are not accepted in *Sources; use AllowInlineStyle for inline style
}
