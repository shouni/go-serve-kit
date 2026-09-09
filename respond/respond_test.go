package respond_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shouni/go-serve-kit/respond"
)

func TestJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	respond.JSON(rec, req, http.StatusCreated, map[string]string{"id": "job-1"})

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"id":"job-1"}` {
		t.Errorf("body = %q", got)
	}
	// JSON しか返さない経路に Vary は要らない。立てるのは WantsJSON の仕事。
	if got := rec.Header().Get("Vary"); got != "" {
		t.Errorf("Vary = %q, want 空（JSON は表現を出し分けない）", got)
	}
}

// TestJSONLogsEncodeFailure は、ヘッダー送信後に失敗しても記録が残ることを検証します。
// 状態コードはもう差し替えられないため、記録が唯一の手掛かりになります。
func TestJSONLogsEncodeFailure(t *testing.T) {
	var buf bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(restore) })

	// chan は JSON にできないため、Encode がヘッダー送信後に失敗します。
	// リクエストの有無で分けているのは、ログ用のコンテキストを r から取るためです。
	// ハンドラーの外から呼ばれても記録は残る必要があります。
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/", nil),
		nil,
	} {
		buf.Reset()
		rec := httptest.NewRecorder()

		respond.JSON(rec, req, http.StatusOK, make(chan int))

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500（壊れた本文を送らず失敗へ振り替える）", rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
			t.Errorf("Content-Type = %q（失敗時も JSON のまま返す）", got)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Errorf("失敗時の本文が JSON として読めません: %q", rec.Body.String())
		}
		if !strings.Contains(buf.String(), "エンコードに失敗") {
			t.Errorf("エンコード失敗が記録されていません (req=%v): %s", req != nil, buf.String())
		}
	}
}

func TestError(t *testing.T) {
	tests := []struct {
		name            string
		accept          string
		wantContentType string
		wantBody        string
	}{
		{
			name:            "JSON を求めた相手には JSON",
			accept:          "application/json",
			wantContentType: "application/json; charset=utf-8",
			wantBody:        `{"error":"not found"}`,
		},
		{
			name:            "ページを求めた相手には text/plain",
			accept:          "text/html",
			wantContentType: "text/plain; charset=utf-8",
			wantBody:        "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Accept", tt.accept)

			respond.Error(rec, req, http.StatusNotFound, "not found")

			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != tt.wantContentType {
				t.Errorf("Content-Type = %q, want %q", got, tt.wantContentType)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tt.wantBody {
				t.Errorf("body = %q, want %q", got, tt.wantBody)
			}
			// 応答が Accept で変わる以上、キャッシュへ伝える必要がある。
			if got := rec.Header().Get("Vary"); got != "Accept" {
				t.Errorf("Vary = %q, want %q", got, "Accept")
			}
		})
	}
}

// TestErrorBodyShape は、エラー本文の形が {"error": ...} であることを固定します。
// 4 つのバックエンドを 1 つのクライアントから呼ぶため、形が割れると読み方が変わります。
func TestErrorBodyShape(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/json")

	respond.Error(rec, req, http.StatusBadGateway, "ストレージから読めませんでした")

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("本文が JSON として読めません: %v (%s)", err, rec.Body.String())
	}
	if len(body) != 1 {
		t.Errorf("body = %v, want キーは error だけ", body)
	}
	if body["error"] != "ストレージから読めませんでした" {
		t.Errorf("body[error] = %v", body["error"])
	}
}

func TestWriteNilArgs(t *testing.T) {
	// nil の ResponseWriter でも落ちない。
	respond.JSON(nil, nil, http.StatusOK, map[string]string{})

	// リクエストが nil でも書き出せる（ログ用のコンテキストだけ既定に倒れる）。
	noReq := httptest.NewRecorder()
	respond.JSON(noReq, nil, http.StatusOK, map[string]string{"ok": "yes"})
	if got := strings.TrimSpace(noReq.Body.String()); got != `{"ok":"yes"}` {
		t.Errorf("body = %q", got)
	}

	// r が nil なら JSON は求められていない扱いになり、text/plain へ倒れる。
	rec := httptest.NewRecorder()
	respond.Error(rec, nil, http.StatusInternalServerError, "boom")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "boom" {
		t.Errorf("body = %q", got)
	}
}

// TestErrorJSON は、相手が何を求めていても JSON を返すことを検証します。
//
// JSON しか返さないルートでは、成功時が無条件 JSON である以上、エラーも同じ形で
// なければ呼び出し側が成功と失敗で本文の読み方を変えることになります。
func TestErrorJSON(t *testing.T) {
	for _, accept := range []string{"application/json", "text/html", "*/*", ""} {
		t.Run("Accept="+accept, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if accept != "" {
				req.Header.Set("Accept", accept)
			}

			respond.ErrorJSON(rec, req, http.StatusBadGateway, "読み出しに失敗しました")

			if rec.Code != http.StatusBadGateway {
				t.Errorf("status = %d, want 502", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Errorf("Content-Type = %q", got)
			}

			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("本文が JSON として読めません: %v (%s)", err, rec.Body.String())
			}
			if body["error"] != "読み出しに失敗しました" {
				t.Errorf("body[error] = %v", body["error"])
			}
			// 応答が Accept で変わらない以上、Vary を立てる理由がありません。
			if got := rec.Header().Get("Vary"); got != "" {
				t.Errorf("Vary = %q, want 空", got)
			}
		})
	}
}

// TestErrorAndErrorJSONAgreeForJSONCallers は、JSON を求めた相手には
// 2 つが同じ応答を返すことを固定します。違いは「求めていない相手に何を返すか」
// だけである、という関係を崩さないためです。
func TestErrorAndErrorJSONAgreeForJSONCallers(t *testing.T) {
	newReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept", "application/json")
		return req
	}

	negotiated := httptest.NewRecorder()
	respond.Error(negotiated, newReq(), http.StatusNotFound, "見つかりません")

	always := httptest.NewRecorder()
	respond.ErrorJSON(always, newReq(), http.StatusNotFound, "見つかりません")

	if negotiated.Code != always.Code {
		t.Errorf("status = %d / %d", negotiated.Code, always.Code)
	}
	if negotiated.Body.String() != always.Body.String() {
		t.Errorf("body = %q / %q", negotiated.Body.String(), always.Body.String())
	}
	if got, want := negotiated.Header().Get("Content-Type"), always.Header().Get("Content-Type"); got != want {
		t.Errorf("Content-Type = %q / %q", got, want)
	}
	// Error は判定するので Vary を立て、ErrorJSON は立てません。
	if negotiated.Header().Get("Vary") != "Accept" {
		t.Error("Error が Vary: Accept を立てていません")
	}
	if always.Header().Get("Vary") != "" {
		t.Error("ErrorJSON が Vary を立てています")
	}
}

// TestJSONDoesNotLeakPartialBody は、エンコードが途中で失敗する値を渡しても、
// 書きかけの本文が送られないことを検証します。バッファへ組み立ててから送る
// 理由がこれで、直接流すと {"ok":"..." までが 200 で届きます。
func TestJSONDoesNotLeakPartialBody(t *testing.T) {
	payload := struct {
		OK  string   `json:"ok"`
		Bad chan int `json:"bad"` // ここでエンコードが失敗する
	}{OK: "この値は届いてはいけません"}

	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))

	rec := httptest.NewRecorder()
	respond.JSON(rec, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusOK, payload)

	if strings.Contains(rec.Body.String(), "届いてはいけません") {
		t.Errorf("書きかけの本文が送られています: %q", rec.Body.String())
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// failingWriter は Write が必ず失敗する ResponseWriter です。
// 接続が切れた後の書き出しを模します。
type failingWriter struct {
	header http.Header
	code   int
}

func (f *failingWriter) Header() http.Header {
	if f.header == nil {
		f.header = http.Header{}
	}
	return f.header
}

func (f *failingWriter) Write([]byte) (int, error) { return 0, errors.New("connection reset") }
func (f *failingWriter) WriteHeader(code int)      { f.code = code }

// TestJSONLogsWriteFailure は、組み立てには成功したが送信に失敗した場合
// （相手が切断した等）に記録が残ることを検証します。ここで黙って返ると、
// 応答が届いていない事実がどこにも残りません。
func TestJSONLogsWriteFailure(t *testing.T) {
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))

	w := &failingWriter{}
	respond.JSON(w, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusOK, map[string]string{"ok": "yes"})

	if w.code != http.StatusOK {
		t.Errorf("status = %d, want 200（組み立ては成功している）", w.code)
	}
	if !strings.Contains(logs.String(), "書き出しに失敗") {
		t.Errorf("書き出し失敗が記録されていません: %s", logs.String())
	}
}

// TestJSONSkipsBodyForBodilessStatus は、本文を持てない状態コードで本文を書かず、
// 失敗も記録しないことを検証します。net/http は 204 への Write を拒むので、書こうと
// すると応答は正しいのに毎回 ERROR が残り、本当の書き出し失敗が埋もれます。
func TestJSONSkipsBodyForBodilessStatus(t *testing.T) {
	var logs bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(restore) })

	// 実サーバーで確かめる。httptest.ResponseRecorder は 204 への Write を拒まないため、
	// レコーダーだけでは net/http の挙動を再現できない。
	// 1xx は net/http が中間応答として扱い最終応答にならないので、下のレコーダーで見る。
	for _, status := range []int{http.StatusNoContent, http.StatusNotModified} {
		logs.Reset()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			respond.JSON(w, r, status, map[string]string{"ignored": "yes"})
		}))
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			srv.Close()
			t.Fatalf("%d: request failed: %v", status, err)
		}
		_ = resp.Body.Close()
		srv.Close()

		if resp.StatusCode != status {
			t.Errorf("status = %d, want %d", resp.StatusCode, status)
		}
		if got := resp.Header.Get("Content-Type"); got != "" {
			t.Errorf("%d: Content-Type = %q, want none（本文が無い）", status, got)
		}
		if logs.Len() != 0 {
			t.Errorf("%d: unexpected log: %s", status, logs.String())
		}
	}

	// レコーダー上でも本文が空であることを直接確かめる（1xx はここでしか見られない）。
	for _, status := range []int{http.StatusContinue, http.StatusNoContent, http.StatusNotModified} {
		rec := httptest.NewRecorder()
		respond.JSON(rec, nil, status, map[string]string{"ignored": "yes"})
		if rec.Code != status || rec.Body.Len() != 0 {
			t.Errorf("status = %d, body = %q; want %d and empty body", rec.Code, rec.Body.String(), status)
		}
	}
}
