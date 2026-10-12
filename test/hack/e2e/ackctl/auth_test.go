package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
)

func rrsaTestConfig() *Config {
	return &Config{AlibabaCloud: CloudConfig{RegionID: "cn-hangzhou", ClusterID: "c-test"}, Cluster: ClusterConfig{Name: "ack-test"}, ControllerRRSA: &ControllerRRSAConfig{RoleARN: "acs:ram::123:role/controller", ProviderARN: "acs:ram::123:oidc-provider/ack-rrsa-c-test", SystemNodeSelector: map[string]string{"e2e.example/system": "true"}, ImageRepository: "example.com/controller", ImageTag: strings.Repeat("a", 40) + "@sha256:" + strings.Repeat("b", 64)}}
}

func TestSTSCredentialsAreAtomicAndNeverPersisted(t *testing.T) {
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_ID", "session-id")
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET", "session-secret")
	t.Setenv("ALIBABA_CLOUD_SECURITY_TOKEN", "session-token")
	cfg := rrsaTestConfig()
	cfg.AlibabaCloud.Authentication = "sts"
	require.NoError(t, loadCloudCredentials(&cfg.AlibabaCloud))
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, SaveConfig(path, cfg))
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, secret := range []string{"session-id", "session-secret", "session-token"} {
		require.NotContains(t, string(b), secret)
		require.NotContains(t, cfg.RedactedString(), secret)
	}
	for _, missing := range []string{"ALIBABA_CLOUD_ACCESS_KEY_ID", "ALIBABA_CLOUD_ACCESS_KEY_SECRET", "ALIBABA_CLOUD_SECURITY_TOKEN"} {
		t.Run(missing, func(t *testing.T) {
			t.Setenv(missing, "")
			c := CloudConfig{Authentication: "sts"}
			require.Error(t, loadCloudCredentials(&c))
		})
	}
	require.Error(t, loadCloudCredentials(&CloudConfig{Authentication: "sts", AccessKeyID: "file-id", AccessKeySecret: "file-secret"}))
	require.Error(t, loadCloudCredentials(&CloudConfig{Authentication: "access_key"}))
	implicit := CloudConfig{}
	require.NoError(t, loadCloudCredentials(&implicit))
	require.Equal(t, "sts", implicit.Authentication)
	require.Error(t, loadCloudCredentials(&CloudConfig{AccessKeyID: "file-id", AccessKeySecret: "file-secret"}))
}

func TestEveryACKClientConsumesSTSAndNewSessions(t *testing.T) {
	for _, token := range []string{"session-one", "session-two"} {
		cfg := &Config{AlibabaCloud: CloudConfig{RegionID: "cn-hangzhou", Authentication: "sts", AccessKeyID: "id", AccessKeySecret: "secret", SecurityToken: token}}
		cs, err := newCSClient(cfg)
		require.NoError(t, err)
		ecs, err := newECSClient(cfg)
		require.NoError(t, err)
		vpc, err := newVPCClient(cfg)
		require.NoError(t, err)
		slb, err := newSLBClient(cfg)
		require.NoError(t, err)
		for _, credential := range []interface{ GetSecurityToken() (*string, error) }{cs.Credential, ecs.Credential, vpc.Credential, slb.Credential} {
			got, err := credential.GetSecurityToken()
			require.NoError(t, err)
			require.Equal(t, token, tea.StringValue(got))
		}
		cfg.AlibabaCloud.SecurityToken = ""
		_, err = cloudClientConfig(cfg)
		require.Error(t, err)
	}
}

func TestRRSAValuesRejectBadIdentityAndExcludeRunnerCredentials(t *testing.T) {
	cfg := rrsaTestConfig()
	cfg.AlibabaCloud.AccessKeyID = "runner-id"
	cfg.AlibabaCloud.AccessKeySecret = "runner-secret"
	cfg.AlibabaCloud.SecurityToken = "runner-token"
	v, err := controllerRRSAValues(cfg, "c-test", "https://ack.example")
	require.NoError(t, err)
	b, err := json.Marshal(v)
	require.NoError(t, err)
	for _, s := range []string{"runner-id", "runner-secret", "runner-token", "secretKeyRef"} {
		require.NotContains(t, string(b), s)
	}
	for _, mutate := range []func(*ControllerRRSAConfig){func(r *ControllerRRSAConfig) { r.ProviderARN = "acs:ram::456:oidc-provider/foreign" }, func(r *ControllerRRSAConfig) { r.SystemNodeSelector = nil }, func(r *ControllerRRSAConfig) { r.ImageTag = "latest" }} {
		c := rrsaTestConfig()
		mutate(c.ControllerRRSA)
		_, err := controllerRRSAValues(c, "c-test", "https://ack.example")
		require.Error(t, err)
	}
}

func TestAdmittedRRSAPodRejectsSecretInjectionAndWrongAudience(t *testing.T) {
	base := `{"metadata":{"namespace":"karpenter"},"spec":{"serviceAccountName":"karpenter","nodeSelector":{"e2e.example/system":"true"},"containers":[{"name":"controller","env":[{"name":"ALIBABA_CLOUD_ROLE_ARN","value":"acs:ram::123:role/controller"},{"name":"ALIBABA_CLOUD_OIDC_PROVIDER_ARN","value":"acs:ram::123:oidc-provider/ack-rrsa-c-test"},{"name":"ALIBABA_CLOUD_OIDC_TOKEN_FILE","value":"/var/run/secrets/ack.alibabacloud.com/rrsa-tokens/token"}],"volumeMounts":[{"name":"rrsa-oidc-token","mountPath":"/var/run/secrets/ack.alibabacloud.com/rrsa-tokens","readOnly":true}]}],"volumes":[{"name":"rrsa-oidc-token","projected":{"sources":[{"serviceAccountToken":{"audience":"sts.aliyuncs.com","path":"token"}}]}}]}}`
	require.NoError(t, verifyRRSAPod(rrsaTestConfig(), []byte(base)))
	require.Error(t, verifyRRSAPod(rrsaTestConfig(), []byte(strings.Replace(base, `"serviceAccountName"`, `"initContainers":[{"name":"injected"}],"serviceAccountName"`, 1))))
	for _, invalid := range []string{strings.Replace(base, "sts.aliyuncs.com", "foreign", 1), strings.Replace(base, `"serviceAccountName":"karpenter"`, `"serviceAccountName":"wrong"`, 1), strings.Replace(base, `"name":"controller"`, `"name":"controller","envFrom":[{"secretRef":{"name":"runner"}}]`, 1), strings.Replace(base, `"name":"ALIBABA_CLOUD_ROLE_ARN"`, `"name":"ALIBABA_CLOUD_ACCESS_KEY_ID"`, 1)} {
		require.Error(t, verifyRRSAPod(rrsaTestConfig(), []byte(invalid)))
	}
}
