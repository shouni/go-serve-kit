# 📤 Go Serve Kit

[![CI](https://github.com/shouni/go-serve-kit/actions/workflows/ci.yml/badge.svg)](https://github.com/shouni/go-serve-kit/actions/workflows/ci.yml)
[![Status](https://img.shields.io/badge/Status-Active-brightgreen)](#)
[![Language](https://img.shields.io/badge/Language-Go-blue)](https://go.dev/)
[![Go Version](https://img.shields.io/github/go-mod/go-version/shouni/go-serve-kit)](https://go.dev/)
[![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/shouni/go-serve-kit)](https://github.com/shouni/go-serve-kit/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Reference](https://pkg.go.dev/badge/github.com/shouni/go-serve-kit.svg)](https://pkg.go.dev/github.com/shouni/go-serve-kit)

## 🚀 概要 (About) - 何を返すかだけを持ち、サーバーそのものは持たない

**Go Serve Kit** は、HTTP サービスが応答を返す側で毎回書く定型（防御的ヘッダー・表現の出し分け・役割の宣言・静的ファイルの配信）を引き受けるツールキットです。
`http.Server` もルーターも `main` も持たず、どの役割が何を提供するかも利用側のルーターが決めます。

---

## ✨ 提供機能 (Features)

パッケージは独立しており、必要なものだけを import できます。ここに挙げるのは
**採否を左右する前提**だけです。API の詳細と個々の判断理由は各パッケージの godoc にあります。

* **`respond`**: 応答の書き出し（`JSON` / `Error` / `ErrorJSON`）と、`Accept` による表現の選択（`WantsJSON`）
  * **判定は `application/json` の部分一致で、q 値は解釈しません。** `*/*`（curl や多くの HTTP
    クライアントの既定）は HTML 側に倒れます。JSON を期待する呼び出し元には、明示的な `Accept` を送らせてください。
  * `WantsJSON` は判定と同時に `Vary: Accept` を立てるので、`ResponseWriter` を要求します。
    共有キャッシュや CDN を前に置いたとき、宣言を忘れると JSON を求めたクライアントへ HTML が返るためです。
* **`secureheaders`**: CSP・HSTS・`nosniff`・`Referrer-Policy`・`Permissions-Policy` を全応答へ付与
  * **既定は「外部オリジンを 1 つも許可せず、インラインスタイルも許さない」** で、第三者製の JS/CSS を
    CDN からではなく自前配信している前提です。CDN を `script-src` に載せない理由は、jsDelivr のような
    ホストが npm の全パッケージを配信しており、任意の npm パッケージの読み込みを許可するに等しくなるためです。
  * 開けられるのは `Config` の `*Sources` と `AllowInlineStyle` だけで、**インラインスクリプトを許す手段はありません。**
    `*Sources` にキーワードや `*` を渡すと起動時に落ちます。
* **`staticfiles`**: 埋め込んだ CSS / JS の配信と、パスで決まる `Cache-Control` と `ETag`
  * **自前のファイルは 5 分、`vendor/` 配下は 1 年の `immutable`** です。第三者製の配布物は、
    パスにバージョンが入る形（`vendor/bootstrap-5.3.8/`）で `vendor/` の下に置いてください。
* **`serverrole`**: `web` / `worker` / `both` の語彙と `Parse`
  * **未設定と未知の値はエラーです。** 未設定を `both` に倒すと、環境変数が 1 つ欠けただけで
    公開している Web 面に Worker のルートが復活します。

---

## 🚦 使い方 (Usage)

1 つの `main` で 4 パッケージを組み合わせた例です。分岐（`Error` と `ErrorJSON` の使い分け、
設定構造体への直接バインド、ETag の無効化など）は
[pkg.go.dev の Example](https://pkg.go.dev/github.com/shouni/go-serve-kit#section-directories) を参照してください。

```go
func run() error {
    role, err := serverrole.Parse(os.Getenv("SERVER_ROLE"))
    if err != nil {
        return err // 未設定・未知の値はここで止めます
    }

    files, err := staticfiles.New(staticfiles.Config{FS: assets.StaticFiles, Dir: "static"})
    if err != nil {
        return err // Dir の取り違えはここで止まります
    }

    mux := http.NewServeMux()
    mux.Handle("/static/", files) // 認証の外側に置きます
    if role.ServesWeb() {
        mux.HandleFunc("GET /comics", listComics)
    }
    if role.ServesWorker() {
        mux.Handle("POST /tasks/run", workerHandler)
    }

    handler := secureheaders.Middleware(secureheaders.Config{
        MediaSources:     []string{"https://storage.example.com"}, // 署名付き URL へ 302 する場合
        AllowInlineStyle: true,                                    // Bootstrap の collapse / tab を使う場合
    })(mux)

    srv := &http.Server{Addr: ":" + os.Getenv("PORT"), Handler: handler, ReadHeaderTimeout: 5 * time.Second}
    return srv.ListenAndServe()
}

// 画面と API が同じ URL を共有するルートです。ルートは 1 本に保ち、表現だけを Accept で選びます。
func listComics(w http.ResponseWriter, r *http.Request) {
    comics, err := store.List(r.Context())
    if err != nil {
        respond.Error(w, r, http.StatusInternalServerError, "一覧を取得できませんでした")
        return
    }
    if respond.WantsJSON(w, r) { // Vary: Accept もここで立ちます
        respond.JSON(w, r, http.StatusOK, comics)
        return
    }
    _ = tmpl.Execute(w, page{Comics: comics})
}
```

---

## 📦 パッケージ構成 (Package Structure)

```text
go-serve-kit/
├── respond/        # 応答の書き出し（JSON / Error）と、Accept による表現の選択・Vary: Accept
├── secureheaders/  # ブラウザ向けの防御的レスポンスヘッダーと CSP の組み立て
├── serverrole/     # web / worker / both の語彙と Parse
└── staticfiles/    # 埋め込んだ静的ファイルの配信と、パスで決まる Cache-Control
```

---

## 🤝 依存関係 (Dependencies)

**ありません。** `go.mod` の `require` は空で、4 パッケージともテストを含めて標準ライブラリだけで動きます。

---

## 📜 ライセンス (License)

このプロジェクトは [MIT License](https://opensource.org/licenses/MIT) の下で公開されています。
