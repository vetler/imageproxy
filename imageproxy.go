// Copyright 2013 The imageproxy authors.
// SPDX-License-Identifier: Apache-2.0

// Package imageproxy provides an image proxy server.  For typical use of
// creating and using a Proxy, see cmd/imageproxy/main.go.
package imageproxy // import "willnorris.com/go/imageproxy"

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/fcjr/aia-transport-go"
	"github.com/gregjones/httpcache"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	tphttp "willnorris.com/go/imageproxy/third_party/http"
	tphc "willnorris.com/go/imageproxy/third_party/httpcache"
)

// Maximum number of redirection-followings allowed.
const maxRedirects = 10

const (
	maxRetries    = 3
	retryInterval = 100 * time.Millisecond
)

// Proxy serves image requests.
type Proxy struct {
	Client *http.Client // client used to fetch remote URLs
	Cache  Cache        // cache used to cache responses

	// AllowHosts specifies a list of remote hosts that images can be
	// proxied from.  An empty list means all hosts are allowed.
	AllowHosts []string

	// DenyHosts specifies a list of remote hosts that images cannot be
	// proxied from.
	DenyHosts []string

	// Referrers, when given, requires that requests to the image
	// proxy come from a referring host. An empty list means all
	// hosts are allowed.
	Referrers []string

	// IncludeReferer controls whether the original Referer request header
	// is included in remote requests.
	IncludeReferer bool

	// FollowRedirects controls whether imageproxy will follow redirects or not.
	FollowRedirects bool

	// DefaultBaseURL is the URL that relative remote URLs are resolved in
	// reference to.  If nil, all remote URLs specified in requests must be
	// absolute.
	DefaultBaseURL *url.URL

	// The Logger used by the image proxy
	Logger *log.Logger

	// SignatureKeys is a list of HMAC keys used to verify signed requests.
	// Any of them can be used to verify signed requests.
	SignatureKeys [][]byte

	// Allow images to scale beyond their original dimensions.
	ScaleUp bool

	// Timeout specifies a time limit for requests served by this Proxy.
	// If a call runs for longer than its time limit, a 504 Gateway Timeout
	// response is returned.  A Timeout of zero means no timeout.
	Timeout time.Duration

	// If true, log additional debug messages
	Verbose bool

	// ContentTypes specifies a list of content types to allow. An empty
	// list means all content types are allowed.
	ContentTypes []string

	// The User-Agent used by imageproxy when requesting origin image
	UserAgent string

	// PassRequestHeaders identifies HTTP headers to pass from inbound
	// requests to the proxied server.
	PassRequestHeaders []string

	// PassResponseHeaders identifies HTTP headers to pass from server responses to the proxy client.
	// If nil, a default set of headers is passed: Cache-Control, Last-Modified, Expires, Etag, Link.
	PassResponseHeaders []string

	// MinimumCacheDuration is the minimum duration to cache remote images.
	// This will override cache duration from the remote server.
	MinimumCacheDuration time.Duration

	// ForceCache, when true, forces caching of all images, even if the
	// remote server specifies 'private' or 'no-store' in the cache-control
	// header.
	ForceCache bool

	timeNow time.Time // current time, used for testing
}

// NewProxy constructs a new proxy.  The provided http RoundTripper will be
// used to fetch remote URLs.  If nil is provided, http.DefaultTransport will
// be used.
func NewProxy(transport http.RoundTripper, cache Cache) *Proxy {
	if transport == nil {
		transport, _ = aia.NewTransport()
	}
	if cache == nil {
		cache = NopCache
	}

	proxy := &Proxy{
		Cache: cache,
	}

	client := new(http.Client)
	client.Transport = &httpcache.Transport{
		Transport: &TransformingTransport{
			Transport:     transport,
			CachingClient: client,
			limiter:       make(chan struct{}, runtime.NumCPU()),
			log: func(format string, v ...any) {
				if proxy.Verbose {
					proxy.logf(format, v...)
				}
			},
			updateCacheHeaders: proxy.updateCacheHeaders,
		},
		Cache:               cache,
		MarkCachedResponses: true,
	}

	proxy.Client = client

	return proxy
}

