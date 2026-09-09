// Package staticfiles は、埋め込んだ静的ファイルを配信し、パスに応じた Cache-Control を付けます。
//
// 対象は //go:embed した CSS / JS と、assets/static/vendor に置いた第三者製の配布物です。
// 前者は URL を変えずに中身が変わるので短命に、後者はパスにバージョンが入るので不変として
// 扱います。この使い分けと定数を 5 つの兄弟アプリが同じ 30 行で持っていました。
//
// ETag は New の時点で中身から計算して付けます。埋め込んだ FS は起動後に変わらないので、
// 期限切れの再検証を 304 で済ませられます（FileServer 単体では Last-Modified も ETag も
// 出せず、期限が切れるたびに全体を取り直していました）。
//
//	files, err := staticfiles.New(staticfiles.Config{FS: assets.StaticFiles, Dir: "static"})
//	mux.Handle("/static/", files) // chi なら r.Handle("/static/*", files)
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
	// //go:embed した FileServer は Last-Modified を出せない（embed の ModTime がゼロ値のため
	// net/http が省く）ので、再検証はこのパッケージが付ける ETag に頼ります。期限が切れると
	// If-None-Match の往復が必ず 1 回入るため、バージョン付きの vendor は分けて往復自体を無くします。
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

	// DisableETag は ETag の計算と付与を止めます。
	//
	// ETag は New の時点で全ファイルの中身から計算し、以後は変わりません。FS が起動後に
	// 変わる場合（os.DirFS で開発中など）は古い ETag に 304 を返してしまうので、true にします。
	// //go:embed した FS では不要です。
	DisableETag bool
}

// New は静的ファイルを配信する http.Handler を返します。
//
// GET と HEAD だけを受けます。ディレクトリは一覧を出さず 404 にします。無いファイルも
// 404 で、Cache-Control は付けません（1 年の immutable を 404 に付けると、後から置いた
// ファイルがその期間ブラウザに届きません）。パスの解決は net/http に委ねているので、
// ".." による脱出はそちらが塞ぎます。
//
// index.html は配信しません。http.FileServer は ".../index.html" をディレクトリへの
// 301 に変えるため、ディレクトリを 404 にしている以上、その入口も閉じておきます。
//
// ETag は DisableETag でない限り、ここで Dir 配下の全ファイルを読んで計算します。
// 起動時に 1 度だけ走る処理で、埋め込んだ静的ファイルの量なら無視できる時間です。
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
		// FileServer と同じ正規化を先に掛けて、実在するファイルかを見ます。ディレクトリは
		// 一覧を出さず、無いものには Cache-Control を付けません。
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
		// FileServer は呼び出し側が先に置いた ETag を If-None-Match の照合に使い、
		// 一致すれば 304 を返します。
		if tag, ok := etags[name]; ok {
			w.Header().Set("ETag", tag)
		}
		files.ServeHTTP(w, r)
	}), nil
}

// indexPage は http.FileServer がディレクトリへの 301 に変えるファイル名です。
const indexPage = "index.html"

// computeETags は root 配下の全ファイルについて、中身のハッシュから強い ETag を作ります。
//
// ハッシュは SHA-256 の先頭 128 ビットです。衝突を気にする長さとして十分で、ヘッダーに
// 載せる文字数は抑えられます。
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
