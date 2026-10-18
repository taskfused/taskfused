// Copyright TaskFused.com 2026
// SPDX-License-Identifier: Apache-2.0

package taskfused

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestEventPaginationHasNoDuplicatesGapsOrCrossOperationLeak(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	op := submitTest(t, s, request())
	r := request()
	r.Namespace = "other"
	r.Key = "other"
	other := submitTest(t, s, r)
	c := claimTest(t, s)
	if err := s.Complete(context.Background(), c, []byte("answer")); err != nil {
		t.Fatal(err)
	}
	var after int64
	var sequences []int64
	for {
		page, err := s.Events(context.Background(), op.Namespace, op.ID, EventQuery{After: after, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page.Events {
			if e.OperationID != op.ID || e.Sequence <= after || e.ActionKey != "" || e.ExpectedAttempts != 0 {
				t.Fatalf("page leak/order: %+v", e)
			}
			sequences = append(sequences, e.Sequence)
		}
		if page.NextAfter < after {
			t.Fatal("cursor moved backward")
		}
		after = page.NextAfter
		if !page.HasMore {
			break
		}
	}
	if len(sequences) != 3 || sequences[1]-sequences[0] != 2 {
		t.Fatalf("global cursor gaps lost: %v", sequences)
	}
	empty, err := s.Events(context.Background(), op.Namespace, op.ID, EventQuery{After: after, Limit: 1})
	if err != nil || len(empty.Events) != 0 || empty.HasMore || empty.NextAfter != after {
		t.Fatalf("empty tail: %+v %v", empty, err)
	}
	if _, err := s.Events(context.Background(), other.Namespace, op.ID, EventQuery{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong namespace read: %v", err)
	}
}

func TestEventsPageBoundAndLiveTail(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	op := submitTest(t, s, request())
	// A deterministic fixture with enough rows to exercise the hard page bound.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxEventPageSize; i++ {
		if _, err := tx.Exec("INSERT INTO events(operation_id,kind,occurred_at) VALUES(?,'fixture',?)", op.ID, op.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00")); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	page, err := s.Events(context.Background(), op.Namespace, op.ID, EventQuery{Limit: MaxEventPageSize})
	if err != nil || len(page.Events) != MaxEventPageSize || !page.HasMore {
		t.Fatalf("page bound: %d %+v %v", len(page.Events), page, err)
	}
	tail, err := s.Events(context.Background(), op.Namespace, op.ID, EventQuery{After: page.NextAfter})
	if err != nil || len(tail.Events) != 1 || tail.HasMore {
		t.Fatalf("tail: %+v %v", tail, err)
	}
	if _, err := s.Cancel(context.Background(), op.Namespace, op.ID); err != nil {
		t.Fatal(err)
	}
	appended, err := s.Events(context.Background(), op.Namespace, op.ID, EventQuery{After: tail.NextAfter})
	if err != nil || len(appended.Events) != 1 || appended.Events[0].Kind != "canceled" {
		t.Fatalf("live append lost: %+v %v", appended, err)
	}
}

func TestEventsValidationAndDefaultLimit(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "store.db"))
	op := submitTest(t, s, request())
	for _, q := range []EventQuery{{After: -1}, {Limit: -1}, {Limit: MaxEventPageSize + 1}} {
		if _, err := s.Events(context.Background(), op.Namespace, op.ID, q); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid query: %v", err)
		}
	}
	if page, err := s.Events(context.Background(), op.Namespace, op.ID, EventQuery{}); err != nil || len(page.Events) != 1 {
		t.Fatalf("default: %+v %v", page, err)
	}
	if _, err := s.Events(context.Background(), op.Namespace, "missing", EventQuery{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Events(ctx, op.Namespace, op.ID, EventQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read: %v", err)
	}
}