// updateCacheHeaders updates the cache-control headers in the provided headers.
//
// If the cache-control header includes the 'private' directive,
// then 'no-store' is added to the header to prevent caching.
// If p.ForceCache is set, then 'private' and 'no-store' are both ignored and removed.
//
// This method also sets the cache-control max-age value to the maximum of the minimum cache
// duration, the expires header, and the max-age header. It also removes the
// expires header.
func (p *Proxy) updateCacheHeaders(hdr http.Header) {
	cc := tphc.ParseCacheControl(hdr)

	// respect 'private' and 'no-store' directives unless ForceCache is set.
	// The httpcache package ignores the 'private' directive,
	// since it's not intended to be used as a shared cache.
	// imageproxy IS a shared cache, so we enforce the 'private' directive ourself
	// by setting 'no-store', which httpcache does respect.
	if p.ForceCache {
		delete(cc, "private")
		delete(cc, "no-store")
		hdr.Set("Cache-Control", cc.String())
	} else {
		if _, ok := cc["private"]; ok {
			cc["no-store"] = ""
			hdr.Set("Cache-Control", cc.String())
			return
		}
		if _, ok := cc["no-store"]; ok {
			return
		}
	}

	if p.MinimumCacheDuration == 0 {
		return
	}

	var expiresDuration time.Duration
	var maxAgeDuration time.Duration

	if maxAge, ok := cc["max-age"]; ok {
		maxAgeDuration, _ = time.ParseDuration(maxAge + "s")
	}
	if date, err := httpcache.Date(hdr); err == nil {
		if expiresHeader := hdr.Get("Expires"); expiresHeader != "" {
			if expires, err := time.Parse(time.RFC1123, expiresHeader); err == nil {
				expiresDuration = expires.Sub(date)
			}
		}
	}

	maxAge := max(p.MinimumCacheDuration, expiresDuration, maxAgeDuration)
	cc["max-age"] = fmt.Sprintf("%d", int(maxAge.Seconds()))

	hdr.Set("Cache-Control", cc.String())
	hdr.Del("Expires")
}

// ServeHTTP handles incoming requests.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/favicon.ico" {
		return // ignore favicon requests
	}

	if r.URL.Path == "/" || r.URL.Path == "/health-check" {
		fmt.Fprint(w, "OK")
		return
	}

	if r.URL.Path == "/metrics" {
		var h = promhttp.Handler()
		h.ServeHTTP(w, r)
		return
	}

	var h http.Handler = http.HandlerFunc(p.serveImage)
	if p.Timeout > 0 {
		h = tphttp.TimeoutHandler(h, p.Timeout, "Gateway timeout waiting for remote resource.")
	}

	timer := prometheus.NewTimer(metricRequestDuration)
	metricRequestsInFlight.Inc()
	defer func() {
		timer.ObserveDuration()
		metricRequestsInFlight.Dec()
	}()

	h.ServeHTTP(w, r)
}

