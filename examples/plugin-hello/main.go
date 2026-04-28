// Sample blacknode plugin. Speaks the JSON-RPC handshake and then idles.
//
// Build:  go build -o plugin-hello .
// Install: copy plugin-hello + plugin.json into
//          $XDG_DATA_HOME/blacknode/plugins/hello/
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func main() {
	dec := json.NewDecoder(bufio.NewReader(os.Stdin))
	enc := json.NewEncoder(os.Stdout)
	for {
		var req rpcRequest
		if err := dec.Decode(&req); err != nil {
			fmt.Fprintln(os.Stderr, "decode error:", err)
			return
		}
		switch req.Method {
		case "init":
			_ = enc.Encode(rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"name":    "Hello plugin",
					"version": "0.1.0",
					"description": "Sample blacknode plugin showing the " +
						"init handshake and shutdown notification.",
					"capabilities": []string{},
				},
			})
		case "shutdown":
			// Notification — no reply expected.
			return
		default:
			_ = enc.Encode(rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &rpcError{
					Code:    -32601,
					Message: "method not found: " + req.Method,
				},
			})
		}
	}
}
