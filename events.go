// Copyright 2026 TaskFused. All rights reserved.
package taskfused

import (
	"context"
	"fmt"
	"time"
)

const MaxEventPageSize = 200

type Event struct {
	Sequence           int64     `json:"sequence"`
	OperationID        string    `json:"operation_id"`
	AttemptID          string    `json:"attempt_id,omitempty"`
	Kind               string    `json:"kind"`
	OccurredAt         time.Time `json:"occurred_at"`
	ActionKey          string    `json:"action_key,omitempty"`
	ExpectedAttempts   int       `json:"expected_attempts,omitempty"`
	AdditionalAttempts int       `json:"additional_attempts,omitempty"`
	AttemptLimit       int       `json:"attempt_limit,omitempty"`
}

type EventQuery struct {
	After int64
	Limit int
}

type EventPage struct {
	Events    []Event `json:"events"`
	NextAfter int64   `json:"next_after"`
	HasMore   bool    `json:"has_more"`
}

// Events reads an operation's events after an exclusive global sequence cursor.
// Pages contain at most 200 records (default 50). Cursor gaps are normal because
// other operations share the sequence. Pagination is live, not a frozen snapshot;
// later appends can be read by calling again with NextAfter even after HasMore=false.
func (s *Store) Events(ctx context.Context, namespace, id string, q EventQuery) (EventPage, error) {
	if q.After < 0 || q.Limit < 0 || q.Limit > MaxEventPageSize {
		return EventPage{}, fmt.Errorf("%w: after must be nonnegative and limit 1–200 (zero defaults to 50)", ErrInvalid)
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if _, err := s.Get(ctx, namespace, id); err != nil {
		return EventPage{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.sequence,e.operation_id,e.attempt_id,e.kind,e.occurred_at,e.action_key,
 COALESCE(a.expected_attempts,0),COALESCE(a.additional_attempts,0),COALESCE(a.attempt_limit,0)
 FROM events e LEFT JOIN retry_actions a ON a.operation_id=e.operation_id AND a.action_key=e.action_key
 WHERE e.operation_id=? AND e.sequence>? ORDER BY e.sequence LIMIT ?`, id, q.After, q.Limit+1)
	if err != nil {
		return EventPage{}, err
	}
	defer rows.Close()
	page := EventPage{Events: []Event{}, NextAfter: q.After}
	for rows.Next() {
		var e Event
		var occurred string
		if err := rows.Scan(&e.Sequence, &e.OperationID, &e.AttemptID, &e.Kind, &occurred, &e.ActionKey, &e.ExpectedAttempts, &e.AdditionalAttempts, &e.AttemptLimit); err != nil {
			return EventPage{}, err
		}
		e.OccurredAt, err = time.Parse(time.RFC3339Nano, occurred)
		if err != nil {
			return EventPage{}, err
		}
		page.Events = append(page.Events, e)
	}
	if err := rows.Err(); err != nil {
		return EventPage{}, err
	}
	if len(page.Events) > q.Limit {
		page.HasMore = true
		page.Events = page.Events[:q.Limit]
	}
	if len(page.Events) > 0 {
		page.NextAfter = page.Events[len(page.Events)-1].Sequence
	}
	return page, nil
}
