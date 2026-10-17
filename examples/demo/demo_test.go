package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Re-execute this test binary in the demo's private child modes, so the race-tested
// integration exercises real processes without relying on an installed CLI binary.
func TestMain(m *testing.M) {
	if os.Getenv("TASKFUSED_DEMO_TEST_CHILD") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestEndToEndDemoWithActualProcessKillAndIsolatedSamples(t *testing.T) {
	t.Setenv("TASKFUSED_DEMO_TEST_CHILD", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	result, err := runDemo(ctx, configuration{requests: 100, storageOperations: 12, payloadBytes: 32, waitDuration: 100 * time.Millisecond, samples: 1})
	if err != nil {
		t.Fatal(err)
	}
	shared := result.Functional.Shared
	if shared.Requests != 100 || shared.NewOperations != 1 || shared.Joined != 99 || shared.SuccessfulWaiters != 99 || shared.TimedOutWaiters != 1 || shared.Attempts != 1 {
		t.Fatalf("shared demo: %+v", shared)
	}
	crash := result.Functional.Crash
	if crash.KillExit == "" || crash.Attempts != 2 || !crash.SameOperation || !crash.StaleCommitRejected || crash.NewToken <= crash.OldToken {
		t.Fatalf("crash demo: %+v", crash)
	}
	if !result.Functional.Cancellation.ObservedByHandler || !result.Functional.Cancellation.SurvivorSucceeded {
		t.Fatalf("cancellation: %+v", result.Functional.Cancellation)
	}
	if result.Functional.Retry.AppliedActions != 1 || result.Functional.Retry.Replays != 1 || result.Functional.Retry.Events != 6 {
		t.Fatalf("retry: %+v", result.Functional.Retry)
	}
	if result.Storage.Records != (recordCounts{Operations: 12, Attempts: 12, Events: 36}) || result.Storage.Submission.Samples != 12 || result.Storage.Execution.Samples != 12 {
		t.Fatalf("storage: %+v", result.Storage)
	}
	if result.Storage.CompletedClosed.WAL != 0 || result.Storage.CompletedClosed.SHM != 0 {
		t.Fatal("closed size snapshot retained live sidecars")
	}
	if len(result.Waiting) != 3 {
		t.Fatalf("waiting samples: %d", len(result.Waiting))
	}
	for i, sample := range result.Waiting {
		expected := []int{0, 1, 100}[i]
		if sample.Waiters != expected || sample.WaitsFinished != expected || sample.FileGrowthBytes != 0 || sample.PollIntervalMS != 100 {
			t.Fatalf("waiting sample: %+v", sample)
		}
	}
	// No environment-specific latency, CPU or byte-per-record performance threshold.
}

func TestLatencySummaryNearestRankAndInputOwnership(t *testing.T) {
	if summarize(nil) != (latencySummary{}) {
		t.Fatal("empty samples produced a distribution")
	}
	values := make([]time.Duration, 100)
	for i := range values {
		values[i] = time.Duration(100-i) * time.Millisecond
	}
	original := append([]time.Duration(nil), values...)
	got := summarize(values)
	if got.Samples != 100 || got.MinMS != 1 || got.P50MS != 50 || got.P95MS != 95 || got.P99MS != 99 || got.MaxMS != 100 || got.MeanMS != 50.5 {
		t.Fatalf("distribution: %+v", got)
	}
	if !reflect.DeepEqual(values, original) {
		t.Fatal("summary reordered caller samples")
	}
}

func TestDemoWorkloadBoundsAndCanceledRun(t *testing.T) {
	good := configuration{requests: 100, storageOperations: 1000, payloadBytes: 128, waitDuration: time.Second, samples: 3}
	if err := validateConfiguration(good); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*configuration){
		func(c *configuration) { c.requests = 1 }, func(c *configuration) { c.storageOperations = 0 },
		func(c *configuration) { c.payloadBytes = -1 }, func(c *configuration) { c.samples = 0 },
		func(c *configuration) { c.waitDuration = 0 }, func(c *configuration) { c.storageOperations = 10000; c.payloadBytes = 65536 },
	} {
		c := good
		change(&c)
		if err := validateConfiguration(c); err == nil {
			t.Fatalf("accepted invalid workload: %+v", c)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runDemo(ctx, good); err == nil {
		t.Fatal("canceled demo succeeded")
	}
}

func TestWaitingMeasurementRejectsInvalidBounds(t *testing.T) {
	for _, n := range []int{-1, 1001} {
		if _, err := measureWaiting(context.Background(), filepath.Join(t.TempDir(), "invalid.db"), n, time.Second); err == nil {
			t.Fatal("accepted invalid waiter count")
		}
	}
	if _, err := measureWaiting(context.Background(), filepath.Join(t.TempDir(), "invalid.db"), 1, 0); err == nil {
		t.Fatal("accepted invalid sample duration")
	}
}
