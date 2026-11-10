// Package respond holds the boilerplate of the side that writes HTTP responses: writing
// the response body (JSON / Error / ErrorJSON) and deciding which representation fits the
// caller (WantsJSON).
//
// The decision and the writing live in one package because sharing only one of them lets
// the other split per app. What had actually split was the Content-Type
// ("application/json" vs "application/json; charset=utf-8") and the handling of an encode
// failure (logged with context, logged without it, or swallowed).
//
// A representation is chosen only when one resource has two representations. Splitting a
// route into a screen route and an API route means keeping two copies of the same fetch,
// and fixing only one makes the screen and the machine-readable result disagree. Keep one
// route and choose only the representation by Accept. Something with no JSON counterpart,
// such as an input form, is a separate resource, so it stays a separate route.
package respond

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
)

// contentTypeJSON is the Content-Type set on JSON responses.
//
// RFC 8259 defines JSON as UTF-8, so the charset parameter changes nothing about how it is
// read, but it is fixed to one form so that a client calling several backends does not
// have to handle both values.
const contentTypeJSON = "application/json; charset=utf-8"

// errorBody is the error body {"error": "..."} returned to callers that asked for JSON.
type errorBody struct {
	Error string `json:"error"`
}

// encodeFailureBody is the body returned when encoding fails.
//
// It is prepared without going through errorBody so that, right after an encode failure,
// the same encoding path is not run a second time.
var encodeFailureBody = []byte(`{"error":"Internal Server Error"}` + "\n")

// JSON writes payload as JSON.
//
// It builds the body in a buffer before sending. Streaming a value that can fail midway
// (a chan, a cycle, a MarshalJSON that returns an error) straight into w delivers the
// broken JSON written so far under a 200, which the caller sees in the most confusing form:
// "it succeeded, but the body is broken". Building first lets the failure become a 500.
//
// The failure body is JSON too. If a route that promises JSON switched to text/plain only
// on failure, callers would have to read the body one way on success and another on
// failure (the same reason as ErrorJSON).
//
// Failures are logged to slog.Default() so that, in apps that replaced the default logger,
// severity and trace correlation apply as they are.
//
// It does not set Vary: Accept. Whether the representation varies is known to whoever
// called WantsJSON, and a route that returns only JSON needs no Vary.
//
// For statuses that cannot carry a body (1xx / 204 / 304) the payload is discarded and only
// the status is sent. net/http refuses writes on those statuses, so attempting one would log
// a write failure every time even though the response arrives correctly, burying the real
// failures.
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
		slog.ErrorContext(requestContext(r), "respond: failed to encode JSON response",
			"error", err, "status", status)
		w.Header().Set("Content-Type", contentTypeJSON)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(encodeFailureBody)
		return
	}

	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)

	if _, err := w.Write(buf.Bytes()); err != nil {
		slog.ErrorContext(requestContext(r), "respond: failed to write JSON response",
			"error", err, "status", status)
	}
}

// Error returns an error as JSON if the caller asked for JSON, and as text/plain otherwise.
//
// It is for routes whose URL is shared by a screen and an API. It is not fixed to JSON
// because the screen's JS reads the error body with resp.text(); fixed to text/plain, an
// agent going through the same route could not read the body as structured data. The
// choice is delegated to WantsJSON, so Vary: Accept is set there as well.
//
// Use ErrorJSON on routes that return only JSON.
func Error(w http.ResponseWriter, r *http.Request, status int, message string) {
	if WantsJSON(w, r) {
		ErrorJSON(w, r, status, message)
		return
	}
	http.Error(w, message, status)
}

// ErrorJSON returns an error as JSON whatever the caller asked for.
//
// It is for routes that return only JSON. Such routes return JSON unconditionally on
// success, so if only errors changed shape with Accept, callers would have to read the body
// one way on success and another on failure (a browser fetch that sends no Accept reads the
// error body as JSON too).
//
// It does not set Vary: Accept, because this response does not vary with Accept.
func ErrorJSON(w http.ResponseWriter, r *http.Request, status int, message string) {
	JSON(w, r, status, errorBody{Error: message})
}

// ServerError returns a 5xx response in the form "the cause goes to the log, the body is
// canned text".
//
// The text of an internal error (the host it connected to, a storage path, an SDK message)
// is visible to the user if it goes into the body as is. That is why URL convention 2.6
// says "log the details of a 5xx and keep them out of the body". When every handler writes
// that as the two lines "slog, then canned text", the canned text splits per service
// ("internal server error", StatusText, Japanese) and paths that pass err.Error() straight
// through creep in.
//
// It logs with r's context, so attributes put on ctx through slogctx (job_id and so on)
// are included. The body is JSON or text/plain according to Accept (the same as Error).
// Do not use it for 4xx: a 4xx body is the explanation owed to the user, not something to
// hide.
func ServerError(w http.ResponseWriter, r *http.Request, status int, err error, msg string, attrs ...any) {
	logServerError(r, status, err, msg, attrs)
	Error(w, r, status, http.StatusText(status))
}

// ServerErrorJSON is the version of ServerError that always returns JSON (the same
// relationship as ErrorJSON to Error).
func ServerErrorJSON(w http.ResponseWriter, r *http.Request, status int, err error, msg string, attrs ...any) {
	logServerError(r, status, err, msg, attrs)
	ErrorJSON(w, r, status, http.StatusText(status))
}

// logServerError logs the cause of a 5xx with the request's context.
func logServerError(r *http.Request, status int, err error, msg string, attrs []any) {
	attrs = append(attrs, "status", status, "error", err)
	slog.ErrorContext(requestContext(r), msg, attrs...)
}

// bodyAllowed reports whether a response with status can carry a body.
// The rule is the same as the condition under which net/http refuses Write (1xx / 204 / 304).
func bodyAllowed(status int) bool {
	if status >= 100 && status <= 199 {
		return false
	}
	return status != http.StatusNoContent && status != http.StatusNotModified
}

// requestContext returns a context that is usable even when r is nil.
// Like the other functions in this package, it must not crash on zero values or nil.
func requestContext(r *http.Request) context.Context {
	if r == nil {
		return context.Background()
	}
	return r.Context()
}
