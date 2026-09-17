package proxy

import (
	"net/http"
	"testing"
)

func TestConfigure(t *testing.T) {
	t.Cleanup(Off)
	if err := Configure("http://proxy.lan:3128", "localhost, 192.168.0.0/16, example.org"); err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport)
	for target, want := range map[string]string{"https://api.themoviedb.org/3/x": "http://proxy.lan:3128", "http://192.168.1.50:8112/json": "", "http://sub.example.org/": "", "http://localhost:8080/": ""} {
		req, _ := http.NewRequest(http.MethodGet, target, nil)
		u, _ := transport.Proxy(req)
		got := ""
		if u != nil {
			got = u.String()
		}
		if got != want {
			t.Errorf("%s: want proxy %q, got %q", target, want, got)
		}
	}
	if err := Configure("socks5://x:1", ""); err == nil {
		t.Error("want socks refused")
	}
}
