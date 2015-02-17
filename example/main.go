package main

import (
	"flag"
	"fmt"
	"github.com/bradfitz/gomemcache/memcache"
	"mcproxy"
	"net/http"
	"net/url"
	"strconv"
)

func main() {
	address := flag.String("addr", "", "service address")
	port := flag.Int("port", -1, "port")
	mcaddress := flag.String("mc", "", "memcached address")
	prefix := flag.String("prefix", "", "memcached keys prefix")
	flag.Parse()

	if (address == nil || *address == "") || (port == nil || *port == -1) || (mcaddress == nil || *mcaddress == "") || (prefix == nil) {
		fmt.Println("Usage:")
		flag.PrintDefaults()
		return
	}

	mc := memcache.New(*mcaddress)
	u, err := url.Parse(*address)
	if err != nil {
		panic(err)
	}
	if err := http.ListenAndServe(":"+strconv.Itoa(*port), mcproxy.NewMemcachedReverseProxy(u, mc, *prefix)); err != nil {
		panic(err)
	}
}
