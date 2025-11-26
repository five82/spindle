package ipc

import (
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"time"
)

// Client provides RPC access to the daemon.
type Client struct {
	conn   net.Conn
	client *rpc.Client
}

// Dial connects to the IPC server at the given socket path.
func Dial(path string) (*Client, error) {
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, err
	}
	rpcClient := rpc.NewClientWithCodec(jsonrpc.NewClientCodec(conn))
	return &Client{conn: conn, client: rpcClient}, nil
}

// Close closes the underlying connection.
func (c *Client) Close() error {
	if c.client != nil {
		_ = c.client.Close()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Start requests the daemon to start processing.
func (c *Client) Start() (*StartResponse, error) {
	var resp StartResponse
	if err := c.client.Call("Spindle.Start", StartRequest{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Stop requests the daemon to stop processing.
func (c *Client) Stop() (*StopResponse, error) {
	var resp StopResponse
	if err := c.client.Call("Spindle.Stop", StopRequest{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Status retrieves the daemon status.
func (c *Client) Status() (*StatusResponse, error) {
	var resp StatusResponse
	if err := c.client.Call("Spindle.Status", StatusRequest{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// LogTail returns log lines from the daemon.
func (c *Client) LogTail(req LogTailRequest) (*LogTailResponse, error) {
	var resp LogTailResponse
	if err := c.client.Call("Spindle.LogTail", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DatabaseHealth retrieves detailed database diagnostics.
func (c *Client) DatabaseHealth() (*DatabaseHealthResponse, error) {
	var resp DatabaseHealthResponse
	if err := c.client.Call("Spindle.DatabaseHealth", DatabaseHealthRequest{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// TestNotification triggers a notification test via the daemon.
func (c *Client) TestNotification() (*TestNotificationResponse, error) {
	var resp TestNotificationResponse
	if err := c.client.Call("Spindle.TestNotification", TestNotificationRequest{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// QueueList returns queue items optionally filtered by statuses.
func (c *Client) QueueList(statuses []string) (*QueueListResponse, error) {
	var resp QueueListResponse
	req := QueueListRequest{Statuses: statuses}
	if err := c.client.Call("Spindle.QueueList", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// QueueDescribe returns details for a single queue item.
func (c *Client) QueueDescribe(id int64) (*QueueDescribeResponse, error) {
	var resp QueueDescribeResponse
	req := QueueDescribeRequest{ID: id}
	if err := c.client.Call("Spindle.QueueDescribe", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// QueueClear removes all items from the queue.
func (c *Client) QueueClear() (*QueueClearResponse, error) {
	var resp QueueClearResponse
	if err := c.client.Call("Spindle.QueueClear", QueueClearRequest{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// QueueClearCompleted removes only completed items from the queue.
func (c *Client) QueueClearCompleted() (*QueueClearCompletedResponse, error) {
	var resp QueueClearCompletedResponse
	if err := c.client.Call("Spindle.QueueClearCompleted", QueueClearCompletedRequest{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// QueueClearFailed removes failed items from the queue.
func (c *Client) QueueClearFailed() (*QueueClearFailedResponse, error) {
	var resp QueueClearFailedResponse
	if err := c.client.Call("Spindle.QueueClearFailed", QueueClearFailedRequest{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// QueueReset resets items stuck in processing states.
func (c *Client) QueueReset() (*QueueResetResponse, error) {
	var resp QueueResetResponse
	if err := c.client.Call("Spindle.QueueReset", QueueResetRequest{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// QueueRetry retries failed items.
func (c *Client) QueueRetry(ids []int64) (*QueueRetryResponse, error) {
	var resp QueueRetryResponse
	req := QueueRetryRequest{IDs: ids}
	if err := c.client.Call("Spindle.QueueRetry", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// QueueHealth returns queue diagnostics.
func (c *Client) QueueHealth() (*QueueHealthResponse, error) {
	var resp QueueHealthResponse
	if err := c.client.Call("Spindle.QueueHealth", QueueHealthRequest{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
