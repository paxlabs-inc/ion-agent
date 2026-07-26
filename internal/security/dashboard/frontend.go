package dashboard

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"time"
)

//go:embed frontend/*
var frontendFiles embed.FS

// Frontend serves the embedded safety dashboard with a restrictive CSP.
func Frontend() (http.Handler, error) {
	subtree, err := fs.Sub(frontendFiles, "frontend")
	if err != nil {
		return nil, err
	}
	files := http.FileServer(http.FS(subtree))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Security-Policy",
			"default-src 'self'; connect-src 'self' ws: wss:; "+
				"script-src 'self'; style-src 'self'; object-src 'none'; "+
				"base-uri 'none'; frame-ancestors 'none'")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		if request.URL.Path == "/" {
			payload, readErr := fs.ReadFile(subtree, "index.html")
			if readErr != nil {
				http.Error(writer, "frontend unavailable", http.StatusInternalServerError)
				return
			}
			http.ServeContent(writer, request, "index.html", time.Time{}, bytes.NewReader(payload))
			return
		}
		files.ServeHTTP(writer, request)
	}), nil
}
