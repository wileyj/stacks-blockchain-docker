package main

import (
	"fmt"
	"strings"
)

// The `proxy` profile is how the API is served to other machines (owner, 2026-11-05): the API and the
// node RPC listen on 127.0.0.1 by default, and nginx publishes on PROXY_BIND_ADDR (default 0.0.0.0).
// With PROXY_TLS=1 it serves HTTPS on 443 with fullchain.pem / privkey.pem from PROXY_TLS_DIR and
// redirects HTTP to it; /status stays on plain HTTP for the healthcheck. The API proxies /v2/* to the
// node itself, so the node's RPC is reachable through it too.

// RenderProxy renders conf/nginx/proxy.conf.
func RenderProxy(s Settings) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "%s. Do not edit: it is rewritten on every `docker compose up`.\n", Marker)
	b.WriteString(`map $http_upgrade $connection_upgrade {
  default upgrade;
  '' close;
}
upstream stacks-blockchain-api {
`)
	fmt.Fprintf(&b, "  server stacks-blockchain-api:%s;\n}\n", s.APIPort)
	api := `  location / {
    proxy_pass http://stacks-blockchain-api/;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
  }
`
	status := "  location /status {\n    default_type text/plain;\n    return 200 ok;\n  }\n"
	b.WriteString("server {\n  listen 80;\n  server_name _;\n" + status)
	if !s.ProxyTLS {
		b.WriteString(api + "}\n")
		return []byte(b.String())
	}
	b.WriteString("  location / {\n    return 301 https://$host$request_uri;\n  }\n}\n")
	b.WriteString(`server {
  listen 443 ssl;
  http2 on;
  server_name _;
  ssl_certificate /etc/nginx/tls/fullchain.pem;
  ssl_certificate_key /etc/nginx/tls/privkey.pem;
  ssl_protocols TLSv1.2 TLSv1.3;
  ssl_session_cache shared:tls:10m;
  add_header Strict-Transport-Security "max-age=31536000" always;
` + status + api + "}\n")
	return []byte(b.String())
}
