package imagefamily

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
)

type Option func(*Provider)

type ackCompatibility struct {
	client            clients.CSClient
	region, clusterID string
	gate              chan struct{}
	key               string
	expires           time.Time
	images            []ecs.DescribeImagesResponseBodyImagesImage
	types             map[string]string
}

// WithACKCompatibility enables the production ACK compatibility gate.
func WithACKCompatibility(client clients.CSClient, region, clusterID string) Option {
	return func(p *Provider) {
		p.ack = &ackCompatibility{client: client, region: region, clusterID: clusterID, gate: make(chan struct{}, 1)}
	}
}

func (p *Provider) ACKCompatibilityEnabled() bool { return p.ack != nil }

// ResolveForNodeClass resolves selectors using ACK's version-specific public image
// IDs. ECS image-family queries alone may return an image ACK has not adopted yet.
func (p *Provider) ResolveForNodeClass(ctx context.Context, nc *v1alpha1.ECSNodeClass) ([]v1alpha1.Image, error) {
	spec, err := nc.LaunchSpec()
	if err != nil {
		return nil, err
	}
	nc = nc.DeepCopy()
	nc.Spec = spec
	if err := v1alpha1.ValidateImageSelectorTerms(nc.Spec.ImageSelectorTerms); err != nil {
		return nil, err
	}
	if err := v1alpha1.ValidateImageCompatibilityPolicy(nc.Spec); err != nil {
		return nil, err
	}
	if p.ack == nil {
		images, err := p.Resolve(ctx, nc.Spec.ImageSelectorTerms)
		if err != nil {
			return nil, err
		}
		return images, ValidateBootImages(nc.Spec, images)
	}
	if nc.Spec.ClusterID != "" && nc.Spec.ClusterID != p.ack.clusterID {
		return nil, fmt.Errorf("NodeClass clusterID differs from provider ACK cluster")
	}
	var result []v1alpha1.Image
	var incompatible error
	if nc.Spec.ImageCompatibilityPolicy == "Custom" {
		for _, term := range nc.Spec.ImageSelectorTerms {
			if term.ID == nil {
				return nil, fmt.Errorf("Custom images require exact IDs")
			}
			images, err := p.ecsClient.DescribeImages(ctx, []string{*term.ID}, nil)
			if err != nil {
				return nil, fmt.Errorf("describe custom image: %w", err)
			}
			if len(images) != 1 || tea.StringValue(images[0].ImageId) != *term.ID {
				return nil, fmt.Errorf("custom image ID did not resolve exactly")
			}
			img := images[0]
			if owner := tea.StringValue(img.ImageOwnerAlias); owner != "self" && owner != "others" {
				return nil, fmt.Errorf("Custom policy permits only self/shared images; public images must use ACK policy")
			}
			if !strings.EqualFold(tea.StringValue(img.Status), "Available") {
				return nil, fmt.Errorf("custom image is not Available")
			}
			result = append(result, resolvedImage(img, ""))
		}
	} else {
		images, types, err := p.ackImages(ctx)
		if err != nil {
			return nil, err
		}
		for i, term := range nc.Spec.ImageSelectorTerms {
			if term.ImageOwnerAlias != nil && *term.ImageOwnerAlias != "system" {
				return nil, fmt.Errorf("imageSelectorTerms[%d]: ACK policy selects public images; use Custom with exact self/shared IDs", i)
			}
			matched := false
			for _, img := range images {
				if !matchesACKSelector(term, img) {
					continue
				}
				candidate := resolvedImage(img, types[tea.StringValue(img.ImageId)])
				if err := ValidateBootImages(nc.Spec, []v1alpha1.Image{candidate}); err != nil {
					if term.ID != nil {
						return nil, err // An explicit pin is mandatory, never replaced.
					}
					incompatible = err
					continue // Automatic discovery returns viable alternatives only.
				}
				result = append(result, candidate)
				matched = true
			}
			if term.ID != nil && !matched {
				return nil, fmt.Errorf("imageSelectorTerms[%d]: exact image ID is not an available Linux image in the target ACK compatibility list; pin is not replaced", i)
			}
		}
	}
	result = removeDuplicateImages(result)
	if len(result) == 0 {
		if incompatible != nil {
			return nil, fmt.Errorf("no boot-compatible images match the NodeClass selectors: %w", incompatible)
		}
		return nil, fmt.Errorf("no boot-compatible images match the NodeClass selectors")
	}
	if err := ValidateBootImages(nc.Spec, result); err != nil {
		return nil, err
	}
	return result, nil
}

