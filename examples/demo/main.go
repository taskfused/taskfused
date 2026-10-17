// Copyright 2026 TaskFused. All rights reserved.
// The demo re-executes this binary for real worker crashes and isolated samples.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/taskfused/taskfused/internal/buildinfo"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

type configuration struct {
	requests          int
	storageOperations int
	payloadBytes      int
	waitDuration      time.Duration
	samples           int
}

type report struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Environment environment      `json:"environment"`
	Functional  functionalReport `json:"functional"`
	Storage     storageReport    `json:"storage"`
	Waiting     []waitingSample  `json:"waiting_samples"`
	Notes       []string         `json:"notes"`
}

type environment struct {
	Go            string            `json:"go"`
	OS            string            `json:"os"`
	Arch          string            `json:"arch"`
	LogicalCPUs   int               `json:"logical_cpus"`
	GoMaxProcs    int               `json:"gomaxprocs"`
	SQLite        string            `json:"sqlite"`
	CPUQuota      string            `json:"cgroup_cpu_max,omitempty"`
	Storage       string            `json:"storage"`
	BuildSettings map[string]string `json:"build_settings,omitempty"`
}

func main() {
	var childWorker, childSampleDB string
	var showVersion bool
	var httpDemo bool
	var httpListen string
	var sampleWaiters int
	var sampleDuration time.Duration
	fs := flag.NewFlagSet("taskfused-demo", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.BoolVar(&showVersion, "version", false, "print executable build metadata and exit")
	cfg := configuration{}
	fs.BoolVar(&httpDemo, "http", false, "run HTTP correctness demo instead of measurements")
	fs.StringVar(&httpListen, "http-listen", "127.0.0.1:0", "HTTP demo listener (literal loopback IP:port)")
	fs.IntVar(&cfg.requests, "requests", 100, "equivalent submissions (2–1000)")
	fs.IntVar(&cfg.storageOperations, "operations", 1000, "distinct operations for sequential storage/latency measurement (1–10000)")
	fs.IntVar(&cfg.payloadBytes, "payload-bytes", 128, "input/result bytes per measured operation (0–65536)")
	fs.DurationVar(&cfg.waitDuration, "wait-duration", time.Second, "requested duration per isolated waiting sample (100ms–10s)")
	fs.IntVar(&cfg.samples, "samples", 3, "samples each for 0, 1 and 100 waiters (1–10)")
	fs.StringVar(&childWorker, "child-worker", "", "internal worker mode")
	fs.StringVar(&childSampleDB, "child-db", "", "internal database path")
	fs.IntVar(&sampleWaiters, "child-waiters", -1, "internal waiting sample size")
	fs.DurationVar(&sampleDuration, "child-duration", 0, "internal waiting duration")
	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected positional arguments")
		os.Exit(1)
	}
	if showVersion {
		if err := json.NewEncoder(os.Stdout).Encode(buildinfo.Info()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var err error
	switch {
	case childWorker != "":
		err = runChildWorker(ctx, childWorker, childSampleDB)
	case sampleWaiters >= 0:
		var sample waitingSample
		sample, err = measureWaiting(ctx, childSampleDB, sampleWaiters, sampleDuration)
		if err == nil {
			err = json.NewEncoder(os.Stdout).Encode(sample)
		}
	case httpDemo:
		var result httpDemoReport
		result, err = runHTTPDemo(ctx, cfg.requests, httpListen)
		if err == nil {
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			err = encoder.Encode(result)
		}
	default:
		var result report
		result, err = runDemo(ctx, cfg)
		if err == nil {
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			err = encoder.Encode(result)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "taskfused demo:", err)
		os.Exit(1)
	}
}

func validateConfiguration(c configuration) error {
	if c.requests < 2 || c.requests > 1000 || c.storageOperations < 1 || c.storageOperations > 10000 || c.payloadBytes < 0 || c.payloadBytes > 65536 || c.waitDuration < 100*time.Millisecond || c.waitDuration > 10*time.Second || c.samples < 1 || c.samples > 10 || int64(c.storageOperations)*int64(c.payloadBytes) > 64*1024*1024 {
		return fmt.Errorf("invalid workload; use -h for bounds")
	}
	return nil
}

func runDemo(parent context.Context, cfg configuration) (report, error) {
	if err := validateConfiguration(cfg); err != nil {
		return report{}, err
	}
	ctx, cancel := context.WithTimeout(parent, cfg.waitDuration*time.Duration(cfg.samples*3)+2*time.Minute)
	defer cancel()
	directory, err := os.MkdirTemp("", "taskfused-demo-")
	if err != nil {
		return report{}, err
	}
	defer os.RemoveAll(directory)
	executable, err := os.Executable()
	if err != nil {
		return report{}, err
	}
	fmt.Fprintln(os.Stderr, "Checking shared work, timeout isolation, process-kill recovery, cancellation and operator retry...")
	functional, err := runFunctional(ctx, executable, filepath.Join(directory, "functional.db"), cfg.requests)
	if err != nil {
		return report{}, err
	}
	fmt.Fprintln(os.Stderr, "Measuring sequential durable submission/execution and database growth...")
	storage, sqlite, err := measureStorage(ctx, filepath.Join(directory, "storage.db"), cfg.storageOperations, cfg.payloadBytes)
	if err != nil {
		return report{}, err
	}
	samples := []waitingSample{}
	for _, waiters := range []int{0, 1, 100} {
		fmt.Fprintf(os.Stderr, "Measuring %d waiters in %d isolated process samples...\n", waiters, cfg.samples)
		for i := 0; i < cfg.samples; i++ {
			sample, err := runWaitingChild(ctx, executable, filepath.Join(directory, fmt.Sprintf("wait-%d-%d.db", waiters, i)), waiters, cfg.waitDuration)
			if err != nil {
				return report{}, err
			}
			sample.Repetition = i + 1
			samples = append(samples, sample)
		}
	}
	quota, _ := os.ReadFile("/sys/fs/cgroup/cpu.max")
	settings := map[string]string{}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "-race", "CGO_ENABLED", "GOAMD64", "vcs.revision", "vcs.modified":
				settings[setting.Key] = setting.Value
			}
		}
	}
	return report{
		GeneratedAt: time.Now().UTC(),
		Environment: environment{BuildSettings: settings, Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, LogicalCPUs: runtime.NumCPU(), GoMaxProcs: runtime.GOMAXPROCS(0), SQLite: sqlite, CPUQuota: strings.TrimSpace(string(quota)), Storage: "os.MkdirTemp; underlying physical storage not characterized"},
		Functional:  functional, Storage: storage, Waiting: samples,
		Notes: []string{
			"Development baseline on this environment, not a capacity claim or cross-machine comparison.",
			"SQLite WAL with Store's FULL synchronization; filesystem/hardware durability is not independently validated.",
			"Submission and RunOnce latency samples are sequential with trivial handlers, no network work, and no race instrumentation unless the binary was built with -race.",
			"Waiting samples run in fresh processes; CPU and Go allocations include waiter setup/teardown. Go allocation counts exclude SQLite/C allocations. No SQL-read-count instrumentation is enabled.",
			"Zero waiters is an idle baseline. Compare repeated samples; short CPU differences can be noise. Host activity is not controlled.",
			"File sizes are logical lengths, not allocated disk blocks. Closed sizes include automatic last-connection checkpoint effects; live WAL/SHM sizes are reported separately.",
			"The real process kill deliberately bypasses worker/store cleanup. Recovery delay includes lease expiry, scheduling, SQLite access and 100ms Wait polling.",
		},
	}, nil
}
