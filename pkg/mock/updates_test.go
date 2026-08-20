package mock

import (
	"context"
	"testing"
	"time"
)

func TestUpdateQueueOffsetAndLimit(t *testing.T) {
	q := newUpdateQueue()
	for id := int64(1); id <= 3; id++ {
		q.Enqueue(Update{UpdateID: id})
	}

	got := q.Poll(context.Background(), 2, 1, 0)
	if len(got) != 1 || got[0].UpdateID != 2 {
		t.Fatalf("Poll(offset=2, limit=1) = %+v, want update 2", got)
	}

	got = q.Poll(context.Background(), 3, 100, 0)
	if len(got) != 1 || got[0].UpdateID != 3 {
		t.Fatalf("Poll(offset=3) = %+v, want update 3", got)
	}
	if q.Len() != 1 {
		t.Fatalf("queue length = %d, want delivered update retained until acknowledged", q.Len())
	}

	got = q.Poll(context.Background(), 4, 100, 0)
	if len(got) != 0 || q.Len() != 0 {
		t.Fatalf("Poll(offset=4) = %+v, queue length %d; want empty acknowledged queue", got, q.Len())
	}
}

func TestUpdateQueueNegativeOffset(t *testing.T) {
	q := newUpdateQueue()
	for id := int64(1); id <= 3; id++ {
		q.Enqueue(Update{UpdateID: id})
	}

	got := q.Poll(context.Background(), -2, 100, 0)
	if len(got) != 2 || got[0].UpdateID != 2 || got[1].UpdateID != 3 {
		t.Fatalf("Poll(offset=-2) = %+v, want updates 2 and 3", got)
	}
}

func TestUpdateQueueLongPollWakesOnEnqueue(t *testing.T) {
	q := newUpdateQueue()
	result := make(chan []Update, 1)
	go func() {
		result <- q.Poll(context.Background(), 0, 100, time.Second)
	}()

	q.Enqueue(Update{UpdateID: 42})
	select {
	case got := <-result:
		if len(got) != 1 || got[0].UpdateID != 42 {
			t.Fatalf("long poll = %+v, want update 42", got)
		}
	case <-time.After(time.Second):
		t.Fatal("long poll did not wake after enqueue")
	}
}
