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
	// A route that returns only JSON needs no Vary; setting it is WantsJSON's job.
	if got := rec.Header().Get("Vary"); got != "" {
		t.Errorf("Vary = %q, want empty (JSON does not vary its representation)", got)
	}
}

// TestJSONLogsEncodeFailure verifies that a failure leaves a log record even after the
// headers are sent. The status can no longer be replaced, so the log is the only clue.
func TestJSONLogsEncodeFailure(t *testing.T) {
	var buf bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(restore) })

	// A chan cannot be JSON, so Encode fails after the headers are sent.
	// The cases split on whether there is a request because the logging context comes from
	// r; the record must be kept even when called from outside a handler.
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/", nil),
		nil,
	} {
		buf.Reset()
		rec := httptest.NewRecorder()

		respond.JSON(rec, req, http.StatusOK, make(chan int))

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500 (turn it into a failure instead of sending a broken body)", rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
			t.Errorf("Content-Type = %q (stay JSON on failure too)", got)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Errorf("failure body is not valid JSON: %q", rec.Body.String())
		}
		if !strings.Contains(buf.String(), "failed to encode") {
			t.Errorf("encode failure not logged (req=%v): %s", req != nil, buf.String())
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
			// The response varies with Accept, so caches must be told.
			if got := rec.Header().Get("Vary"); got != "Accept" {
				t.Errorf("Vary = %q, want %q", got, "Accept")
			}
		})
	}
}

// TestErrorBodyShape pins the error body shape to {"error": ...}.
// One client calls four backends, so a split in shape changes how it has to read them.
func TestErrorBodyShape(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/json")

	respond.Error(rec, req, http.StatusBadGateway, "ストレージから読めませんでした")

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, rec.Body.String())
	}
	if len(body) != 1 {
		t.Errorf("body = %v, want error as the only key", body)
	}
	if body["error"] != "ストレージから読めませんでした" {
		t.Errorf("body[error] = %v", body["error"])
	}
}

func TestWriteNilArgs(t *testing.T) {
	// A nil ResponseWriter does not crash.
	respond.JSON(nil, nil, http.StatusOK, map[string]string{})

	// A nil request can still be written (only the logging context falls back to the default).
	noReq := httptest.NewRecorder()
	respond.JSON(noReq, nil, http.StatusOK, map[string]string{"ok": "yes"})
	if got := strings.TrimSpace(noReq.Body.String()); got != `{"ok":"yes"}` {
		t.Errorf("body = %q", got)
	}

	// A nil r counts as not asking for JSON and falls back to text/plain.
	rec := httptest.NewRecorder()
	respond.Error(rec, nil, http.StatusInternalServerError, "boom")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "boom" {
		t.Errorf("body = %q", got)
	}
}

// TestErrorJSON verifies that JSON is returned whatever the caller asked for.
//
// On a route that returns only JSON, success is unconditionally JSON, so unless errors take
// the same shape, callers have to read the body one way on success and another on failure.
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
				t.Fatalf("body is not valid JSON: %v (%s)", err, rec.Body.String())
			}
			if body["error"] != "読み出しに失敗しました" {
				t.Errorf("body[error] = %v", body["error"])
			}
			// The response does not vary with Accept, so there is no reason to set Vary.
			if got := rec.Header().Get("Vary"); got != "" {
				t.Errorf("Vary = %q, want empty", got)
			}
		})
	}
}

// TestErrorAndErrorJSONAgreeForJSONCallers pins that the two return the same response to a
// caller that asked for JSON, so that their only difference remains "what to return to a
// caller that did not ask for it".
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
	// Error decides, so it sets Vary; ErrorJSON does not.
	if negotiated.Header().Get("Vary") != "Accept" {
		t.Error("Error did not set Vary: Accept")
	}
	if always.Header().Get("Vary") != "" {
		t.Error("ErrorJSON set Vary")
	}
}

