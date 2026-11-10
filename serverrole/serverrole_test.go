package serverrole

import (
	"encoding"
	"encoding/json"
	"strings"
	"testing"
)

// Rejecting the empty string and unknown values is why this package exists.
// Folding unset into Both brings the worker routes back onto the public web surface.
func TestParse(t *testing.T) {
	t.Parallel()

	t.Run("valid values", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			raw  string
			want Role
		}{
			{"both", Both},
			{"web", Web},
			{"worker", Worker},
			// Case and surrounding whitespace are normalized.
			{" WEB ", Web},
			{"Worker", Worker},
		}
		for _, tt := range tests {
			raw, want := tt.raw, tt.want
			got, err := Parse(raw)
			if err != nil {
				t.Errorf("Parse(%q) error = %v", raw, err)
				continue
			}
			if got != want {
				t.Errorf("Parse(%q) = %q, want %q", raw, got, want)
			}
		}
	})

	t.Run("empty and unknown values fail", func(t *testing.T) {
		t.Parallel()
		for _, raw := range []string{"", "   ", "wrker", "all", "true", "batch"} {
			got, err := Parse(raw)
			if err == nil {
				t.Errorf("Parse(%q) = %q, want error", raw, got)
			}
		}
	})

	// Without the configured value in the error message, a typo takes longer to find.
	t.Run("error shows input and choices", func(t *testing.T) {
		t.Parallel()
		_, err := Parse("wrker")
		if err == nil {
			t.Fatal("Parse(\"wrker\") = nil error, want error")
		}
		for _, want := range []string{`"wrker"`, string(Web), string(Worker), string(Both)} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), want)
			}
		}
	})
}

func TestRoleServes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		role       Role
		servesWeb  bool
		servesWork bool
	}{
		{Both, true, true},
		{Web, true, false},
		{Worker, false, true},
		// A custom role added by the consumer serves neither of the two surfaces the kit knows.
		{Role("batch"), false, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			t.Parallel()
			if got := tt.role.ServesWeb(); got != tt.servesWeb {
				t.Errorf("ServesWeb() = %v, want %v", got, tt.servesWeb)
			}
			if got := tt.role.ServesWorker(); got != tt.servesWork {
				t.Errorf("ServesWorker() = %v, want %v", got, tt.servesWork)
			}
		})
	}
}

// TestRoleUnmarshalText verifies that Parse's strictness applies at decode time.
//
// Apps receive Role directly through an env tag, so if it is not rejected here an unknown
// value goes straight into Role, producing a service that starts but serves no routes.
func TestRoleUnmarshalText(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		want    Role
		wantErr bool
	}{
		{name: "known value", text: "worker", want: Worker},
		{name: "normalizes case and spaces", text: "  Web  ", want: Web},
		{name: "empty fails", text: "", wantErr: true},
		{name: "unknown value fails", text: "api", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var role Role
			err := role.UnmarshalText([]byte(tt.text))

			if tt.wantErr {
				if err == nil {
					t.Fatalf("UnmarshalText(%q) = nil, want error", tt.text)
				}
				// A failure leaves the value untouched (the caller can keep treating it as the zero value).
				if role != "" {
					t.Errorf("role = %q was set on error", role)
				}
				return
			}
			if err != nil {
				t.Fatalf("UnmarshalText(%q) = %v", tt.text, err)
			}
			if role != tt.want {
				t.Errorf("role = %q, want %q", role, tt.want)
			}
		})
	}
}

// TestRoleImplementsTextUnmarshaler pins that decoders actually take this path.
// encoding/json dispatches on encoding.TextUnmarshaler, and caarlos0/env (which the apps
// use) goes through the same check.
func TestRoleImplementsTextUnmarshaler(t *testing.T) {
	var _ encoding.TextUnmarshaler = (*Role)(nil)

	var cfg struct {
		Role Role `json:"role"`
	}
	if err := json.Unmarshal([]byte(`{"role":"WORKER"}`), &cfg); err != nil {
		t.Fatalf("Unmarshal = %v", err)
	}
	if cfg.Role != Worker {
		t.Errorf("role = %q, want %q", cfg.Role, Worker)
	}

	if err := json.Unmarshal([]byte(`{"role":"api"}`), &cfg); err == nil {
		t.Error("unknown value passed decoding")
	}
}
