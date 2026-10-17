// Command hello-http runs the hello example extension:
//
//	HELLO_SECRET=<secret> HELLO_ADDR=127.0.0.1:8787 go run ./examples/extensions/hello-http/cmd/hello-http
package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	hello "github.com/zadstack/gorun/examples/extensions/hello-http"
)

func main() {
	secret := os.Getenv("HELLO_SECRET")
	if secret == "" {
		slog.Error("HELLO_SECRET is required (shown once when the extension is installed)")
		os.Exit(1)
	}
	addr := os.Getenv("HELLO_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8787"
	}
	h := hello.Handler(secret)
	h.Log = slog.New(slog.NewTextHandler(os.Stdout, nil))
	mux := http.NewServeMux()
	mux.Handle("/gorun", h)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	slog.Info("hello-http listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("hello-http", "err", err)
		os.Exit(1)
	}
}
