package main

import (
	"context"
	"time"
)

// Bounded in-memory counters. Only keyed email hashes are retained temporarily.
type quotaState struct {
	Day         string           `json:"day"`
	Count       int              `json:"count"`
	Minute      int64            `json:"minute"`
	MinuteCount int              `json:"minuteCount"`
	Senders     map[string]int64 `json:"senders"`
}

func (q *quotaState) take(now time.Time, sender string) error {
	day := now.UTC().Format("2006-01-02")
	if q.Day != day {
		*q = quotaState{Day: day, Senders: map[string]int64{}}
	}
	if q.Senders == nil {
		q.Senders = map[string]int64{}
	}
	minute := now.Unix() / 60
	if q.Minute != minute {
		q.Minute = minute
		q.MinuteCount = 0
	}
	if q.Count >= 50 || q.MinuteCount >= 3 {
		return errLimited
	}
	if last, ok := q.Senders[sender]; ok && now.Unix()-last < 600 {
		return errLimited
	}
	for key, last := range q.Senders {
		if now.Unix()-last >= 600 {
			delete(q.Senders, key)
		}
	}
	q.Count++
	q.MinuteCount++
	q.Senders[sender] = now.Unix()
	return nil
}

// Limits are local to this instance and reset on restart; no database is used.
func (s *contactServer) reserveQuota(ctx context.Context, sender string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	return s.quota.take(time.Now(), sender)
}
