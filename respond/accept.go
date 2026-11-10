package respond

import (
	"net/http"
	"strings"
)

// mediaTypeJSON is the media type taken to mean the caller wants JSON.
const mediaTypeJSON = "application/json"

// WantsJSON reports whether the caller wants JSON.
//
// At the same time it sets Vary: Accept on the response. The decision and the declaration
// are one function to close the gap where the same URL changes its content by Accept
// without telling caches. With a shared cache or CDN in front, a missing Vary can hand HTML
// to a client that asked for JSON.
//
// The check only looks at whether Accept contains "application/json". So curl's default
// "*/*" falls to the HTML side, and "application/json;q=0" (JSON not wanted) also counts as
// JSON. q-values are not parsed because every consumer sends an explicit Accept.
//
// If w is nil, it only decides and sets no Vary. If r is nil, it returns false.
func WantsJSON(w http.ResponseWriter, r *http.Request) bool {
	if w != nil {
		AddVaryAccept(w.Header())
	}
	if r == nil {
		return false
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), mediaTypeJSON)
}

// AddVaryAccept adds Vary: Accept. It does nothing if Accept is already listed.
//
// Use it on paths that vary the response without going through the check (branching in a
// template, for example) and only need the Vary declaration.
func AddVaryAccept(h http.Header) {
	if h == nil {
		return
	}
	for _, value := range h.Values("Vary") {
		for field := range strings.SplitSeq(value, ",") {
			if strings.EqualFold(strings.TrimSpace(field), "Accept") {
				return
			}
		}
	}
	h.Add("Vary", "Accept")
}
