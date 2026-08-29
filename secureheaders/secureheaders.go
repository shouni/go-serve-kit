// Package secureheaders は、ブラウザ向けの防御的なレスポンスヘッダーを
// 全応答へ付けるミドルウェアを提供します。
//
// 既定は「外部オリジンを 1 つも許可せず、インラインスタイルも許さない」です。
// 第三者製の JS/CSS を CDN からではなく自前配信していること（assets/static/vendor）
// を前提にしており、足りない分は Config の *Sources と AllowInlineStyle で開けます。
//
//	r.Use(secureheaders.Middleware(secureheaders.Config{
//	    MediaSources: []string{"https://storage.googleapis.com"},
//	}))
package secureheaders

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 既定値。5 つの兄弟アプリが 1 バイト違わず同じ値を持っていたものです。
const (
	// DefaultReferrerPolicy は same-origin です。外部オリジンへの参照を 1 つも
	// 持たないため、ここまで絞れます。唯一の越境は署名付き URL への 302 で、
	// GCS は Referer を見ません。
	DefaultReferrerPolicy = "same-origin"

	// DefaultPermissionsPolicy は、使っていない機能だけを塞ぎます。
	// autoplay は入れません。履歴画面が音声や動画を続けて再生するためです。
	DefaultPermissionsPolicy = "geolocation=(), camera=(), microphone=(), payment=(), usb=()"

	// DefaultHSTSMaxAge は 1 年です。Cloud Run は HTTPS でしか受けないので
	// 現状の実害はありませんが、独自ドメインを当てたときに平文へ降格させない
	// ための宣言です。preload は付けません（撤回にブラウザベンダーへの申請が
	// 要るうえ、得るものが少ないため）。
	DefaultHSTSMaxAge = 365 * 24 * time.Hour
)

// Config は付与するヘッダーの設定です。すべて任意で、ゼロ値は既定へ倒れます。
type Config struct {
	// ImageSources / MediaSources は、CSP の img-src / media-src に足す
	// 外部オリジンです。GCS の署名付き URL へ 302 する画面がここを要します。
	//
	// 画面が指すのは同一オリジンのエンドポイントですが、そこから GCS へ
	// リダイレクトします。リダイレクト先を CSP がどう扱うかはブラウザ実装に
	// 幅があるため、送り先を明示して依存しないようにします。
	ImageSources []string
	MediaSources []string

	// ScriptSources / StyleSources / ConnectSources は、それぞれ script-src /
	// style-src / connect-src に足す外部オリジンです。
	//
	// CDN を script-src に載せるのは避けてください。jsDelivr のようなホストは
	// npm の全パッケージを配信しているため、「任意の npm パッケージの読み込みを
	// 許可する」に等しくなります（既知の CSP バイパス・ガジェットを持ち込めます）。
	ScriptSources  []string
	StyleSources   []string
	ConnectSources []string

	// AllowInlineStyle は style-src に 'unsafe-inline' を足します。
	//
	// 既定は false（付けない）です。インラインスタイルを当てる JS を積んでいる
	// 場合にだけ true にしてください（Bootstrap の collapse / tab が該当します）。
	// 既定を false にしてあるのは、必要としないアプリまで巻き込まないためで、
	// どのアプリがこれを要求しているかが設定に現れます。
	//
	// script-src に対応する項目は用意していません。インラインスクリプトを
	// 許すと CSP が防いでいるものの大半が無くなるためです。
	AllowInlineStyle bool

	// ContentSecurityPolicy は、組み立てを使わず CSP 全体を指定します。
	// 空なら他の項目から組み立てます。
	//
	// 最後の手段です。これを渡すと object-src 'none' や base-uri 'none' まで
	// 呼び出し側の責任になります。1 つのディレクティブを緩めたいだけなら、
	// 上の *Sources を使ってください。
	ContentSecurityPolicy string

	// ReferrerPolicy / PermissionsPolicy は空なら既定値です。
	ReferrerPolicy    string
	PermissionsPolicy string

	// HSTSMaxAge は Strict-Transport-Security の max-age です。
	// 0 なら既定（1 年）、負値なら HSTS を付けません。
	HSTSMaxAge time.Duration
}

// Middleware は、設定に基づく防御的ヘッダーを全応答へ付けるミドルウェアを返します。
//
// ヘッダーの値はリクエストごとに変わらないため、組み立ては 1 度だけ行います。
func Middleware(cfg Config) func(http.Handler) http.Handler {
	values := cfg.headers()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := w.Header()
			for name, value := range values {
				header.Set(name, value)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// headers は、実際に付ける名前と値の対応を組み立てます。
func (c Config) headers() map[string]string {
	values := map[string]string{
		"Content-Security-Policy": c.contentSecurityPolicy(),
		// MIME スニッフィングを止めます。署名付き URL へ 302 する経路があるため、
		// 取り違えが起きたときの被害を型で抑えます。
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        orDefault(c.ReferrerPolicy, DefaultReferrerPolicy),
		"Permissions-Policy":     orDefault(c.PermissionsPolicy, DefaultPermissionsPolicy),
	}

	if maxAge := c.hstsMaxAge(); maxAge > 0 {
		values["Strict-Transport-Security"] = "max-age=" +
			strconv.FormatInt(int64(maxAge.Seconds()), 10) + "; includeSubDomains"
	}
	return values
}

// contentSecurityPolicy は CSP を組み立てます。
//
// 既定はどのディレクティブも 'self' だけで、外部オリジンは Config で足した分しか
// 入りません。object-src / base-uri / frame-ancestors / form-action は調整点を
// 用意していません。緩める理由が無く、緩めた状態で気付かれないほうが高くつきます。
func (c Config) contentSecurityPolicy() string {
	if csp := strings.TrimSpace(c.ContentSecurityPolicy); csp != "" {
		return csp
	}

	// 'unsafe-inline' は 'self' の直後に置きます（順序に意味はありませんが、
	// 緩めている事実がディレクティブの先頭で読めるようにするためです）。
	style := []string{"'self'"}
	if c.AllowInlineStyle {
		style = append(style, "'unsafe-inline'")
	}
	style = append(style, c.StyleSources...)

	return strings.Join([]string{
		"default-src 'self'",
		sourceList("script-src", append([]string{"'self'"}, c.ScriptSources...)),
		sourceList("style-src", style),
		sourceList("img-src", append([]string{"'self'", "data:"}, c.ImageSources...)),
		sourceList("media-src", append([]string{"'self'"}, c.MediaSources...)),
		"font-src 'self'",
		sourceList("connect-src", append([]string{"'self'"}, c.ConnectSources...)),
		"object-src 'none'",
		"base-uri 'none'",
		"frame-ancestors 'none'",
		"form-action 'self'",
	}, "; ")
}

func (c Config) hstsMaxAge() time.Duration {
	if c.HSTSMaxAge == 0 {
		return DefaultHSTSMaxAge
	}
	return c.HSTSMaxAge
}

// sourceList は "img-src 'self' data: https://..." の形へ組み立てます。
// 空白のみの要素は落とします（環境変数から分割した値が混ざっても、
// ディレクティブが壊れないようにするためです）。
func sourceList(directive string, sources []string) string {
	parts := make([]string, 0, len(sources)+1)
	parts = append(parts, directive)
	for _, source := range sources {
		if trimmed := strings.TrimSpace(source); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, " ")
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
