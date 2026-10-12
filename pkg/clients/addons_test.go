package clients

import (
	"context"
	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"strings"
	"testing"
)

type addonHTTP struct {
	called       bool
	path         string
	contextValue interface{}
}

func (c *addonHTTP) Call(req *http.Request, _ *http.Transport) (*http.Response, error) {
	c.called = true
	c.path = req.URL.Path
	c.contextValue = req.Context().Value(offlineContextKey{})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"addons":[{"name":"terway-eniip","state":"active","version":"v1.16.4"}]}`))}, nil
}
func TestInstalledAddonsUsesNativeSDKAndCallerContext(t *testing.T) {
	native, err := cs.NewClient(localSDKConfig("http://offline.invalid"))
	require.NoError(t, err)
	transport := &addonHTTP{}
	native.HttpClient = transport
	adapter := NewDefaultCSClient(native).(*DefaultCSClient)
	ctx := context.WithValue(context.Background(), offlineContextKey{}, "present")
	response, err := adapter.ListClusterAddonInstances(ctx, "cluster-offline")
	require.NoError(t, err)
	require.True(t, transport.called)
	require.Equal(t, "/clusters/cluster-offline/addon_instances", transport.path)
	require.Equal(t, "present", transport.contextValue)
	require.Len(t, response.Body.Addons, 1)
	require.Equal(t, "v1.16.4", *response.Body.Addons[0].Version)
	transport.called = false
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = adapter.ListClusterAddonInstances(canceled, "cluster-offline")
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, transport.called)
}
