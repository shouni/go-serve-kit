# 📤 Go Serve Kit

[![CI](https://github.com/shouni/go-serve-kit/actions/workflows/ci.yml/badge.svg)](https://github.com/shouni/go-serve-kit/actions/workflows/ci.yml)
[![Status](https://img.shields.io/badge/Status-Active-brightgreen)](#)
[![Language](https://img.shields.io/badge/Language-Go-blue)](https://go.dev/)
[![Go Version](https://img.shields.io/github/go-mod/go-version/shouni/go-serve-kit)](https://go.dev/)
[![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/shouni/go-serve-kit)](https://github.com/shouni/go-serve-kit/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Reference](https://pkg.go.dev/badge/github.com/shouni/go-serve-kit.svg)](https://pkg.go.dev/github.com/shouni/go-serve-kit)

English | [日本語](README.ja.md)

## 🚀 About - owns what to respond with, not the server itself

**Go Serve Kit** takes over the boilerplate an HTTP service writes on the responding side every time: defensive headers, picking a representation, declaring the process's role, and serving static files.
It has no `http.Server`, no router and no `main`, and which role serves what is decided by the consuming router.

---

## ✨ Features

The packages are independent; import only the ones you need. Listed here are only
**the premises that decide whether the kit fits**. API details and the reasoning behind each decision are in each package's godoc.

* **`respond`**: writing the response (`JSON` / `Error` / `ErrorJSON`, plus `ServerError` / `ServerErrorJSON` for 5xx) and choosing the representation from `Accept` (`WantsJSON`)
  * **The check is a substring match on `application/json`; q-values are not parsed.** `*/*` (the default of curl and many HTTP
    clients) falls to the HTML side. Have callers that expect JSON send an explicit `Accept`.
  * `WantsJSON` sets `Vary: Accept` as it decides, so it requires the `ResponseWriter`.
    With a shared cache or CDN in front, forgetting that declaration can hand HTML to a client that asked for JSON.
* **`secureheaders`**: adds CSP, HSTS, `nosniff`, `Referrer-Policy` and `Permissions-Policy` to every response
  * **The default allows no external origin and no inline style**, on the premise that third-party JS/CSS is
    self-hosted rather than pulled from a CDN. A CDN stays out of `script-src` because hosts like jsDelivr
    serve every npm package, so allowing one amounts to allowing any npm package to load.
  * Only `Config`'s `*Sources` and `AllowInlineStyle` can open things up; **there is no way to allow inline script.**
    Passing a keyword or `*` to `*Sources` fails at startup.
* **`staticfiles`**: serves embedded CSS / JS with a `Cache-Control` decided by path, and an `ETag`
  * **Your own files get 5 minutes, files under `vendor/` get a year of `immutable`.** Put third-party distributions
    under `vendor/` with the version in the path (`vendor/bootstrap-5.3.8/`).
* **`serverrole`**: the `web` / `worker` / `both` vocabulary and `Parse`
  * **Unset and unknown values are errors.** Folding unset into `both` would mean that one missing environment variable
    brings the worker routes back onto the public web surface.

---

## 🚦 Usage

An example that combines all four packages in one `main`. For the branch cases (`Error` vs `ErrorJSON`,
binding directly into a config struct, disabling the ETag, and so on) see
[the Examples on pkg.go.dev](https://pkg.go.dev/github.com/shouni/go-serve-kit#section-directories).

```go
func run() error {
    role, err := serverrole.Parse(os.Getenv("SERVER_ROLE"))
    if err != nil {
        return err // unset and unknown values stop here
    }

    files, err := staticfiles.New(staticfiles.Config{FS: assets.StaticFiles, Dir: "static"})
    if err != nil {
        return err // a wrong Dir stops here
    }

    mux := http.NewServeMux()
    mux.Handle("/static/", files) // keep it outside authentication
    if role.ServesWeb() {
        mux.HandleFunc("GET /comics", listComics)
    }
    if role.ServesWorker() {
        mux.Handle("POST /tasks/run", workerHandler)
    }

    handler := secureheaders.Middleware(secureheaders.Config{
        MediaSources:     []string{"https://storage.example.com"}, // when redirecting (302) to signed URLs
        AllowInlineStyle: true,                                    // when using Bootstrap's collapse / tab
    })(mux)

    srv := &http.Server{Addr: ":" + os.Getenv("PORT"), Handler: handler, ReadHeaderTimeout: 5 * time.Second}
    return srv.ListenAndServe()
}

// A route whose URL is shared by a screen and an API. Keep one route and choose only the representation by Accept.
func listComics(w http.ResponseWriter, r *http.Request) {
    comics, err := store.List(r.Context())
    if err != nil {
        respond.Error(w, r, http.StatusInternalServerError, "could not load the list")
        return
    }
    if respond.WantsJSON(w, r) { // Vary: Accept is set here too
        respond.JSON(w, r, http.StatusOK, comics)
        return
    }
    _ = tmpl.Execute(w, page{Comics: comics})
}
```

---

## 📦 Package Structure

```text
go-serve-kit/
├── respond/        # writing responses (JSON / Error), choosing the representation by Accept, Vary: Accept
├── secureheaders/  # defensive browser response headers and CSP assembly
├── serverrole/     # the web / worker / both vocabulary and Parse
└── staticfiles/    # serving embedded static files with a Cache-Control decided by path
```

---

## 🤝 Dependencies

**None.** `go.mod` has no `require`; all four packages, tests included, run on the standard library alone.

---

## 📜 License

This project is released under the [MIT License](https://opensource.org/licenses/MIT).
