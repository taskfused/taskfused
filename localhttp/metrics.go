// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0
package localhttp

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/taskfused/taskfused"
)

// labelValue escapes a Prometheus label value. Namespaces are any non-NUL
// bytes, but the text format requires UTF-8, so each byte of an invalid
// sequence is written as %XX, and a literal % as %25; distinct namespaces stay
// distinct labels.
func labelValue(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size <= 1:
			fmt.Fprintf(&b, "%%%02X", s[i])
		case r == '%':
			b.WriteString("%25")
		case r == '\\':
			b.WriteString(`\\`)
		case r == '"':
			b.WriteString(`\"`)
		case r == '\n':
			b.WriteString(`\n`)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// metricsPage writes Store.Metrics in the Prometheus text format (0.0.4).
func metricsPage(store *taskfused.Store) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		m, err := store.Metrics(r.Context())
		if err != nil {
			return err
		}
		var b bytes.Buffer
		family := func(name, help string) {
			fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
		}
		family("taskfused_build_info", "TaskFused version and schema of the serving binary.")
		fmt.Fprintf(&b, "taskfused_build_info{version=\"%s\",schema=\"%d\"} 1\n", labelValue(taskfused.Version), taskfused.SchemaVersion)
		family("taskfused_operations", "Operations kept, by namespace and state. Queue depth is queued plus retry_wait.")
		for _, ns := range m.Namespaces {
			for _, state := range taskfused.OperationStates {
				fmt.Fprintf(&b, "taskfused_operations{namespace=\"%s\",state=\"%s\"} %d\n", labelValue(ns.Namespace), state, ns.Operations[state])
			}
		}
		family("taskfused_attempts", "Attempts kept, by namespace and state; expired attempts are lease expiries. Drops when identities expire.")
		for _, ns := range m.Namespaces {
			for _, state := range taskfused.AttemptStates {
				fmt.Fprintf(&b, "taskfused_attempts{namespace=\"%s\",state=\"%s\"} %d\n", labelValue(ns.Namespace), state, ns.Attempts[state])
			}
		}
		family("taskfused_oldest_queued_age_seconds", "Seconds since the oldest queued operation was submitted; 0 when none is queued.")
		for _, ns := range m.Namespaces {
			age := 0.0
			if ns.OldestQueued != nil {
				if d := m.ReadAt.Sub(*ns.OldestQueued).Seconds(); d > 0 {
					age = d
				}
			}
			fmt.Fprintf(&b, "taskfused_oldest_queued_age_seconds{namespace=\"%s\"} %s\n", labelValue(ns.Namespace), strconv.FormatFloat(age, 'f', -1, 64))
		}
		family("taskfused_limit_max_operations", "Admission limit on kept identities; 0 is unlimited.")
		for _, ns := range m.Namespaces {
			fmt.Fprintf(&b, "taskfused_limit_max_operations{namespace=\"%s\"} %d\n", labelValue(ns.Namespace), ns.Limits.MaxOperations)
		}
		family("taskfused_limit_max_active", "Admission limit on queued, running and retry_wait operations; 0 is unlimited.")
		for _, ns := range m.Namespaces {
			fmt.Fprintf(&b, "taskfused_limit_max_active{namespace=\"%s\"} %d\n", labelValue(ns.Namespace), ns.Limits.MaxActive)
		}
		family("taskfused_identity_retention_seconds", "How long finished operations keep their identity; 0 keeps them forever.")
		for _, ns := range m.Namespaces {
			fmt.Fprintf(&b, "taskfused_identity_retention_seconds{namespace=\"%s\"} %s\n", labelValue(ns.Namespace), strconv.FormatFloat(ns.Limits.IdentityRetention.Seconds(), 'f', -1, 64))
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write(b.Bytes())
		return nil
	}
}
