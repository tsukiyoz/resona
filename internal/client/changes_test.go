package client

import "testing"

func TestChangesCoalesceWithoutBlockingAndUnsubscribe(t *testing.T) {
	s, err := New(&memoryStore{})
	if err != nil {
		t.Fatal(err)
	}
	changes, unsubscribe := s.SubscribeChanges()
	<-changes
	for range 100 {
		if _, err := s.OpenPreview(); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-changes:
	default:
		t.Fatal("mutation did not signal")
	}
	select {
	case <-changes:
		t.Fatal("slow consumer accumulated signals")
	default:
	}
	if _, err := s.GetWorkspace(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changes:
		t.Fatal("read scheduled another refresh")
	default:
	}
	unsubscribe()
	unsubscribe()
	if _, err := s.LeavePreview(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changes:
		t.Fatal("unsubscribed consumer signaled")
	default:
	}
}
