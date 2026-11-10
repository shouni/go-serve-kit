package respond_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shouni/go-serve-kit/respond"
)

func TestWantsJSON(t *testing.T) {
	tests := []struct {
		name   string
		accept string
		want   bool
	}{
		{"explicit JSON is true", "application/json", true},
		{"with charset is true", "application/json; charset=utf-8", true},
		{"uppercase is true", "Application/JSON", true},
		{"browser Accept is false", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", false},
		{"curl default */* is false", "*/*", false},
		{"no Accept is false", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}
			rec := httptest.NewRecorder()

			if got := respond.WantsJSON(rec, req); got != tt.want {
				t.Errorf("WantsJSON() = %v, want %v", got, tt.want)
			}
			// Vary is always set, whatever the decision.
			if got := rec.Header().Get("Vary"); got != "Accept" {
				t.Errorf("Vary = %q, want %q", got, "Accept")
			}
		})
	}
}

// Checks the intent behind keeping the decision and Vary in one function:
// a path that only decides, without passing w, must not panic.
func TestWantsJSON_NilArgs(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/json")

	if got := respond.WantsJSON(nil, req); !got {
		t.Error("WantsJSON(nil, req) = false, want true")
	}
	if got := respond.WantsJSON(httptest.NewRecorder(), nil); got {
		t.Error("WantsJSON(w, nil) = true, want false")
	}
}

func TestAddVaryAccept(t *testing.T) {
	t.Run("does not add duplicates", func(t *testing.T) {
		h := http.Header{}
		respond.AddVaryAccept(h)
		respond.AddVaryAccept(h)

		if got := h.Values("Vary"); len(got) != 1 || got[0] != "Accept" {
			t.Errorf("Vary = %v, want [Accept]", got)
		}
	})

	// When Vary is already set for another header, Accept is added.
	t.Run("keeps existing Vary", func(t *testing.T) {
		h := http.Header{}
		h.Add("Vary", "Origin")
		respond.AddVaryAccept(h)

		got := h.Values("Vary")
		if len(got) != 2 || got[0] != "Origin" || got[1] != "Accept" {
			t.Errorf("Vary = %v, want [Origin Accept]", got)
		}
	})

	// When already listed in a comma-separated value, it is not added.
	t.Run("detects existing comma-separated value", func(t *testing.T) {
		h := http.Header{}
		h.Add("Vary", "Origin, accept")
		respond.AddVaryAccept(h)

		if got := h.Values("Vary"); len(got) != 1 {
			t.Errorf("Vary = %v, want Accept not added again", got)
		}
	})

	t.Run("nil does not panic", func(_ *testing.T) {
		respond.AddVaryAccept(nil)
	})
}