// RevalidateForLaunch checks the concrete status IDs against current cluster
// metadata, including status written by a previous controller version.
func (p *Provider) RevalidateForLaunch(ctx context.Context, nc *v1alpha1.ECSNodeClass, images []v1alpha1.Image) ([]v1alpha1.Image, error) {
	current := nc.DeepCopy()
	if current.Spec.LaunchTemplateID != nil {
		// Template status represents one concrete template image, not alternatives.
		if len(images) != 1 {
			return nil, fmt.Errorf("launch template requires exactly one resolved image")
		}
		current.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ID: tea.String(images[0].ID)}}
	}
	resolved, err := p.ResolveForNodeClass(ctx, current)
	if err != nil {
		return nil, err
	}
	statusIDs := make(map[string]bool, len(images))
	for _, image := range images {
		statusIDs[image.ID] = true
	}
	var result []v1alpha1.Image
	for _, image := range resolved {
		if statusIDs[image.ID] {
			result = append(result, image)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no status images remain boot-compatible; reconcile NodeClass status before launching")
	}
	return result, nil
}

func resolvedImage(img ecs.DescribeImagesResponseBodyImagesImage, imageType string) v1alpha1.Image {
	return v1alpha1.Image{ID: tea.StringValue(img.ImageId), Name: tea.StringValue(img.ImageName), Architecture: tea.StringValue(img.Architecture), OSType: strings.ToLower(tea.StringValue(img.OSType)), ImageType: imageType, CreationTime: tea.StringValue(img.CreationTime)}
}

// ValidateBootImages enforces the implemented bootstrap and disk layout boundary.
// Custom image provenance and runtime compatibility still require user validation.
func ValidateBootImages(spec v1alpha1.ECSNodeClassSpec, images []v1alpha1.Image) error {
	for _, img := range images {
		if !strings.EqualFold(img.OSType, "linux") {
			return fmt.Errorf("image %s: only Linux images with a known ECS OSType are supported", img.ID)
		}
		if img.Architecture != "x86_64" && img.Architecture != "arm64" {
			return fmt.Errorf("image %s: unsupported or unknown architecture", img.ID)
		}
		if !strings.HasPrefix(img.ImageType, "ContainerOS") {
			continue
		}
		disks, err := v1alpha1.NormalizeDisks(spec)
		if err != nil {
			return err
		}
		if len(disks.DataDisks) != 1 || disks.InstanceStorePolicy != nil {
			return fmt.Errorf("ContainerOS requires exactly one explicit data disk and does not support instanceStorePolicy")
		}
		if strings.Contains(disks.DataDisks[0].Category, "ephemeral") {
			return fmt.Errorf("ContainerOS requires a persistent cloud data disk")
		}
		if disks.DataDisks[0].SnapshotID != "" {
			return fmt.Errorf("ContainerOS data disk must be newly initialized, not restored from a snapshot")
		}
	}
	return nil
}

func matchesACKSelector(term v1alpha1.ImageSelectorTerm, img ecs.DescribeImagesResponseBodyImagesImage) bool {
	if term.ID != nil && *term.ID != tea.StringValue(img.ImageId) {
		return false
	}
	if term.ImageFamily != nil && *term.ImageFamily != tea.StringValue(img.ImageFamily) {
		return false
	}
	if term.Name != nil {
		pattern := "^" + strings.ReplaceAll(strings.ReplaceAll(regexp.QuoteMeta(*term.Name), `\*`, ".*"), `\?`, ".") + "$"
		if ok, _ := regexp.MatchString(pattern, tea.StringValue(img.ImageName)); !ok {
			return false
		}
	}
	if len(term.Tags) > 0 {
		tags := map[string]string{}
		if img.Tags != nil {
			for _, tag := range img.Tags.Tag {
				if tag != nil {
					tags[tea.StringValue(tag.TagKey)] = tea.StringValue(tag.TagValue)
				}
			}
		}
		for key, value := range term.Tags {
			if actual, exists := tags[key]; !exists || actual != value {
				return false
			}
		}
	}
	return true
}

