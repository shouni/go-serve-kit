package secureheaders_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shouni/go-serve-kit/secureheaders"
)

// serve returns the headers of a response that went through the middleware.
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
		t.Fatal("next handler was not called")
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

// TestMatchesCurrentAppPolicies checks that this assembly reproduces the CSPs the consuming
// apps send.
//
// adk-review and ap-story do not write media-src, but with default-src 'self', writing
// media-src 'self' or not means the same thing. This states explicitly that lifting the
// policy into the kit does not change behavior.
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
			app:      "ap-mv（画像も音声も外部ストレージ）",
			cfg:      secureheaders.Config{ImageSources: []string{gcs}, MediaSources: []string{gcs}},
			wantImg:  "img-src 'self' data: " + gcs,
			wantMedi: "media-src 'self' " + gcs,
		},
		{
			app:      "ap-story（画像だけ外部ストレージ）",
			cfg:      secureheaders.Config{ImageSources: []string{gcs}},
			wantImg:  "img-src 'self' data: " + gcs,
			wantMedi: "media-src 'self'",
		},
		{
			app:      "ap-voice（音声だけ外部ストレージ）",
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
					t.Errorf("CSP lacks %q:\n %s", want, csp)
				}
			}
			// Only img-src / media-src allow external origins.
			for _, directive := range []string{"script-src 'self';", "style-src 'self';"} {
				if !strings.Contains(csp, directive) {
					t.Errorf("CSP lacks %q:\n %s", directive, csp)
				}
			}
		})
	}
}

func TestExplicitCSPWins(t *testing.T) {
	const custom = "default-src 'none'"

	header := serve(t, secureheaders.Config{
		ContentSecurityPolicy: custom,
		// When given explicitly, the assembly settings are ignored.
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

// TestBlankSourcesAreDropped verifies that the directive does not break when values split
// from an environment variable contain empty elements.
func TestBlankSourcesAreDropped(t *testing.T) {
	csp := serve(t, secureheaders.Config{
		MediaSources: []string{"", "  ", "https://example.com", " "},
	}).Get("Content-Security-Policy")

	if !strings.Contains(csp, "media-src 'self' https://example.com;") {
		t.Errorf("media-src is broken:\n %s", csp)
	}
	if strings.Contains(csp, "  ") {
		t.Errorf("CSP contains extra whitespace:\n %s", csp)
	}
}

// TestConfigIsNotMutated verifies that reusing the same Config does not grow the CSP.
// The assembly uses append, so this pins that the caller's slice is not rewritten.
func TestConfigIsNotMutated(t *testing.T) {
	sources := make([]string, 1, 4) // leave capacity for append to write back into
	sources[0] = "https://a.example.com"

	cfg := secureheaders.Config{ImageSources: sources, MediaSources: sources}
	first := serve(t, cfg).Get("Content-Security-Policy")
	second := serve(t, cfg).Get("Content-Security-Policy")

	if first != second {
		t.Errorf("CSP changed with the same Config:\n first = %s\n second = %s", first, second)
	}
	if sources[0] != "https://a.example.com" || len(sources) != 1 {
		t.Errorf("caller's slice was rewritten: %v", sources)
	}
}

// TestInlineStyleIsOptIn verifies that 'unsafe-inline' is absent by default and goes into
// style-src only when asked for. It is the very reason the default is strict, so getting it
// wrong would silently loosen every app.
func TestInlineStyleIsOptIn(t *testing.T) {
	strict := serve(t, secureheaders.Config{}).Get("Content-Security-Policy")
	if strings.Contains(strict, "'unsafe-inline'") {
		t.Errorf("'unsafe-inline' present by default:\n %s", strict)
	}

	relaxed := serve(t, secureheaders.Config{AllowInlineStyle: true}).Get("Content-Security-Policy")
	if !strings.Contains(relaxed, "style-src 'self' 'unsafe-inline';") {
		t.Errorf("AllowInlineStyle has no effect:\n %s", relaxed)
	}
	// Only style-src is loosened; script-src is not dragged along.
	if !strings.Contains(relaxed, "script-src 'self';") {
		t.Errorf("script-src was loosened too:\n %s", relaxed)
	}
}

// TestSourcesGoToTheirOwnDirective verifies that an added origin goes only into the
// directive it was given for. Mixing them up would open a script source while meaning to
// allow images.
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
		// Directives with nothing added stay 'self'.
		"img-src 'self' data:;",
		"media-src 'self';",
		"font-src 'self';",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %q:\n %s", want, csp)
		}
	}
}

