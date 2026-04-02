package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	pprofhttp "net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/gechr/clog"
)

const (
	envPprofAddr       = "PRL_PPROF_ADDR"
	defaultPprofAddr   = "127.0.0.1:6060"
	pprofReadHeaderTTL = 5 * time.Second
	pprofShutdownTTL   = 1 * time.Second
)

func maybeStartPprofServer() (func(), string, error) {
	addr := pprofListenAddr(os.Getenv(envPprofAddr))
	if addr == "" {
		return nil, "", nil
	}

	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("starting pprof listener on %q: %w", addr, err)
	}

	server := &http.Server{
		Handler:           newPprofMux(),
		ReadHeaderTimeout: pprofReadHeaderTTL,
	}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(
			serveErr,
			http.ErrServerClosed,
		) {
			clog.Warn().
				Err(serveErr).
				Str("addr", listener.Addr().String()).
				Msg("pprof server stopped")
		}
	}()

	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), pprofShutdownTTL)
		defer cancel()
		_ = server.Shutdown(ctx)
	}
	return stop, listener.Addr().String(), nil
}

func pprofListenAddr(value string) string {
	switch v := strings.TrimSpace(strings.ToLower(value)); v {
	case "", "0", "false", "off", "no":
		return ""
	case "1", "true", "on", "yes":
		return defaultPprofAddr
	default:
		return strings.TrimSpace(value)
	}
}

func newPprofMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprofhttp.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprofhttp.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprofhttp.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprofhttp.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprofhttp.Trace)
	return mux
}
