package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
)

func TestACKCommandsRejectUnsupportedTargetsBeforeCloudOrOutput(t *testing.T) {
	for _, value := range []string{"1.33.9-aliyun.1", "1.9", "", "latest"} {
		t.Run(value, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.yaml")
			require.NoError(t, os.WriteFile(configPath, []byte(fmt.Sprintf("alibaba_cloud:\n  region_id: cn-hangzhou\ncluster:\n  name: minimum-test\n  kubernetes_version: %q\n", value)), 0600))
			envPath, manifestPath, renderedPath := filepath.Join(dir, "ack.env"), filepath.Join(dir, "manifest.yaml"), filepath.Join(dir, "rendered.yaml")
			// No dry-run and no credentials: invalid input must stop before GPU discovery/cloud clients.
			err := run([]string{"setup", "--config", configPath, "--output-env", envPath, "--manifest", manifestPath, "--discover-gpu"})
			require.ErrorContains(t, err, "Kubernetes")
			require.NoFileExists(t, envPath)
			require.NoFileExists(t, manifestPath)
			require.ErrorContains(t, run([]string{"render-config", "--config", configPath, "--output", renderedPath}), "Kubernetes")
			require.NoFileExists(t, renderedPath)
			for _, command := range []string{"ensure-nodepool", "ensure-capacity-reservation"} {
				require.ErrorContains(t, run([]string{command, "--config", configPath, "--manifest", manifestPath}), "Kubernetes")
			}
			cfg, err := LoadConfig(configPath)
			require.NoError(t, err, "loading config for cleanup/read-only commands must remain possible")
			_, _, _, err = SetupACKCluster(context.Background(), cfg, "minimum-test", "test")
			require.ErrorContains(t, err, "Kubernetes")
			require.Empty(t, cfg.Cluster.NodePool.LoginPassword)
		})
	}
}

func TestRenderConfigValidatesTheEffectiveOverride(t *testing.T) {
	dir := t.TempDir()
	input, output := filepath.Join(dir, "input.yaml"), filepath.Join(dir, "output.yaml")
	require.NoError(t, os.WriteFile(input, []byte("alibaba_cloud:\n  region_id: cn-hangzhou\ncluster:\n  name: test\n  kubernetes_version: '1.32'\n"), 0600))
	require.NoError(t, run([]string{"render-config", "--config", input, "--output", output, "--k8s-version", "1.34.10-aliyun.1"}))
	cfg, err := LoadConfig(output)
	require.NoError(t, err)
	require.Equal(t, "1.34.10-aliyun.1", cfg.Cluster.KubernetesVersion)
	require.ErrorContains(t, run([]string{"render-config", "--config", output, "--output", filepath.Join(dir, "invalid.yaml"), "--k8s-version", "1.33"}), "requires Kubernetes >= 1.34")
}

func TestVersionMatrixRejectsInvalidOrUnsupportedInput(t *testing.T) {
	for _, input := range []string{"", "null", "[]", `{"version":"1.34"}`, `[1.34]`, `["1.34", "1.33"]`, `["1.34", "1.34"]`, `["1.34", ""]`} {
		_, err := validateVersionList(input)
		require.Error(t, err, input)
	}
	values, err := validateVersionList(`["1.34.10-aliyun.1", "1.35", "1.36"]`)
	require.NoError(t, err)
	require.Equal(t, []string{"1.34.10-aliyun.1", "1.35", "1.36"}, values)
}

type fakeACKClusterDetail func(*string) (*cs.DescribeClusterDetailResponse, error)

func (f fakeACKClusterDetail) DescribeClusterDetail(id *string) (*cs.DescribeClusterDetailResponse, error) {
	return f(id)
}

func TestRunningACKClusterMustActuallyMeetTheVersionMinimum(t *testing.T) {
	for _, value := range []string{"1.33.9-aliyun.1", "", "1.34.10-aliyun.1", "1.35.0"} {
		t.Run(value, func(t *testing.T) {
			calls := 0
			client := fakeACKClusterDetail(func(id *string) (*cs.DescribeClusterDetailResponse, error) {
				calls++
				require.Equal(t, "c-test", tea.StringValue(id))
				return &cs.DescribeClusterDetailResponse{Body: &cs.DescribeClusterDetailResponseBody{State: tea.String("running"), CurrentVersion: tea.String(value)}}, nil
			})
			err := waitForClusterRunning(context.Background(), client, "c-test", time.Second)
			if value == "1.33.9-aliyun.1" || value == "" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, 1, calls)
		})
	}
}

func TestObservedACKVersionIsRetainedForReleaseEvidence(t *testing.T) {
	resources := ExtractClusterResources(&cs.DescribeClusterDetailResponseBody{CurrentVersion: tea.String("1.34.10-aliyun.1")})
	manifest := &Manifest{KubernetesVersion: resources.KubernetesVersion}
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	require.NoError(t, SaveManifest(path, manifest))
	loaded, err := LoadManifest(path)
	require.NoError(t, err)
	require.Equal(t, "1.34.10-aliyun.1", loaded.KubernetesVersion)
	require.Equal(t, loaded.KubernetesVersion, envValues(&Config{}, loaded, "config.yaml", "", nil, resources)["TEST_KUBERNETES_VERSION"])
}
