package mock

import (
	"context"
	"sync"
	"time"
)

// updateQueue retains unconfirmed updates for a bot using Telegram's offset
// semantics. A closed notify channel wakes every active long poll; enqueue then
// installs a fresh channel for the next generation of waiters.
type updateQueue struct {
	mu      sync.Mutex
	updates []Update
	notify  chan struct{}
}

func newUpdateQueue() *updateQueue {
	return &updateQueue{notify: make(chan struct{})}
}

func (q *updateQueue) Enqueue(upd Update) {
	q.mu.Lock()
	q.updates = append(q.updates, upd)
	close(q.notify)
	q.notify = make(chan struct{})
	q.mu.Unlock()
}

func (q *updateQueue) Poll(ctx context.Context, offset int64, limit int, timeout time.Duration) []Update {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	var timer *time.Timer
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		defer timer.Stop()
	}

	for {
		q.mu.Lock()
		q.applyOffset(offset)
		if len(q.updates) > 0 {
			n := min(limit, len(q.updates))
			out := append([]Update(nil), q.updates[:n]...)
			q.mu.Unlock()
			return out
		}
		notify := q.notify
		q.mu.Unlock()

		if timeout <= 0 {
			return []Update{}
		}
		select {
		case <-ctx.Done():
			return []Update{}
		case <-timer.C:
			return []Update{}
		case <-notify:
			// Re-check under the lock. Another poll may have acknowledged the
			// update between the notification and this goroutine waking.
		}
	}
}

func (q *updateQueue) applyOffset(offset int64) {
	switch {
	case offset > 0:
		first := 0
		for first < len(q.updates) && q.updates[first].UpdateID < offset {
			first++
		}
		q.updates = q.updates[first:]
	case offset < 0:
		keep := int(-offset)
		if keep < len(q.updates) {
			q.updates = q.updates[len(q.updates)-keep:]
		}
	}
}

func (q *updateQueue) Clear() int {
	q.mu.Lock()
	n := len(q.updates)
	q.updates = nil
	q.mu.Unlock()
	return n
}

func (q *updateQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.updates)
}

type updateDelivery struct {
	Method string
	Status int
}

// updateDispatcher selects the active Telegram delivery mode. A configured
// webhook wins; otherwise the update remains queued until getUpdates polls it.
type updateDispatcher struct {
	webhooks *WebhookDispatcher
}

func newUpdateDispatcher(webhooks *WebhookDispatcher) *updateDispatcher {
	return &updateDispatcher{webhooks: webhooks}
}

func (d *updateDispatcher) Deliver(ctx context.Context, bot *botEntry, upd Update) (updateDelivery, error) {
	if d.webhooks.hasTarget(bot) {
		status, err := d.webhooks.Deliver(ctx, bot, upd)
		return updateDelivery{Method: "webhook", Status: status}, err
	}
	bot.updates.Enqueue(upd)
	return updateDelivery{Method: "polling"}, nil
}
