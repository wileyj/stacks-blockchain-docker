package main

import (
	"fmt"
	"regexp"
	"strings"
)

// The `proxy` profile is how the API is served to other machines (owner, 2026-11-05): the API and the
// node RPC listen on 127.0.0.1 by default, and the proxy publishes it on PROXY_BIND_ADDR (default
// 0.0.0.0). The API proxies /v2/* to the node itself, so the node's RPC is reachable through it too.
//
// The proxy is Caddy (owner, 2026-11-13). HTTP only by default; PROXY_TLS=1 adds HTTPS, with a Let's
// Encrypt certificate when PROXY_DOMAIN is set (Caddy obtains and renews it; the domain must point here
// and ports 80/443 be reachable) and a self-signed one otherwise. Either way port 80 then redirects.

var (
	domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	emailRe  = regexp.MustCompile(`^[^@\s{}]+@[^@\s{}]+\.[^@\s{}]+$`)
)

// proxySettings reads and checks PROXY_TLS, PROXY_DOMAIN and PROXY_ACME_EMAIL.
func proxySettings(s *Settings, getenv func(string) string) error {
	switch v := orDefault(getenv("PROXY_TLS"), "0"); v {
	case "1":
		s.ProxyTLS = true
	case "0":
	default:
		return fmt.Errorf("PROXY_TLS must be 0 or 1, got %q", v)
	}
	s.ProxyDomain = strings.ToLower(strings.TrimSpace(getenv("PROXY_DOMAIN")))
	s.ProxyACMEEmail = strings.TrimSpace(getenv("PROXY_ACME_EMAIL"))
	switch {
	case s.ProxyDomain != "" && !domainRe.MatchString(s.ProxyDomain):
		return fmt.Errorf("PROXY_DOMAIN must be a DNS name like api.example.org, got %q", s.ProxyDomain)
	case s.ProxyDomain != "" && !s.ProxyTLS:
		return fmt.Errorf("PROXY_DOMAIN=%s is only used for HTTPS: set PROXY_TLS=1 too (or remove PROXY_DOMAIN)", s.ProxyDomain)
	case s.ProxyACMEEmail != "" && !emailRe.MatchString(s.ProxyACMEEmail):
		return fmt.Errorf("PROXY_ACME_EMAIL must be an email address, got %q", s.ProxyACMEEmail)
	case s.ProxyACMEEmail != "" && s.ProxyDomain == "":
		return fmt.Errorf("PROXY_ACME_EMAIL is for Let's Encrypt, which needs PROXY_DOMAIN")
	}
	return nil
}

// RenderProxy renders conf/proxy/Caddyfile.
func RenderProxy(s Settings) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "%s. Do not edit: it is rewritten on every `docker compose up`.\n\n", Marker)
	// HTTP/1.1 and 2 only: HTTP/3 needs UDP 443, which compose doesn't publish
	b.WriteString("{\n\tservers {\n\t\tprotocols h1 h2\n\t}\n")
	if s.ProxyACMEEmail != "" {
		fmt.Fprintf(&b, "\temail %s\n", s.ProxyACMEEmail)
	}
	b.WriteString("}\n\n")
	// Caddy's reverse_proxy passes Host and sets X-Forwarded-For/-Proto, and handles WebSocket upgrades
	fmt.Fprintf(&b, "(api) {\n\thandle /status {\n\t\trespond \"ok\" 200\n\t}\n\thandle {\n\t\treverse_proxy stacks-blockchain-api:%s\n\t}\n}\n\n", s.APIPort)
	switch {
	case !s.ProxyTLS:
		b.WriteString("http:// {\n\timport api\n}\n")
	case s.ProxyDomain != "":
		// Automatic HTTPS: the certificate, its renewal and the port 80 redirect
		fmt.Fprintf(&b, "%s {\n\timport api\n}\n", s.ProxyDomain)
	default:
		// A self-signed certificate for whatever name or address clients use
		b.WriteString("https:// {\n\ttls internal {\n\t\ton_demand\n\t}\n\timport api\n}\n\nhttp:// {\n\tredir https://{host}{uri} 308\n}\n")
	}
	return []byte(b.String())
}
