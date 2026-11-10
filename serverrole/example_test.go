package serverrole_test

import (
	"encoding/json"
	"fmt"

	"github.com/shouni/go-serve-kit/serverrole"
)

// The role must be stated explicitly. Unset and unknown values both stop at startup.
func ExampleParse() {
	for _, raw := range []string{" Web ", "worker", "", "api"} {
		role, err := serverrole.Parse(raw)
		if err != nil {
			fmt.Printf("%q -> error\n", raw)
			continue
		}
		fmt.Printf("%q -> %s (web=%t worker=%t)\n", raw, role, role.ServesWeb(), role.ServesWorker())
	}
	// Output:
	// " Web " -> web (web=true worker=false)
	// "worker" -> worker (web=false worker=true)
	// "" -> error
	// "api" -> error
}

// Binding directly into a config struct runs Parse at decode time.
func ExampleRole_UnmarshalText() {
	type config struct {
		Role serverrole.Role `json:"role"`
	}
	var cfg config
	fmt.Println(json.Unmarshal([]byte(`{"role":"both"}`), &cfg), cfg.Role)
	fmt.Println(json.Unmarshal([]byte(`{"role":"api"}`), &cfg) != nil)
	// Output:
	// <nil> both
	// true
}
