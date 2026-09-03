package staticfiles_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/shouni/go-serve-kit/staticfiles"
)

// testFS は、アプリの assets/static と同じ形（自前の CSS と、バージョン付きの vendor）です。
func testFS() fstest.MapFS {
	return fstest.MapFS{
		"static/css/app.css":                          {Data: []byte("body{}")},
		"static/vendor/bootstrap-5.3.8/bootstrap.css": {Data: []byte(".btn{}")},
		"secret.txt":                                  {Data: []byte("not served")},
	}
}

func newHandler(t *testing.T, cfg staticfiles.Config) http.Handler {
	t.Helper()
	h, err := staticfiles.New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return h
}

func get(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// TestServesWithCacheControl は、自前のファイルは短命、vendor 配下は不変として配信されることを
// 確認します。属性は落ちても機能が動くので気付けません。
func TestServesWithCacheControl(t *testing.T) {
	t.Parallel()

	h := newHandler(t, staticfiles.Config{FS: testFS(), Dir: "static"})

	tests := []struct {
		path      string
		wantBody  string
		wantCache string
	}{
		{"/static/css/app.css", "body{}", staticfiles.DefaultOwnCacheControl},
		{"/static/vendor/bootstrap-5.3.8/bootstrap.css", ".btn{}", staticfiles.DefaultVendorCacheControl},
	}
	for _, tt := range tests {
		rec := get(h, http.MethodGet, tt.path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", tt.path, rec.Code)
		}
		if got := rec.Body.String(); got != tt.wantBody {
			t.Errorf("%s: body = %q, want %q", tt.path, got, tt.wantBody)
		}
		if got := rec.Header().Get("Cache-Control"); got != tt.wantCache {
			t.Errorf("%s: Cache-Control = %q, want %q", tt.path, got, tt.wantCache)
		}
	}
}

// TestNotFoundCarriesNoCacheControl は、無いファイルの 404 に Cache-Control が付かないことを
// 確認します。1 年の immutable を 404 に付けると、後から置いたファイルがその期間届きません。
func TestNotFoundCarriesNoCacheControl(t *testing.T) {
	t.Parallel()

	h := newHandler(t, staticfiles.Config{FS: testFS(), Dir: "static"})

	for _, target := range []string{
		"/static/css/missing.css",
		"/static/vendor/bootstrap-9.9.9/bootstrap.css",
		"/elsewhere/app.css", // Prefix の外
	} {
		rec := get(h, http.MethodGet, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", target, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "" {
			t.Errorf("%s: Cache-Control = %q, want none on a 404", target, got)
		}
	}
}

// TestDirectoriesAreNotListed は、ディレクトリの一覧を出さないことを確認します。
// http.FileServer は既定で出します。
func TestDirectoriesAreNotListed(t *testing.T) {
	t.Parallel()

	h := newHandler(t, staticfiles.Config{FS: testFS(), Dir: "static"})

	for _, target := range []string{"/static/", "/static/css/", "/static/vendor", "/static/css"} {
		rec := get(h, http.MethodGet, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404 (no listing)", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "app.css") {
			t.Errorf("%s: body lists directory contents", target)
		}
	}
}

// TestDoesNotEscapeTheRoot は、".." で Dir の外へ出られないことを確認します。
func TestDoesNotEscapeTheRoot(t *testing.T) {
	t.Parallel()

	h := newHandler(t, staticfiles.Config{FS: testFS(), Dir: "static"})

	for _, target := range []string{"/static/../secret.txt", "/static/css/../../secret.txt", "/static/%2e%2e/secret.txt"} {
		rec := get(h, http.MethodGet, target)
		if strings.Contains(rec.Body.String(), "not served") {
			t.Fatalf("%s: served a file outside Dir", target)
		}
	}
}

// TestOnlyGetAndHead は、静的ファイルに GET / HEAD 以外を受けないことを確認します。
func TestOnlyGetAndHead(t *testing.T) {
	t.Parallel()

	h := newHandler(t, staticfiles.Config{FS: testFS(), Dir: "static"})

	if rec := get(h, http.MethodHead, "/static/css/app.css"); rec.Code != http.StatusOK {
		t.Errorf("HEAD status = %d, want 200", rec.Code)
	}
	rec := get(h, http.MethodPost, "/static/css/app.css")
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST status = %d, Allow = %q; want 405 and GET, HEAD", rec.Code, rec.Header().Get("Allow"))
	}
}

// TestConfigOverrides は、Prefix / VendorDir / Cache-Control の差し替えが効くことを確認します。
func TestConfigOverrides(t *testing.T) {
	t.Parallel()

	h := newHandler(t, staticfiles.Config{
		FS:                 testFS(),
		Dir:                "static",
		Prefix:             "/assets/",
		VendorDir:          "vendor/",
		OwnCacheControl:    "no-store",
		VendorCacheControl: "public, max-age=60",
	})

	if rec := get(h, http.MethodGet, "/assets/css/app.css"); rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("own: status = %d, Cache-Control = %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if rec := get(h, http.MethodGet, "/assets/vendor/bootstrap-5.3.8/bootstrap.css"); rec.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Errorf("vendor: Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	if rec := get(h, http.MethodGet, "/static/css/app.css"); rec.Code != http.StatusNotFound {
		t.Errorf("old prefix: status = %d, want 404", rec.Code)
	}
}

// TestNewRejectsBadConfig は、設定の欠けや形の誤りを構築時に止めることを確認します。
func TestNewRejectsBadConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  staticfiles.Config
	}{
		{"nil FS", staticfiles.Config{}},
		{"missing Dir", staticfiles.Config{FS: testFS(), Dir: "nope"}},
		{"prefix without leading slash", staticfiles.Config{FS: testFS(), Prefix: "static/"}},
		{"prefix without trailing slash", staticfiles.Config{FS: testFS(), Prefix: "/static"}},
		{"vendor dir without trailing slash", staticfiles.Config{FS: testFS(), VendorDir: "vendor"}},
	}
	for _, tt := range tests {
		if _, err := staticfiles.New(tt.cfg); err == nil {
			t.Errorf("%s: New() error = nil, want error", tt.name)
		}
	}
}
