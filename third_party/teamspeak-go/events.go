package teamspeak

import "slices"

// OnTextMessage registers a handler for incoming text messages.
func (c *Client) OnTextMessage(handler func(TextMessage)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.textMsgHandlers = append(c.textMsgHandlers, handler)
}

// OnClientEnter registers a handler for clients entering view.
func (c *Client) OnClientEnter(handler func(ClientInfo)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clientEnterHandlers = append(c.clientEnterHandlers, handler)
}

// OnClientLeave registers a handler for clients leaving view.
func (c *Client) OnClientLeave(handler func(ClientLeftViewEvent)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clientLeaveHandlers = append(c.clientLeaveHandlers, handler)
}

// OnPoked registers a handler for when this client is poked by another user.
func (c *Client) OnPoked(handler func(PokeEvent)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pokedHandlers = append(c.pokedHandlers, handler)
}

// OnKicked registers a handler when this client is kicked (channel or server).
func (c *Client) OnKicked(handler func(string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kickedHandlers = append(c.kickedHandlers, handler)
}

// OnClientMoved registers a handler for clients moving between channels.
func (c *Client) OnClientMoved(handler func(ClientMovedEvent)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clientMoveHandlers = append(c.clientMoveHandlers, handler)
}

// OnConnected registers a handler for when the client is fully connected.
func (c *Client) OnConnected(handler func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connectedHandlers = append(c.connectedHandlers, handler)
}

// OnDisconnected registers a handler for when the client is disconnected.
func (c *Client) OnDisconnected(handler func(error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disconnectedHandlers = append(c.disconnectedHandlers, handler)
}

func (c *Client) UseCommandMiddleware(mw ...CommandMiddleware) {
	c.cmdMiddlewares = append(c.cmdMiddlewares, mw...)
	c.rebuildMiddlewareChains()
}

func (c *Client) UseEventMiddleware(mw ...EventMiddleware) {
	c.eventMiddlewares = append(c.eventMiddlewares, mw...)
	c.rebuildMiddlewareChains()
}

func (c *Client) rebuildMiddlewareChains() {
	c.finalCmdHandler = func(cmd string) error {
		return c.handler.SendPacket(2, []byte(cmd), 0)
	}
	for _, middleware := range slices.Backward(c.cmdMiddlewares) {
		c.finalCmdHandler = middleware(c.finalCmdHandler)
	}

	c.finalEvtHandler = func(evt any) {
		c.dispatchEvent(evt)
	}
	for _, middleware := range slices.Backward(c.eventMiddlewares) {
		c.finalEvtHandler = middleware(c.finalEvtHandler)
	}
}

func (c *Client) dispatchEvent(evt any) {
	switch e := evt.(type) {
	case TextMessage:
		for _, h := range c.textMsgHandlers {
			go h(e)
		}
	case ClientInfo:
		for _, h := range c.clientEnterHandlers {
			go h(e)
		}
	case ClientLeftViewEvent:
		for _, h := range c.clientLeaveHandlers {
			go h(e)
		}
	case ClientMovedEvent:
		for _, h := range c.clientMoveHandlers {
			go h(e)
		}
	case PokeEvent:
		for _, h := range c.pokedHandlers {
			go h(e)
		}
	}
}
