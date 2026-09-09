package staticfiles_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing/fstest"

	"github.com/shouni/go-serve-kit/staticfiles"
)

// 実際には //go:embed した embed.FS を FS に渡します。ここでは代わりに fstest.MapFS を使います。
func ExampleNew() {
	assets := fstest.MapFS{
		"static/css/app.css":                          {Data: []byte("body{}")},
		"static/vendor/bootstrap-5.3.8/bootstrap.css": {Data: []byte(".btn{}")},
	}

	files, err := staticfiles.New(staticfiles.Config{FS: assets, Dir: "static"})
	if err != nil {
		fmt.Println(err) // Dir の取り違えはここで止まります
		return
	}
	mux := http.NewServeMux()
	mux.Handle("/static/", files)

	for _, target := range []string{"/static/css/app.css", "/static/vendor/bootstrap-5.3.8/bootstrap.css", "/static/css/"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		fmt.Printf("%s -> %d %q\n", target, rec.Code, rec.Header().Get("Cache-Control"))
	}
	// Output:
	// /static/css/app.css -> 200 "public, max-age=300, must-revalidate"
	// /static/vendor/bootstrap-5.3.8/bootstrap.css -> 200 "public, max-age=31536000, immutable"
	// /static/css/ -> 404 ""
}
