package teamspeak

import (
	"testing"
	"time"
)

func TestOnTextMessage_RegistersHandler(t *testing.T) {
	c := newTestClient(t)

	called := make(chan TextMessage, 1)
	c.OnTextMessage(func(m TextMessage) { called <- m })
	c.rebuildMiddlewareChains()

	c.finalEvtHandler(TextMessage{Message: "hi"})

	select {
	case m := <-called:
		if m.Message != "hi" {
			t.Errorf("expected 'hi', got %q", m.Message)
		}
	case <-time.After(time.Second):
		t.Error("OnTextMessage handler not called")
	}
}

func TestOnClientLeave_RegistersHandler(t *testing.T) {
	c := newTestClient(t)

	called := make(chan ClientLeftViewEvent, 1)
	c.OnClientLeave(func(e ClientLeftViewEvent) { called <- e })
	c.rebuildMiddlewareChains()

	c.finalEvtHandler(ClientLeftViewEvent{ID: 5})

	select {
	case e := <-called:
		if e.ID != 5 {
			t.Errorf("expected ID=5, got %d", e.ID)
		}
	case <-time.After(time.Second):
		t.Error("OnClientLeave handler not called")
	}
}

func TestOnDisconnected_RegistersHandler(t *testing.T) {
	c := newTestClient(t)

	called := make(chan error, 1)
	c.OnDisconnected(func(err error) { called <- err })

	// OnDisconnected handlers are called by onClosed; fire it directly.
	c.mu.Lock()
	handlers := c.disconnectedHandlers
	c.mu.Unlock()

	for _, h := range handlers {
		go h(nil)
	}

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Error("OnDisconnected handler not called")
	}
}

func TestMultipleHandlers_AllCalled(t *testing.T) {
	c := newTestClient(t)

	a := make(chan struct{}, 1)
	b := make(chan struct{}, 1)
	c.OnTextMessage(func(_ TextMessage) { a <- struct{}{} })
	c.OnTextMessage(func(_ TextMessage) { b <- struct{}{} })
	c.rebuildMiddlewareChains()

	c.finalEvtHandler(TextMessage{Message: "test"})

	for _, ch := range []chan struct{}{a, b} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Error("not all handlers called")
		}
	}
}

func TestUseCommandMiddleware_InterceptsCommands(t *testing.T) {
	c := newTestClient(t)

	intercepted := make(chan string, 1)
	c.UseCommandMiddleware(func(next func(string) error) func(string) error {
		return func(cmd string) error {
			intercepted <- cmd

			return next(cmd)
		}
	})

	err := c.finalCmdHandler("test cmd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case cmd := <-intercepted:
		if cmd != "test cmd" {
			t.Errorf("expected 'test cmd', got %q", cmd)
		}
	case <-time.After(time.Second):
		t.Error("middleware not called")
	}
}

func TestUseCommandMiddleware_ChainOrder(t *testing.T) {
	c := newTestClient(t)

	var order []string
	c.UseCommandMiddleware(
		func(next func(string) error) func(string) error {
			return func(cmd string) error {
				order = append(order, "first")

				return next(cmd)
			}
		},
		func(next func(string) error) func(string) error {
			return func(cmd string) error {
				order = append(order, "second")

				return next(cmd)
			}
		},
	)

	_ = c.finalCmdHandler("x")

	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Errorf("unexpected middleware order: %v", order)
	}
}

func TestUseCommandMiddleware_CanShortCircuit(t *testing.T) {
	c := newTestClient(t)

	sent := make(chan string, 1)
	c.UseCommandMiddleware(func(next func(string) error) func(string) error {
		return func(cmd string) error {
			if cmd == "blocked" {
				return nil // don't call next
			}

			return next(cmd)
		}
	})

	// Wrap the base handler to detect if it was called.
	origBase := c.finalCmdHandler
	c.finalCmdHandler = func(cmd string) error {
		sent <- cmd

		return origBase(cmd)
	}
	// Re-apply middleware on top of new base.
	c.UseCommandMiddleware()

	_ = c.SendCommandNoWait("blocked")

	select {
	case <-sent:
		t.Error("short-circuited command should not reach base handler")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestUseEventMiddleware_InterceptsEvents(t *testing.T) {
	c := newTestClient(t)

	intercepted := make(chan any, 1)
	c.UseEventMiddleware(func(next func(any)) func(any) {
		return func(evt any) {
			intercepted <- evt
			next(evt)
		}
	})

	c.finalEvtHandler(TextMessage{Message: "intercepted"})

	select {
	case evt := <-intercepted:
		if m, ok := evt.(TextMessage); !ok || m.Message != "intercepted" {
			t.Errorf("unexpected event: %v", evt)
		}
	case <-time.After(time.Second):
		t.Error("event middleware not called")
	}
}

func TestUseEventMiddleware_CanFilter(t *testing.T) {
	c := newTestClient(t)

	reached := make(chan TextMessage, 1)
	c.OnTextMessage(func(m TextMessage) { reached <- m })

	// Filter: block all text messages.
	c.UseEventMiddleware(func(next func(any)) func(any) {
		return func(evt any) {
			if _, ok := evt.(TextMessage); ok {
				return // drop
			}
			next(evt)
		}
	})

	c.finalEvtHandler(TextMessage{Message: "filtered"})

	select {
	case <-reached:
		t.Error("filtered event should not reach handler")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFinalEvtHandler_UnknownType_NoPanic(t *testing.T) {
	c := newTestClient(t)
	// Should not panic for an unhandled event type.
	c.finalEvtHandler(struct{ X int }{X: 42})
}
