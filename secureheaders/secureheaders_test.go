package secureheaders_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shouni/go-serve-kit/secureheaders"
)

// serve は、ミドルウェアを通した応答のヘッダーを返します。
func serve(t *testing.T, cfg secureheaders.Config) http.Header {
	t.Helper()

	var reached bool
	handler := secureheaders.Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !reached {
		t.Fatal("次のハンドラーが呼ばれていません")
	}
	return rec.Header()
}

func TestDefaults(t *testing.T) {
	header := serve(t, secureheaders.Config{})

	want := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "same-origin",
		"Permissions-Policy":        "geolocation=(), camera=(), microphone=(), payment=(), usb=()",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
		"Content-Security-Policy": "default-src 'self'; script-src 'self'; " +
			"style-src 'self'; img-src 'self' data:; media-src 'self'; " +
			"font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; " +
			"frame-ancestors 'none'; form-action 'self'",
	}

	for name, value := range want {
		if got := header.Get(name); got != value {
			t.Errorf("%s = %q,\n want %q", name, got, value)
		}
	}
}

// TestMatchesCurrentAppPolicies は、5 つの兄弟アプリが今出している CSP を
// この組み立てで再現できることを確認します。
//
// adk-review と ap-story は media-src を書いていませんが、default-src が 'self'
// である以上 media-src 'self' は書いても書かなくても同じ意味です。引き上げで
// 挙動が変わらないことを、ここで明示しておきます。
func TestMatchesCurrentAppPolicies(t *testing.T) {
	const gcs = "https://storage.googleapis.com"

	tests := []struct {
		app      string
		cfg      secureheaders.Config
		wantImg  string
		wantMedi string
	}{
		{
			app:      "adk-review（外部オリジンなし）",
			cfg:      secureheaders.Config{},
			wantImg:  "img-src 'self' data:",
			wantMedi: "media-src 'self'",
		},
		{
			app:      "ap-comp / ap-mv（画像も音声も GCS）",
			cfg:      secureheaders.Config{ImageSources: []string{gcs}, MediaSources: []string{gcs}},
			wantImg:  "img-src 'self' data: " + gcs,
			wantMedi: "media-src 'self' " + gcs,
		},
		{
			app:      "ap-story（画像だけ GCS）",
			cfg:      secureheaders.Config{ImageSources: []string{gcs}},
			wantImg:  "img-src 'self' data: " + gcs,
			wantMedi: "media-src 'self'",
		},
		{
			app:      "ap-voice（音声だけ GCS）",
			cfg:      secureheaders.Config{MediaSources: []string{gcs}},
			wantImg:  "img-src 'self' data:",
			wantMedi: "media-src 'self' " + gcs,
		},
	}

	for _, tt := range tests {
		t.Run(tt.app, func(t *testing.T) {
			csp := serve(t, tt.cfg).Get("Content-Security-Policy")

			for _, want := range []string{tt.wantImg, tt.wantMedi} {
				if !strings.Contains(csp, want+";") && !strings.HasSuffix(csp, want) {
					t.Errorf("CSP に %q が含まれていません:\n %s", want, csp)
				}
			}
			// 外部オリジンを許すのは img-src / media-src だけ。
			for _, directive := range []string{"script-src 'self';", "style-src 'self';"} {
				if !strings.Contains(csp, directive) {
					t.Errorf("CSP に %q が含まれていません:\n %s", directive, csp)
				}
			}
		})
	}
}

func TestExplicitCSPWins(t *testing.T) {
	const custom = "default-src 'none'"

	header := serve(t, secureheaders.Config{
		ContentSecurityPolicy: custom,
		// 明示した場合、組み立て用の設定は無視される。
		MediaSources: []string{"https://example.com"},
	})

	if got := header.Get("Content-Security-Policy"); got != custom {
		t.Errorf("CSP = %q, want %q", got, custom)
	}
}

