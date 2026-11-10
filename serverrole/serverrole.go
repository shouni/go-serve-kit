// Package serverrole provides the vocabulary for which side a process takes when one image
// is deployed as two services, a web surface and a worker surface.
//
// It holds only the vocabulary and its validation; what each role serves is decided by the
// consumer. The kit never branches on the role, so an app that needs a fourth role or
// per-role attributes can add them on its own side without changing this package (Role is
// a defined string type, so declaring your own constant and wrapping Parse with your own
// interpretation is enough).
package serverrole

import (
	"fmt"
	"strings"
)

// Role is the role a process takes.
type Role string

const (
	// Both serves both the web surface and the worker surface (for local development).
	Both Role = "both"
	// Web serves only the web surface (the browser UI and the service-to-service API).
	Web Role = "web"
	// Worker serves only the worker surface (the execution side called from a task queue).
	Worker Role = "worker"
)

// Parse converts a setting such as SERVER_ROLE into a role. Both the empty string and
// unknown values are errors. Case and surrounding whitespace are normalized and accepted.
//
// Unset is not taken as Both. Treating it that way would mean that a single missing
// environment variable in production brings the worker routes back onto the public web
// surface. Silently accepting an unknown value is just as dangerous: this time a service
// that serves no routes at all gets deployed. Failing at startup is safer in both cases.
func Parse(raw string) (Role, error) {
	role := Role(strings.ToLower(strings.TrimSpace(raw)))
	switch role {
	case Both, Web, Worker:
		return role, nil
	default:
		return "", fmt.Errorf("server role (%q) must be one of %q, %q, %q",
			raw, Web, Worker, Both)
	}
}

// ServesWeb reports whether this role serves the web surface.
func (r Role) ServesWeb() bool { return r == Both || r == Web }

// ServesWorker reports whether this role serves the worker surface.
func (r Role) ServesWorker() bool { return r == Both || r == Worker }

// UnmarshalText implements encoding.TextUnmarshaler.
//
// It exists so that Parse runs at the moment environment variables or JSON are decoded.
// Without it, a decoder sees only that Role is a defined string type and assigns even an
// unknown value; if the app then forgets to call Parse, it starts with both ServesWeb and
// ServesWorker false, and a service that serves no routes at all gets deployed.
//
// Most decoders do not call this method when no value is given. Rejecting unset is the
// tag's job (for example env:"SERVER_ROLE,required").
func (r *Role) UnmarshalText(text []byte) error {
	role, err := Parse(string(text))
	if err != nil {
		return err
	}
	*r = role
	return nil
}
