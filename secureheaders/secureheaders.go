// Package secureheaders provides middleware that adds defensive browser response headers
// to every response.
//
// The default allows no external origin and no inline style. It assumes third-party JS/CSS
// is self-hosted rather than pulled from a CDN; anything more is opened through Config's
// *Sources and AllowInlineStyle.
//
//	r.Use(secureheaders.Middleware(secureheaders.Config{
//	    MediaSources: []string{"https://storage.googleapis.com"},
//	}))
package secureheaders

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultReferrerPolicy is same-origin. Since the default CSP allows no reference to an
	// external origin, there is no reason to send the Referer outside either.
	DefaultReferrerPolicy = "same-origin"

	// DefaultPermissionsPolicy blocks only features a browser-facing service almost never
	// uses. autoplay is left out because it would get in the way of screens that play audio
	// or video back to back.
	DefaultPermissionsPolicy = "geolocation=(), camera=(), microphone=(), payment=(), usb=()"

	// DefaultHSTSMaxAge is one year. preload is not set (withdrawing it requires a request
	// to the browser vendors, and there is little to gain).
	DefaultHSTSMaxAge = 365 * 24 * time.Hour
)

// Config configures the headers that are added. Every field is optional, and zero values
// fall back to the defaults.
type Config struct {
	// ImageSources / MediaSources are external origins added to the CSP's img-src /
	// media-src.
	//
	// When a same-origin endpoint redirects (302) to a signed URL on external storage, add
	// the redirect target here too. How CSP treats redirect targets varies across browser
	// implementations, so state it explicitly rather than depending on it.
	ImageSources []string
	MediaSources []string

	// ScriptSources / StyleSources / ConnectSources are external origins added to
	// script-src / style-src / connect-src respectively.
	//
	// *Sources accept origins (and schemes) only. New rejects keywords such as
	// 'unsafe-inline' and values that do not pin down an origin, such as "*" / "https:".
	//
	// Avoid putting a CDN in script-src. Hosts like jsDelivr serve every npm package, so it
	// amounts to "allow loading any npm package" (and lets in the known CSP-bypass
	// gadgets).
	ScriptSources  []string
	StyleSources   []string
	ConnectSources []string

	// AllowInlineStyle adds 'unsafe-inline' to style-src.
	//
	// The default is false (not added). Set it to true only when you ship JS that applies
	// inline styles (Bootstrap's collapse / tab do). It defaults to false so that apps that
	// do not need it are not dragged along, and so the config shows which app requires it.
	//
	// There is no script-src counterpart. Allowing inline script removes most of what CSP
	// protects against.
	AllowInlineStyle bool

	// ContentSecurityPolicy specifies the whole CSP instead of having it assembled.
	// If empty, the CSP is assembled from the other fields.
	//
	// It is the last resort. Passing it makes even object-src 'none' and base-uri 'none'
	// the caller's responsibility. To loosen a single directive, use the *Sources above.
	ContentSecurityPolicy string

	// ReferrerPolicy / PermissionsPolicy use the defaults when empty.
	ReferrerPolicy    string
	PermissionsPolicy string

	// HSTSMaxAge is the max-age of Strict-Transport-Security.
	// Zero means the default (one year); a negative value omits HSTS.
	HSTSMaxAge time.Duration
}

// Middleware returns the same middleware as New and panics if the config is invalid.
//
// The config is fixed at startup, so failing there beats serving a loosened CSP.
// Use New to receive the error instead.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	mw, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return mw
}

