package imagefamily

import (
	"context"
	"fmt"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"regexp"
	"strconv"
	"strings"
)

type addonLister interface {
	ListClusterAddonInstances(context.Context, string) (*cs.ListClusterAddonInstancesResponse, error)
}
type addonSnapshot struct {
	loaded bool
	addons []*cs.ListClusterAddonInstancesResponseBodyAddons
	err    error
}

var terwayVersion = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:\.\d+)?(?:-[a-zA-Z0-9.-]+)?$`)
var prereleaseVersion = regexp.MustCompile(`(?i)(?:^|[.-])(?:alpha|beta|rc)(?:[.-]|[0-9]|$)`)
var nvidiaModel = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:nvidia|tesla|T4|V100|A100|A10|P100|P4|RTX|H100|H200|A800|H800|L20|L40|L4)(?:[^a-z0-9]|$)`)

func supportedTerway(raw string) bool {
	if prereleaseVersion.MatchString(raw) {
		return false
	}
	m := terwayVersion.FindStringSubmatch(raw)
	if m == nil {
		return false
	}
	major, e1 := strconv.Atoi(m[1])
	minor, e2 := strconv.Atoi(m[2])
	patch, e3 := strconv.Atoi(m[3])
	if e1 != nil || e2 != nil || e3 != nil {
		return false
	}
	return major > 1 || (major == 1 && (minor > 16 || minor == 16 && patch >= 4 || minor == 9 && patch >= 18))
}
func isLinux4(img v1alpha1.Image) bool {
	return strings.HasPrefix(img.ID, "aliyun_4_") || strings.Contains(strings.ToLower(img.ImageType), "linux4")
}
func isUbuntu(img v1alpha1.Image) bool {
	return strings.HasPrefix(img.ID, "ubuntu_") || strings.Contains(strings.ToLower(img.ImageType), "ubuntu")
}
func isGPUImage(img v1alpha1.Image) bool {
	return strings.Contains(strings.ToLower(img.ImageType), "gpu")
}

func (p *Provider) validatePrerequisites(ctx context.Context, spec v1alpha1.ECSNodeClassSpec, img v1alpha1.Image, snapshot *addonSnapshot) error {
	if spec.ImageCompatibilityPolicy == "Custom" || isGPUImage(img) || isUbuntu(img) {
		if !spec.ImagePrerequisitesVerified {
			return fmt.Errorf("image %s requires imagePrerequisitesVerified=true after validating custom provenance, GPU driver/runtime/device-plugin or Ubuntu integration restrictions", img.ID)
		}
	}
	if !isLinux4(img) {
		return nil
	}
	if !snapshot.loaded {
		snapshot.loaded = true
		lister, ok := p.ack.client.(addonLister)
		if !ok {
			snapshot.err = fmt.Errorf("CS client cannot list installed addons; OS prerequisites cannot be verified")
		} else {
			response, err := lister.ListClusterAddonInstances(ctx, p.ack.clusterID)
			if err != nil {
				snapshot.err = fmt.Errorf("list installed ACK addons: %w", err)
			} else if response == nil || response.Body == nil || response.Body.Addons == nil {
				snapshot.err = fmt.Errorf("incomplete installed ACK addon metadata")
			} else {
				snapshot.addons = response.Body.Addons
			}
		}
	}
	if snapshot.err != nil {
		return snapshot.err
	}
	networks := 0
	for _, addon := range snapshot.addons {
		if addon == nil || tea.StringValue(addon.Name) == "" {
			return fmt.Errorf("incomplete installed addon entry")
		}
		name := *addon.Name
		if isLinux4(img) && (name == "terway" || name == "terway-eniip" || name == "kube-flannel-ds" || name == "kube-flannel-ds-vxlan") {
			networks++
			if tea.StringValue(addon.State) != "active" {
				return fmt.Errorf("network addon %s is not active", name)
			}
			if strings.HasPrefix(name, "terway") && !supportedTerway(tea.StringValue(addon.Version)) {
				return fmt.Errorf("阿里云 Linux 4 requires Terway [1.9.18,1.10.0) or >=1.16.4; installed %s", tea.StringValue(addon.Version))
			}
		}
	}
	if isLinux4(img) && networks != 1 {
		return fmt.Errorf("阿里云 Linux 4 requires exactly one known active network addon")
	}
	return nil
}

// The acceleration label applies to this NodePool/NodeClaim. Merely installing
// an addon elsewhere in the cluster does not prove it applies to Ubuntu nodes.
func ValidateNodeLabels(img v1alpha1.Image, labels map[string]string) error {
	if isUbuntu(img) && strings.EqualFold(labels["alibabacloud.com/image-accelerate-enabled"], "true") {
		return fmt.Errorf("Ubuntu cannot enable alibabacloud.com/image-accelerate-enabled")
	}
	return nil
}

// ValidateHardware pairs each image with a real SKU; missing local-disk metadata
// does not prove a diskless instance. Attestation cannot waive known restrictions.
func ValidateHardware(spec v1alpha1.ECSNodeClassSpec, img v1alpha1.Image, it *instancetype.InstanceType) error {
	if it == nil {
		return fmt.Errorf("instance hardware metadata unavailable")
	}
	gpu := it.GPU != nil && it.GPU.Count != nil && it.GPU.Count.Sign() > 0
	if gpu && !nvidiaModel.MatchString(it.GPU.Model) {
		return fmt.Errorf("unknown/non-NVIDIA GPU model cannot be advertised as nvidia.com/gpu")
	}
	if isGPUImage(img) && !gpu {
		return fmt.Errorf("GPU image requires known GPU hardware")
	}
	if gpu && spec.ImageCompatibilityPolicy != "Custom" && !isGPUImage(img) {
		return fmt.Errorf("GPU hardware requires a matching GPU image or a verified Custom image")
	}
	if gpu && !spec.ImagePrerequisitesVerified {
		return fmt.Errorf("GPU driver/runtime/device-plugin prerequisites require explicit verification")
	}
	if strings.HasPrefix(img.ImageType, "ContainerOS") {
		if !it.LocalDiskKnown || it.LocalDiskCount < 0 || it.LocalDiskCapacityGiB < 0 {
			return fmt.Errorf("ContainerOS instance %s has unknown local-disk metadata", it.Name)
		}
		if it.LocalDiskCount > 0 || it.LocalDiskCapacityGiB > 0 {
			return fmt.Errorf("ContainerOS does not support instance-local disks")
		}
	}
	return nil
}
