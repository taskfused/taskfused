//go:build linux || darwin

// Copyright 2026 TaskFused. All rights reserved.
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
