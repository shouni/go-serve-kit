// Package respond は、HTTP 応答を返す側の定型を持ちます。応答本体の書き出し
// （JSON / Error / ErrorJSON）と、相手に合わせて表現を選ぶ判定（WantsJSON）です。
//
// 判定と書き出しを 1 つのパッケージに置くのは、片方だけを共有すると、もう片方が
// アプリごとに割れるためです。実際に割れていたのは Content-Type
// （"application/json" と "application/json; charset=utf-8"）と、エンコードに
// 失敗したときの扱い（文脈付きで記録・文脈なしで記録・握り潰す）でした。
//
// 表現を選ぶのは、1 つのリソースに 2 つの表現がある場合だけです。画面用と API 用に
// ルートを分けると、同じ取得処理を 2 本持つことになり、片方だけ直したときに画面の
// 表示と機械可読な結果が食い違います。ルートは 1 本に保ち、表現だけを Accept で
// 選びます。ただし入力フォームのように JSON の対応物が無いものは別のリソースなので、
// そちらは分けたままにします。
package respond

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
)

// contentTypeJSON は JSON 応答に付ける Content-Type です。
//
// RFC 8259 が JSON を UTF-8 と定めている以上 charset の有無で解釈は変わりませんが、
// 複数のバックエンドを呼ぶクライアントが両方の値を扱うことにならないよう、片方に固定します。
const contentTypeJSON = "application/json; charset=utf-8"

// errorBody は、JSON を求めた呼び出し元へ返すエラー本文 {"error": "..."} です。
type errorBody struct {
	Error string `json:"error"`
}

// encodeFailureBody は、エンコードに失敗したときに返す本文です。
//
// errorBody を通さずに用意してあるのは、エンコードが失敗した直後に同じ
// エンコード経路をもう一度通らせないためです。
var encodeFailureBody = []byte(`{"error":"Internal Server Error"}` + "\n")

// JSON は payload を JSON として書き出します。
//
// 先にバッファへ組み立ててから送ります。途中で失敗しうる値（chan、循環参照、
// エラーを返す MarshalJSON）を w へ直接流すと、書けたところまでの壊れた JSON が
// 状態コード 200 のまま届き、呼び出し側からは「成功したが本文が壊れている」
// という最も紛らわしい形になります。組み立ててから送れば 500 に振り替えられます。
//
// 失敗時の本文も JSON です。JSON を返す約束のルートで失敗時だけ text/plain に
// なると、呼び出し側は成功と失敗で本文の読み方を変えることになります
// （ErrorJSON と同じ理由です）。
//
// 記録先が slog.Default() なのは、既定ロガーを差し替えてあるアプリで、
// severity やトレース相関がそのまま効くためです。
//
// Vary: Accept は立てません。表現を出し分けるかどうかを知っているのは
// WantsJSON を呼んだ側で、JSON しか返さない経路に Vary は要らないためです。
//
// 本文を持てない状態コード（1xx / 204 / 304）では payload を捨て、状態コードだけを
// 返します。net/http はそれらへの書き込みを拒むため、書こうとすると応答は正しく
// 届くのに「書き出しに失敗」が毎回記録され、本当の失敗が埋もれます。
func JSON(w http.ResponseWriter, r *http.Request, status int, payload any) {
	if w == nil {
		return
	}
	if !bodyAllowed(status) {
		w.WriteHeader(status)
		return
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		slog.ErrorContext(requestContext(r), "respond: JSON 応答のエンコードに失敗しました",
			"error", err, "status", status)
		w.Header().Set("Content-Type", contentTypeJSON)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(encodeFailureBody)
		return
	}

	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)

	if _, err := w.Write(buf.Bytes()); err != nil {
		slog.ErrorContext(requestContext(r), "respond: JSON 応答の書き出しに失敗しました",
			"error", err, "status", status)
	}
}

// Error は、呼び出し元が JSON を求めていれば JSON で、そうでなければ
// text/plain でエラーを返します。
//
// 画面と API が同じ URL を共有するルート用です。JSON 固定にしないのは、画面側の
// JS がエラー本文を resp.text() で読んでいるためで、逆に text/plain 固定にすると、
// 通したエージェントが本文を構造化して読めません。どちらを返すかの判定は WantsJSON に
// 委ねるので、Vary: Accept もそこで立ちます。
//
// JSON しか返さないルートでは ErrorJSON を使ってください。
func Error(w http.ResponseWriter, r *http.Request, status int, message string) {
	if WantsJSON(w, r) {
		ErrorJSON(w, r, status, message)
		return
	}
	http.Error(w, message, status)
}

// ErrorJSON は、相手が何を求めていても JSON でエラーを返します。
//
// JSON しか返さないルート用です。そういうルートは成功時も無条件に JSON を返すので、
// エラーだけ Accept で形が変わると、呼び出し側は成功と失敗で本文の読み方を変えることに
// なります（Accept を送らないブラウザの fetch は、エラー本文も JSON として読みます）。
//
// Vary: Accept は立てません。この応答は Accept で変わらないためです。
func ErrorJSON(w http.ResponseWriter, r *http.Request, status int, message string) {
	JSON(w, r, status, errorBody{Error: message})
}

// bodyAllowed は、status の応答が本文を持てるかを返します。
// 規則は net/http が Write を拒む条件（1xx / 204 / 304）と同じです。
func bodyAllowed(status int) bool {
	if status >= 100 && status <= 199 {
		return false
	}
	return status != http.StatusNoContent && status != http.StatusNotModified
}

// requestContext は、r が nil でも使えるコンテキストを返します。
// このパッケージの他の関数と同じく、ゼロ値・nil で落ちないようにするためです。
func requestContext(r *http.Request) context.Context {
	if r == nil {
		return context.Background()
	}
	return r.Context()
}
