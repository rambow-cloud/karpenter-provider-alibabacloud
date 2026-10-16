package clients

import (
	"context"
	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Exercise SDK serialization and context transport, without cloud credentials.
func TestNativeSDKNodePoolReadRequests(t *testing.T) {
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		require.Equal(t, "GET", r.Method)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/clusters/c-test/nodepools" {
			_, _ = w.Write([]byte(`{"nodepools":[{"nodepool_info":{"nodepool_id":"np-test","is_default":true}}]}`))
		} else {
			_, _ = w.Write([]byte(`{"nodepool_info":{"nodepool_id":"np-test","type":"ess"},"status":{"state":"active"}}`))
		}
	}))
	defer server.Close()
	native, err := cs.NewClient(localSDKConfig(server.URL))
	require.NoError(t, err)
	adapter := NewDefaultCSClient(native).(*DefaultCSClient)
	list, err := adapter.DescribeClusterNodePools(context.Background(), "c-test")
	require.NoError(t, err)
	require.Len(t, list.Body.Nodepools, 1)
	detail, err := adapter.DescribeClusterNodePoolDetail(context.Background(), "c-test", "np-test")
	require.NoError(t, err)
	require.Equal(t, "np-test", *detail.Body.NodepoolInfo.NodepoolId)
	require.Equal(t, []string{"/clusters/c-test/nodepools", "/clusters/c-test/nodepools/np-test"}, paths)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = adapter.DescribeClusterNodePoolDetail(ctx, "c-test", "np-test")
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, paths, 2)
}

func TestNativeSDKRemovalUsesExactInstanceAndNoSecondDrain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "DELETE", r.Method)
		require.Equal(t, "/clusters/c-test/nodepools/np-test/nodes", r.URL.Path)
		require.Equal(t, `["i-owned"]`, r.URL.Query().Get("instance_ids"))
		require.Equal(t, "false", r.URL.Query().Get("drain_node"))
		require.Equal(t, "false", r.URL.Query().Get("release_node"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"T-owned"}`))
	}))
	defer server.Close()
	native, err := cs.NewClient(localSDKConfig(server.URL))
	require.NoError(t, err)
	adapter := NewDefaultCSClient(native).(*DefaultCSClient)
	response, err := adapter.RemoveNodePoolNodes(context.Background(), "c-test", "np-test", &cs.RemoveNodePoolNodesRequest{InstanceIds: tea.StringSlice([]string{"i-owned"}), DrainNode: tea.Bool(false), ReleaseNode: tea.Bool(false)})
	require.NoError(t, err)
	require.Equal(t, "T-owned", *response.Body.TaskId)
}
