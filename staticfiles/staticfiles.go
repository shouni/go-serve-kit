// Package staticfiles は、埋め込んだ静的ファイルを配信し、パスに応じた Cache-Control を付けます。
//
// 対象は //go:embed した CSS / JS と、assets/static/vendor に置いた第三者製の配布物です。
// 前者は URL を変えずに中身が変わるので短命に、後者はパスにバージョンが入るので不変として
// 扱います。この使い分けと定数を 5 つの兄弟アプリが同じ 30 行で持っていました。
//
//	files, err := staticfiles.New(staticfiles.Config{FS: assets.StaticFiles, Dir: "static"})
//	mux.Handle("/static/", files) // chi なら r.Handle("/static/*", files)
package staticfiles

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// 既定値。5 つの兄弟アプリが 1 バイト違わず同じ値を持っていたものです。
const (
	// DefaultPrefix は配信するパスの先頭です。
	DefaultPrefix = "/static/"

	// DefaultVendorDir は、Dir 配下で「不変」として扱うディレクトリです。ここより下は
	// 第三者製の配布物で、パスにバージョンが入っています（vendor/bootstrap-5.3.8 など）。
	// 更新すれば必ず別の URL になるので、再検証させる理由がありません。
	DefaultVendorDir = "vendor/"

	// DefaultOwnCacheControl は自前の CSS / JS 用です。URL を変えずに中身が変わるため短命にします。
	//
	// //go:embed した FileServer は Last-Modified も ETag も出せない（embed の ModTime が
	// ゼロ値のため net/http が両方を省く）ので、期限が切れた時点で必ず全体を取り直します。
	// バージョン付きの vendor を分けているのは、その再取得を無くすためです。
	DefaultOwnCacheControl = "public, max-age=300, must-revalidate"

	// DefaultVendorCacheControl は DefaultVendorDir 配下用です。
	DefaultVendorCacheControl = "public, max-age=31536000, immutable"
)

// Config は配信の設定です。FS 以外は任意で、ゼロ値は既定へ倒れます。
type Config struct {
	// FS は配信するファイル群です（必須）。//go:embed した embed.FS をそのまま渡せます。
	FS fs.FS

	// Dir は FS の中で配信の根にするディレクトリです（例: "static"）。空なら FS の根です。
	Dir string

	// Prefix は配信するパスの先頭で、"/" で始まり "/" で終わる必要があります。
	// 既定は DefaultPrefix です。
	Prefix string

	// VendorDir は Dir 配下で不変として扱うディレクトリです（末尾 "/"）。既定は DefaultVendorDir です。
	VendorDir string

	// OwnCacheControl / VendorCacheControl は、それぞれの Cache-Control を差し替えます。
	OwnCacheControl    string
	VendorCacheControl string
}

// New は静的ファイルを配信する http.Handler を返します。
//
// GET と HEAD だけを受けます。ディレクトリは一覧を出さず 404 にします。無いファイルも
// 404 で、Cache-Control は付けません（1 年の immutable を 404 に付けると、後から置いた
// ファイルがその期間ブラウザに届きません）。パスの解決は net/http に委ねているので、
// ".." による脱出はそちらが塞ぎます。
func New(cfg Config) (http.Handler, error) {
	if cfg.FS == nil {
		return nil, errors.New("staticfiles: Config.FS must not be nil")
	}
	root := cfg.FS
	if cfg.Dir != "" {
		// fs.Sub は遅延評価で、無いディレクトリでもエラーを返しません。ここで Stat して
		// おかないと、埋め込み先の名前を変えただけの取り違えが起動時ではなく全 404 で現れます。
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
		// FileServer と同じ正規化を先に掛けて、実在するファイルかを見ます。ディレクトリは
		// 一覧を出さず、無いものには Cache-Control を付けません。
		name := strings.TrimPrefix(path.Clean("/"+rel), "/")
		if name == "" || !fs.ValidPath(name) {
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
		files.ServeHTTP(w, r)
	}), nil
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