// TestJSONDoesNotLeakPartialBody verifies that a half-written body is not sent even when
// the value fails midway through encoding. This is why the body is built in a buffer first:
// streamed directly, everything up to {"ok":"..." would arrive under a 200.
func TestJSONDoesNotLeakPartialBody(t *testing.T) {
	payload := struct {
		OK  string   `json:"ok"`
		Bad chan int `json:"bad"` // encoding fails here
	}{OK: "この値は届いてはいけません"}

	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))

	rec := httptest.NewRecorder()
	respond.JSON(rec, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusOK, payload)

	if strings.Contains(rec.Body.String(), "届いてはいけません") {
		t.Errorf("a half-written body was sent: %q", rec.Body.String())
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// failingWriter is a ResponseWriter whose Write always fails.
// It simulates writing after the connection has dropped.
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

// TestJSONLogsWriteFailure verifies that a record is left when building succeeded but
// sending failed (the peer disconnected, for example). Returning silently here would leave
// no trace anywhere that the response never arrived.
func TestJSONLogsWriteFailure(t *testing.T) {
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))

	w := &failingWriter{}
	respond.JSON(w, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusOK, map[string]string{"ok": "yes"})

	if w.code != http.StatusOK {
		t.Errorf("status = %d, want 200 (building succeeded)", w.code)
	}
	if !strings.Contains(logs.String(), "failed to write") {
		t.Errorf("write failure not logged: %s", logs.String())
	}
}

// TestJSONSkipsBodyForBodilessStatus verifies that no body is written, and no failure is
// logged, for statuses that cannot carry a body. net/http refuses Write on a 204, so trying
// would leave an ERROR every time although the response is correct, burying the real write
// failures.
func TestJSONSkipsBodyForBodilessStatus(t *testing.T) {
	var logs bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(restore) })

	// Check against a real server: httptest.ResponseRecorder does not refuse Write on a 204,
	// so a recorder alone cannot reproduce net/http's behavior.
	// net/http treats 1xx as interim responses, never final ones, so those are checked with
	// the recorder below.
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
			t.Errorf("%d: Content-Type = %q, want none (there is no body)", status, got)
		}
		if logs.Len() != 0 {
			t.Errorf("%d: unexpected log: %s", status, logs.String())
		}
	}

	// Also check directly on a recorder that the body is empty (1xx can only be seen here).
	for _, status := range []int{http.StatusContinue, http.StatusNoContent, http.StatusNotModified} {
		rec := httptest.NewRecorder()
		respond.JSON(rec, nil, status, map[string]string{"ignored": "yes"})
		if rec.Code != status || rec.Body.Len() != 0 {
			t.Errorf("status = %d, body = %q; want %d and empty body", rec.Code, rec.Body.String(), status)
		}
	}
}

// TestServerErrorHidesCauseAndLogsIt verifies that the cause of a 5xx goes to the log, not
// the body. Internal error text contains connection targets and paths, which the user would
// see if they went into the body.
func TestServerErrorHidesCauseAndLogsIt(t *testing.T) {
	var buf bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(restore) })

	cause := errors.New("dial tcp 10.0.0.5:443: connection refused")

	t.Run("Error は Accept に応じて形を変える", func(t *testing.T) {
		buf.Reset()
		req := httptest.NewRequest(http.MethodGet, "/jobs", nil)
		req.Header.Set("Accept", "application/json")
		rec := httptest.NewRecorder()

		respond.ServerError(rec, req, http.StatusBadGateway, cause, "backend unreachable", "job_id", "j1")

		if rec.Code != http.StatusBadGateway {
			t.Errorf("status = %d, want 502", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "10.0.0.5") {
			t.Errorf("body leaks the cause: %s", rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), http.StatusText(http.StatusBadGateway)) {
			t.Errorf("body = %q, want the status text", rec.Body.String())
		}
		for _, want := range []string{"backend unreachable", "10.0.0.5", `"job_id":"j1"`, `"status":502`} {
			if !strings.Contains(buf.String(), want) {
				t.Errorf("log lacks %q: %s", want, buf.String())
			}
		}
	})

	t.Run("ServerErrorJSON は常に JSON", func(t *testing.T) {
		buf.Reset()
		rec := httptest.NewRecorder()

		respond.ServerErrorJSON(rec, httptest.NewRequest(http.MethodGet, "/jobs", nil), http.StatusInternalServerError, cause, "boom")

		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] != http.StatusText(http.StatusInternalServerError) {
			t.Errorf("body = %q, want {\"error\":\"Internal Server Error\"}", rec.Body.String())
		}
		if !strings.Contains(buf.String(), "connection refused") {
			t.Errorf("cause not logged: %s", buf.String())
		}
	})

	t.Run("nil のリクエストでも落ちない", func(t *testing.T) {
		rec := httptest.NewRecorder()
		respond.ServerError(rec, nil, http.StatusInternalServerError, cause, "boom")
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
	})
}
