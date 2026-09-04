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

---

## ✨ 提供機能 (Features)

パッケージは独立しており、必要なものだけを import できます。ここに挙げるのは
**知らずに踏むと高くつく前提**だけです。API の詳細と個々の判断理由は各パッケージの godoc にあります。

* **`respond`**: 応答の書き出しと、`Accept` による表現の選択
  * `WantsJSON(w, r)` が表現を選び、**同時に `Vary: Accept` を立てます**。判定だけして宣言を忘れると、
    共有キャッシュや CDN を前に置いたとき、JSON を求めたクライアントへ HTML が返ります。
  * **判定は `application/json` の部分一致で、q 値は解釈しません。** したがって `*/*`（curl や多くの
    HTTP クライアントの既定）は HTML 側に倒れ、`application/json;q=0` は JSON と判定されます。
    JSON を期待する呼び出し元には、明示的な `Accept` を送らせてください。
  * `JSON` / `Error` / `ErrorJSON` の**使い分けはルートの性質で決まります。** 画面と API が同じ URL を
    共有するなら `Error`（相手が JSON を求めていれば `{"error": ...}`、そうでなければ `text/plain`）。
    **JSON しか返さないルートは `ErrorJSON`** です — 成功時が無条件 JSON なのにエラーだけ `Accept` で
    形が変わると、呼び出し側は成功と失敗で本文の読み方を変えることになります。
  * `JSON` は**バッファへ組み立ててから送ります**。途中で失敗しうる値（`chan`、循環参照、
    エラーを返す `MarshalJSON`）を直接流すと、書けたところまでの壊れた JSON が 200 のまま
    届くためです。失敗した場合は 500 と `{"error": ...}` を返します。
* **`secureheaders`**: CSP・HSTS・`nosniff`・`Referrer-Policy`・`Permissions-Policy` を全応答へ付与
  * CSP は開けたいディレクティブだけを `Config` で渡せば、残りはキットが組み立てます
    （`ImageSources` / `MediaSources` / `ScriptSources` / `StyleSources` / `ConnectSources`）。
    CSP 全体を文字列で受け渡す形を既定にしないのは、`object-src 'none'` や `'self'` が
    1 アプリずつ静かに抜け落ちるためです。`ContentSecurityPolicy` は最後の手段で、
    渡した時点で `base-uri` などの責任も呼び出し側に移ります。
  * **`object-src` / `base-uri` / `frame-ancestors` / `form-action` に調整点はありません。**
    緩める理由が無く、緩んだ状態で気付かれないほうが高くつくためです。
  * **既定は「外部オリジンを 1 つも許可しない」** で、第三者製の JS/CSS を CDN からではなく
    自前配信している前提です。CDN を `script-src` の allowlist に載せない理由は、jsDelivr のような
    ホストが npm の全パッケージを配信しており、「任意の npm パッケージの読み込みを許可する」に
    等しくなるためです。
  * **`'unsafe-inline'` は既定で付きません。** インラインスタイルを当てる JS を積んでいる場合
    （Bootstrap の collapse / tab が該当します）だけ `AllowInlineStyle: true` を渡してください。
    既定を厳格にしてあるので、**どのアプリがこれを必要としているかが設定に現れます**。
    `script-src` に対応する項目は用意していません。インラインスクリプトを許すと、
    CSP が防いでいるものの大半が無くなるためです。
  * HSTS は既定 1 年で、`preload` は付けません（撤回にブラウザベンダーへの申請が要るため）。
    負値を渡すと付与しません。
* **`staticfiles`**: 埋め込んだ CSS / JS の配信と、パスで決まる `Cache-Control`
  * **自前のファイルは 5 分、`vendor/` 配下は 1 年の `immutable`** です。`//go:embed` した FileServer は
    `Last-Modified` も `ETag` も出せないので、期限が切れると必ず全体を取り直します。バージョンが
    パスに入る vendor を分けているのは、その再取得を無くすためです。
  * **404 には `Cache-Control` を付けません。** 無い vendor パスに 1 年の `immutable` が付くと、
    後から置いたファイルがその期間ブラウザに届きません。
  * **ディレクトリは一覧を出さず 404 です。** `http.FileServer` は既定で `/static/` の一覧を返します。
  * **`Dir` の実在は `New` が確かめます。** `fs.Sub` は無いディレクトリでもエラーを返さないので、
    埋め込み先の名前を変えただけの取り違えが、起動時ではなく全 404 として現れます。
