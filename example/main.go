package main

import (
	"net/http"
	"net/url"
	"github.com/bradfitz/gomemcache/memcache"
	"mcproxy"
)

func main() {
	mc := memcache.New("localhost:11211")
	u, err := url.Parse("http://localhost:10000")
	if err != nil {
		panic(err)
	}
	if err := http.ListenAndServe(":10001", mcproxy.NewMemcachedReverseProxy(u, mc)); err != nil {
		panic(err)
	}
}

