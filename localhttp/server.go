// Copyright 2026 TaskFused. All rights reserved.
// Package localhttp exposes TaskFused on one numeric loopback address. It is not
// an authenticated remote service. Local users/processes can access all namespaces.
package localhttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/taskfused/taskfused"
)

// Listen accepts only a literal loopback IP and a port (zero selects a free port).
// Hostnames, wildcard addresses and non-loopback IPs are rejected before binding.
func Listen(address string) (net.Listener, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("listen: literal loopback IP and port required")
	}
	ip := net.ParseIP(host)
	n, err := strconv.Atoi(port)
	if ip == nil || !ip.IsLoopback() || err != nil || n < 0 || n > 65535 {
		return nil, fmt.Errorf("listen: literal loopback IP and port required")
	}
	return net.Listen("tcp", address)
}

// Serve owns and closes listener, but never closes store or starts handlers/workers.
// Canceling ctx cancels in-flight requests, then allows up to five seconds to drain.
func Serve(ctx context.Context, store *taskfused.Store, listener net.Listener) error {
	if listener == nil {
		return fmt.Errorf("serve: TCP loopback listener required")
	}
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() {
		return fmt.Errorf("serve: TCP loopback listener required")
	}
	handler, err := NewHandler(store, listener.Addr().String())
	if err != nil {
		return err
	}
	base, cancel := context.WithCancel(ctx)
	defer cancel()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return base }}
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		drain, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		err := server.Shutdown(drain)
		if err != nil {
			server.Close()
		}
		<-done
		return err
	}
}
