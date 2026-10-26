/* SPDX-License-Identifier: MIT */
/*
 * Author: Jianhui Zhao <zhaojh329@gmail.com>
 */

package server

import (
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

type RttyServer struct {
	mu            sync.RWMutex
	groups        sync.Map
	cfg           Config
	httpProxyPort int
	shares        *shareManager
	tenant        *TenantAuth
	pprofToken    string
}

type Config struct {
	TenantDB              string
	BootstrapPasswordFile string
	AddrDev               string
	AddrUser              string
	AddrHttpProxy         string
	ShareBindHost         string
	SharePublicHost       string
	SharePortStart        int
	SharePortEnd          int
	ShareHostKey          string

	HttpProxyRedirURL    string
	HttpProxyRedirDomain string

	Token        string
	DevHookUrl   string
	UserHookUrl  string
	LocalAuth    bool
	Password     string
	AllowOrigins bool

	PprofAddr      string
	PprofTokenFile string

	SslCert string
	SslKey  string
	CaCert  string
}

func New(cfg Config) *RttyServer {
	if cfg.SharePortStart == 0 {
		cfg.SharePortStart = 20000
	}
	if cfg.SharePortEnd == 0 {
		cfg.SharePortEnd = 21000
	}
	srv := &RttyServer{cfg: cfg}
	srv.shares = &shareManager{srv: srv, entries: make(map[string]*Share)}
	return srv
}

type DeviceGroup struct {
	devices sync.Map
	count   atomic.Int32
}

func (srv *RttyServer) Run() error {
	if srv.cfg.PprofAddr != "" {
		addr, err := pprofListenAddress(srv.cfg.PprofAddr)
		if err != nil {
			return err
		}
		srv.cfg.PprofAddr = addr
		token, err := readPprofToken(srv.cfg.PprofTokenFile)
		if err != nil {
			return err
		}
		srv.pprofToken = token
	}
	if srv.cfg.TenantDB != "" {
		if srv.cfg.SslCert == "" || srv.cfg.SslKey == "" {
			return fmt.Errorf("tenant mode requires TLS certificate and key")
		}
		var err error
		srv.tenant, err = openTenantAuth(srv)
		if err != nil {
			return err
		}
	}

	if srv.cfg.PprofAddr != "" {
		go srv.ListenPprof()
	}

	go srv.ListenDevices()
	go srv.ListenHttpProxy()

	return srv.ListenAPI()
}

func (srv *RttyServer) ListenPprof() {
	listenAddr, err := pprofListenAddress(srv.cfg.PprofAddr)
	if err != nil {
		log.Error().Err(err).Msg("Invalid pprof address")
		return
	}
	token := srv.pprofToken
	if token == "" {
		token, err = readPprofToken(srv.cfg.PprofTokenFile)
		if err != nil {
			log.Error().Err(err).Msg("Invalid pprof token file")
			return
		}
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to start pprof server")
		return
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	log.Info().Msgf("Starting pprof server on: %s", addr)

	log.Info().Msgf("Access pprof at: http://%s/debug/pprof/", addr.String())
	err = (&http.Server{Handler: newPprofHandler(token), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}).Serve(ln)
	if err != nil {
		log.Error().Err(err).Msgf("pprof server failed")
	}
}

func readPprofToken(path string) (string, error) {
	secret, err := readPrivateSecretFile(path)
	if err != nil {
		return "", fmt.Errorf("pprof requires a private pprof-token-file: %w", err)
	}
	token := strings.TrimSpace(string(secret))
	if len(token) < 32 || len(token) > 256 {
		return "", fmt.Errorf("pprof token must be 32-256 bytes")
	}
	return token, nil
}

func newPprofHandler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	want := digest("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		got := digest(r.Header.Get("Authorization"))
		if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="pprof"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func pprofListenAddress(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("pprof requires an explicit loopback address: %w", err)
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	n, err := strconv.Atoi(port)
	if ip == nil || !ip.IsLoopback() || err != nil || n < 0 || n > 65535 {
		return "", fmt.Errorf("pprof must bind to a loopback IP and numeric port")
	}
	return net.JoinHostPort(ip.String(), port), nil
}

func (srv *RttyServer) GetDevice(group, id string) *Device {
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	g := srv.GetGroup(group, false)
	if g == nil {
		return nil
	}

	if v, ok := g.devices.Load(id); ok {
		return v.(*Device)
	}

	return nil
}

func (srv *RttyServer) AddDevice(dev *Device) bool {
	srv.mu.Lock()
	defer srv.mu.Unlock()

	g := srv.GetGroup(dev.group, true)

	if _, loaded := g.devices.LoadOrStore(dev.id, dev); loaded {
		return false
	}

	g.count.Add(1)

	return true
}

func (srv *RttyServer) DelDevice(dev *Device) {
	srv.shares.closeDevice(dev)
	srv.mu.Lock()
	defer srv.mu.Unlock()

	g := srv.GetGroup(dev.group, false)
	if g == nil {
		return
	}

	if deleted := g.devices.CompareAndDelete(dev.id, dev); deleted {
		if g.count.Add(-1) == 0 {
			srv.groups.Delete(dev.group)
		}
	}
}

func (srv *RttyServer) GetGroup(group string, create bool) *DeviceGroup {
	if create {
		val, _ := srv.groups.LoadOrStore(group, &DeviceGroup{})
		return val.(*DeviceGroup)
	} else {
		val, ok := srv.groups.Load(group)
		if !ok {
			return nil
		}
		return val.(*DeviceGroup)
	}
}