func (p *Provider) ackImages(ctx context.Context) ([]ecs.DescribeImagesResponseBodyImagesImage, map[string]string, error) {
	a := p.ack
	if a.client == nil || a.clusterID == "" {
		return nil, nil, fmt.Errorf("ACK compatibility requires a CS client and cluster ID")
	}
	detail, err := a.client.DescribeClusterDetail(ctx, a.clusterID)
	if err != nil {
		return nil, nil, fmt.Errorf("describe ACK cluster for image compatibility: %w", err)
	}
	if detail == nil {
		return nil, nil, fmt.Errorf("ACK cluster detail response is nil")
	}
	request, err := clients.VersionMetadataRequest(a.region, detail.Body, "containerd")
	if err != nil {
		return nil, nil, err
	}
	key := strings.Join([]string{a.clusterID, tea.StringValue(request.Region), tea.StringValue(request.ClusterType), tea.StringValue(request.KubernetesVersion), tea.StringValue(request.Profile), tea.StringValue(request.Runtime)}, "/")
	select {
	case a.gate <- struct{}{}:
		defer func() { <-a.gate }()
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if a.key == key && time.Now().Before(a.expires) {
		return a.images, a.types, nil
	}
	metadata, err := a.client.DescribeKubernetesVersionMetadata(ctx, request)
	if err != nil {
		return nil, nil, fmt.Errorf("describe ACK image compatibility metadata: %w", err)
	}
	types, architectures := map[string]string{}, map[string]string{}
	for _, version := range metadata {
		if version == nil || tea.StringValue(version.Version) != tea.StringValue(request.KubernetesVersion) {
			continue
		}
		for _, img := range version.Images {
			if img == nil || !strings.EqualFold(tea.StringValue(img.OsType), "Linux") || tea.StringValue(img.ImageId) == "" || tea.StringValue(img.ImageType) == "" {
				continue
			}
			id := *img.ImageId
			types[id], architectures[id] = tea.StringValue(img.ImageType), tea.StringValue(img.Architecture)
		}
	}
	if len(types) == 0 {
		return nil, nil, fmt.Errorf("ACK metadata contains no Linux images for the exact cluster version")
	}
	ids := make([]string, 0, len(types))
	for id := range types {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var images []ecs.DescribeImagesResponseBodyImagesImage
	for start := 0; start < len(ids); start += 10 {
		end := min(start+10, len(ids))
		batch, err := p.ecsClient.DescribeImages(ctx, ids[start:end], nil)
		if err != nil {
			return nil, nil, fmt.Errorf("describe ACK-compatible ECS image IDs: %w", err)
		}
		for _, img := range batch {
			id := tea.StringValue(img.ImageId)
			if _, ok := types[id]; !ok {
				continue
			}
			if tea.StringValue(img.ImageOwnerAlias) != "system" || !strings.EqualFold(tea.StringValue(img.OSType), "Linux") || !strings.EqualFold(tea.StringValue(img.Status), "Available") {
				continue
			}
			if architecture := architectures[id]; architecture == "" || architecture != tea.StringValue(img.Architecture) {
				continue
			}
			images = append(images, img)
		}
	}
	if len(images) == 0 {
		return nil, nil, fmt.Errorf("ACK-compatible Linux images are unavailable from ECS")
	}
	// One bounded snapshot; do not cache failures or reuse it across cluster upgrades.
	a.key, a.expires, a.images, a.types = key, time.Now().Add(5*time.Minute), images, types
	return images, types, nil
}
