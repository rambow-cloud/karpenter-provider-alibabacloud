package clients

import (
	"context"
	"net/http"
	"sync"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/alibabacloud-go/tea/tea"
)

// Alibaba Cloud SDK timeout fields are milliseconds, not seconds.
const (
	SDKConnectTimeoutMillis = 30_000
	SDKReadTimeoutMillis    = 30_000
)

// sdkHTTPClient retains the SDK-built proxy/TLS/dial configuration and reuses
// its transport across calls. Each adapter has an immutable endpoint/config.
type sdkHTTPClient struct {
	once    sync.Once
	client  *http.Client
	timeout time.Duration
}

func (c *sdkHTTPClient) Call(req *http.Request, transport *http.Transport) (*http.Response, error) {
	c.once.Do(func() {
		transport.IdleConnTimeout = 90 * time.Second
		transport.TLSHandshakeTimeout = 10 * time.Second
		c.client = &http.Client{Transport: transport, Timeout: c.timeout}
	})
	return c.client.Do(req)
}

func prepareHTTPClient(config *openapi.Client) {
	if config.HttpClient != nil {
		return // Preserve callers' explicitly configured HTTP clients.
	}
	connect, read := tea.IntValue(config.ConnectTimeout), tea.IntValue(config.ReadTimeout)
	if connect <= 0 {
		connect = SDKConnectTimeoutMillis
	}
	if read <= 0 {
		read = SDKReadTimeoutMillis
	}
	config.ConnectTimeout, config.ReadTimeout = tea.Int(connect), tea.Int(read)
	config.HttpClient = &sdkHTTPClient{timeout: time.Duration(connect+read) * time.Millisecond}
}

type contextHTTPClient struct {
	ctx  context.Context
	base dara.HttpClient
}

func (c contextHTTPClient) Call(req *http.Request, transport *http.Transport) (*http.Response, error) {
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	return c.base.Call(req.WithContext(c.ctx), transport)
}

// callSDK checks cancellation before signing/credential lookup and normalizes
// generated SDK error wrappers. HTTP cancellation cannot undo requests already
// accepted by the service or interrupt the SDK's context-free credential refresh.
func callSDK[T any](ctx context.Context, call func() (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	response, err := call()
	if err != nil && ctx.Err() != nil {
		return response, ctx.Err()
	}
	return response, err
}
