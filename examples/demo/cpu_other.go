//go:build !linux && !darwin

// Copyright 2026 TaskFused. All rights reserved.
package main

// The demo remains usable without claiming a CPU measurement on other platforms.
func readCPU() cpuSnapshot { return cpuSnapshot{} }
