// compatibility validates the deployment artifacts against the pinned upstream modules.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
)

type module struct{ Path, Version, Dir string }
type rule struct {
	APIGroups []string `yaml:"apiGroups"`
	Resources []string `yaml:"resources"`
	Verbs     []string `yaml:"verbs"`
}
type manifest struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Rules []rule `yaml:"rules"`
	Spec  struct {
		Template struct {
			Spec struct {
				Containers []struct {
					Args []string `yaml:"args"`
					Env  []struct {
						Name  string `yaml:"name"`
						Value string `yaml:"value"`
					} `yaml:"env"`
				} `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

func run(name string, args ...string) ([]byte, error) {
	command := exec.Command(name, args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %v: %w: %s", name, args, err, stderr.String())
	}
	return output, nil
}
func loadModule(path string) (module, error) {
	data, err := run("go", "list", "-m", "-json", path)
	if err != nil {
		return module{}, err
	}
	var m module
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.Dir == "" {
		return m, fmt.Errorf("module %s is not downloaded; run go mod download", path)
	}
	return m, nil
}
func manifests(data []byte) ([]manifest, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var result []manifest
	for {
		var item manifest
		err := decoder.Decode(&item)
		if err == io.EOF {
			return result, nil
		}
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
}
func permissions(rules []rule) map[string]bool {
	result := map[string]bool{}
	for _, r := range rules {
		for _, g := range r.APIGroups {
			for _, resource := range r.Resources {
				for _, verb := range r.Verbs {
					result[g+"/"+resource+"/"+verb] = true
				}
			}
		}
	}
	return result
}
func checkKWOKChart(core module) error {
	data, err := run("helm", "template", "karpenter", filepath.Join(core.Dir, "kwok", "charts"),
		"--namespace", "kube-system", "--values", "test/hack/kwok/values.yaml")
	if err != nil {
		return err
	}
	items, err := manifests(data)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Kind != "Deployment" {
			continue
		}
		for _, container := range item.Spec.Template.Spec.Containers {
			for _, env := range container.Env {
				if env.Name != "FEATURE_GATES" {
					continue
				}
				gates, err := coreoptions.ParseFeatureGates(env.Value)
				if err != nil {
					return fmt.Errorf("KWOK chart FEATURE_GATES %q: %w", env.Value, err)
				}
				if !gates.StaticCapacity || !gates.CapacityBuffer || !gates.NodeRepair {
					return fmt.Errorf("KWOK regression requires StaticCapacity, CapacityBuffer and NodeRepair")
				}
				fmt.Printf("KWOK chart gates passed core %s parsing: %s\n", core.Version, env.Value)
				return nil
			}
		}
	}
	return fmt.Errorf("KWOK chart has no deployment FEATURE_GATES")
}
func check(sync, kwokOnly bool) error {
	core, err := loadModule("sigs.k8s.io/karpenter")
	if err != nil {
		return err
	}
	if core.Version != "v1.14.1" {
		return fmt.Errorf("expected stable core v1.14.1, got %s", core.Version)
	}
	if kwokOnly {
		return checkKWOKChart(core)
	}
	aws, err := loadModule("github.com/aws/karpenter-provider-aws")
	if err != nil {
		return err
	}
	if core.Version != "v1.14.1" || aws.Version != core.Version {
		return fmt.Errorf("expected stable core/AWS harness v1.14.1, got %s / %s", core.Version, aws.Version)
	}
	files, err := filepath.Glob(filepath.Join(core.Dir, "pkg", "apis", "crds", "*.yaml"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("upstream module has no CRDs")
	}
	for _, source := range files {
		upstream, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		for _, directory := range []string{"charts/karpenter/crds", "pkg/apis/crds"} {
			target := filepath.Join(directory, filepath.Base(source))
			if sync {
				if err := os.WriteFile(target, upstream, 0644); err != nil {
					return err
				}
			} else {
				current, err := os.ReadFile(target)
				if err != nil {
					return err
				}
				if !bytes.Equal(current, upstream) {
					return fmt.Errorf("%s differs from upstream %s; run make sync-core-crds", target, core.Version)
				}
			}
		}
	}
	if sync {
		fmt.Printf("Synchronized %d CRDs from core %s\n", len(files), core.Version)
		return nil
	}
	actualData, err := run("helm", "template", "karpenter", "charts/karpenter", "--namespace", "karpenter",
		"--set-string", "settings.featureGates=NodeOverlay=true",
		"--set-string", "settings.providerFeatureGates=DeploymentSet=true")
	if err != nil {
		return err
	}
	expectedData, err := run("helm", "template", "reference", filepath.Join(aws.Dir, "charts", "karpenter"), "--namespace", "karpenter", "--set-string", "settings.clusterName=compatibility-test")
	if err != nil {
		return err
	}
	actual, err := manifests(actualData)
	if err != nil {
		return err
	}
	expected, err := manifests(expectedData)
	if err != nil {
		return err
	}
	var actualRules, expectedRules []rule
	coreFound, separateGates := false, false
	for _, item := range expected {
		if item.Kind == "ClusterRole" && strings.HasSuffix(item.Metadata.Name, "-core") {
			expectedRules = append(expectedRules, item.Rules...)
			coreFound = true
		}
	}
	for _, item := range actual {
		if item.Kind == "ClusterRole" {
			actualRules = append(actualRules, item.Rules...)
		}
		if item.Kind == "Deployment" {
			for _, container := range item.Spec.Template.Spec.Containers {
				args := map[string]bool{}
				for _, arg := range container.Args {
					args[arg] = true
				}
				if args["--feature-gates=NodeOverlay=true"] && args["--provider-feature-gates=DeploymentSet=true"] {
					separateGates = true
				}
			}
		}
	}
	if !coreFound {
		return fmt.Errorf("upstream chart has no core ClusterRole")
	}
	granted := permissions(actualRules)
	for permission := range permissions(expectedRules) {
		if !granted[permission] {
			return fmt.Errorf("Helm is missing upstream core permission: %s", permission)
		}
	}
	if !separateGates {
		return fmt.Errorf("Helm must render independent core and provider feature-gate flags")
	}
	if err := checkKWOKChart(core); err != nil {
		return err
	}
	fmt.Printf("Compatibility passed: core/harness %s, %d synchronized CRDs, upstream core RBAC, separate feature gates\n", core.Version, len(files))
	return nil
}
func main() {
	sync := flag.Bool("sync", false, "copy pinned upstream CRDs without rendering Helm charts")
	kwokOnly := flag.Bool("kwok-only", false, "validate KWOK Helm feature gates with the pinned core parser")
	flag.Parse()
	if err := check(*sync, *kwokOnly); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
