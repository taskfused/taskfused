// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0
package localhttp

import (
	"bytes"
	"html/template"
	"net/http"

	"github.com/taskfused/taskfused"
)

var page = template.Must(template.New("status").Parse(`<!doctype html>
<html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>TaskFused local status</title>
<style>body{font:16px system-ui,sans-serif;color:#182536;background:#f4f6f8;max-width:850px;margin:40px auto;padding:0 20px}h1{letter-spacing:-1px}section{background:white;border:1px solid #d6dfe8;border-radius:10px;padding:22px;margin:20px 0}label{display:block;margin:12px 0}input{font:inherit;padding:8px;box-sizing:border-box;max-width:100%;width:420px;border:1px solid #98a7b5;border-radius:4px}button{font:inherit;background:#164a72;color:white;padding:9px 20px;border:0;border-radius:4px}dt{color:#526173}dd{margin:2px 0 14px;overflow-wrap:anywhere}pre{white-space:pre-wrap;overflow-wrap:anywhere;font:13px ui-monospace,monospace}small{color:#526173}</style>
<h1>TaskFused</h1><p>Local execution status</p>
<form method="get" action="/"><label>Namespace <input name="namespace" value="{{.Namespace}}" required maxlength="256"></label><label>Operation ID <input name="id" value="{{.ID}}" placeholder="Optional: op_…"></label><button type="submit">Inspect</button></form>
{{if .Usage}}<section><h2>Namespace usage</h2><dl><dt>Operations</dt><dd>{{.Usage.Operations}}</dd><dt>Active operations</dt><dd>{{.Usage.Active}}</dd><dt>Admission limits: identities / active</dt><dd>{{.Usage.Limits.MaxOperations}} / {{.Usage.Limits.MaxActive}} <small>(0 means unlimited)</small></dd><dt>Retained input / result bytes</dt><dd>{{.Usage.InputBytes}} / {{.Usage.ResultBytes}}</dd><dt>Pruned results</dt><dd>{{.Usage.PrunedResults}}</dd><dt>Attempts / events / retry actions</dt><dd>{{.Usage.Attempts}} / {{.Usage.Events}} / {{.Usage.RetryActions}}</dd></dl></section>{{end}}
{{if .Operation}}<section><h2>Operation</h2><dl><dt>ID</dt><dd>{{.Operation.ID}}</dd><dt>Handler / version</dt><dd>{{.Operation.Handler}} / {{.Operation.Version}}</dd><dt>Key</dt><dd>{{.Operation.Key}}</dd><dt>State</dt><dd>{{.Operation.State}}</dd><dt>Attempts / lifetime limit</dt><dd>{{.Operation.AttemptCount}} / {{.Operation.AttemptLimit}}</dd><dt>Result</dt><dd>{{if .Operation.ResultPruned}}Pruned — succeeded; result bytes were removed.{{else if eq .Operation.State "succeeded"}}Available ({{len .Operation.Result}} bytes).{{else}}No successful result.{{end}}</dd>{{if .Operation.LastError}}<dt>Last error</dt><dd>{{.Operation.LastError}}</dd>{{end}}</dl></section><section><h2>Event history</h2><pre>{{.Events}}</pre><small>First 50 events. Use the event API to read later pages.</small></section>{{end}}
<p><small>Read-only inspection. Reload to refresh. Execution, cancellation, retries and retention are controlled separately.</small></p></html>`))

func statusPage(store *taskfused.Store) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		data := struct {
			Namespace, ID string
			Usage         *taskfused.Usage
			Operation     *taskfused.Operation
			Events        string
		}{Namespace: r.URL.Query().Get("namespace"), ID: r.URL.Query().Get("id")}
		if data.ID != "" && data.Namespace == "" {
			return bad("namespace is required for operation inspection")
		}
		if data.Namespace != "" {
			usage, err := store.Usage(r.Context(), data.Namespace)
			if err != nil {
				return err
			}
			data.Usage = &usage
			if data.ID != "" {
				op, err := store.Get(r.Context(), data.Namespace, data.ID)
				if err != nil {
					return err
				}
				data.Operation = &op
				events, err := store.Events(r.Context(), data.Namespace, data.ID, taskfused.EventQuery{})
				if err != nil {
					return err
				}
				for _, event := range events.Events {
					data.Events += event.OccurredAt.Format("2006-01-02 15:04:05Z07:00") + "  " + event.Kind + "\n"
				}
			}
		}
		var output bytes.Buffer
		if err := page.Execute(&output, data); err != nil {
			return err
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(output.Bytes())
		return nil
	}
}
