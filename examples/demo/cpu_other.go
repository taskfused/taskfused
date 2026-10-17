//go:build !linux && !darwin

// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0
package main

// The demo remains usable without claiming a CPU measurement on other platforms.
func readCPU() cpuSnapshot { return cpuSnapshot{} }
