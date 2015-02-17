package mcproxy

import (
	"bytes"
	"github.com/bradfitz/gomemcache/memcache"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// onExitFlushLoop is a callback set by tests to detect the state of the
// flushLoop() goroutine.
var onExitFlushLoop func()

// McReverseProxy is an HTTP Handler that takes an incoming request and
// sends it to another server, proxying the response back to the
// client.
type McReverseProxy struct {
	// Director must be a function which modifies
	// the request into a new request to be sent
	// using Transport. Its response is then copied
	// back to the original client unmodified.
	Director func(*http.Request)

	// The transport used to perform proxy requests.
	// If nil, http.DefaultTransport is used.
	Transport http.RoundTripper

	// FlushInterval specifies the flush interval
	// to flush to the client while copying the
	// response body.
	// If zero, no periodic flushing is done.
	FlushInterval time.Duration

	// ErrorLog specifies an optional logger for errors
	// that occur when attempting to proxy the request.
	// If nil, logging goes to os.Stderr via the log package's
	// standard logger.
	ErrorLog *log.Logger

	Mc *memcache.Client

	Prefix string

	TTL time.Duration
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

// NewSingleHostMcReverseProxy returns a new McReverseProxy that rewrites
// URLs to the scheme, host, and base path provided in target. If the
// target's path is "/base" and the incoming request was for "/dir",
// the target request will be for /base/dir.
func NewMemcachedReverseProxy(target *url.URL, mc *memcache.Client, prefix string, ttl time.Duration) *McReverseProxy {
	targetQuery := target.RawQuery
	director := func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = singleJoiningSlash(target.Path, req.URL.Path)
		if targetQuery == "" || req.URL.RawQuery == "" {
			req.URL.RawQuery = targetQuery + req.URL.RawQuery
		} else {
			req.URL.RawQuery = targetQuery + "&" + req.URL.RawQuery
		}
	}
	return &McReverseProxy{
		Director: director,
		Mc:       mc,
		Prefix:   prefix,
		TTL:      ttl,
	}
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// Hop-by-hop headers. These are removed when sent to the backend.
// http://www.w3.org/Protocols/rfc2616/rfc2616-sec13.html
var hopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te", // canonicalized version of "TE"
	"Trailers",
	"Transfer-Encoding",
	"Upgrade",
}

func (self *McReverseProxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	mckey := self.Prefix + req.URL.String()
	it, err := self.Mc.Get(mckey)
	if err != nil && err != memcache.ErrCacheMiss {
		self.logf("0 memcached error: %v", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}
	if err == nil {
		rw.WriteHeader(http.StatusOK)
		self.copyResponse(rw, bytes.NewReader(it.Value))
		return
	}

	transport := self.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	outreq := new(http.Request)
	*outreq = *req // includes shallow copies of maps, but okay

	self.Director(outreq)
	outreq.Proto = "HTTP/1.1"
	outreq.ProtoMajor = 1
	outreq.ProtoMinor = 1
	outreq.Close = false

	// Remove hop-by-hop headers to the backend.  Especially
	// important is "Connection" because we want a persistent
	// connection, regardless of what the client sent to us.  This
	// is modifying the same underlying map from req (shallow
	// copied above) so we only copy it if necessary.
	copiedHeaders := false
	for _, h := range hopHeaders {
		if outreq.Header.Get(h) != "" {
			if !copiedHeaders {
				outreq.Header = make(http.Header)
				copyHeader(outreq.Header, req.Header)
				copiedHeaders = true
			}
			outreq.Header.Del(h)
		}
	}

	if clientIP, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		// If we aren't the first proxy retain prior
		// X-Forwarded-For information as a comma+space
		// separated list and fold multiple headers into one.
		if prior, ok := outreq.Header["X-Forwarded-For"]; ok {
			clientIP = strings.Join(prior, ", ") + ", " + clientIP
		}
		outreq.Header.Set("X-Forwarded-For", clientIP)
	}

	resp, err := transport.RoundTrip(outreq)
	if err != nil {
		self.logf("http: proxy error: %v", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	for _, h := range hopHeaders {
		resp.Header.Del(h)
	}

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		self.logf("http: read error: %v", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	if err = self.Mc.Set(&memcache.Item{Key: mckey, Value: data, Expiration: int32(self.TTL / time.Second)}); err != nil {
		self.logf("1 memcached error: %v", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	copyHeader(rw.Header(), resp.Header)
	rw.WriteHeader(resp.StatusCode)
	self.copyResponse(rw, bytes.NewReader(data))
}

func (self *McReverseProxy) copyResponse(dst io.Writer, src io.Reader) {
	if self.FlushInterval != 0 {
		if wf, ok := dst.(writeFlusher); ok {
			mlw := &maxLatencyWriter{
				dst:     wf,
				latency: self.FlushInterval,
				done:    make(chan bool),
			}
			go mlw.flushLoop()
			defer mlw.stop()
			dst = mlw
		}
	}

	io.Copy(dst, src)
}

func (self *McReverseProxy) logf(format string, args ...interface{}) {
	if self.ErrorLog != nil {
		self.ErrorLog.Printf(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

type writeFlusher interface {
	io.Writer
	http.Flusher
}

type maxLatencyWriter struct {
	dst     writeFlusher
	latency time.Duration

	lk   sync.Mutex // protects Write + Flush
	done chan bool
}

func (self *maxLatencyWriter) Write(p []byte) (int, error) {
	self.lk.Lock()
	defer self.lk.Unlock()
	return self.dst.Write(p)
}

func (self *maxLatencyWriter) flushLoop() {
	t := time.NewTicker(self.latency)
	defer t.Stop()
	for {
		select {
		case <-self.done:
			if onExitFlushLoop != nil {
				onExitFlushLoop()
			}
			return
		case <-t.C:
			self.lk.Lock()
			self.dst.Flush()
			self.lk.Unlock()
		}
	}
}

func (self *maxLatencyWriter) stop() { self.done <- true }
