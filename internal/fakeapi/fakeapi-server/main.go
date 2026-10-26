// Command fakeapi-server serves the fake OpenAI and Anthropic Admin APIs on
// a local address for the README demo. It never talks to a real provider.
//
//	go run ./internal/fakeapi/fakeapi-server 127.0.0.1:8789
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/sudodrift/keyburn/internal/fakeapi"
)

func main() {
	addr := "127.0.0.1:8789"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}
	srv := fakeapi.New(fakeapi.Default(time.Now()))
	fmt.Fprintf(os.Stderr, "fake OpenAI and Anthropic Admin APIs on http://%s\n", addr)
	if err := http.ListenAndServe(addr, srv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
