// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/taskfused/taskfused"
)

type latencySummary struct {
	Samples int     `json:"samples"`
	MinMS   float64 `json:"min_ms"`
	P50MS   float64 `json:"p50_ms"`
	P95MS   float64 `json:"p95_ms"`
	P99MS   float64 `json:"p99_ms"`
	MaxMS   float64 `json:"max_ms"`
	MeanMS  float64 `json:"mean_ms"`
}

func milliseconds(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
func summarize(values []time.Duration) latencySummary {
	if len(values) == 0 {
		return latencySummary{}
	}
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	var total time.Duration
	for _, d := range ordered {
		total += d
	}
	percentile := func(p int) float64 { return milliseconds(ordered[(len(ordered)*p+99)/100-1]) }
	return latencySummary{Samples: len(values), MinMS: milliseconds(ordered[0]), P50MS: percentile(50), P95MS: percentile(95), P99MS: percentile(99), MaxMS: milliseconds(ordered[len(ordered)-1]), MeanMS: milliseconds(total) / float64(len(values))}
}

type fileSizes struct {
	Main  int64 `json:"main_bytes"`
	WAL   int64 `json:"wal_bytes"`
	SHM   int64 `json:"shm_bytes"`
	Total int64 `json:"total_bytes"`
}

func sizes(path string) (fileSizes, error) {
	result := fileSizes{}
	for _, item := range []struct {
		path string
		dest *int64
	}{{path, &result.Main}, {path + "-wal", &result.WAL}, {path + "-shm", &result.SHM}} {
		info, err := os.Stat(item.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fileSizes{}, err
		}
		*item.dest = info.Size()
		result.Total += info.Size()
	}
	return result, nil
}

type recordCounts struct {
	Operations int `json:"operations"`
	Attempts   int `json:"attempts"`
	Events     int `json:"events"`
}

func readMetadata(ctx context.Context, path string) (recordCounts, string, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	q.Set("_busy_timeout", "5000")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return recordCounts{}, "", err
	}
	defer db.Close()
	var counts recordCounts
	var version string
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM operations),(SELECT count(*) FROM attempts),(SELECT count(*) FROM events)`).Scan(&counts.Operations, &counts.Attempts, &counts.Events); err != nil {
		return recordCounts{}, "", err
	}
	if err := db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version); err != nil {
		return recordCounts{}, "", err
	}
	return counts, version, nil
}

type storageReport struct {
	Operations              int            `json:"operations"`
	InputBytesPerOperation  int            `json:"input_bytes_per_operation"`
	ResultBytesPerOperation int            `json:"result_bytes_per_operation"`
	Mode                    string         `json:"mode"`
	EmptyClosed             fileSizes      `json:"empty_closed"`
	QueuedLive              fileSizes      `json:"queued_live"`
	QueuedClosed            fileSizes      `json:"queued_closed"`
	CompletedLive           fileSizes      `json:"completed_live"`
	CompletedClosed         fileSizes      `json:"completed_closed"`
	CompletedGrowthBytes    int64          `json:"completed_growth_bytes"`
	GrowthPerOperationBytes float64        `json:"amortized_completed_growth_per_operation_bytes"`
	Records                 recordCounts   `json:"records"`
	Submission              latencySummary `json:"submission"`
	SubmissionTotalMS       float64        `json:"submission_phase_ms"`
	Execution               latencySummary `json:"run_once"`
	ExecutionTotalMS        float64        `json:"execution_phase_ms"`
}

func measureStorage(ctx context.Context, path string, n, payloadBytes int) (storageReport, string, error) {
	result := storageReport{Operations: n, InputBytesPerOperation: payloadBytes, ResultBytesPerOperation: payloadBytes, Mode: "sequential submission, then sequential RunOnce with a trivial echo handler; WAL/FULL"}
	s, err := taskfused.Open(ctx, path)
	if err != nil {
		return storageReport{}, "", err
	}
	if err := s.Close(); err != nil {
		return storageReport{}, "", err
	}
	result.EmptyClosed, err = sizes(path)
	if err != nil {
		return storageReport{}, "", err
	}
	s, err = taskfused.Open(ctx, path)
	if err != nil {
		return storageReport{}, "", err
	}
	defer s.Close()
	payload := bytes.Repeat([]byte("x"), payloadBytes)
	submissions := make([]time.Duration, 0, n)
	start := time.Now()
	for i := 0; i < n; i++ {
		began := time.Now()
		_, err := s.Submit(ctx, taskfused.Request{Namespace: "measure", Handler: "echo", Version: "v1", Key: fmt.Sprintf("operation-%06d", i), Input: payload, MaxAttempts: 1})
		if err != nil {
			return storageReport{}, "", err
		}
		submissions = append(submissions, time.Since(began))
	}
	result.SubmissionTotalMS = milliseconds(time.Since(start))
	result.Submission = summarize(submissions)
	result.QueuedLive, err = sizes(path)
	if err != nil {
		return storageReport{}, "", err
	}
	if err := s.Close(); err != nil {
		return storageReport{}, "", err
	}
	result.QueuedClosed, err = sizes(path)
	if err != nil {
		return storageReport{}, "", err
	}
	s, err = taskfused.Open(ctx, path)
	if err != nil {
		return storageReport{}, "", err
	}
	defer s.Close()
	worker, err := taskfused.NewWorker(s, taskfused.WorkerOptions{Namespace: "measure", LeaseDuration: 30 * time.Second, Handlers: []taskfused.Registration{{Name: "echo", Version: "v1", Handle: func(ctx context.Context, c taskfused.Claim) ([]byte, error) { return c.Operation.Input, nil }}}})
	if err != nil {
		return storageReport{}, "", err
	}
	executions := make([]time.Duration, 0, n)
	start = time.Now()
	for i := 0; i < n; i++ {
		began := time.Now()
		worked, err := worker.RunOnce(ctx)
		if err != nil {
			return storageReport{}, "", err
		}
		if !worked {
			return storageReport{}, "", fmt.Errorf("worker stopped after %d/%d operations", i, n)
		}
		executions = append(executions, time.Since(began))
	}
	result.ExecutionTotalMS = milliseconds(time.Since(start))
	result.Execution = summarize(executions)
	if worked, err := worker.RunOnce(ctx); err != nil || worked {
		return storageReport{}, "", fmt.Errorf("unexpected remaining work: %v, %v", worked, err)
	}
	counts, version, err := readMetadata(ctx, path)
	if err != nil {
		return storageReport{}, "", err
	}
	if counts.Operations != n || counts.Attempts != n || counts.Events != 3*n {
		return storageReport{}, "", fmt.Errorf("measurement record counts: %+v", counts)
	}
	result.Records = counts
	result.CompletedLive, err = sizes(path)
	if err != nil {
		return storageReport{}, "", err
	}
	if err := s.Close(); err != nil {
		return storageReport{}, "", err
	}
	result.CompletedClosed, err = sizes(path)
	if err != nil {
		return storageReport{}, "", err
	}
	result.CompletedGrowthBytes = result.CompletedClosed.Total - result.EmptyClosed.Total
	result.GrowthPerOperationBytes = float64(result.CompletedGrowthBytes) / float64(n)
	return result, version, nil
}

type cpuSnapshot struct {
	available bool
	user      time.Duration
	system    time.Duration
}
type cpuUsage struct {
	Available        bool    `json:"available"`
	UserMS           float64 `json:"user_ms"`
	SystemMS         float64 `json:"system_ms"`
	TotalMS          float64 `json:"total_ms"`
	PercentOfOneCore float64 `json:"percent_of_one_core"`
}

func cpuDelta(before, after cpuSnapshot, wall time.Duration) cpuUsage {
	if !before.available || !after.available {
		return cpuUsage{}
	}
	user, system := after.user-before.user, after.system-before.system
	return cpuUsage{Available: true, UserMS: milliseconds(user), SystemMS: milliseconds(system), TotalMS: milliseconds(user + system), PercentOfOneCore: 100 * float64(user+system) / float64(wall)}
}

type waitingSample struct {
	Repetition          int       `json:"repetition"`
	Waiters             int       `json:"waiters"`
	RequestedDurationMS float64   `json:"requested_duration_ms"`
	ActualWallMS        float64   `json:"actual_wall_ms"`
	CPU                 cpuUsage  `json:"process_cpu"`
	GoAllocatedBytes    uint64    `json:"go_allocated_bytes"`
	GoAllocations       uint64    `json:"go_allocations"`
	GCCount             uint32    `json:"gc_count"`
	PollIntervalMS      int       `json:"configured_poll_interval_ms"`
	WaitsFinished       int       `json:"waits_finished"`
	BeforeFiles         fileSizes `json:"before_files"`
	AfterFiles          fileSizes `json:"after_files"`
	FileGrowthBytes     int64     `json:"file_growth_bytes"`
}

func measureWaiting(parent context.Context, path string, n int, duration time.Duration) (waitingSample, error) {
	if n < 0 || n > 1000 || duration < 100*time.Millisecond || duration > 10*time.Second {
		return waitingSample{}, fmt.Errorf("invalid waiting sample bounds")
	}
	s, err := taskfused.Open(parent, path)
	if err != nil {
		return waitingSample{}, err
	}
	defer s.Close()
	submitted, err := s.Submit(parent, taskfused.Request{Namespace: "measure", Handler: "pending", Version: "v1", Key: "waiting"})
	if err != nil {
		return waitingSample{}, err
	}
	beforeFiles, err := sizes(path)
	if err != nil {
		return waitingSample{}, err
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	cpuBefore := readCPU()
	start := time.Now()
	gate := make(chan struct{})
	ready := make(chan struct{}, n)
	errs := make(chan error, n)
	var waitCtx context.Context
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready <- struct{}{}
			<-gate
			_, err := s.Wait(waitCtx, "measure", submitted.Operation.ID)
			if !errors.Is(err, context.DeadlineExceeded) {
				errs <- fmt.Errorf("unexpected wait outcome: %v", err)
			}
		}()
	}
	for i := 0; i < n; i++ {
		<-ready
	}
	waitCtx, cancel := context.WithTimeout(parent, duration)
	defer cancel()
	close(gate)
	<-waitCtx.Done()
	wg.Wait()
	close(errs)
	for err := range errs {
		return waitingSample{}, err
	}
	if err := parent.Err(); err != nil {
		return waitingSample{}, err
	}
	wall := time.Since(start)
	cpuAfter := readCPU()
	runtime.ReadMemStats(&after)
	afterFiles, err := sizes(path)
	if err != nil {
		return waitingSample{}, err
	}
	counts, _, err := readMetadata(parent, path)
	if err != nil {
		return waitingSample{}, err
	}
	op, err := s.Get(parent, "measure", submitted.Operation.ID)
	if err != nil {
		return waitingSample{}, err
	}
	if op.State != taskfused.StateQueued || counts.Operations != 1 || counts.Attempts != 0 || counts.Events != 1 {
		return waitingSample{}, fmt.Errorf("waiting changed operation records: %+v", counts)
	}
	return waitingSample{Waiters: n, RequestedDurationMS: milliseconds(duration), ActualWallMS: milliseconds(wall), CPU: cpuDelta(cpuBefore, cpuAfter, wall), GoAllocatedBytes: after.TotalAlloc - before.TotalAlloc, GoAllocations: after.Mallocs - before.Mallocs, GCCount: after.NumGC - before.NumGC, PollIntervalMS: 100, WaitsFinished: n, BeforeFiles: beforeFiles, AfterFiles: afterFiles, FileGrowthBytes: afterFiles.Total - beforeFiles.Total}, nil
}

func runWaitingChild(ctx context.Context, executable, path string, n int, duration time.Duration) (waitingSample, error) {
	command := exec.CommandContext(ctx, executable, "-child-db", path, "-child-waiters", fmt.Sprint(n), "-child-duration", duration.String())
	output, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return waitingSample{}, fmt.Errorf("waiting sample: %w: %s", err, exit.Stderr)
		}
		return waitingSample{}, err
	}
	var sample waitingSample
	if err := json.Unmarshal(output, &sample); err != nil {
		return waitingSample{}, fmt.Errorf("waiting sample report: %w", err)
	}
	return sample, nil
}