* **`serverrole`**: `web` / `worker` / `both` の語彙と `Parse`
  * **未設定と未知の値はエラーです。** 未設定を `both` に倒すと、環境変数が 1 つ欠けただけで
    公開している Web 面に Worker のルートが復活します。未知の値を黙って受け入れると、今度は
    何のルートも提供しないサービスがデプロイされます。どちらも起動時に落とします。
  * `Role` は `encoding.TextUnmarshaler` を実装しているので、`env:"SERVER_ROLE"` や JSON から
    読む時点で `Parse` を通せます（未設定を弾くのは `,required` タグの役目です）。
  * **キットは役割で分岐しません。** 各面が何を提供するかは利用側のルーターが決めます。4 つ目の
    役割が要るアプリは、独自の定数を宣言して `Parse` を包めば、このパッケージを変更せずに足せます。

---

## 🚦 使い方 (Usage)

1 つの `main` の中身を順に分けたものです。上から連結すればそのまま動きます。

### 1. どの面を提供するかを決める

役割は明示が必須です。ここで落とすことに意味があります。

```go
role, err := serverrole.Parse(os.Getenv("SERVER_ROLE"))
if err != nil {
    return err // 未設定・未知の値はここで止めます
}

mux := http.NewServeMux()
if role.ServesWeb() {
    mux.Handle("GET /comics", http.HandlerFunc(listComics))
}
if role.ServesWorker() {
    mux.Handle("POST /tasks/run", workerHandler)
}
```

設定構造体へ直接バインドする場合は、`Parse` の呼び忘れが起きません。

```go
type Config struct {
    Role serverrole.Role `env:"SERVER_ROLE,required"` // UnmarshalText が Parse を通します
}
```

### 2. 防御的ヘッダーを全応答へ付ける

外部オリジンを足すのは、実際に越境する `img-src` / `media-src` だけです。

```go
handler := secureheaders.Middleware(secureheaders.Config{
    MediaSources:     []string{"https://storage.googleapis.com"}, // 署名付き URL へ 302 する場合
    AllowInlineStyle: true,                                       // Bootstrap の collapse / tab を使う場合
})(mux)

srv := &http.Server{
    Addr:              ":" + os.Getenv("PORT"),
    Handler:           handler,
    ReadHeaderTimeout: 5 * time.Second,
}
return srv.ListenAndServe()
```

### 3. 通した相手に合わせて表現を選ぶ

1 本のルートで人（ブラウザ）と機械（エージェント）の両方へ答えます。画面用と API 用にルートを
分けると同じ取得処理を 2 本持つことになり、片方だけ直したときに表示と機械可読な結果が食い違います。

```go
func listComics(w http.ResponseWriter, r *http.Request) {
    comics, err := store.List(r.Context())
    if err != nil {
        // 画面と API が同じ URL を共有するルートなので Error を使います。
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

JSON しか返さないルートでは、エラーの形を成功時と揃えます。

```go
func createComic(w http.ResponseWriter, r *http.Request) {
    var req createRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        // Accept を送らない fetch から呼ばれても {"error": ...} で返ります。
        respond.ErrorJSON(w, r, http.StatusBadRequest, "リクエストを解釈できませんでした")
        return
    }
    respond.JSON(w, r, http.StatusCreated, created)
}
```

なお **JSON 対応物が無いもの（入力フォームなど）は別のリソース**なので、ルートは分けたままにします。
`Accept` による出し分けは「1 つのリソースに 2 つの表現がある」場合のためのもので、
`JSON` / `ErrorJSON` は表現が 1 つしかないルートで使います。

### 4. 静的ファイルを配信する

認証の外側に置きます。スタイルシートにログインを求める理由は無く、ログイン画面からも参照されます。

```go
files, err := staticfiles.New(staticfiles.Config{FS: assets.StaticFiles, Dir: "static"})
if err != nil {
    return err // Dir の取り違えはここで止まります
}
mux.Handle("/static/", files) // chi なら r.Handle("/static/*", files)
```

より詳しい例は [pkg.go.dev の Example](https://pkg.go.dev/github.com/shouni/go-serve-kit) を参照してください。

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
