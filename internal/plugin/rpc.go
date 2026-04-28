package plugin

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// JSON-RPC 2.0 request / response envelopes. Notification = no id field.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// rpcClient is a minimal JSON-RPC 2.0 client over a duplex stream pair.
// Suitable for line-delimited stdio. Requests get correlated to responses
// via auto-incrementing integer IDs; notifications use no ID and never
// produce a reply.
type rpcClient struct {
	enc *json.Encoder
	dec *json.Decoder

	writeMu sync.Mutex
	nextID  atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan rpcResponse

	closeOnce sync.Once
	closed    chan struct{}
}

func newRPCClient(stdin io.Writer, stdout io.Reader) *rpcClient {
	c := &rpcClient{
		enc:     json.NewEncoder(stdin),
		dec:     json.NewDecoder(bufio.NewReader(stdout)),
		pending: make(map[int64]chan rpcResponse),
		closed:  make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *rpcClient) readLoop() {
	for {
		var resp rpcResponse
		if err := c.dec.Decode(&resp); err != nil {
			c.shutdown()
			return
		}
		// Resolve the id back to an int64 — strings and numbers both work
		// per spec, but we always send numbers, so anything else is a
		// stray message we drop.
		var id int64
		if err := json.Unmarshal(resp.ID, &id); err != nil {
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[id]
		if ok {
			delete(c.pending, id)
		}
		c.mu.Unlock()
		if ok {
			select {
			case ch <- resp:
			default:
			}
		}
	}
}

func (c *rpcClient) shutdown() {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.mu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
	})
}

// Call sends a request and waits for the matching reply. The result is
// JSON-decoded into out (which may be nil to discard).
func (c *rpcClient) Call(method string, params any, out any) error {
	id := c.nextID.Add(1)
	rawID, _ := json.Marshal(id)

	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal params: %w", err)
		}
		rawParams = b
	}
	req := rpcRequest{JSONRPC: "2.0", ID: rawID, Method: method, Params: rawParams}

	ch := make(chan rpcResponse, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	c.writeMu.Lock()
	if err := c.enc.Encode(&req); err != nil {
		c.writeMu.Unlock()
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("encode: %w", err)
	}
	c.writeMu.Unlock()

	select {
	case <-c.closed:
		return errors.New("rpc connection closed")
	case resp, ok := <-ch:
		if !ok {
			return errors.New("rpc connection closed")
		}
		if resp.Error != nil {
			return resp.Error
		}
		if out != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return fmt.Errorf("unmarshal result: %w", err)
			}
		}
		return nil
	}
}

// Notify sends a fire-and-forget request (no ID, no reply expected).
func (c *rpcClient) Notify(method string, params any) error {
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal params: %w", err)
		}
		rawParams = b
	}
	req := rpcRequest{JSONRPC: "2.0", Method: method, Params: rawParams}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.enc.Encode(&req)
}
