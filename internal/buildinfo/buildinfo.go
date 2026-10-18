// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0

// Package buildinfo reports executable provenance without opening a database.
package buildinfo

import (
	"runtime"
	"runtime/debug"

	"github.com/taskfused/taskfused"
)

// Info's version is taskfused.Version. Its revision and modified flag come
// from the Go VCS metadata stamped into builds made from a git checkout.

type Record struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
	Modified bool   `json:"modified"`
	Go       string `json:"go"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	CGO      bool   `json:"cgo"`
	Schema   int    `json:"schema"`
}

func Info() Record {
	result := Record{Version: taskfused.Version, Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, Schema: taskfused.SchemaVersion}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if result.Revision == "" {
					result.Revision = setting.Value
				}
			case "vcs.modified":
				result.Modified = setting.Value == "true"
			case "CGO_ENABLED":
				result.CGO = setting.Value == "1"
			}
		}
	}
	if result.Revision == "" {
		result.Revision = "unknown"
	}
	return result
}
