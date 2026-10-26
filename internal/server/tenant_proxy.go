package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

type secureProxy struct {
	lease  *accessLease
	ctx    context.Context
	cancel context.CancelFunc
	dev    *Device
	target *url.URL
	host   string
	id     string
	cookie string
}
type proxyClaim struct {
	proxy   *secureProxy
	path    string
	expires time.Time
}

var secureProxies sync.Map
var secureClaims sync.Map

func validWebTargets(targets []string) bool {
	if len(targets) > 16 {
		return false
	}
	for _, s := range targets {
		u, e := url.Parse(s)
		if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") || net.ParseIP(u.Hostname()).To4() == nil {
			return false
		}
		if u.Port() != "" {
			p, e := strconv.Atoi(u.Port())
			if e != nil || p < 1 || p > 65535 {
				return false
			}
		}
	}
	return true
}
func (a *APIServer) secureProxyRedirect(c *gin.Context) {
	auth := a.srv.tenant
	p, d, err := auth.access(tenantSession(c), c.Param("devid"), true)
	if err != nil {
		c.Status(403)
		return
	}
	target, err := url.Parse(c.Param("proto") + "://" + c.Param("addr"))
	if err != nil || !validWebTargets([]string{target.String()}) {
		c.Status(400)
		return
	}
	allowed := false
	for _, s := range d.WebTargets {
		u, _ := url.Parse(s)
		if u.Scheme == target.Scheme && u.Host == target.Host {
			allowed = true
		}
	}
	if !allowed {
		c.Status(403)
		return
	}
	root, err := url.Parse(a.srv.cfg.HttpProxyRedirURL)
	if err != nil || root.Scheme != "https" || root.Host == "" || root.User != nil {
		c.JSON(503, gin.H{"error": "isolated HTTPS proxy origin is not configured"})
		return
	}
	console := strings.Split(c.Request.Host, ":")[0]
	if root.Hostname() == console || net.ParseIP(root.Hostname()) != nil {
		c.JSON(503, gin.H{"error": "proxy requires a separate wildcard DNS hostname"})
		return
	}
	dev := a.srv.GetDevice(p.Tenant, d.ID)
	if dev == nil {
		c.Status(503)
		return
	}
	id := randomID()[:32]
	secret := randomID()
	cookieHash := digest(secret)
	host := id + "." + root.Hostname()
	if root.Port() != "" {
		host = net.JoinHostPort(host, root.Port())
	}
	ctx, cancel := context.WithCancel(dev.ctx)
	ses := &secureProxy{ctx: ctx, cancel: cancel, dev: dev, target: target, host: host, id: id, cookie: secret}
	lease, err := auth.attach(p.Session, d.ID, func() { cancel(); secureProxies.Delete(cookieHash) })
	if err != nil {
		cancel()
		c.Status(403)
		return
	}
	ses.lease = lease
	secureProxies.Store(cookieHash, ses)
	go func() {
		defer auth.detach(lease)
		defer secureProxies.Delete(cookieHash)
		timer := time.NewTimer(15 * time.Minute)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		cancel()
	}()
	path := c.Param("path")
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		path = "/"
	}
	ticket := randomID()
	secureClaims.Store(digest(ticket), proxyClaim{proxy: ses, path: path, expires: time.Now().Add(30 * time.Second)})
	time.AfterFunc(30*time.Second, func() { secureClaims.Delete(digest(ticket)) })
	c.Header("Referrer-Policy", "no-referrer")
	c.Redirect(302, "https://"+host+"/_rtty/claim?ticket="+ticket)
}
func (srv *RttyServer) listenSecureProxy() {
	if srv.cfg.AddrHttpProxy == "" {
		return
	}
	server := &http.Server{Addr: srv.cfg.AddrHttpProxy, ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.URL.Path == "/_rtty/claim" {
			ticket := r.URL.Query().Get("ticket")
			v, ok := secureClaims.LoadAndDelete(digest(ticket))
			if !ok {
				http.Error(w, "expired ticket", 401)
				return
			}
			claim := v.(proxyClaim)
			if r.Host != claim.proxy.host || time.Now().After(claim.expires) || !claim.proxy.lease.valid() {
				http.Error(w, "unauthorized", 401)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "rt_proxy", Value: claim.proxy.cookie, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 900})
			http.Redirect(w, r, claim.path, 302)
			return
		}
		cookie, err := r.Cookie("rt_proxy")
		if err != nil {
			http.Error(w, "unauthorized", 401)
			return
		}
		v, ok := secureProxies.Load(digest(cookie.Value))
		if !ok {
			http.Error(w, "unauthorized", 401)
			return
		}
		ses := v.(*secureProxy)
		if r.Host != ses.host || !ses.lease.valid() {
			http.Error(w, "unauthorized", 401)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "https://"+r.Host {
			http.Error(w, "forbidden", 403)
			return
		}
		proxy := httputil.NewSingleHostReverseProxy(ses.target)
		director := proxy.Director
		proxy.Director = func(req *http.Request) {
			director(req)
			req.Host = ses.target.Host
			req.Header.Del("Authorization")
			cookies := req.Cookies()
			req.Header.Del("Cookie")
			for _, cookie := range cookies {
				if cookie.Name != "rt_proxy" && cookie.Name != "rt_sid" && cookie.Name != tenantSessionCookie {
					req.AddCookie(cookie)
				}
			}
			req.Header.Del("Referer")
			req.Header.Del("X-Forwarded-For")
		}
		proxy.ModifyResponse = func(resp *http.Response) error {
			resp.Header.Del("Access-Control-Allow-Origin")
			resp.Header.Del("Access-Control-Allow-Credentials")
			cookies := resp.Cookies()
			resp.Header.Del("Set-Cookie")
			for _, cookie := range cookies {
				if cookie.Name != "rt_proxy" && cookie.Name != "rt_sid" && cookie.Name != tenantSessionCookie {
					cookie.Domain = ""
					cookie.Secure = true
					resp.Header.Add("Set-Cookie", cookie.String())
				}
			}
			if loc, err := resp.Location(); err == nil && loc.Host == ses.target.Host {
				loc.Host = ses.host
				loc.Scheme = "https"
				resp.Header.Set("Location", loc.String())
			}
			return nil
		}
		proxy.Transport = &deviceTransport{ses: ses}
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) { http.Error(w, "device unavailable", 502) }
		proxy.ServeHTTP(w, r)
	})}
	if err := server.ListenAndServeTLS(srv.cfg.SslCert, srv.cfg.SslKey); err != nil {
		log.Error().Err(err).Msg("secure device proxy stopped")
	}
}

