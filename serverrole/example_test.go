package serverrole_test

import (
	"encoding/json"
	"fmt"

	"github.com/shouni/go-serve-kit/serverrole"
)

// 役割は明示が必須です。未設定も未知の値も起動時に止めます。
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

// 設定構造体へ直接バインドすると、デコードの時点で Parse が通ります。
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