func TestHSTS(t *testing.T) {
	tests := []struct {
		name   string
		maxAge time.Duration
		want   string
	}{
		{"未指定は 1 年", 0, "max-age=31536000; includeSubDomains"},
		{"指定した長さ", 30 * 24 * time.Hour, "max-age=2592000; includeSubDomains"},
		{"負値なら付けない", -1, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serve(t, secureheaders.Config{HSTSMaxAge: tt.maxAge}).Get("Strict-Transport-Security")
			if got != tt.want {
				t.Errorf("Strict-Transport-Security = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPolicyOverrides(t *testing.T) {
	header := serve(t, secureheaders.Config{
		ReferrerPolicy:    "no-referrer",
		PermissionsPolicy: "camera=()",
	})

	if got := header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q", got)
	}
	if got := header.Get("Permissions-Policy"); got != "camera=()" {
		t.Errorf("Permissions-Policy = %q", got)
	}
}

// TestBlankSourcesAreDropped は、環境変数から分割した値に空要素が混ざっても
// ディレクティブが壊れないことを検証します。
func TestBlankSourcesAreDropped(t *testing.T) {
	csp := serve(t, secureheaders.Config{
		MediaSources: []string{"", "  ", "https://example.com", " "},
	}).Get("Content-Security-Policy")

	if !strings.Contains(csp, "media-src 'self' https://example.com;") {
		t.Errorf("media-src が壊れています:\n %s", csp)
	}
	if strings.Contains(csp, "  ") {
		t.Errorf("CSP に余分な空白が入っています:\n %s", csp)
	}
}

// TestConfigIsNotMutated は、同じ Config を使い回しても CSP が育たないことを
// 検証します。組み立てで append を使うため、呼び出し側のスライスを
// 書き換えていないかを押さえます。
func TestConfigIsNotMutated(t *testing.T) {
	sources := make([]string, 1, 4) // append が書き戻せる余地を残す
	sources[0] = "https://a.example.com"

	cfg := secureheaders.Config{ImageSources: sources, MediaSources: sources}
	first := serve(t, cfg).Get("Content-Security-Policy")
	second := serve(t, cfg).Get("Content-Security-Policy")

	if first != second {
		t.Errorf("同じ Config で CSP が変わりました:\n 1回目 = %s\n 2回目 = %s", first, second)
	}
	if sources[0] != "https://a.example.com" || len(sources) != 1 {
		t.Errorf("呼び出し側のスライスが書き換えられています: %v", sources)
	}
}

// TestInlineStyleIsOptIn は、'unsafe-inline' が既定で付かず、明示したときだけ
// style-src に入ることを検証します。既定を厳格にしてある理由そのものなので、
// 取り違えるとどのアプリも黙って緩みます。
func TestInlineStyleIsOptIn(t *testing.T) {
	strict := serve(t, secureheaders.Config{}).Get("Content-Security-Policy")
	if strings.Contains(strict, "'unsafe-inline'") {
		t.Errorf("既定で 'unsafe-inline' が入っています:\n %s", strict)
	}

	relaxed := serve(t, secureheaders.Config{AllowInlineStyle: true}).Get("Content-Security-Policy")
	if !strings.Contains(relaxed, "style-src 'self' 'unsafe-inline';") {
		t.Errorf("AllowInlineStyle が効いていません:\n %s", relaxed)
	}
	// 緩めるのは style-src だけで、script-src は巻き込まない。
	if !strings.Contains(relaxed, "script-src 'self';") {
		t.Errorf("script-src まで緩んでいます:\n %s", relaxed)
	}
}

// TestSourcesGoToTheirOwnDirective は、足したオリジンが指定したディレクティブに
// だけ入ることを検証します。取り違えると、画像を許したつもりでスクリプトの
// 読み込み元が開きます。
func TestSourcesGoToTheirOwnDirective(t *testing.T) {
	csp := serve(t, secureheaders.Config{
		ScriptSources:    []string{"https://script.example.com"},
		StyleSources:     []string{"https://style.example.com"},
		ConnectSources:   []string{"https://api.example.com"},
		AllowInlineStyle: true,
	}).Get("Content-Security-Policy")

	for _, want := range []string{
		"script-src 'self' https://script.example.com;",
		"style-src 'self' 'unsafe-inline' https://style.example.com;",
		"connect-src 'self' https://api.example.com;",
		// 足していないディレクティブは 'self' のまま。
		"img-src 'self' data:;",
		"media-src 'self';",
		"font-src 'self';",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP に %q が含まれていません:\n %s", want, csp)
		}
	}
}

// TestHardenedDirectivesHaveNoKnob は、緩める手段を用意していないディレクティブが
// 常に付くことを検証します。CSP 全体を差し替えたときだけ呼び出し側の責任になります。
func TestHardenedDirectivesHaveNoKnob(t *testing.T) {
	csp := serve(t, secureheaders.Config{
		ScriptSources: []string{"https://script.example.com"},
		ImageSources:  []string{"https://img.example.com"},
	}).Get("Content-Security-Policy")

	for _, want := range []string{
		"object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'", "form-action 'self'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP に %q が含まれていません:\n %s", want, csp)
		}
	}
}
