// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0

package localhttp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taskfused/taskfused"
)

func TestListenerAndAuthorityValidation(t *testing.T) {
	for _, address := range []string{"localhost:8080", ":8080", "0.0.0.0:8080", "[::]:8080", "192.0.2.1:8080", "127.0.0.1:-1", "127.0.0.1:65536", "127.0.0.1:http"} {
		if l, err := Listen(address); err == nil {
			l.Close()
			t.Fatalf("accepted %q", address)
		}
	}
	s, err := taskfused.Open(context.Background(), filepath.Join(t.TempDir(), "listener.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, authority := range []string{"localhost:8080", "127.0.0.1:0", "127.0.0.1", "[::]:8080"} {
		if _, err := NewHandler(s, authority); err == nil {
			t.Fatalf("accepted authority %q", authority)
		}
	}
	if _, err := NewHandler(nil, "127.0.0.1:8080"); err == nil {
		t.Fatal("accepted nil store")
	}
	// Verify the serving path also rejects a listener supplied by an embedder.
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := Serve(context.Background(), s, listener); err == nil {
		t.Fatal("served wildcard listener")
	}
}

func TestShutdownCancelsPendingHTTPWaitWithoutCancelingOperation(t *testing.T) {
	s, err := taskfused.Open(context.Background(), filepath.Join(t.TempDir(), "shutdown.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sub, err := s.Submit(context.Background(), taskfused.Request{Namespace: "demo", Handler: "echo", Version: "v1", Key: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, s, listener) }()
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "GET /api/namespaces/demo/operations/%s/wait HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", sub.Operation.ID, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	if _, err := reader.Peek(1); err == nil {
		t.Fatal("pending wait already returned")
	} else if neterr, ok := err.(net.Error); !ok || !neterr.Timeout() {
		t.Fatal(err)
	}
	// Shutdown must cancel the request's base context rather than wait for its 30s
	// application timeout. A generous bound is a liveness check, not a benchmark.
	cancel()
	conn.SetReadDeadline(time.Now().Add(6 * time.Second))
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 503 || !strings.Contains(string(data), "unavailable") {
		t.Fatalf("shutdown response: %d %s", response.StatusCode, data)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("server did not stop")
	}
	op, err := s.Get(context.Background(), "demo", sub.Operation.ID)
	if err != nil || op.State != taskfused.StateQueued {
		t.Fatalf("shutdown changed durable work: %+v %v", op, err)
	}
	if c, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second); err == nil {
		c.Close()
		t.Fatal("listener remained open")
	}
}

func TestIPv6LoopbackServingWhenAvailable(t *testing.T) {
	listener, err := Listen("[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	s, err := taskfused.Open(context.Background(), filepath.Join(t.TempDir(), "ipv6.db"))
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, s, listener) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get("http://" + listener.Addr().String() + "/api/namespaces/demo/usage")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(data), `"operations":0`) {
		t.Fatalf("IPv6 usage: %d %s %v", response.StatusCode, data, err)
	}
}

func TestSlowClientsAreBoundedWithoutWritingOperations(t *testing.T) {
	for _, test := range []struct{ name, request string }{
		{"header", "POST /api/namespaces/demo/operations HTTP/1.1\r\nHost: "},
		{"body", "POST /api/namespaces/demo/operations HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			s, err := taskfused.Open(context.Background(), filepath.Join(t.TempDir(), "slow.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			listener, err := Listen("127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- Serve(ctx, s, listener) }()
			defer func() {
				cancel()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			request := test.request
			if test.name == "body" {
				request = fmt.Sprintf(request, listener.Addr().String())
			}
			if _, err := io.WriteString(conn, request); err != nil {
				t.Fatal(err)
			}
			conn.SetReadDeadline(time.Now().Add(7 * time.Second))
			response, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				if neterr, ok := err.(net.Error); ok && neterr.Timeout() {
					t.Fatal("server did not bound stalled client")
				}
				if err != io.EOF && err != io.ErrUnexpectedEOF {
					t.Fatal(err)
				}
			} else {
				response.Body.Close()
				if response.StatusCode < 400 || response.StatusCode >= 500 {
					t.Fatalf("stalled request HTTP%d", response.StatusCode)
				}
			}
			usage, err := s.Usage(context.Background(), "demo")
			if err != nil || usage.Operations != 0 || usage.Events != 0 {
				t.Fatalf("stalled request wrote records: %+v %v", usage, err)
			}
			// A stalled peer must not prevent independent clients from using the service.
			transport := &http.Transport{Proxy: nil}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: time.Second}
			res, err := client.Get("http://" + listener.Addr().String() + "/api/namespaces/demo/usage")
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != 200 {
				t.Fatalf("survivor HTTP%d", res.StatusCode)
			}
		})
	}
}
