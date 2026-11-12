package events

import "testing"

func TestCoalescesToLatest(t *testing.T) {
	h := NewHub(4, 2)
	c, _ := h.Subscribe()
	for i := 0; i < 100; i++ {
		h.Publish([]byte{byte(i)})
	}
	h.Tick()
	h.Tick() // nothing new: no second send
	if got := <-c.C; got[0] != 99 {
		t.Fatalf("got %v, want latest", got)
	}
	select {
	case <-c.C:
		t.Fatal("sent twice for one change")
	default:
	}
}

func TestCapAndSlowPageDropped(t *testing.T) {
	h := NewHub(4, 1)
	c, _ := h.Subscribe()
	if _, err := h.Subscribe(); err != ErrTooMany {
		t.Fatal("page cap ignored")
	}
	for i := 0; i < 41; i++ { // never reads
		h.Publish([]byte{1})
		h.Tick()
	}
	select {
	case <-c.Done:
	default:
		t.Fatal("slow page not dropped")
	}
	if h.Open() != 0 {
		t.Fatal("dropped page still counted")
	}
}