// serveImage handles incoming requests for proxied images.
func (p *Proxy) serveImage(w http.ResponseWriter, r *http.Request) {
	req, err := NewRequest(r, p.DefaultBaseURL)
	if err != nil {
		msg := fmt.Sprintf("invalid request URL: %v", err)
		p.log(msg)
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	if err := p.allowed(req); err != nil {
		p.logf("%s: %v", err, req)
		http.Error(w, msgNotAllowed, http.StatusForbidden)
		return
	}

	// assign static settings from proxy to req.Options
	req.Options.ScaleUp = p.ScaleUp

	actualReq, _ := http.NewRequest("GET", req.String(), nil)
	if p.UserAgent != "" {
		actualReq.Header.Set("User-Agent", p.UserAgent)
	}
	if len(p.ContentTypes) != 0 {
		actualReq.Header.Set("Accept", strings.Join(p.ContentTypes, ", "))
	}
	if p.IncludeReferer {
		// pass along the referer header from the original request
		copyHeader(actualReq.Header, r.Header, "referer")
	}
	if len(p.PassRequestHeaders) != 0 {
		copyHeader(actualReq.Header, r.Header, p.PassRequestHeaders...)
	}
	if p.FollowRedirects {
		// FollowRedirects is true (default), ensure that the redirected host is allowed
		p.Client.CheckRedirect = func(newreq *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				if p.Verbose {
					p.logf("followed too many redirects (%d).", len(via))
				}
				return errTooManyRedirects
			}
			if hostMatches(p.DenyHosts, newreq.URL) {
				http.Error(w, msgNotAllowedInRedirect, http.StatusForbidden)
				return errNotAllowed
			}
			return nil
		}
	} else {
		// FollowRedirects is false, don't follow redirects
		p.Client.CheckRedirect = func(newreq *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	resp, err := p.doRequestWithRetries(actualReq)
	if err != nil {
		msg := fmt.Sprintf("error fetching remote image: %v", err)
		p.log(msg)
		http.Error(w, msg, http.StatusInternalServerError)
		metricRemoteErrors.Inc()
		return
	}
	// close the original resp.Body, even if we wrap it in a NopCloser below
	defer resp.Body.Close()

	// return early on 404s.  Perhaps handle additional status codes here?
	if resp.StatusCode == http.StatusNotFound {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	cached := resp.Header.Get(httpcache.XFromCache) == "1"
	if p.Verbose {
		p.logf("request: %+v (served from cache: %t)", *actualReq, cached)
	}

	if cached {
		metricServedFromCache.Inc()
	}

	if p.PassResponseHeaders == nil {
		// pass default set of response headers
		copyHeader(w.Header(), resp.Header, "Cache-Control", "Last-Modified", "Expires", "Etag", "Link")
	} else {
		copyHeader(w.Header(), resp.Header, p.PassResponseHeaders...)
	}

	if should304(r, resp) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if contentType == "" || contentType == "application/octet-stream" || contentType == "binary/octet-stream" {
		// try to detect content type
		b := bufio.NewReader(resp.Body)
		resp.Body = io.NopCloser(b)
		contentType = peekContentType(b)
	}
	if resp.ContentLength != 0 && !contentTypeMatches(p.ContentTypes, contentType) {
		p.logf("content-type not allowed: %q", contentType)
		http.Error(w, msgNotAllowed, http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", contentType)

	copyHeader(w.Header(), resp.Header, "Content-Length")

	// Enable CORS for 3rd party applications
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Add a Content-Security-Policy to prevent stored-XSS attacks via SVG files
	w.Header().Set("Content-Security-Policy", "script-src 'none'")

	// Disable Content-Type sniffing
	w.Header().Set("X-Content-Type-Options", "nosniff")

	// Block potential XSS attacks especially in legacy browsers which do not support CSP
	w.Header().Set("X-XSS-Protection", "1; mode=block")

	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		p.logf("error copying response: %v", err)
	}
}

// peekContentType peeks at the first 512 bytes of p, and attempts to detect
// the content type.  Returns empty string if error occurs.
func peekContentType(p *bufio.Reader) string {
	byt, err := p.Peek(512)
	if err != nil && !errors.Is(err, bufio.ErrBufferFull) && !errors.Is(err, io.EOF) {
		return ""
	}
	return http.DetectContentType(byt)
}

// copyHeader copies values for specified headers from src to dst, adding to
// any existing values with the same header name.
func copyHeader(dst, src http.Header, headerNames ...string) {
	for _, name := range headerNames {
		k := http.CanonicalHeaderKey(name)
		for _, v := range src[k] {
			dst.Add(k, v)
		}
	}
}

var (
	errReferrer         = errors.New("request does not contain an allowed referrer")
	errDeniedHost       = errors.New("request contains a denied host")
	errNotAllowed       = errors.New("request does not contain an allowed host or valid signature")
	errTooManyRedirects = errors.New("too many redirects")
	errNotValid         = errors.New("request is no longer valid")

	msgNotAllowed           = "requested URL is not allowed"
	msgNotAllowedInRedirect = "requested URL in redirect is not allowed"
)

func (p *Proxy) now() time.Time {
	if !p.timeNow.IsZero() {
		return p.timeNow
	}
	return time.Now()
}

// allowed determines whether the specified request contains an allowed
// referrer, host, and signature.  It returns an error if the request is not
// allowed or not valid any longer.
func (p *Proxy) allowed(r *Request) error {
	if !r.Options.ValidUntil.IsZero() {
		if !p.now().Before(r.Options.ValidUntil) {
			return errNotValid
		}
	}

	if len(p.Referrers) > 0 && !referrerMatches(p.Referrers, r.Original) {
		return errReferrer
	}

	if hostMatches(p.DenyHosts, r.URL) {
		return errDeniedHost
	}

	if len(p.AllowHosts) == 0 && len(p.SignatureKeys) == 0 {
		return nil // no allowed hosts or signature key, all requests accepted
	}

	if len(p.AllowHosts) > 0 && hostMatches(p.AllowHosts, r.URL) {
		return nil
	}

	for _, signatureKey := range p.SignatureKeys {
		if len(signatureKey) > 0 && validSignature(signatureKey, r) {
			return nil
		}
	}

	return errNotAllowed
}

// contentTypeMatches returns whether contentType matches one of the allowed patterns.
func contentTypeMatches(patterns []string, contentType string) bool {
	if len(patterns) == 0 {
		return true
	}

	for _, pattern := range patterns {
		if ok, err := path.Match(pattern, contentType); ok && err == nil {
			return true
		}
	}

	return false
}

// hostMatches returns whether the host in u matches one of hosts.
func hostMatches(hosts []string, u *url.URL) bool {
	for _, host := range hosts {
		if u.Hostname() == host {
			return true
		}
		if strings.HasPrefix(host, "*.") && strings.HasSuffix(u.Hostname(), host[2:]) {
			return true
		}
		// Checks whether the host in u is an IP
		if ip := net.ParseIP(u.Hostname()); ip != nil {
			// Checks whether our current host is a CIDR
			if _, ipnet, err := net.ParseCIDR(host); err == nil {
				// Checks if our host contains the IP in u
				if ipnet.Contains(ip) {
					return true
				}
			}
		}
	}

	return false
}

// returns whether the referrer from the request is in the host list.
func referrerMatches(hosts []string, r *http.Request) bool {
	u, err := url.Parse(r.Header.Get("Referer"))
	if err != nil { // malformed or blank header, just deny
		return false
	}

	return hostMatches(hosts, u)
}

// validSignature returns whether the request signature is valid.
func validSignature(key []byte, r *Request) bool {
	sig := r.Options.Signature
	if m := len(sig) % 4; m != 0 { // add padding if missing
		sig += strings.Repeat("=", 4-m)
	}

	got, err := base64.URLEncoding.DecodeString(sig)
	if err != nil {
		log.Printf("error base64 decoding signature %q", r.Options.Signature)
		return false
	}

	// check signature with URL only
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(r.URL.String()))
	want := mac.Sum(nil)
	if hmac.Equal(got, want) {
		return true
	}

	// check signature with URL and options
	u, opt := *r.URL, r.Options // make copies
	opt.Signature = ""
	u.Fragment = opt.String()

	mac = hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(u.String()))
	want = mac.Sum(nil)
	return hmac.Equal(got, want)
}

// should304 returns whether we should send a 304 Not Modified in response to
// req, based on the response resp.  This is determined using the last modified
// time and the entity tag of resp.
func should304(req *http.Request, resp *http.Response) bool {
	// TODO(willnorris): if-none-match header can be a comma separated list
	// of multiple tags to be matched, or the special value "*" which
	// matches all etags
	etag := resp.Header.Get("Etag")
	if etag != "" && etag == req.Header.Get("If-None-Match") {
		return true
	}

	lastModified, err := time.Parse(time.RFC1123, resp.Header.Get("Last-Modified"))
	if err != nil {
		return false
	}
	ifModSince, err := time.Parse(time.RFC1123, req.Header.Get("If-Modified-Since"))
	if err != nil {
		return false
	}
	if lastModified.Before(ifModSince) || lastModified.Equal(ifModSince) {
		return true
	}

	return false
}

func (p *Proxy) log(v ...any) {
	if p.Logger != nil {
		p.Logger.Print(v...)
	} else {
		log.Print(v...)
	}
}

func (p *Proxy) logf(format string, v ...any) {
	if p.Logger != nil {
		p.Logger.Printf(format, v...)
	} else {
		log.Printf(format, v...)
	}
}

// TransformingTransport is an implementation of http.RoundTripper that
// optionally transforms images using the options specified in the request URL
// fragment.
type TransformingTransport struct {
	// Transport is the underlying http.RoundTripper used to satisfy
	// non-transform requests (those that do not include a URL fragment).
	Transport http.RoundTripper

	// CachingClient is used to fetch images to be resized.  This client is
	// used rather than Transport directly in order to ensure that
	// responses are properly cached.
	CachingClient *http.Client

	// limiter limits the number of concurrent transformations being processed.
	limiter chan struct{}

	log func(format string, v ...any)

	updateCacheHeaders func(hdr http.Header)
}

// RoundTrip implements the http.RoundTripper interface.
func (t *TransformingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Fragment == "" {
		// normal requests pass through
		if t.log != nil {
			t.log("fetching remote URL: %v", req.URL)
		}
		resp, err := t.Transport.RoundTrip(req)
		if err == nil && t.updateCacheHeaders != nil {
			t.updateCacheHeaders(resp.Header)
		}
		return resp, err
	}

	f := req.URL.Fragment
	req.URL.Fragment = ""
	resp, err := t.CachingClient.Do(req)
	req.URL.Fragment = f
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if should304(req, resp) {
		// bare 304 response, full response will be used from cache
		return &http.Response{
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Status:     fmt.Sprintf("%d %s", http.StatusNotModified, http.StatusText(http.StatusNotModified)),
			StatusCode: http.StatusNotModified,
			Body:       http.NoBody,
		}, nil
	}

	// enforce limiter after we've checked if we can early return a 304 response,
	// but before we read the response body and perform transformations.
	if t.limiter != nil {
		t.limiter <- struct{}{}
		defer func() {
			<-t.limiter
		}()
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	opt := ParseOptions(req.URL.Fragment)

	img, err := Transform(b, opt)
	if err != nil {
		log.Printf("error transforming image %s: %v", req.URL.String(), err)
		img = b
	}

	// replay response with transformed image and updated content length
	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, "%s %s\n", resp.Proto, resp.Status)
	if err := resp.Header.WriteSubset(buf, map[string]bool{
		"Content-Length": true,
		// exclude Content-Type header if the format may have changed during transformation
		"Content-Type": opt.Format != "" || resp.Header.Get("Content-Type") == "image/webp" || resp.Header.Get("Content-Type") == "image/tiff",
	}); err != nil {
		t.log("error copying headers: %v", err)
	}
	fmt.Fprintf(buf, "Content-Length: %d\n\n", len(img))
	buf.Write(img)

	return http.ReadResponse(bufio.NewReader(buf), req)
}

// doRequestWithRetries handles retries for HTTP requests.
func (p *Proxy) doRequestWithRetries(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(retryInterval * time.Duration(attempt))
			p.logf("Retry attempt %d for %s", attempt, req.URL)
		}

		resp, err = p.Client.Do(req)
		if err != nil {
			continue
		}

		// Retry on server errors (500s) and specific client errors
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}

		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			continue
		}

		// Don't retry on other client errors or redirects
		return resp, nil
	}

	if err != nil {
		return nil, err
	}
	return resp, nil
}
