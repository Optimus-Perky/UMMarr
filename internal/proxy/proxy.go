// Package proxy routes UMMarr's outbound HTTP through a proxy - Sonarr's
// Settings > General > Proxy - except for hosts on the bypass list.
package proxy

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// base is the standard library's transport, taken before anything wraps
// http.DefaultTransport (internal/logging traces requests by wrapping it),
// so the proxy is still set on the transport that does the work.
var base, _ = http.DefaultTransport.(*http.Transport)

// Configure points the default transport (which every client here uses) at
// proxyURL, bypassing the comma-separated hosts and CIDRs in bypass.
func Configure(proxyURL, bypass string) error {
	u, err := url.Parse(proxyURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("proxy URL must be http://host:port or https://host:port")
	}
	var hosts []string
	var nets []*net.IPNet
	for _, item := range strings.Split(bypass, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(item); err == nil {
			nets = append(nets, n)
			continue
		}
		hosts = append(hosts, strings.ToLower(item))
	}
	direct := func(host string) bool {
		host = strings.ToLower(host)
		for _, h := range hosts {
			if host == h || strings.HasSuffix(host, "."+h) {
				return true
			}
		}
		if ip := net.ParseIP(host); ip != nil {
			for _, n := range nets {
				if n.Contains(ip) {
					return true
				}
			}
		}
		return false
	}
	if base == nil {
		return fmt.Errorf("default transport isn't an *http.Transport")
	}
	base.Proxy = func(r *http.Request) (*url.URL, error) {
		if direct(r.URL.Hostname()) {
			return nil, nil
		}
		return u, nil
	}
	return nil
}

// Off removes any proxy.
func Off() {
	if base != nil {
		base.Proxy = http.ProxyFromEnvironment
	}
}
