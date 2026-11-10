package staticfiles_test

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/shouni/go-serve-kit/staticfiles"
)

// testFS has the same shape as an app's assets/static (own CSS plus a versioned vendor).
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

// TestServesWithCacheControl checks that own files are served short-lived and files under
// vendor as immutable. Losing the header would go unnoticed, since everything still works.
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

// TestNotFoundCarriesNoCacheControl checks that a 404 for a missing file has no
// Cache-Control. A year of immutable on a 404 would keep a file added later from arriving
// for that whole period.
func TestNotFoundCarriesNoCacheControl(t *testing.T) {
	t.Parallel()

	h := newHandler(t, staticfiles.Config{FS: testFS(), Dir: "static"})

	for _, target := range []string{
		"/static/css/missing.css",
		"/static/vendor/bootstrap-9.9.9/bootstrap.css",
		"/elsewhere/app.css", // outside Prefix
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

// TestDirectoriesAreNotListed checks that directories are not listed.
// http.FileServer lists them by default.
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

// TestDoesNotEscapeTheRoot checks that ".." cannot escape Dir.
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

// TestOnlyGetAndHead checks that static files accept nothing but GET / HEAD.
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

// TestConfigOverrides checks that overriding Prefix / VendorDir / Cache-Control takes effect.
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

// TestNewRejectsBadConfig checks that missing or malformed settings are stopped at construction.
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

// TestETagEnablesConditionalRequests checks that an ETag made from the contents is attached
// and that a matching If-None-Match gets a 304. An embedded FileServer cannot emit
// Last-Modified, so without it the whole file is refetched every 5 minutes.
func TestETagEnablesConditionalRequests(t *testing.T) {
	t.Parallel()

	h := newHandler(t, staticfiles.Config{FS: testFS(), Dir: "static"})

	first := get(h, http.MethodGet, "/static/css/app.css")
	tag := first.Header().Get("ETag")
	if tag == "" || !strings.HasPrefix(tag, `"`) || !strings.HasSuffix(tag, `"`) {
		t.Fatalf("ETag = %q, want a quoted strong validator", tag)
	}
	if other := get(h, http.MethodGet, "/static/vendor/bootstrap-5.3.8/bootstrap.css").Header().Get("ETag"); other == tag {
		t.Errorf("different contents share ETag %q", tag)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/static/css/app.css", nil)
	req.Header.Set("If-None-Match", tag)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Errorf("status = %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("304 carries a body: %q", rec.Body.String())
	}
	// The 304 carries Cache-Control too, extending freshness.
	if got := rec.Header().Get("Cache-Control"); got != staticfiles.DefaultOwnCacheControl {
		t.Errorf("304 Cache-Control = %q, want %q", got, staticfiles.DefaultOwnCacheControl)
	}

	// Changed contents change the ETag, and a stale validator refetches with a 200.
	changed := testFS()
	changed["static/css/app.css"] = &fstest.MapFile{Data: []byte("body{margin:0}")}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/static/css/app.css", nil)
	req.Header.Set("If-None-Match", tag)
	newHandler(t, staticfiles.Config{FS: changed, Dir: "static"}).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("ETag") == tag {
		t.Errorf("changed file: status = %d, ETag = %q; want 200 with a new ETag", rec.Code, rec.Header().Get("ETag"))
	}

	if got := get(h, http.MethodGet, "/static/css/missing.css").Header().Get("ETag"); got != "" {
		t.Errorf("404 ETag = %q, want none", got)
	}
}

// TestDisableETag checks that the ETag can be turned off when the FS changes after startup.
func TestDisableETag(t *testing.T) {
	t.Parallel()

	h := newHandler(t, staticfiles.Config{FS: testFS(), Dir: "static", DisableETag: true})

	rec := get(h, http.MethodGet, "/static/css/app.css")
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("ETag"); got != "" {
		t.Errorf("ETag = %q, want none with DisableETag", got)
	}
}

// TestIndexHTMLIsNotRedirected checks that index.html does not become a 301 to the directory.
// http.FileServer redirects ".../index.html" to "./" and then tries to list it.
func TestIndexHTMLIsNotRedirected(t *testing.T) {
	t.Parallel()

	files := testFS()
	files["static/index.html"] = &fstest.MapFile{Data: []byte("<p>")}
	files["static/docs/index.html"] = &fstest.MapFile{Data: []byte("<p>")}
	h := newHandler(t, staticfiles.Config{FS: files, Dir: "static"})

	for _, target := range []string{"/static/index.html", "/static/docs/index.html"} {
		rec := get(h, http.MethodGet, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", target, rec.Code)
		}
		if got := rec.Header().Get("Location"); got != "" {
			t.Errorf("%s: Location = %q, want no redirect", target, got)
		}
		if got := rec.Header().Get("Cache-Control"); got != "" {
			t.Errorf("%s: Cache-Control = %q, want none", target, got)
		}
	}
}

// unreadableFS is an FS where one specific file cannot be opened. It checks that when ETag
// computation hits an unreadable file, startup stops instead of silently serving without ETags.
type unreadableFS struct {
	fs.FS
	name string
}

func (u unreadableFS) Open(name string) (fs.File, error) {
	if name == u.name {
		return nil, &fs.PathError{Op: "open", Path: name, Err: errors.New("unreadable")}
	}
	return u.FS.Open(name)
}

// TestNewFailsWhenETagCannotBeComputed checks that New returns an error when a file's ETag
// cannot be computed, and that with DisableETag it builds without reading the files.
func TestNewFailsWhenETagCannotBeComputed(t *testing.T) {
	t.Parallel()

	broken := unreadableFS{FS: testFS(), name: "static/css/app.css"}
	if _, err := staticfiles.New(staticfiles.Config{FS: broken, Dir: "static"}); err == nil {
		t.Error("New() error = nil, want error for an unreadable file")
	}
	if _, err := staticfiles.New(staticfiles.Config{FS: broken, Dir: "static", DisableETag: true}); err != nil {
		t.Errorf("New() with DisableETag error = %v, want nil", err)
	}
}
