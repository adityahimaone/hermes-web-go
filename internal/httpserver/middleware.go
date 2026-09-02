package httpserver

import (
	"io"
	"log"
	"net/http"
	"os"
	"runtime/debug"
	"time"
)

var logWriter io.Writer = os.Stderr

func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.New(logWriter, "", 0).Printf("panic: %v\n%s", rec, debug.Stack())
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"Internal server error"}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.New(logWriter, "", 0).Printf(`{"time":%q,"method":%q,"path":%q,"dur_ms":%d}`, time.Now().UTC().Format(time.RFC3339), r.Method, r.URL.Path, time.Since(start).Milliseconds())
	})
}
