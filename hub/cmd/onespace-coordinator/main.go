package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"onespace.local/hub/coordination"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] != "serve" {
		return errors.New("only the serve command is supported")
	}
	flags := flag.NewFlagSet("onespace-coordinator", flag.ContinueOnError)
	listen := flags.String("listen", "127.0.0.1:47604", "HTTPS listen address")
	cert := flags.String("cert", "", "TLS certificate PEM path")
	key := flags.String("key", "", "TLS private key PEM path")
	devices := flags.String("devices", "", "owner-only JSON file containing device token hashes")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *cert == "" || *key == "" || *devices == "" {
		return errors.New("usage: onespace-coordinator serve --listen ADDRESS --cert CERT --key KEY --devices HASHES")
	}
	authorizer, err := coordination.LoadTokenAuthorizer(*devices)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	broker, err := coordination.New(mux, authorizer.Authenticate, func(viewer, target string) bool {
		return authorizer.AllowCamera(viewer, target)
	})
	if err != nil {
		return err
	}
	defer broker.Close()

	// CORS middleware for browser-based signaling
	corsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		mux.ServeHTTP(w, r)
	})

	server := &http.Server{
		Addr:              *listen,
		Handler:           corsHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       65 * time.Second,
		WriteTimeout:      65 * time.Second,
		IdleTimeout:       70 * time.Second,
		MaxHeaderBytes:    16 << 10,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13},
	}
	return server.ListenAndServeTLS(*cert, *key)
}
EOF