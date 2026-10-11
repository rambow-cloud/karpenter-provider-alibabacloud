package clients

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
)

func metadataRequest() *cs.DescribeKubernetesVersionMetadataRequest {
	return &cs.DescribeKubernetesVersionMetadataRequest{Region: tea.String("cn-hangzhou"), ClusterType: tea.String("ManagedKubernetes"), KubernetesVersion: tea.String("1.34.10-aliyun.1")}
}

func localSDKConfig(endpoint string) *openapi.Config {
	return &openapi.Config{AccessKeyId: tea.String("offline-key"), AccessKeySecret: tea.String("offline-secret"), Endpoint: tea.String(strings.TrimPrefix(endpoint, "http://")), Protocol: tea.String("http"), RegionId: tea.String("cn-hangzhou")}
}

func TestNativeSDKCancellation(t *testing.T) {
	for _, operation := range []string{"CS metadata", "ECS images"} {
		t.Run(operation, func(t *testing.T) {
			started, disconnected := make(chan struct{}), make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				close(started)
				select {
				case <-r.Context().Done():
					close(disconnected)
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			config := localSDKConfig(server.URL)
			var call func(context.Context) error
			if operation == "CS metadata" {
				native, err := cs.NewClient(config)
				require.NoError(t, err)
				adapter := NewDefaultCSClient(native)
				require.Nil(t, native.HttpClient, "adapter must not mutate caller's SDK client")
				call = func(ctx context.Context) error {
					_, err := adapter.DescribeKubernetesVersionMetadata(ctx, metadataRequest())
					return err
				}
			} else {
				native, err := ecs.NewClient(config)
				require.NoError(t, err)
				adapter := NewDefaultECSClient(native, "cn-hangzhou")
				call = func(ctx context.Context) error {
					_, err := adapter.DescribeImages(ctx, []string{"m-offline"}, nil)
					return err
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- call(ctx) }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("SDK request did not reach server")
			}
			cancel()
			select {
			case err := <-done:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(3 * time.Second):
				t.Fatal("cancellation did not interrupt SDK HTTP")
			}
			select {
			case <-disconnected:
			case <-time.After(3 * time.Second):
				t.Fatal("HTTP continued after cancellation")
			}
		})
	}
}

func TestCanceledSDKDoesNotSendAndConcurrentContextsAreIndependent(t *testing.T) {
	started := make(chan struct{}, 1)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Query().Get("KubernetesVersion") == "hold" {
			started <- struct{}{}
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	native, err := cs.NewClient(localSDKConfig(server.URL))
	require.NoError(t, err)
	adapter := NewDefaultCSClient(native)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = adapter.DescribeKubernetesVersionMetadata(ctx, metadataRequest())
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, requests.Load())
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	request := metadataRequest()
	request.KubernetesVersion = tea.String("hold")
	go func() { _, err := adapter.DescribeKubernetesVersionMetadata(ctx, request); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("held request did not start")
	}
	_, err = adapter.DescribeKubernetesVersionMetadata(context.Background(), metadataRequest())
	require.NoError(t, err)
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("held request did not cancel")
	}
	_, err = adapter.DescribeKubernetesVersionMetadata(context.Background(), metadataRequest())
	require.NoError(t, err, "canceled context must not leak into later calls")
}

type recordingHTTPClient struct{ called bool }

type offlineContextKey struct{}

func (c *recordingHTTPClient) Call(req *http.Request, transport *http.Transport) (*http.Response, error) {
	c.called = true
	if req.Context().Value(offlineContextKey{}) != "present" {
		return nil, errors.New("missing caller context")
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`[]`))}, nil
}

func TestSDKPreservesExplicitHTTPClientAndBoundsDefaultHTTP(t *testing.T) {
	native, err := cs.NewClient(localSDKConfig("http://offline.invalid"))
	require.NoError(t, err)
	custom := &recordingHTTPClient{}
	native.HttpClient = custom
	adapter := NewDefaultCSClient(native)
	ctx := context.WithValue(context.Background(), offlineContextKey{}, "present")
	_, err = adapter.DescribeKubernetesVersionMetadata(ctx, metadataRequest())
	require.NoError(t, err)
	require.True(t, custom.called)
	require.Same(t, custom, native.HttpClient)

	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { started <- struct{}{}; <-r.Context().Done() }))
	defer server.Close()
	config := localSDKConfig(server.URL)
	config.ConnectTimeout, config.ReadTimeout = tea.Int(50), tea.Int(50)
	native, err = cs.NewClient(config)
	require.NoError(t, err)
	adapter = NewDefaultCSClient(native)
	done := make(chan error, 1)
	go func() {
		_, err := adapter.DescribeKubernetesVersionMetadata(context.Background(), metadataRequest())
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("bounded request did not start")
	}
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP timeout was unbounded")
	}
}