// New returns middleware that adds the configured defensive headers to every response.
//
// It returns an error if *Sources contain a CSP keyword ('unsafe-inline' and the like) or a
// value that pins down no origin at all ("*" or "https:"). If a keyword could get in here,
// "allow inline script", which is deliberately not offered, would pass without changing the
// shape of the config. Each element is one source expression, and for the same reason an
// element containing whitespace, ";" or "," is an error too. ContentSecurityPolicy is not
// checked, since as the last resort it is left to the caller's responsibility.
//
// Header values do not change per request, so they are assembled only once.
func New(cfg Config) (func(http.Handler) http.Handler, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	values := cfg.headers()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := w.Header()
			for name, value := range values {
				header.Set(name, value)
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// validate checks each element of the *Sources.
func (c Config) validate() error {
	lists := []struct {
		field   string
		sources []string
	}{
		{"ImageSources", c.ImageSources},
		{"MediaSources", c.MediaSources},
		{"ScriptSources", c.ScriptSources},
		{"StyleSources", c.StyleSources},
		{"ConnectSources", c.ConnectSources},
	}
	for _, list := range lists {
		for i, source := range list.sources {
			if err := validateSource(source); err != nil {
				return fmt.Errorf("secureheaders: Config.%s[%d] = %q: %w", list.field, i, source, err)
			}
		}
	}
	return nil
}

// anyOriginSources are source expressions that allow every origin on the network.
// Adding one makes the directive as good as absent.
var anyOriginSources = map[string]bool{
	"*":     true,
	"http:": true, "https:": true, "ws:": true, "wss:": true,
	"http://*": true, "https://*": true, "ws://*": true, "wss://*": true,
}

// validateSource checks one source expression. A whitespace-only value passes, because
// sourceList drops it. sourceList also trims surrounding whitespace, so the check runs on
// the trimmed value.
func validateSource(source string) error {
	trimmed := strings.ToLower(strings.TrimSpace(source))
	switch {
	case trimmed == "":
		return nil
	case strings.ContainsAny(trimmed, sourceSeparators):
		return errMultipleSources
	case strings.HasPrefix(trimmed, "'"):
		return errKeywordSource
	case anyOriginSources[trimmed]:
		return errAnyOriginSource
	}
	return nil
}

// sourceSeparators are separators that must not appear inside one source expression.
//
// Values are concatenated into the CSP as is, so whitespace lets a second, unchecked
// expression follow ("https://cdn.example 'unsafe-inline'"). ";" separates directives, and
// since the first occurrence of a directive wins, writing "x; object-src *" could loosen
// object-src or frame-ancestors, which have no knob at all. "," splits the header into
// several policies.
const sourceSeparators = " \t\r\n\f\v;,"

var (
	errKeywordSource   = errors.New("CSP keywords are not accepted in *Sources; use AllowInlineStyle for inline style")
	errAnyOriginSource = errors.New("source allows any origin, which disables the directive")
	errMultipleSources = errors.New("source contains whitespace, ';' or ','; pass one source expression per element")
)

// headers builds the map of header names to the values actually set.
func (c Config) headers() map[string]string {
	values := map[string]string{
		"Content-Security-Policy": c.contentSecurityPolicy(),
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         orDefault(c.ReferrerPolicy, DefaultReferrerPolicy),
		"Permissions-Policy":      orDefault(c.PermissionsPolicy, DefaultPermissionsPolicy),
	}

	if maxAge := c.hstsMaxAge(); maxAge > 0 {
		values["Strict-Transport-Security"] = "max-age=" +
			strconv.FormatInt(int64(maxAge.Seconds()), 10) + "; includeSubDomains"
	}
	return values
}

// contentSecurityPolicy assembles the CSP.
//
// By default every directive is 'self' only, and external origins are only those added
// through Config. object-src / base-uri / frame-ancestors / form-action have no knobs:
// there is no reason to loosen them, and one loosened without anyone noticing costs more.
func (c Config) contentSecurityPolicy() string {
	if csp := strings.TrimSpace(c.ContentSecurityPolicy); csp != "" {
		return csp
	}

	// 'unsafe-inline' goes right after 'self' (the order means nothing, but this way the
	// fact that the directive is loosened reads at its start).
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

// sourceList assembles the form "img-src 'self' data: https://...".
// Whitespace-only elements are dropped (so that values split from an environment variable
// cannot break the directive).
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
