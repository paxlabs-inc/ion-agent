package office

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// OfficeProxy is a same-origin reverse proxy to the ONLYOFFICE document server.
type OfficeProxy struct {
	target       *url.URL
	proxy        *httputil.ReverseProxy
	authenticate func(*http.Request) (ActorContext, error)
	requireCSRF  func(*http.Request, ActorContext) error
	origin       string
	publicPath   string
}

// ActorContext holds the authenticated actor for proxy requests.
type ActorContext struct {
	ActorID string
}

// NewOfficeProxy creates a new reverse proxy to the ONLYOFFICE engine.
func NewOfficeProxy(
	internalURL string,
	publicPath string,
	origin string,
	authenticate func(*http.Request) (ActorContext, error),
	requireCSRF func(*http.Request, ActorContext) error,
) (*OfficeProxy, error) {
	if internalURL == "" {
		return nil, fmt.Errorf("office: proxy target URL is required")
	}
	if authenticate == nil || strings.TrimSpace(origin) == "" {
		return nil, fmt.Errorf("office: proxy authentication and origin are required")
	}
	publicPath = "/" + strings.Trim(strings.TrimSpace(publicPath), "/") + "/"
	target, err := url.Parse(internalURL)
	if err != nil || target.Scheme == "" || target.Host == "" ||
		target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return nil, fmt.Errorf("office: invalid proxy target")
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = func(response *http.Response) error {
		response.Header.Set("Permissions-Policy", "unload=*")
		return nil
	}

	// Custom transport for internal-only access
	proxy.Transport = &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	return &OfficeProxy{
		target:       target,
		proxy:        proxy,
		authenticate: authenticate,
		requireCSRF:  requireCSRF,
		origin:       origin,
		publicPath:   publicPath,
	}, nil
}

// ServeHTTP proxies authenticated requests to the ONLYOFFICE engine.
func (p *OfficeProxy) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	// Authenticate the Ion browser session
	actor, err := p.authenticate(request)
	if err != nil {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}

	if request.Method == http.MethodGet &&
		strings.HasSuffix(request.URL.Path, "/document_editor_service_worker.js") {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		writer.Header().Set("Permissions-Policy", "unload=*")
		writer.Header().Set(
			"Service-Worker-Allowed",
			strings.TrimSuffix(p.publicPath, "/")+"/",
		)
		_, _ = writer.Write([]byte(
			`self.addEventListener("install",event=>event.waitUntil(self.skipWaiting()));` +
				`self.addEventListener("activate",event=>event.waitUntil(self.clients.claim()));`,
		))
		return
	}

	// WebSocket and stateful proxy requests must originate from Ion. The
	// document-server client cannot attach Ion's API CSRF header, so the proxy
	// relies on the authenticated SameSite cookie plus this exact-origin gate.
	if isWebSocketUpgrade(request) || isMutation(request) {
		if request.Header.Get("Origin") != p.origin {
			http.Error(writer, "forbidden", http.StatusForbidden)
			return
		}
		if p.requireCSRF != nil && request.Header.Get("X-Ion-CSRF") != "" {
			if err := p.requireCSRF(request, actor); err != nil {
				http.Error(writer, "forbidden", http.StatusForbidden)
				return
			}
		}
	}

	// Rewrite the request for the upstream
	originalHost := request.Host
	targetPath := strings.TrimPrefix(request.URL.Path, strings.TrimSuffix(p.publicPath, "/"))
	if targetPath == "" {
		targetPath = "/"
	}
	request.URL.Path = targetPath
	// Set forwarding headers
	request.Header.Set("X-Forwarded-Host", originalHost)
	request.Header.Set("X-Forwarded-Proto", forwardedProtocol(request))
	request.Header.Set("X-Forwarded-Prefix", strings.TrimSuffix(p.publicPath, "/"))

	// Proxy the request
	p.proxy.ServeHTTP(writer, request)
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Connection"), "upgrade") &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func isMutation(r *http.Request) bool {
	return r.Method == http.MethodPost || r.Method == http.MethodPut ||
		r.Method == http.MethodPatch || r.Method == http.MethodDelete
}

func forwardedProtocol(request *http.Request) string {
	if request.TLS != nil {
		return "https"
	}
	if value := request.Header.Get("X-Forwarded-Proto"); value == "https" {
		return value
	}
	return "http"
}