// TestHardenedDirectivesHaveNoKnob verifies that the directives with no way to loosen them
// are always present. They become the caller's responsibility only when the whole CSP is
// replaced.
func TestHardenedDirectivesHaveNoKnob(t *testing.T) {
	csp := serve(t, secureheaders.Config{
		ScriptSources: []string{"https://script.example.com"},
		ImageSources:  []string{"https://img.example.com"},
	}).Get("Content-Security-Policy")

	for _, want := range []string{
		"object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'", "form-action 'self'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %q:\n %s", want, csp)
		}
	}
}

// TestSourcesRejectKeywordsAndWildcards verifies that CSP keywords and any-origin
// allowances cannot get in through *Sources. There is no way to add 'unsafe-inline' to
// script-src, but if the lists accepted anything, that absence would mean nothing.
func TestSourcesRejectKeywordsAndWildcards(t *testing.T) {
	bad := []string{
		"'unsafe-inline'", "'UNSAFE-EVAL'", "'strict-dynamic'", " 'self' ",
		"*", "https:", "HTTPS://*", "wss:", "http://*",
		// Forms that pack several expressions or directives into one element to bypass the
		// checks above.
		"https://cdn.example 'unsafe-inline'", "https://cdn.example\t'unsafe-eval'",
		"https://cdn.example *", "x; object-src *", "x;frame-ancestors *", "x, script-src *",
	}
	for _, source := range bad {
		for name, cfg := range map[string]secureheaders.Config{
			"ScriptSources":  {ScriptSources: []string{source}},
			"StyleSources":   {StyleSources: []string{source}},
			"ImageSources":   {ImageSources: []string{source}},
			"MediaSources":   {MediaSources: []string{source}},
			"ConnectSources": {ConnectSources: []string{source}},
		} {
			mw, err := secureheaders.New(cfg)
			if err == nil {
				t.Errorf("%s = %q: New() error = nil, want error", name, source)
			}
			if mw != nil {
				t.Errorf("%s = %q: New() returned a middleware alongside the error", name, source)
			}
			if err != nil && !strings.Contains(err.Error(), name) {
				t.Errorf("%s = %q: error does not name the field: %v", name, source, err)
			}
		}
	}

	// Values that pin down an origin pass. Subdomain wildcards and blob: are accepted too,
	// since what they allow stays within the same origin or specific hosts.
	good := []string{"https://example.com", "https://*.example.com", "blob:", "example.com:8443", ""}
	if _, err := secureheaders.New(secureheaders.Config{
		ScriptSources: good, StyleSources: good, ImageSources: good, MediaSources: good, ConnectSources: good,
	}); err != nil {
		t.Errorf("New() error = %v, want nil for origin sources", err)
	}
}

// TestMiddlewarePanicsOnInvalidConfig verifies that Middleware fails at startup on an
// invalid config. Stopping at startup is safer than serving a loosened CSP.
func TestMiddlewarePanicsOnInvalidConfig(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Middleware() did not panic on 'unsafe-inline' in ScriptSources")
		}
	}()
	secureheaders.Middleware(secureheaders.Config{ScriptSources: []string{"'unsafe-inline'"}})
}

// TestExplicitCSPIsNotValidated pins that a whole-CSP override is not validated, keeping
// the promise that this last resort hands the responsibility to the caller along with it.
func TestExplicitCSPIsNotValidated(t *testing.T) {
	const custom = "script-src 'unsafe-inline'"
	if got := serve(t, secureheaders.Config{ContentSecurityPolicy: custom}).Get("Content-Security-Policy"); got != custom {
		t.Errorf("CSP = %q, want %q", got, custom)
	}
}
