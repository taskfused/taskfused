//go:build linux || darwin

// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"syscall"
	"time"
)

func readCPU() cpuSnapshot {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return cpuSnapshot{}
	}
	return cpuSnapshot{available: true, user: time.Duration(usage.Utime.Sec)*time.Second + time.Duration(usage.Utime.Usec)*time.Microsecond, system: time.Duration(usage.Stime.Sec)*time.Second + time.Duration(usage.Stime.Usec)*time.Microsecond}
}
