# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Go Serve Kit (`github.com/shouni/go-serve-kit`) is a Go library (not a service) of four independent
packages covering what an HTTP service writes on the *responding* side every time: defensive response
headers, picking a representation, declaring the process's role, and serving embedded static files.
**It does not own the server itself** — no `http.Server`, no router, no `main`.

**`go.mod` has no `require`**, tests included; all four packages are stdlib-only. Keep it that way — the
empty dependency set is part of what makes the kit safe to pull into anything.

These packages started out inside `github.com/shouni/gcp-kit` and were split out because none of them
depended on GCP; a plain HTTP service should not have to pull a Cloud Run kit to get them. **Neither
module requires the other, in either direction** — do not add an edge back.

## Commands

```bash
go build ./...
go vet ./...
gofmt -l .                 # must print nothing (CI fails otherwise)
go test -race ./...        # what CI runs
go test ./respond -run TestWantsJSON -v        # single test
golangci-lint run ./...    # config: .golangci.yml
go run golang.org/x/exp/cmd/gorelease@latest   # run before tagging: incompatibilities + next version
```

CI (`.github/workflows/ci.yml`) is a thin caller of the shared
`shouni/workflows/.github/workflows/go-ci.yml@v1`. **Its `fuzz-targets` list is deliberately empty**: no
package here parses attacker-supplied input on its own — `Accept` matching is a substring check, CSP and
`Role` assemble configured values, and path resolution is left to `net/http`. Add a target only if that
stops being true.

## Package boundaries and why they're separate

Each package is imported on its own; nothing here imports anything else here.

- **`respond`** — writing the body (`JSON` / `Error` / `ErrorJSON`) *and* choosing the representation
  (`WantsJSON`). The two live together on purpose: sharing only one of them let the other drift per app —
  the `Content-Type` (`application/json` vs `application/json; charset=utf-8`) and the handling of an
  encode failure (logged with context / without / swallowed) had each split across sibling apps.
- **`secureheaders`** — CSP, HSTS, `nosniff`, `Referrer-Policy`, `Permissions-Policy` on every response.
- **`serverrole`** — the `web` / `worker` / `both` vocabulary and `Parse`, nothing else.
- **`staticfiles`** — serving an embedded FS with `Cache-Control` decided by path.

## Invariants worth preserving

### `respond`

- **`WantsJSON` takes the `ResponseWriter` and sets `Vary: Accept` as it decides.** Deciding without
  declaring is the bug this shape prevents: with a shared cache or CDN in front, the same URL can hand
  HTML to a client that asked for JSON. Five sibling apps had copied the one-line check and all five had
  dropped the `Vary`. Requiring `w` makes "forgot the declaration" structurally unwritable. (`AddVaryAccept`
  exists for routes that branch elsewhere and only need the declaration.)
- **The check is a substring match on `application/json`; q-values are not parsed.** So `*/*` — curl's
  default — falls to HTML, and `application/json;q=0` counts as JSON. That is acceptable because every
  consumer sends an explicit `Accept`. Do not grow a negotiation parser without that changing first.
- **`Error` vs `ErrorJSON` is chosen by the route's nature, not by taste.** A route shared by a screen and
  an API uses `Error`; a JSON-only route uses `ErrorJSON`, so callers do not have to read the body one way
  on success and another on failure.
- **`JSON` marshals into a buffer before writing.** Streaming a value that can fail mid-encode (`chan`,
  a cycle, a failing `MarshalJSON`) delivers truncated JSON under a 200 that is already committed. On
  failure it answers 500 with `{"error": ...}`.

### `secureheaders`

- **The default allows no external origin and no inline style.** The premise is that third-party JS/CSS is
  self-hosted rather than pulled from a CDN; callers open only what they need via `ImageSources` /
  `MediaSources` / `ScriptSources` / `StyleSources` / `ConnectSources`. The defaults were lifted from five
  sibling apps that held byte-identical copies of them.
- **Do not put a CDN in `script-src`.** Hosts like jsDelivr serve all of npm, so allowing one is
  effectively allowing any npm package (and the known CSP-bypass gadgets in them).
- **`AllowInlineStyle` exists; a `script-src` counterpart does not, and must not be added.** Allowing
  inline script removes most of what CSP is for. Keeping inline style opt-in also means the config shows
  which app needs it (Bootstrap's collapse/tab).
- **`object-src` / `base-uri` / `frame-ancestors` / `form-action` have no knobs.** There is no reason to
  loosen them, and a silently loosened one costs more than the missing option.
- **Taking a whole CSP string (`ContentSecurityPolicy`) is the escape hatch, not the interface.** Passing
  it moves responsibility for `object-src 'none'`, `base-uri`, and the rest to the caller — which is how
  those directives quietly fell out of one app at a time before.
- HSTS defaults to one year and never sets `preload` (withdrawal requires a request to browser vendors);
  a negative max-age omits the header.

### `serverrole`

- **Unset and unknown values are both errors — never fold unset into `both`.** Folding unset means one
  missing environment variable brings worker routes back onto the public web surface; silently accepting
  an unknown value deploys a service that serves no routes at all. Both should fail at startup.
- **`Role` implements `encoding.TextUnmarshaler`** so decoders run `Parse` at bind time. Without it, a
  decoder only sees a defined string type, assigns an unknown value, and a forgotten `Parse` leaves both
  `ServesWeb` and `ServesWorker` false. Rejecting *unset* is the tag's job (`env:"SERVER_ROLE,required"`).
- **The kit never branches on the role.** What each surface serves is the consuming router's decision, so
  an app needing a fourth role declares its own constant and wraps `Parse` without touching this package.

### `staticfiles`

- **Own files get 5 minutes, `vendor/` gets a year of `immutable`.** A `//go:embed` FileServer emits
  neither `Last-Modified` nor `ETag`, so every expiry is a full refetch; versioned vendor paths are split
  out to avoid it.
- **404s carry no `Cache-Control`.** A year of `immutable` on a missing vendor path would hide a file
  added later for that entire year.
- **Directories 404 instead of listing.** `http.FileServer` would otherwise serve an index of `/static/`.
- **`New` checks that `Dir` exists.** `fs.Sub` does not error on a missing directory, so a renamed embed
  target would surface as universal 404s at runtime instead of a startup failure.

## Testing notes

- **No assertion library, and `go.mod` must stay empty** — a test-only requirement still lands in every
  consumer's `go.sum`. Plain `testing` only.
- Test packages are **external** (`respond_test`, `secureheaders_test`, `staticfiles_test`) — black-box.
  `serverrole` is the exception and tests in-package; prefer exporting something properly over moving
  another package in.
- Doc comments and package comments in this repo are Japanese, matching the sibling apps that consume it.
  Keep new comments in the same language and register. Error text is mixed today — `staticfiles` uses
  English with a `staticfiles:` prefix, `serverrole` is Japanese; new sentinels should follow the English
  `package: detail` form.
