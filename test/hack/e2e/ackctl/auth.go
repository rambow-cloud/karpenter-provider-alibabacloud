package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"github.com/alibabacloud-go/tea/tea"
	"go.yaml.in/yaml/v3"
	"k8s.io/apimachinery/pkg/util/validation"
)

// STS values are read atomically from the runner environment. Never combine
// an environment AccessKey with a file secret, or persist a session token.
func loadCloudCredentials(c *CloudConfig) error {
	if c.Authentication != "" && c.Authentication != "sts" && c.Authentication != "access_key" {
		return fmt.Errorf("authentication must be sts or access_key")
	}
	if c.Authentication == "sts" && (c.AccessKeyID != "" || c.AccessKeySecret != "") {
		return fmt.Errorf("sts authentication forbids credentials in config files")
	}
	id, secret, token := os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_ID"), os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET"), os.Getenv("ALIBABA_CLOUD_SECURITY_TOKEN")
	if id != "" || secret != "" || token != "" || c.Authentication == "sts" {
		if strings.TrimSpace(id) == "" || strings.TrimSpace(secret) == "" || (c.Authentication == "sts" && strings.TrimSpace(token) == "") {
			return fmt.Errorf("incomplete environment cloud credentials; sts requires AccessKeyId, AccessKeySecret and SecurityToken")
		}
		if c.Authentication == "access_key" && token != "" {
			return fmt.Errorf("access_key authentication cannot consume an STS session")
		}
		c.AccessKeyID, c.AccessKeySecret, c.SecurityToken = id, secret, token
	}
	return nil
}

func cloudClientConfig(cfg *Config) (*openapi.Config, error) {
	if cfg == nil || strings.TrimSpace(cfg.AlibabaCloud.RegionID) == "" {
		return nil, fmt.Errorf("AlibabaCloud region is required")
	}
	c := cfg.AlibabaCloud
	if c.AccessKeyID == "" || c.AccessKeySecret == "" {
		return nil, fmt.Errorf("complete cloud credentials are required")
	}
	if c.Authentication == "sts" && c.SecurityToken == "" {
		return nil, fmt.Errorf("STS SecurityToken is required; refusing credential fallback")
	}
	if c.Authentication == "access_key" && c.SecurityToken != "" {
		return nil, fmt.Errorf("conflicting credential modes")
	}
	mode := "access_key"
	if c.SecurityToken != "" {
		mode = "sts"
	}
	return &openapi.Config{Type: tea.String(mode), AccessKeyId: tea.String(c.AccessKeyID), AccessKeySecret: tea.String(c.AccessKeySecret), SecurityToken: tea.String(c.SecurityToken), RegionId: tea.String(c.RegionID), ConnectTimeout: tea.Int(ackConnectTimeoutMilliseconds), ReadTimeout: tea.Int(30_000), MaxIdleConns: tea.Int(100)}, nil
}

type ControllerRRSAConfig struct {
	RoleARN            string            `yaml:"role_arn"`
	ProviderARN        string            `yaml:"oidc_provider_arn"`
	SystemNodeSelector map[string]string `yaml:"system_node_selector"`
	ImageRepository    string            `yaml:"image_repository"`
	ImageTag           string            `yaml:"image_tag"`
}

const rrsaTokenPath = "/var/run/secrets/ack.alibabacloud.com/rrsa-tokens/token"

func controllerRRSAValues(cfg *Config, clusterID, endpoint string) (map[string]interface{}, error) {
	if cfg == nil || cfg.ControllerRRSA == nil {
		return nil, fmt.Errorf("controller_rrsa configuration is required")
	}
	r := cfg.ControllerRRSA
	role := regexp.MustCompile(`^acs:ram::([0-9]+):role/[a-zA-Z0-9_-]+$`).FindStringSubmatch(r.RoleARN)
	provider := regexp.MustCompile(`^acs:ram::([0-9]+):oidc-provider/[a-zA-Z0-9._-]+$`).FindStringSubmatch(r.ProviderARN)
	if len(role) != 2 || len(provider) != 2 || role[1] != provider[1] {
		return nil, fmt.Errorf("RRSA role/provider must be valid same-account ARNs supported by the Chart")
	}
	if clusterID == "" || !strings.HasPrefix(endpoint, "https://") || cfg.AlibabaCloud.RegionID == "" {
		return nil, fmt.Errorf("RRSA deployment requires cluster ID, HTTPS endpoint and region")
	}
	if len(r.SystemNodeSelector) == 0 {
		return nil, fmt.Errorf("system_node_selector is required to prevent controller self-bootstrap")
	}
	for k, v := range r.SystemNodeSelector {
		if len(validation.IsQualifiedName(k)) != 0 || len(validation.IsValidLabelValue(v)) != 0 || k == "karpenter.sh/nodepool" {
			return nil, fmt.Errorf("invalid system node selector")
		}
	}
	if r.ImageRepository == "" || strings.ContainsAny(r.ImageRepository, " \t\n\r") || !regexp.MustCompile(`^[0-9a-f]{40}@sha256:[0-9a-f]{64}$`).MatchString(r.ImageTag) {
		return nil, fmt.Errorf("controller image requires repository and <git-sha>@sha256:<digest> tag")
	}
	env := []map[string]string{{"name": "ALIBABA_CLOUD_ROLE_ARN", "value": r.RoleARN}, {"name": "ALIBABA_CLOUD_OIDC_PROVIDER_ARN", "value": r.ProviderARN}, {"name": "ALIBABA_CLOUD_OIDC_TOKEN_FILE", "value": rrsaTokenPath}, {"name": "ALIBABA_CLOUD_ECS_METADATA_DISABLED", "value": "true"}}
	return map[string]interface{}{
		"serviceAccount": map[string]interface{}{"create": true, "name": "karpenter", "annotations": map[string]string{}},
		"controller":     map[string]interface{}{"credentialsSecretName": "", "env": env, "nodeSelector": r.SystemNodeSelector, "image": map[string]string{"repository": r.ImageRepository, "tag": r.ImageTag}},
		"settings":       map[string]string{"clusterID": clusterID, "clusterName": cfg.Cluster.Name, "clusterEndpoint": endpoint, "region": cfg.AlibabaCloud.RegionID},
	}, nil
}

