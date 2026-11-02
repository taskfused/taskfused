// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0
package localhttp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/taskfused/taskfused"
)

func TestMetricsEndpoint(t *testing.T) {
	ctx := context.Background()
	s, server := fixture(t)
	submit(t, server, "a", 1)
	submit(t, server, "b", 1)
	submit(t, server, "c", 1)
	if err := s.Complete(ctx, claim(t, s), []byte("ok")); err != nil {
		t.Fatal(err)
	}
	if err := s.Fail(ctx, claim(t, s), "boom"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLimits(ctx, "demo", taskfused.Limits{MaxActive: 7, IdentityRetention: 90 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	// A namespace with a quote and a backslash in its name.
	odd := `we"ird\ns`
	if _, err := s.Submit(ctx, taskfused.Request{Namespace: odd, Handler: "h", Version: "v1", Key: "k"}); err != nil {
		t.Fatal(err)
	}
	res, err := server.Client().Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain; version=0.0.4") {
		t.Fatalf("status %d type %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	for _, want := range []string{
		`taskfused_build_info{version="` + taskfused.Version + `",schema="6"} 1`,
		`taskfused_operations{namespace="demo",state="queued"} 1`,
		`taskfused_operations{namespace="demo",state="succeeded"} 1`,
		`taskfused_operations{namespace="demo",state="failed"} 1`,
		`taskfused_operations{namespace="demo",state="canceled"} 0`,
		`taskfused_attempts{namespace="demo",state="succeeded"} 1`,
		`taskfused_attempts{namespace="demo",state="failed"} 1`,
		`taskfused_attempts{namespace="demo",state="expired"} 0`,
		`taskfused_limit_max_active{namespace="demo"} 7`,
		`taskfused_limit_max_operations{namespace="demo"} 0`,
		`taskfused_identity_retention_seconds{namespace="demo"} 5400`,
		`taskfused_operations{namespace="we\"ird\\ns",state="queued"} 1`,
		"# TYPE taskfused_operations gauge",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if !strings.Contains(text, `taskfused_oldest_queued_age_seconds{namespace="demo"} `) {
		t.Fatalf("no queue age:\n%s", text)
	}
	// Every sample line has a family declared before it.
	declared := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.HasPrefix(line, "# TYPE ") {
			declared[strings.Fields(line)[2]] = true
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		name := line[:strings.IndexAny(line, "{ ")]
		if !declared[name] {
			t.Fatalf("undeclared sample %q", line)
		}
	}
	// Browsers from other origins are refused, as on every route.
	req, _ := http.NewRequest("GET", server.URL+"/metrics", nil)
	req.Header.Set("Origin", "https://example.com")
	if res, err := server.Client().Do(req); err != nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin metrics: %v %v", res, err)
	}
	call(t, server, "POST", "/metrics", map[string]any{}, 405)
}

// Lease expiries show up as expired attempts.
func TestMetricsCountsLeaseExpiries(t *testing.T) {
	ctx := context.Background()
	s, server := fixture(t)
	submit(t, server, "slow", 3)
	claim(t, s)
	time.Sleep(1100 * time.Millisecond) // the claim's one-second lease runs out
	claim(t, s)
	m, err := s.Metrics(ctx)
	if err != nil || len(m.Namespaces) != 1 {
		t.Fatalf("%+v %v", m, err)
	}
	if got := m.Namespaces[0].Attempts["expired"]; got != 1 {
		t.Fatalf("expired attempts %d: %+v", got, m.Namespaces[0])
	}
	if got := m.Namespaces[0].Operations["running"]; got != 1 {
		t.Fatalf("running %d", got)
	}
}

func TestLabelValueKeepsNamespacesDistinct(t *testing.T) {
	cases := map[string]string{
		"plain":       "plain",
		"a\"b\\c\nd":  `a\"b\\c\nd`,
		"50%":         "50%25",
		"bad\xffbyte": "bad%FFbyte",
		"bad\xfebyte": "bad%FEbyte",
		"ünïcode":     "ünïcode",
	}
	for in, want := range cases {
		if got := labelValue(in); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
	if labelValue("x\xff") == labelValue("x\xfe") || labelValue("x%FF") == labelValue("x\xff") {
		t.Fatal("distinct namespaces share a label")
	}
}