type deviceTransport struct{ ses *secureProxy }
type forwardWriter struct {
	ses  *secureProxy
	src  [18]byte
	dest []byte
}

func (f *forwardWriter) Write(p []byte) (int, error) {
	if !f.ses.lease.valid() {
		return 0, errDenied
	}
	total := len(p)
	for len(p) > 0 {
		n := len(p)
		if n > 16384 {
			n = 16384
		}
		sendHttpReq(f.ses.dev, f.ses.target.Scheme == "https", f.src[:], f.dest, p[:n])
		p = p[n:]
	}
	return total, nil
}

type proxyBody struct {
	io.Reader
	writer io.Writer
	close  func()
}

func (b *proxyBody) Close() error                { b.close(); return nil }
func (b *proxyBody) Write(p []byte) (int, error) { return b.writer.Write(p) }
func (t *deviceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s := t.ses
	if !s.lease.valid() {
		return nil, errDenied
	}
	var src [18]byte
	if _, e := rand.Read(src[:]); e != nil {
		return nil, e
	}
	dest, _ := genDestAddrAndHost(s.target.Host, s.target.Scheme == "https")
	if dest == nil {
		return nil, errors.New("invalid destination")
	}
	remote, local := net.Pipe()
	s.dev.https.Store(src, remote)
	remote.SetWriteDeadline(time.Now().Add(60 * time.Second))
	local.SetReadDeadline(time.Now().Add(60 * time.Second))
	once := sync.Once{}
	done := make(chan struct{})
	closeConn := func() {
		once.Do(func() {
			close(done)
			s.dev.https.Delete(src)
			remote.Close()
			local.Close()
			sendHttpReq(s.dev, s.target.Scheme == "https", src[:], dest, nil)
		})
	}
	go func() {
		select {
		case <-s.ctx.Done():
		case <-req.Context().Done():
		case <-done:
			return
		}
		closeConn()
	}()
	writer := &forwardWriter{ses: s, src: src, dest: dest}
	go func() {
		if err := req.Write(writer); err != nil {
			closeConn()
		}
	}()
	reader := bufio.NewReader(local)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		closeConn()
		return nil, err
	}
	if resp.StatusCode == 101 {
		resp.Body = &proxyBody{Reader: reader, writer: writer, close: closeConn}
	} else {
		body := resp.Body
		resp.Body = &proxyBody{Reader: body, writer: writer, close: func() { body.Close(); closeConn() }}
	}
	return resp, nil
}
