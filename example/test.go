package main

import (
	"github.com/bradfitz/gomemcache/memcache"
	"mcproxy"
	"net/http"
	"net/url"
)

func main() {
	mc := memcache.New("localhost:11211")
	u, err := url.Parse("http://localhost:10000")
	if err != nil {
		panic(err)
	}
	if err := http.ListenAndServe(":10001", mcproxy.NewMemcachedReverseProxy(u, mc, "test:", 0)); err != nil {
		panic(err)
	}
}