func runRRSAValues(args []string) error {
	fs := flag.NewFlagSet("rrsa-values", flag.ContinueOnError)
	config := fs.String("config", "", "config path")
	endpoint := fs.String("endpoint", "", "actual cluster HTTPS endpoint")
	output := fs.String("output", "", "values output (no credentials)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *output == "" || *config == "" {
		return fmt.Errorf("--config and --output are required")
	}
	cfg, err := LoadConfig(*config)
	if err != nil {
		return err
	}
	v, err := controllerRRSAValues(cfg, cfg.AlibabaCloud.ClusterID, *endpoint)
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(*output, data, 0600)
}

// Verify the admitted Pod, not merely input Helm values. Webhook injection,
// envFrom or secret mounts could otherwise silently undermine an RRSA-only test.
func verifyRRSAPod(cfg *Config, data []byte) error {
	var p struct {
		Metadata struct {
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Spec struct {
			ServiceAccountName string            `json:"serviceAccountName"`
			NodeSelector       map[string]string `json:"nodeSelector"`
			Containers         []struct {
				Name    string        `json:"name"`
				EnvFrom []interface{} `json:"envFrom"`
				Env     []struct {
					Name      string      `json:"name"`
					Value     string      `json:"value"`
					ValueFrom interface{} `json:"valueFrom"`
				} `json:"env"`
				VolumeMounts []struct {
					Name      string `json:"name"`
					MountPath string `json:"mountPath"`
					ReadOnly  bool   `json:"readOnly"`
				} `json:"volumeMounts"`
			} `json:"containers"`
			Volumes []struct {
				Name      string      `json:"name"`
				Secret    interface{} `json:"secret"`
				Projected *struct {
					Sources []struct {
						Token *struct {
							Audience string `json:"audience"`
							Path     string `json:"path"`
						} `json:"serviceAccountToken"`
					} `json:"sources"`
				} `json:"projected"`
			} `json:"volumes"`
		} `json:"spec"`
	}
	if cfg.ControllerRRSA == nil {
		return fmt.Errorf("controller RRSA config is required")
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if p.Metadata.Namespace != "karpenter" || p.Spec.ServiceAccountName != "karpenter" {
		return fmt.Errorf("controller service account identity mismatch")
	}
	for k, v := range cfg.ControllerRRSA.SystemNodeSelector {
		if p.Spec.NodeSelector[k] != v {
			return fmt.Errorf("controller system selector mismatch")
		}
	}
	wanted := map[string]string{"ALIBABA_CLOUD_ROLE_ARN": cfg.ControllerRRSA.RoleARN, "ALIBABA_CLOUD_OIDC_PROVIDER_ARN": cfg.ControllerRRSA.ProviderARN, "ALIBABA_CLOUD_OIDC_TOKEN_FILE": rrsaTokenPath}
	seen := map[string]bool{}
	controller, mount, token := false, false, false
	for _, c := range p.Spec.Containers {
		if len(c.EnvFrom) != 0 {
			return fmt.Errorf("RRSA-only Pod must not use envFrom")
		}
		for _, e := range c.Env {
			if strings.HasPrefix(e.Name, "ALIBABA_CLOUD_ACCESS_KEY_") || e.Name == "ALIBABA_CLOUD_SECURITY_TOKEN" {
				if e.Value != "" || e.ValueFrom != nil {
					return fmt.Errorf("static or runner credentials found in controller Pod")
				}
			}
			if c.Name == "controller" {
				if expected, ok := wanted[e.Name]; ok {
					if seen[e.Name] || e.Value != expected || e.ValueFrom != nil {
						return fmt.Errorf("RRSA environment mismatch or duplication")
					}
					seen[e.Name] = true
				}
			}
		}
		if c.Name == "controller" {
			controller = true
			for _, m := range c.VolumeMounts {
				if m.Name == "rrsa-oidc-token" && m.MountPath == strings.TrimSuffix(rrsaTokenPath, "/token") && m.ReadOnly {
					mount = true
				}
			}
		}
	}
	for _, v := range p.Spec.Volumes {
		if v.Secret != nil {
			return fmt.Errorf("credential-capable secret mount found in controller Pod")
		}
		if v.Name == "rrsa-oidc-token" && v.Projected != nil {
			for _, s := range v.Projected.Sources {
				if s.Token != nil && s.Token.Audience == "sts.aliyuncs.com" && s.Token.Path == "token" {
					token = true
				}
			}
		}
	}
	if !controller || !mount || !token || len(seen) != len(wanted) {
		return fmt.Errorf("controller RRSA identity/token projection is incomplete")
	}
	return nil
}

func runCheckRRSAPod(args []string) error {
	fs := flag.NewFlagSet("check-rrsa-pod", flag.ContinueOnError)
	config := fs.String("config", "", "config path")
	input := fs.String("input", "", "admitted controller Pod JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := LoadConfig(*config)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(*input)
	if err != nil {
		return err
	}
	return verifyRRSAPod(cfg, data)
}
