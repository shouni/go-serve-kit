// Package staticfiles serves embedded static files with a Cache-Control decided by path.
//
// It covers your own CSS / JS embedded with //go:embed, and third-party distributions placed
// under vendor/ in Dir. The former change content without changing URL, so they are
// short-lived; the latter carry the version in the path, so they are treated as immutable.
//
// An ETag is computed from the contents at New and attached. A FileServer over //go:embed
// cannot emit Last-Modified (net/http omits it because ModTime is the zero value), so
// without the ETag every expiry would refetch the whole file.
//
//	files, err := staticfiles.New(staticfiles.Config{FS: assets.StaticFiles, Dir: "static"})
//	mux.Handle("/static/", files) // with chi: r.Handle("/static/*", files)
package staticfiles

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const (
	// DefaultPrefix is the leading part of the served path.
	DefaultPrefix = "/static/"

	// DefaultVendorDir is the directory under Dir treated as immutable. It is meant for
	// third-party distributions with the version in the path (vendor/bootstrap-5.3.8 and the
	// like); an update always means a different URL, so even the revalidation round trip is
	// skipped.
	DefaultVendorDir = "vendor/"

	// DefaultOwnCacheControl is for your own CSS / JS. Their content changes without the URL
	// changing, so they are short-lived and revalidated with the ETag once expired.
	DefaultOwnCacheControl = "public, max-age=300, must-revalidate"

	// DefaultVendorCacheControl is for files under DefaultVendorDir.
	DefaultVendorCacheControl = "public, max-age=31536000, immutable"
)

// Config configures serving. Every field except FS is optional, and zero values fall back
// to the defaults.
type Config struct {
	// FS holds the files to serve (required). An embed.FS from //go:embed can be passed as is.
	FS fs.FS

	// Dir is the directory in FS used as the serving root (for example "static"). If empty,
	// the root of FS is used.
	Dir string

	// Prefix is the leading part of the served path and must start and end with "/".
	// The default is DefaultPrefix.
	Prefix string

	// VendorDir is the directory under Dir treated as immutable (with a trailing "/").
	// The default is DefaultVendorDir.
	VendorDir string

	// OwnCacheControl / VendorCacheControl replace the respective Cache-Control values.
	OwnCacheControl    string
	VendorCacheControl string

	// DisableETag turns off computing and attaching the ETag. The ETag is computed at New and
	// never changes afterwards, so set it to true when the FS changes after startup (os.DirFS
	// during development, for example) to avoid answering a stale ETag with a 304.
	DisableETag bool
}

// New returns an http.Handler that serves the static files.
//
// It accepts GET and HEAD only. Directories and index.html get a 404 rather than a listing or
// a 301 (http.FileServer turns index.html into a 301 to the directory). Missing files are 404
// too, with no Cache-Control (a year of immutable on a 404 would keep a file added later from
// reaching browsers for that whole period). ".." is folded inside the root by the same
// normalization as http.FileServer.
//
// Unless DisableETag is set, it reads every file under Dir here to compute the ETags. This
// runs once at startup and takes negligible time for the amount of embedded static files.
func New(cfg Config) (http.Handler, error) {
	if cfg.FS == nil {
		return nil, errors.New("staticfiles: Config.FS must not be nil")
	}
	root := cfg.FS
	if cfg.Dir != "" {
		// fs.Sub is lazy and returns no error even for a missing directory. Without this Stat,
		// a mix-up as simple as renaming the embed target would surface as 404s everywhere
		// instead of a startup failure.
		info, err := fs.Stat(cfg.FS, cfg.Dir)
		if err != nil {
			return nil, fmt.Errorf("staticfiles: Config.Dir %q: %w", cfg.Dir, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("staticfiles: Config.Dir %q is not a directory", cfg.Dir)
		}
		sub, err := fs.Sub(cfg.FS, cfg.Dir)
		if err != nil {
			return nil, fmt.Errorf("staticfiles: Config.Dir %q: %w", cfg.Dir, err)
		}
		root = sub
	}

	prefix := orDefault(cfg.Prefix, DefaultPrefix)
	if !strings.HasPrefix(prefix, "/") || !strings.HasSuffix(prefix, "/") {
		return nil, fmt.Errorf("staticfiles: Config.Prefix %q must start and end with /", prefix)
	}
	vendorDir := strings.TrimPrefix(orDefault(cfg.VendorDir, DefaultVendorDir), "/")
	if !strings.HasSuffix(vendorDir, "/") {
		return nil, fmt.Errorf("staticfiles: Config.VendorDir %q must end with /", vendorDir)
	}
	own := orDefault(cfg.OwnCacheControl, DefaultOwnCacheControl)
	vendor := orDefault(cfg.VendorCacheControl, DefaultVendorCacheControl)

	var etags map[string]string
	if !cfg.DisableETag {
		tags, err := computeETags(root)
		if err != nil {
			return nil, err
		}
		etags = tags
	}

	files := http.StripPrefix(prefix, http.FileServer(http.FS(root)))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		rel, ok := strings.CutPrefix(r.URL.Path, prefix)
		if !ok {
			http.NotFound(w, r)
			return
		}
		// Apply the same normalization as FileServer first and check that the file exists.
		// Directories are not listed, and missing files get no Cache-Control.
		name := strings.TrimPrefix(path.Clean("/"+rel), "/")
		if name == "" || !fs.ValidPath(name) || path.Base(name) == indexPage {
			http.NotFound(w, r)
			return
		}
		info, err := fs.Stat(root, name)
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}

		if strings.HasPrefix(name, vendorDir) {
			w.Header().Set("Cache-Control", vendor)
		} else {
			w.Header().Set("Cache-Control", own)
		}
		// FileServer matches If-None-Match against an ETag the caller has already set and
		// returns 304 when they match.
		if tag, ok := etags[name]; ok {
			w.Header().Set("ETag", tag)
		}
		files.ServeHTTP(w, r)
	}), nil
}

// indexPage is the file name http.FileServer turns into a 301 to the directory.
const indexPage = "index.html"

// computeETags makes a strong ETag for every file under root from the first 128 bits of the
// SHA-256 of its contents. That is long enough not to worry about collisions while keeping
// the header short.
func computeETags(root fs.FS) (map[string]string, error) {
	etags := make(map[string]string)
	err := fs.WalkDir(root, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(root, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		etags[name] = `"` + hex.EncodeToString(sum[:16]) + `"`
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("staticfiles: computing ETags: %w", err)
	}
	return etags, nil
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
