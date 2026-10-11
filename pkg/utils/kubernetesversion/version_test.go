package kubernetesversion

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/version"
)

func TestValidateKubernetesReleaseBoundary(t *testing.T) {
	for _, value := range []string{"1.34", "v1.34", "1.34.0", "v1.34.10-aliyun.1", "1.34.0-aliyun.1", "1.35.0+k3s1", "1.36", "1.100.0"} {
		t.Run(value, func(t *testing.T) { require.NoError(t, Validate(value)) })
	}
	for _, value := range []string{"1.9", "1.30.1-aliyun.1", "1.33.999", "v1.33.9-aliyun.1", "0.99.0"} {
		t.Run(value, func(t *testing.T) { require.ErrorContains(t, Validate(value), "requires Kubernetes >= 1.34") })
	}
	for _, value := range []string{"", "1", "latest", "1.34garbage", "1.34.0.1", "1.034.0", "1.34.0-aliyun..1", "1.34.0\nmalformed"} {
		t.Run(value, func(t *testing.T) { require.Error(t, Validate(value)) })
	}
}

type serverVersionResult struct {
	info *version.Info
	err  error
}

func (s serverVersionResult) ServerVersion() (*version.Info, error) { return s.info, s.err }

func TestValidateServerUsesDiscoveredVersionAndPropagatesFailure(t *testing.T) {
	err := errors.New("API server unavailable")
	require.ErrorIs(t, ValidateServer(serverVersionResult{err: err}), err)
	require.ErrorContains(t, ValidateServer(serverVersionResult{}), "no version information")
	require.ErrorContains(t, ValidateServer(serverVersionResult{info: &version.Info{GitVersion: "v1.33.9-aliyun.1"}}), "unsupported")
	require.NoError(t, ValidateServer(serverVersionResult{info: &version.Info{GitVersion: "v1.34.0-aliyun.1"}}))
}
