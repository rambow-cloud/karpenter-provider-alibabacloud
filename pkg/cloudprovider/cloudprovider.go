/*
Copyright 2024 The Alibaba Cloud Karpenter Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cloudprovider

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/cache"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/errors"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/operator/options"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/bootstrap"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/cluster"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/imagefamily"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instanceprofile"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/launchtemplate"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/nodepool"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/pricing"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/securitygroup"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/vswitch"
	ecsutil "github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/utils/ecs"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/utils/securitygroups"
	"github.com/awslabs/operatorpkg/status"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/karpenter/pkg/events"

	"sigs.k8s.io/controller-runtime/pkg/client"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

var _ cloudprovider.CloudProvider = (*CloudProvider)(nil)

// CloudProvider implements the Karpenter CloudProvider interface for Alibaba Cloud.
type CloudProvider struct {
	// Kubernetes client for accessing cluster resources
	kubeClient   client.Client
	launchReader client.Reader

	// Providers for Alibaba Cloud resources
	instanceProvider        *instance.Provider
	instanceTypeProvider    *instancetype.Provider
	imageFamilyProvider     *imagefamily.Provider
	vswitchProvider         *vswitch.Provider
	securityGroupProvider   *securitygroup.Provider
	pricingProvider         *pricing.Provider
	launchTemplateProvider  *launchtemplate.Provider
	bootstrapProvider       *bootstrap.Provider
	instanceProfileProvider *instanceprofile.Provider
	csClient                clients.CSClient

	clusterNetworkConfig *cluster.NetworkConfig
	unavailableOfferings *cache.UnavailableOfferingsCache
	ackNodePoolProvider  *nodepool.Provider
}

// New creates a new CloudProvider with injected dependencies
func New(
	instanceTypeProvider *instancetype.Provider,
	instanceProvider *instance.Provider,
	recorder events.Recorder,
	kubeClient client.Client,
	launchReader client.Reader,
	imageFamilyProvider *imagefamily.Provider,
	securityGroupProvider *securitygroup.Provider,
	vswitchProvider *vswitch.Provider,
	instanceProfileProvider *instanceprofile.Provider,
	pricingProvider *pricing.Provider,
	launchTemplateProvider *launchtemplate.Provider,
	bootstrapProvider *bootstrap.Provider,
	networkConfig *cluster.NetworkConfig,
	unavailableOfferings *cache.UnavailableOfferingsCache,
	ackNodePoolProviders ...*nodepool.Provider,
) *CloudProvider {
	c := &CloudProvider{
		kubeClient:              kubeClient,
		launchReader:            launchReader,
		instanceProvider:        instanceProvider,
		instanceTypeProvider:    instanceTypeProvider,
		imageFamilyProvider:     imageFamilyProvider,
		vswitchProvider:         vswitchProvider,
		securityGroupProvider:   securityGroupProvider,
		pricingProvider:         pricingProvider,
		launchTemplateProvider:  launchTemplateProvider,
		bootstrapProvider:       bootstrapProvider,
		instanceProfileProvider: instanceProfileProvider,
		clusterNetworkConfig:    networkConfig,
		unavailableOfferings:    unavailableOfferings,
	}
	if len(ackNodePoolProviders) > 0 {
		c.ackNodePoolProvider = ackNodePoolProviders[0]
	}
	return c
}

const (
	ImageDrift         cloudprovider.DriftReason = "ImageDrift"
	VSwitchDrift       cloudprovider.DriftReason = "VSwitchDrift"
	SecurityGroupDrift cloudprovider.DriftReason = "SecurityGroupDrift"
	NodeClassDrift     cloudprovider.DriftReason = "NodeClassDrift"
)

const (
	ResourcePodsForExclusiveEniEnabled corev1.ResourceName = "alibabacloud.com/exclusive-eni"
	DefaultPodsLimit                                       = 110
)

// Create creates a new instance for the given NodeClaim.
//
// # Karpenter contract (upstream: cloudprovider/types.go)
//
//   - Return cloudprovider.NewNodeClassNotReadyError() when the NodeClass is not ready.
//     Karpenter treats this as a non-fatal signal and retries; any other error is treated
//     as a hard failure.
//   - Return cloudprovider.NewCreateError() with a reason/message for structured failures
//     (e.g. quota exhaustion, unsupported config) so the event is surfaced on the NodeClaim.
//   - The returned NodeClaim must have Labels populated (at minimum topology.kubernetes.io/zone,
//     node.kubernetes.io/instance-type, karpenter.sh/capacity-type, kubernetes.io/arch, kubernetes.io/os)
//     because Karpenter core reads those labels to build NodePool scheduling constraints.
//   - Set the NodeClass hash annotation (v1alpha1.AnnotationECSNodeClassHash) on the returned
//     NodeClaim so that IsDrifted can detect config changes.
func (c *CloudProvider) Create(ctx context.Context, nodeClaim *coreapis.NodeClaim) (*coreapis.NodeClaim, error) {
	// 1. Get ECSNodeClass from NodeClassRef
	nodeClass := &v1alpha1.ECSNodeClass{}
	if err := c.kubeClient.Get(ctx, client.ObjectKey{
		Name: nodeClaim.Spec.NodeClassRef.Name,
	}, nodeClass); err != nil {
		return nil, fmt.Errorf("failed to get nodeclass %s: %w", nodeClaim.Spec.NodeClassRef.Name, err)
	}
	tags, err := clusterScopedInstanceTags(ctx, nodeClaim, nodeClass)
	if err != nil {
		return nil, cloudprovider.NewCreateError(err, "ClusterOwnershipInvalid", err.Error())
	}

	if nodeClaim.UID != "" {
		if recovered, err := c.recoverLaunch(ctx, nodeClaim, tags); err != nil || recovered != nil {
			return recovered, err
		}
	}
	// Recover first: core deletes an unlaunched claim on NodeClassNotReady.
	if err := nodeClassReadinessError(nodeClass); err != nil {
		if nodeClaim.UID != "" {
			_, attempt, readErr := c.readLaunch(ctx, nodeClaim)
			if readErr != nil {
				return nil, readErr
			}
			if attempt.Token != "" && !attempt.Rejected {
				return nil, fmt.Errorf("unresolved launch waiting for NodeClass: %v", err)
			}
		}
		return nil, err
	}
	// Refresh concrete image IDs against the actual ACK version before any launch.
	launchSpec, err := nodeClass.LaunchSpec()
	if err != nil {
		return nil, err
	}
	nodeClass = nodeClass.DeepCopy()
	nodeClass.Spec = launchSpec
	if c.ackNodePoolProvider != nil {
		pool, err := c.ackNodePoolProvider.Resolve(ctx, nodeClass)
		if err != nil {
			return nil, err
		}
		if !equality.Semantic.DeepEqual(pool, nodeClass.Status.ACKNodePool) {
			return nil, fmt.Errorf("native ACK resource configuration changed; wait for NodeClass status refresh")
		}
	}
	// Old status may lack OS information or have been resolved before an upgrade.
	if c.imageFamilyProvider != nil && c.imageFamilyProvider.ACKCompatibilityEnabled() {
		images, err := c.imageFamilyProvider.RevalidateForLaunch(ctx, nodeClass, nodeClass.Status.Images)
		if err != nil || len(images) == 0 {
			if err == nil {
				err = fmt.Errorf("no compatible Linux images remain")
			}
			return nil, cloudprovider.NewCreateError(err, "ImageCompatibilityFailed", err.Error())
		}
		nodeClass = nodeClass.DeepCopy()
		nodeClass.Status.Images = images
	}

	if nodeClaim.UID == "" {
		return nil, fmt.Errorf("persisted NodeClaim UID is required for creation")
	}
	if err := imagefamily.ValidateBootImages(nodeClass.Spec, nodeClass.Status.Images); err != nil {
		return nil, err
	}
	nodeClass.Status.Images = filterImagesForNodeLabels(nodeClass.Status.Images, nodeClaim.Labels)
	if len(nodeClass.Status.Images) == 0 {
		return nil, fmt.Errorf("no image supports the requested node labels; Ubuntu cannot enable image acceleration")
	}
	hash, err := nodeClass.Hash()
	if err != nil {
		return nil, fmt.Errorf("hash nodeclass: %w", err)
	}

	// 3. Get instance types that match requirements
	instanceTypes, err := c.instanceTypeProvider.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list instance types: %w", err)
	}

	// Filter instance types by NodeClaim requirements
	filteredTypes := filterBootInstanceTypes(c.filterInstanceTypesByRequirements(ctx, instanceTypes, nodeClaim.Spec.Requirements), nodeClass.Status.Images, nodeClass)
	if len(filteredTypes) == 0 {
		return nil, fmt.Errorf("no instance types match the requirements with a compatible image architecture")
	}

	// 4. Resolve images from NodeClass
	images := nodeClass.Status.Images
	if len(images) == 0 {
		return nil, fmt.Errorf("no images available in nodeclass status")
	}

	// 5. Get VSwitches from NodeClass status, filtered by zone requirements
	vswitches := filterVSwitchesByZones(nodeClass.Status.VSwitches, zonesFromRequirements(nodeClaim.Spec.Requirements))
	if len(vswitches) == 0 {
		return nil, fmt.Errorf("no vswitches available matching the zone requirements")
	}

	// 6. Get SecurityGroups from NodeClass status
	securityGroups := nodeClass.Status.SecurityGroups
	if len(securityGroups) == 0 {
		return nil, fmt.Errorf("no security groups available in nodeclass status")
	}

	// 7. Generate UserData using bootstrap provider
	bootstrapOpts := bootstrap.BootstrapOptions{
		ClusterType:     determineClusterType(nodeClass),
		ClusterID:       nodeClass.Spec.ClusterID,
		ClusterName:     nodeClass.Spec.ClusterName,
		ClusterEndpoint: nodeClass.Spec.ClusterEndpoint,
		NodeName:        nodeClaim.Name,
		KubeletConfig:   convertKubeletConfigToBootstrap(nodeClass.Spec.Kubelet),
		Labels:          nodeClaim.Labels,
		Taints:          convertToCoreTaints(nodeClaim.Spec.Taints),
		CustomUserData:  lo.ToPtr(getCustomUserData(nodeClass)),
	}
	if nodeClass.Status.ACKNodePool == nil {
		return nil, fmt.Errorf("native ACK bootstrap pool is unresolved")
	}
	bootstrapOpts.ClusterType = bootstrap.ACKClusterType
	bootstrapOpts.ClusterID = nodeClass.Status.ACKNodePool.ClusterID
	bootstrapOpts.ACKNodePoolID = nodeClass.Status.ACKNodePool.ID
	bootstrapOpts.ACKResourceConfiguration, err = nodeClass.ResourceConfigurationHash()
	if err != nil {
		return nil, err
	}
	for _, image := range nodeClass.Status.Images {
		if strings.HasPrefix(image.ImageType, "ContainerOS") {
			bootstrapOpts.FormatDisk = true
		}
	}
	diskLayout, err := v1alpha1.NormalizeDisks(nodeClass.Spec)
	if err != nil {
		return nil, err
	}
	bootstrapOpts.Disks = &diskLayout

	userData, err := c.bootstrapProvider.GenerateUserData(ctx, bootstrapOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to generate user data: %w", err)
	}

	// 9. Create instance using instance provider
	instanceID, err := c.createInstanceWithRetry(ctx, nodeClaim, nodeClass, filteredTypes, images, vswitches, securityGroups, userData, tags)
	if err != nil {
		return nil, fmt.Errorf("failed to create instance: %w", err)
	}

	// 10. Get the created instance details
	inst, err := c.instanceProvider.Get(ctx, instanceID, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get created instance: %w", err)
	}

	// 11. Convert instance to NodeClaim
	createdNodeClaim, err := c.convertInstanceToNodeClaim(ctx, inst, nodeClaim, filteredTypes, nodeClass.Spec.ClusterID)
	if err != nil {
		return nil, err
	}

	// 12. Set NodeClass hash annotation for drift detection
	setNodeClassHashAnnotation(createdNodeClaim, hash)

	return createdNodeClaim, nil
}

// nodeClassReadinessError gates both scheduling and creation on resolved status.
func nodeClassReadinessError(nodeClass *v1alpha1.ECSNodeClass) error {
	// Reading readiness must not initialize missing conditions or aggregate a
	// new Ready condition from status left by an earlier reconciliation.
	ready := nodeClass.StatusConditions(status.WithObservedOnly()).Get(status.ConditionReady)
	if ready == nil {
		return cloudprovider.NewNodeClassNotReadyError(fmt.Errorf("nodeclass %s has no Ready condition for generation %d", nodeClass.Name, nodeClass.Generation))
	}
	if !ready.IsTrue() || ready.ObservedGeneration != nodeClass.Generation {
		return cloudprovider.NewNodeClassNotReadyError(fmt.Errorf("nodeclass %s is not ready for generation %d (observed %d): %s", nodeClass.Name, nodeClass.Generation, ready.ObservedGeneration, ready.Message))
	}
	return nil
}

// Delete terminates the cloud instance backing the given NodeClaim.
//
// # Karpenter contract (upstream: cloudprovider/types.go)
//
//   - Return cloudprovider.NewNodeClaimNotFoundError() when the instance is already gone.
//     Karpenter keeps retrying Delete until it receives this error, so returning nil when
//     the instance does not exist will cause an infinite retry loop.
//   - Return nil only after deletion has been successfully *triggered* (not necessarily
//     completed); Karpenter will confirm via Get/List once the node disappears.
func (c *CloudProvider) Delete(ctx context.Context, nodeClaim *coreapis.NodeClaim) error {
	log.FromContext(ctx).Info("CloudProvider.Delete called", "nodeClaimName", nodeClaim.Name, "providerID", nodeClaim.Status.ProviderID)

	// 提取 instanceID 从 ProviderID
	// ProviderID 格式: cn-hangzhou.i-xxxxx
	instanceID := fromProviderID(nodeClaim.Status.ProviderID)
	if instanceID == "" {
		return fmt.Errorf("invalid providerID %q: expected region.i-instance-id", nodeClaim.Status.ProviderID)
	}

	log.FromContext(ctx).Info("Extracted instanceID from providerID", "instanceID", instanceID, "providerID", nodeClaim.Status.ProviderID)

	// 调用已实现的 instanceProvider.Delete()
	err := c.instanceProvider.Delete(ctx, instanceID)
	if err != nil {
		return err
	}

	log.FromContext(ctx).Info("Successfully deleted instance", "instanceID", instanceID)
	return nil
}

// Get retrieves the current state of the instance
func (c *CloudProvider) Get(ctx context.Context, providerID string) (*coreapis.NodeClaim, error) {
	// Extract instanceID from ProviderID
	instanceID := fromProviderID(providerID)
	if instanceID == "" {
		return nil, fmt.Errorf("invalid providerID: %s", providerID)
	}

	// Get instance from provider with caching
	inst, err := c.instanceProvider.Get(ctx, instanceID, false)
	if err != nil {
		return nil, err
	}

	// Get all instance types to look up capacity
	instanceTypes, err := c.instanceTypeProvider.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list instance types: %w", err)
	}

	// Find matching instance type for capacity information
	capacity, allocatable, err := c.instanceResources(ctx, inst)
	if err != nil {
		return nil, err
	}
	var matchedIT *instancetype.InstanceType
	for _, it := range instanceTypes {
		if it.Name == inst.InstanceType {
			matchedIT = it
			break
		}
	}

	// Parse launch time
	launchTime := parseLaunchTime(ctx, inst.InstanceID, inst.CreationTime)

	// Create NodeClaim from instance
	nodeClaim := &coreapis.NodeClaim{}
	nodeClaim.Name = inst.InstanceID
	nodeClaim.Labels = instanceLabelsFromInstance(inst, matchedIT)
	nodeClaim.CreationTimestamp = metav1.Time{Time: launchTime}

	// Set Status
	nodeClaim.Status.ProviderID = fmt.Sprintf("%s.%s", inst.Region, inst.InstanceID)
	nodeClaim.Status.Capacity = capacity
	nodeClaim.Status.Allocatable = allocatable
	nodeClaim.Status.ImageID = inst.ImageID

	return nodeClaim, nil
}

// List retrieves all instances managed by Karpenter
func (c *CloudProvider) List(ctx context.Context) ([]*coreapis.NodeClaim, error) {
	clusterID := options.FromContext(ctx).ClusterID
	if clusterID == "" {
		return nil, fmt.Errorf("cluster-id is required for cluster-scoped instance discovery")
	}
	// Shared accounts and regions can contain instances from several clusters.
	tags := map[string]string{
		v1alpha1.TagManagedBy: v1alpha1.TagManagedByValue,
		v1alpha1.TagClusterID: clusterID,
	}

	instances, err := c.instanceProvider.List(ctx, tags)
	if err != nil {
		return nil, err
	}
	if len(instances) == 0 {
		return []*coreapis.NodeClaim{}, nil
	}

	// Pre-load instance types for capacity lookup
	instanceTypes, err := c.instanceTypeProvider.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list instance types: %w", err)
	}

	// Build instance type map for faster lookup
	instanceTypeMap := make(map[string]*instancetype.InstanceType)
	for _, it := range instanceTypes {
		instanceTypeMap[it.Name] = it
	}

	// Convert all instances to NodeClaims
	nodeClaims := make([]*coreapis.NodeClaim, 0, len(instances))
	for _, inst := range instances {
		// Find instance type capacity
		capacity, allocatable, err := c.instanceResources(ctx, inst)
		if err != nil {
			return nil, err
		}
		matchedIT := instanceTypeMap[inst.InstanceType]

		// Parse launch time
		launchTime := parseLaunchTime(ctx, inst.InstanceID, inst.CreationTime)

		// Create NodeClaim
		nodeClaim := &coreapis.NodeClaim{}
		nodeClaim.Name = inst.InstanceID
		nodeClaim.Labels = instanceLabelsFromInstance(inst, matchedIT)
		nodeClaim.CreationTimestamp = metav1.Time{Time: launchTime}

		// Set Status
		nodeClaim.Status.ProviderID = fmt.Sprintf("%s.%s", inst.Region, inst.InstanceID)
		nodeClaim.Status.Capacity = capacity
		nodeClaim.Status.Allocatable = allocatable
		nodeClaim.Status.ImageID = inst.ImageID

		nodeClaims = append(nodeClaims, nodeClaim)
	}

	return nodeClaims, nil
}

// GetInstanceTypes returns instance types supported by this provider for the given NodePool.
//
// # Karpenter contract (upstream: cloudprovider/types.go, controllers/nodeclaim/disruption/drift.go)
//
// ## Offerings must include temporarily-unavailable entries (Available: false)
//
// The drift controller calls GetInstanceTypes() every 30 min (starting 1 h after node creation)
// and runs instanceTypeNotFound(), which checks:
//
//  1. Is the node's instance type present in the returned list?  (lo.Find by name)
//  2. Does any Offering match the node's (zone, capacityType) labels?  (Offerings.HasCompatible)
//
// HasCompatible does NOT filter by Offering.Available — it only checks requirements.
// Therefore: if zone inventory drops to zero and we omit the offering entirely, the drift
// controller sees "no compatible offering" and marks the node as InstanceTypeNotFound,
// triggering an unnecessary replacement.
//
// Correct behaviour: always emit an Offering for every known (zone, capacityType) combination,
// even when inventory is zero. Set Offering.Available = false for out-of-stock combinations.
//
// Callers that create NEW nodes use Offerings.Available() to filter — they correctly skip
// Available=false offerings. Only drift detection uses HasCompatible, which must find them.
//
// ## Offering.Available controls scheduling, not drift
//
//   - Scheduling path: Offerings.Available().HasCompatible(req) — needs Available=true.
//   - Drift path:      Offerings.HasCompatible(req)             — ignores Available.
//   - Price sort:      also filters by Available=true (cloudprovider/types.go:228).
//
// ## InstanceType.Requirements must cover all known zones and capacity types
//
// Requirements on the InstanceType struct are used as a pre-filter by the scheduler.
// They should be built from ALL offerings (including Available=false ones) so that the
// scheduler does not silently exclude an instance type from the result set during drift checks.
func (c *CloudProvider) GetInstanceTypes(ctx context.Context, nodePool *coreapis.NodePool) ([]*cloudprovider.InstanceType, error) {
	logger := log.FromContext(ctx)

	// 1. Get NodeClass from NodePool
	nodeClass := &v1alpha1.ECSNodeClass{}
	if err := c.kubeClient.Get(ctx, client.ObjectKey{
		Name: nodePool.Spec.Template.Spec.NodeClassRef.Name,
	}, nodeClass); err != nil {
		return nil, fmt.Errorf("failed to get nodeclass: %w", err)
	}

	// 2. Verify NodeClass is ready
	if err := nodeClassReadinessError(nodeClass); err != nil {
		return nil, err
	}

	// 3. DEFENSIVE CHECK: Verify VSwitches are populated
	// This catches the race condition where NodeClass is marked Ready but status has not been fully populated.
	// Without this check, we proceed with empty availableZones, resulting in 0 offerings for all instance types.
	if len(nodeClass.Status.VSwitches) == 0 {
		logger.Error(nil, "NodeClass is marked Ready but has no VSwitches - status not fully populated",
			"nodeClass", nodeClass.Name,
			"nodePool", nodePool.Name)
		return nil, cloudprovider.NewNodeClassNotReadyError(fmt.Errorf("nodeclass %s has no vswitches - status not ready", nodeClass.Name))
	}
	// 4. Get all instance types
	instanceTypes, err := c.instanceTypeProvider.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list instance types: %w", err)
	}

	// Log total instance types found (only at debug level)
	logger.V(1).Info("Found instance types", "count", len(instanceTypes))

	// 4. Filter by NodePool requirements
	filteredTypes := c.filterInstanceTypesByRequirements(
		ctx,
		instanceTypes,
		nodePool.Spec.Template.Spec.Requirements,
	)
	filteredTypes = filterBootInstanceTypes(filteredTypes, filterImagesForNodeLabels(nodeClass.Status.Images, nodePool.Spec.Template.Labels), nodeClass)
	filteredTypes = filterInstanceTypesByTemplate(filteredTypes, nodeClass)

	// Log filtered instance types (only at debug level)
	logger.V(1).Info("Filtered instance types", "count", len(filteredTypes))

	// 5. Filter by availability zones (from VSwitches)
	availableZones := make(map[string]bool)
	for _, vs := range nodeClass.Status.VSwitches {
		// Support both Zone and ZoneID fields
		zoneID := vs.ZoneID
		if zoneID == "" {
			zoneID = vs.Zone
		}
		availableZones[zoneID] = true
	}

	// Log available zones (only at debug level)
	logger.V(1).Info("Available zones", "zones", availableZones)

	// 7. Convert to core InstanceType format
	coreInstanceTypes := make([]*cloudprovider.InstanceType, 0, len(filteredTypes))
	var resourceErr error
	for _, it := range filteredTypes {
		// Validate instance type has required fields
		if it.CPU == nil || it.Memory == nil {
			logger.V(1).Info("Skipping instance type with nil CPU or Memory", "instanceType", it.Name)
			continue
		}

		launchSpec, err := nodeClass.LaunchSpec()
		if err != nil {
			return nil, err
		}
		effective := nodeClass.DeepCopy()
		effective.Spec = launchSpec
		model, err := c.resourceModel(ctx, it, effective, selectBootImage(nodeClass.Status.Images, it, nodeClass).ID)
		if err != nil {
			resourceErr = err
			logger.Error(err, "failed to calculate capacity", "instanceType", it.Name)
			continue
		}
		capacity := model.Capacity

		// Create one offering per (zone, capacityType) combination.
		// Offerings are created for ALL known capacity types in each configured zone, including
		// temporarily-unavailable ones (Available=false). This prevents the karpenter drift
		// controller from falsely evicting running nodes when zone inventory drops to zero:
		// HasCompatible() finds the offering regardless of Available; the scheduler uses
		// Offerings.Available() and therefore only places NEW pods on Available=true offerings.
		offerings := make(cloudprovider.Offerings, 0)
		for zoneID, zoneInfo := range it.Zones {
			if !availableZones[zoneID] {
				continue // zone not configured in NodeClass VSwitches — always skip
			}
			// Build per-capacity-type availability: KnownCapacityTypes drives the offering set;
			// CapacityTypes (subset) marks which ones currently have stock.
			capTypeAvailability := make(map[string]bool)
			for _, ct := range zoneInfo.KnownCapacityTypes {
				capTypeAvailability[ct] = false // default: no current stock
			}
			for _, ct := range zoneInfo.CapacityTypes {
				capTypeAvailability[ct] = true // has stock
			}
			for capType, isAvailable := range capTypeAvailability {
				quote, quoteErr := c.offeringQuote(nodeClass, it, filterImagesForNodeLabels(nodeClass.Status.Images, nodePool.Spec.Template.Labels), zoneID, capType)
				isAvailable = isAvailable && quoteErr == nil
				if quoteErr != nil {
					logger.V(1).Info("offering awaits a valid hourly quote", "instanceType", it.Name, "zone", zoneID, "capacityType", capType, "error", quoteErr)
				}
				if c.unavailableOfferings != nil {
					isAvailable = isAvailable && c.unavailableOfferings.IsAvailable(it.Name, zoneID, capType)
				}
				offerings = append(offerings, &cloudprovider.Offering{
					Requirements: scheduling.NewRequirements(
						scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, zoneID),
						scheduling.NewRequirement(coreapis.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, capType),
					),
					Price:     quote.Hourly,
					Available: isAvailable,
				})
			}
		}
		// Skip instance types that have no offerings at all in the configured zones
		// (e.g. NodeClass has no VSwitches, or the instance type is truly unknown here).
		if len(offerings) == 0 {
			logger.V(1).Info("Instance type has no offerings in specified zones",
				"instanceType", it.Name,
				"instanceZones", it.Zones,
				"availableZones", availableZones)
			continue
		}

		// Log instance type with available offerings (only at debug level)
		logger.V(1).Info("Instance type with available offerings",
			"instanceType", it.Name,
			"offerings", len(offerings))

		// Collect unique zones and capacity types across all offerings for InstanceType-level Requirements.
		offeringZones := lo.Keys(lo.SliceToMap(offerings, func(o *cloudprovider.Offering) (string, struct{}) {
			return o.Requirements.Get(corev1.LabelTopologyZone).Any(), struct{}{}
		}))
		offeringCapTypes := lo.Keys(lo.SliceToMap(offerings, func(o *cloudprovider.Offering) (string, struct{}) {
			return o.Requirements.Get(coreapis.CapacityTypeLabelKey).Any(), struct{}{}
		}))

		// Create core InstanceType with fully-populated Requirements.
		// Requirements are required for Karpenter's scheduling logic (arch, OS, zone, capacity-type filtering).
		coreIT := &cloudprovider.InstanceType{
			Name:         it.Name,
			Capacity:     capacity,
			Offerings:    offerings,
			Requirements: buildInstanceTypeRequirements(it, options.FromContext(ctx).Region, offeringZones, offeringCapTypes),
			Overhead:     model.Overhead,
		}

		coreInstanceTypes = append(coreInstanceTypes, coreIT)
	}

	if len(coreInstanceTypes) == 0 {
		// Log detailed information when no instance types are available (only at debug level)
		logger.V(1).Info("No instance types available after filtering",
			"totalInstanceTypes", len(instanceTypes),
			"filteredInstanceTypes", len(filteredTypes),
			"availableZones", availableZones)
		if resourceErr != nil {
			return nil, fmt.Errorf("no instance types available matching requirements: %w", resourceErr)
		}
		return nil, fmt.Errorf("no instance types available matching requirements")
	}

	// Log final result (only at debug level)
	logger.V(1).Info("Returning instance types", "count", len(coreInstanceTypes))

	return coreInstanceTypes, nil
}

func buildInstanceTypeRequirements(it *instancetype.InstanceType, region string, zones, capacityTypes []string) scheduling.Requirements {
	requirements := scheduling.NewRequirements(
		scheduling.NewRequirement(corev1.LabelInstanceTypeStable, corev1.NodeSelectorOpIn, it.Name),
		scheduling.NewRequirement(corev1.LabelArchStable, corev1.NodeSelectorOpIn, ecsArchToKubernetesArch(it.Architecture)),
		scheduling.NewRequirement(corev1.LabelOSStable, corev1.NodeSelectorOpIn, string(corev1.Linux)),
		scheduling.NewRequirement(corev1.LabelTopologyRegion, corev1.NodeSelectorOpIn, region),
		scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, zones...),
		scheduling.NewRequirement(coreapis.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, capacityTypes...),
	)
	for key, value := range instancetype.ResolvedLabels(it) {
		requirements.Add(scheduling.NewRequirement(key, corev1.NodeSelectorOpIn, value))
	}
	return requirements
}

// RepairPolicies returns the repair policies for Alibaba Cloud nodes
func (c *CloudProvider) RepairPolicies() []cloudprovider.RepairPolicy {
	// For Alibaba Cloud, we don't have specific repair policies yet
	// Return an empty slice for now
	return []cloudprovider.RepairPolicy{}
}

// GetSupportedNodeClasses returns the CloudProvider NodeClass that implements status.Object
func (c *CloudProvider) GetSupportedNodeClasses() []status.Object {
	return []status.Object{&v1alpha1.ECSNodeClass{}}
}

// Name returns the CloudProvider implementation name.
func (c *CloudProvider) Name() string {
	return "alibabacloud"
}

// IsDrifted reports whether the running node has drifted from its desired configuration.
//
// # Karpenter contract (upstream: controllers/nodeclaim/disruption/drift.go)
//
// ## Call order
//
// The drift controller calls static checks first (NodePool hash, Requirements), then calls
// GetInstanceTypes() + instanceTypeNotFound(), and finally calls IsDrifted() here.
// IsDrifted is NOT called when the instance type can no longer be found in GetInstanceTypes(),
// so provider-level drift checks run only for nodes whose instance type is still known.
//
// ## Return value semantics
//
//   - Return ("", nil):        node is not drifted — drift condition is cleared.
//   - Return (reason, nil):    node is drifted for reason — drift condition is set and the
//     node becomes a disruption candidate.
//   - Return ("", err):        transient error — drift condition unchanged, reconciler retries.
//
// ## What to check here vs. in GetInstanceTypes
//
// IsDrifted should cover cloud-provider-specific config drift (image, vSwitch, security group,
// NodeClass hash). Instance-type availability drift is handled upstream via instanceTypeNotFound;
// do not duplicate it here. Duplicating it risks false positives when inventory is temporarily
// zero (the upstream check is guarded by a 1-hour delay + 30-min cache; this one is not).
//
// ## Reconcile frequency
//
// The drift reconciler re-queues every 5 minutes, so IsDrifted is called frequently.
// Avoid expensive or rate-limited API calls on the hot path; cache where possible.
func (c *CloudProvider) IsDrifted(ctx context.Context, nodeClaim *coreapis.NodeClaim) (cloudprovider.DriftReason, error) {
	// 1. Get NodeClass
	nodeClass := &v1alpha1.ECSNodeClass{}
	if err := c.kubeClient.Get(ctx, client.ObjectKey{
		Name: nodeClaim.Spec.NodeClassRef.Name,
	}, nodeClass); err != nil {
		return "", fmt.Errorf("failed to get nodeclass: %w", err)
	}
	// Unresolved selector changes must not compare an incomplete resource set
	// against a healthy running node. Wait for this generation's resolution.
	if err := nodeClassReadinessError(nodeClass); err != nil {
		return "", err
	}

	// 2. Get instance details
	instanceID := fromProviderID(nodeClaim.Status.ProviderID)
	if instanceID == "" {
		return "", fmt.Errorf("invalid providerID: %s", nodeClaim.Status.ProviderID)
	}

	inst, err := c.instanceProvider.Get(ctx, instanceID, false)
	if err != nil {
		return "", fmt.Errorf("failed to get instance: %w", err)
	}

	// 3. Check NodeClass hash (most important drift detection)
	currentHash, err := nodeClass.Hash()
	if err != nil {
		return "", err
	}
	annotatedHash := ""
	if nodeClaim.Annotations != nil {
		annotatedHash = nodeClaim.Annotations[v1alpha1.AnnotationECSNodeClassHash]
	}

	if currentHash != annotatedHash || nodeClaim.Annotations[v1alpha1.AnnotationECSNodeClassHashVersion] != v1alpha1.ECSNodeClassHashVersion {
		return NodeClassDrift, nil
	}

	// 4. Check if image is still in allowed list
	if !isImageAllowed(inst.ImageID, nodeClass.Status.Images) {
		return ImageDrift, nil
	}

	if !isVSwitchAllowed(inst.VSwitchID, nodeClass.Status.VSwitches) {
		return VSwitchDrift, nil
	}

	if !isSecurityGroupAllowed(inst.SecurityGroupIDs, nodeClass.Status.SecurityGroups) {
		return SecurityGroupDrift, nil
	}

	return "", nil
}

// fromProviderID extracts the instance ID from a provider ID
// Provider ID format: region.instance-id
// Example: cn-hangzhou.i-bp1abc123
func fromProviderID(providerID string) string {
	region, instanceID, ok := strings.Cut(providerID, ".")
	if !ok || region == "" || !strings.HasPrefix(instanceID, "i-") || len(instanceID) <= 2 {
		return ""
	}
	for _, part := range []string{region, instanceID} {
		for _, ch := range part {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
				return ""
			}
		}
	}
	return instanceID
}

// Helper functions for Create

func determineClusterType(nodeClass *v1alpha1.ECSNodeClass) bootstrap.ClusterType {
	if nodeClass.Spec.ClusterID != "" {
		return bootstrap.ACKClusterType
	}
	return bootstrap.KubeadmClusterType
}

func convertToCoreTaints(taints []corev1.Taint) []corev1.Taint {
	return taints
}

func getCustomUserData(nodeClass *v1alpha1.ECSNodeClass) string {
	if nodeClass.Spec.UserData != nil {
		return *nodeClass.Spec.UserData
	}
	return ""
}

// ecsArchToKubernetesArch maps ECS CpuArchitecture values to Kubernetes arch label values.
// ECS returns "X86" for x86_64 and "ARM" or "ARM64" for AArch64.
func ecsArchToKubernetesArch(ecsArch string) string {
	return ecsutil.KubeArchitecture(ecsArch)
}

// clusterScopedInstanceTags keeps Create and List on the same ownership boundary.
func clusterScopedInstanceTags(ctx context.Context, nodeClaim *coreapis.NodeClaim, nodeClass *v1alpha1.ECSNodeClass) (map[string]string, error) {
	clusterID := options.FromContext(ctx).ClusterID
	if clusterID == "" {
		return nil, fmt.Errorf("cluster-id is required for instance ownership")
	}
	if nodeClass.Spec.ClusterID != "" && nodeClass.Spec.ClusterID != clusterID {
		return nil, fmt.Errorf("nodeclass clusterID %q differs from controller cluster-id %q", nodeClass.Spec.ClusterID, clusterID)
	}
	tags := BuildInstanceTags(nodeClaim, nodeClass)
	tags[v1alpha1.TagManagedBy] = v1alpha1.TagManagedByValue
	tags[v1alpha1.TagClusterID] = clusterID
	return tags, nil
}

func BuildInstanceTags(nodeClaim *coreapis.NodeClaim, nodeClass *v1alpha1.ECSNodeClass) map[string]string {
	tags := make(map[string]string)

	// Layer 1: management tags — always present, required for List() tag filter
	tags[v1alpha1.TagManagedBy] = v1alpha1.TagManagedByValue
	if nodeClass.Spec.ClusterID != "" {
		tags[v1alpha1.TagClusterID] = nodeClass.Spec.ClusterID
	}

	// Layer 2: traceability tags — values are K8s labels we wrote ourselves, always valid
	if v, ok := nodeClaim.Labels[coreapis.NodePoolLabelKey]; ok {
		tags[v1alpha1.TagNodePool] = v
	}
	if nodeClaim.Name != "" {
		tags[v1alpha1.TagNodeClaim] = nodeClaim.Name
	}

	// Layer 3: user tags. Restore identity even when admission is bypassed.
	for k, v := range nodeClass.Spec.Tags {
		tags[k] = v
	}
	if nodeClaim.UID != "" {
		tags[v1alpha1.TagNodeClaimUID] = string(nodeClaim.UID)
	}
	if nodeClaim.Name != "" {
		tags[v1alpha1.TagNodeClaim] = nodeClaim.Name
	}

	return tags
}

// resolveArchitecture returns the Kubernetes arch label value for a discovered
// instance. It prefers the authoritative CpuArchitecture obtained from ECS
// DescribeInstanceTypes (carried on the matched InstanceType), because
// DescribeInstances itself does not return CpuArchitecture. It only falls back
// to the instance-type-name heuristic when the authoritative value is missing
// (e.g. the instance type is no longer offered / not in the cache).
func resolveArchitecture(inst *instance.Instance, it *instancetype.InstanceType) string {
	if it != nil && it.Architecture != "" {
		return ecsutil.KubeArchitecture(it.Architecture)
	}
	return ecsutil.ArchitectureFromInstanceType(inst.InstanceType)
}

// instanceLabelsFromInstance builds K8s NodeClaim labels from a discovered ECS instance.
// It never mirrors raw ECS tags as K8s labels — ECS tag values can contain characters
// that K8s rejects (colons, slashes, @). Only values we control are trusted.
func instanceLabelsFromInstance(inst *instance.Instance, it *instancetype.InstanceType) map[string]string {
	labels := map[string]string{
		corev1.LabelTopologyRegion:     inst.Region,
		corev1.LabelTopologyZone:       inst.Zone,
		v1alpha1.LabelCapacityType:     inst.CapacityType,
		corev1.LabelArchStable:         resolveArchitecture(inst, it),
		corev1.LabelOSStable:           "linux",
		corev1.LabelInstanceTypeStable: inst.InstanceType,
	}
	// Authoritative attributes come from the matched InstanceType (DescribeInstanceTypes),
	// the same source buildInstanceTypeRequirements uses, so planning requirements and
	// returned labels stay consistent.
	for key, value := range instancetype.ResolvedLabels(it) {
		labels[key] = value
	}
	// Fallback for instances whose type is no longer offered: DescribeInstances carries
	// CPU, memory, GPU spec and GPU amount. GPU memory is not returned by DescribeInstances,
	// so it is never fabricated here. Authoritative values above always win.
	for key, value := range map[string]string{
		v1alpha1.LabelInstanceCPU:      quantityLabelValue(inst.CPU),
		v1alpha1.LabelInstanceMemory:   quantityLabelValueMiB(inst.Memory),
		v1alpha1.LabelInstanceGPUName:  v1alpha1.NormalizeLabelValue(inst.GPUSpec),
		v1alpha1.LabelInstanceGPUCount: quantityLabelValue(inst.GPU),
	} {
		if value != "" {
			if _, ok := labels[key]; !ok {
				labels[key] = value
			}
		}
	}
	// Restore NodePool label from the tag we wrote in buildInstanceTags (value is always valid)
	if v, ok := inst.Tags[v1alpha1.TagNodePool]; ok {
		labels[coreapis.NodePoolLabelKey] = v
	}
	return labels
}

func quantityLabelValue(q resource.Quantity) string {
	if q.Sign() <= 0 {
		return ""
	}
	return fmt.Sprint(q.Value())
}

func quantityLabelValueMiB(q resource.Quantity) string {
	if q.Sign() <= 0 {
		return ""
	}
	return fmt.Sprint(q.Value() / (1024 * 1024))
}

// capacityTypeFromRequirements returns "spot" when the NodeClaim requirements include
// karpenter.sh/capacity-type In [spot], otherwise returns "on-demand".
func capacityTypeFromRequirements(reqs []coreapis.NodeSelectorRequirementWithMinValues) string {
	for _, req := range reqs {
		if req.Key == v1alpha1.LabelCapacityType && req.Operator == corev1.NodeSelectorOpIn {
			for _, v := range req.Values {
				if v == v1alpha1.CapacityTypeSpot {
					return v1alpha1.CapacityTypeSpot
				}
			}
		}
	}
	return v1alpha1.CapacityTypeOnDemand
}

// zonesFromRequirements extracts the allowed zones from a NodeClaim's requirements.
// Returns nil when no topology.kubernetes.io/zone requirement is present (meaning any zone is allowed).
func zonesFromRequirements(requirements []coreapis.NodeSelectorRequirementWithMinValues) []string {
	for _, req := range requirements {
		if req.Key == corev1.LabelTopologyZone && req.Operator == corev1.NodeSelectorOpIn {
			return req.Values
		}
	}
	return nil
}

// filterVSwitchesByZones returns only the vswitches whose zone matches one of the allowed zones.
// When allowedZones is nil (no zone requirement), all vswitches are returned unchanged.
func filterVSwitchesByZones(vswitches []v1alpha1.VSwitch, allowedZones []string) []v1alpha1.VSwitch {
	if len(allowedZones) == 0 {
		return vswitches
	}
	allowed := make(map[string]struct{}, len(allowedZones))
	for _, z := range allowedZones {
		allowed[z] = struct{}{}
	}
	var filtered []v1alpha1.VSwitch
	for _, vs := range vswitches {
		zone := vs.ZoneID
		if zone == "" {
			zone = vs.Zone
		}
		if _, ok := allowed[zone]; ok {
			filtered = append(filtered, vs)
		}
	}
	return filtered
}

// filterInstanceTypesByRequirements converts Karpenter requirements to instancetype requirements and filters
func (c *CloudProvider) filterInstanceTypesByRequirements(ctx context.Context, instanceTypes []*instancetype.InstanceType, requirements []coreapis.NodeSelectorRequirementWithMinValues) []*instancetype.InstanceType {
	// Convert Karpenter requirements to instancetype requirements
	var itRequirements []instancetype.InstanceTypeRequirement
	for _, req := range requirements {
		// Convert operator
		var operator instancetype.InstanceTypeOperator
		switch req.Operator {
		case corev1.NodeSelectorOpIn:
			operator = instancetype.InstanceTypeOperatorIn
		case corev1.NodeSelectorOpNotIn:
			operator = instancetype.InstanceTypeOperatorNotIn
		case corev1.NodeSelectorOpExists:
			operator = instancetype.InstanceTypeOperatorExists
		case corev1.NodeSelectorOpDoesNotExist:
			operator = instancetype.InstanceTypeOperatorDoesNotExist
		default:
			// Unknown operator, skip this requirement
			continue
		}

		itRequirements = append(itRequirements, instancetype.InstanceTypeRequirement{
			Key:      req.Key,
			Operator: operator,
			Values:   req.Values,
		})
	}

	// Use instancetype.Provider.Filter for filtering
	return c.instanceTypeProvider.Filter(ctx, instanceTypes, itRequirements)
}

func (c *CloudProvider) createInstanceWithRetry(ctx context.Context, nodeClaim *coreapis.NodeClaim, nodeClass *v1alpha1.ECSNodeClass, instanceTypes []*instancetype.InstanceType, images []v1alpha1.Image, vswitches []v1alpha1.VSwitch, securityGroups []v1alpha1.SecurityGroup, userData string, tags map[string]string) (string, error) {
	if len(instanceTypes) == 0 || len(images) == 0 || len(vswitches) == 0 || len(securityGroups) == 0 {
		return "", fmt.Errorf("missing required parameters for instance creation")
	}
	if nodeClass.Spec.LaunchTemplateID != nil {
		if err := imagefamily.ValidateBootImages(nodeClass.Spec, images); err != nil {
			return "", err
		}
	}
	instanceTypes = filterInstanceTypesByTemplate(filterBootInstanceTypes(instanceTypes, images, nodeClass), nodeClass)
	if len(instanceTypes) == 0 {
		return "", fmt.Errorf("no instance types have a known compatible image architecture")
	}

	ramRoleName, err := validateRAMRoleForCreate(nodeClass)
	if err != nil {
		return "", err
	}
	disks, err := v1alpha1.NormalizeDisks(nodeClass.Spec)
	if err != nil {
		return "", err
	}

	securityGroupIDs := make([]string, 0, len(securityGroups))
	for _, securityGroup := range securityGroups {
		securityGroupIDs = append(securityGroupIDs, securityGroup.ID)
	}
	securityGroupIDs = securitygroups.NormalizeIDs(securityGroupIDs)

	// Admit only fresh quotes for compatible capacity types, inventory and zones.
	candidates, err := c.pricedLaunchCandidates(ctx, nodeClaim, nodeClass, instanceTypes, images, vswitches)
	if err != nil {
		return "", err
	}

	// Build data disks once (independent of instance type).
	var dataDisks []instance.DataDisk
	if len(disks.DataDisks) > 0 {
		dataDisks = make([]instance.DataDisk, 0, len(disks.DataDisks))
		for _, disk := range disks.DataDisks {
			dataDisks = append(dataDisks, instance.DataDisk{
				Category:           disk.Category,
				Size:               disk.Size,
				Device:             disk.Device,
				PerformanceLevel:   disk.PerformanceLevel,
				Encrypted:          disk.Encrypted,
				KMSKeyID:           disk.KMSKeyID,
				SnapshotID:         disk.SnapshotID,
				DeleteWithInstance: disk.DeleteWithInstance,
			})
		}
	}

	// Try the lowest-priced compatible tuples first, retaining Karpenter's Spot
	// precedence and the existing capacity/IP feedback and idempotency guards.
	logger := log.FromContext(ctx)
	var lastErr error
	for _, candidate := range candidates {
		it := candidate.instanceType
		capacityType := candidate.capacityType
		// The candidate image is the exact compatible image used for its quote.
		image := candidate.image
		if image.ID == "" {
			continue // Never submit a mismatched architecture to ECS.
		}

		baseOpts := instance.CreateOptions{
			InstanceType:     it.Name,
			ImageID:          image.ID,
			SecurityGroupIDs: securityGroupIDs,
			UserData:         userData,
			Tags:             tags,
			RAMRoleName:      ramRoleName,
			MetadataOptions:  convertMetadataOptions(nodeClass.Spec.MetadataOptions),
			SystemDisk: instance.SystemDisk{
				Category:         disks.SystemDisk.Category,
				Size:             disks.SystemDisk.Size,
				PerformanceLevel: disks.SystemDisk.PerformanceLevel,
				Encrypted:        disks.SystemDisk.Encrypted,
				KMSKeyID:         disks.SystemDisk.KMSKeyID,
			},
			InstanceStorePolicy: disks.InstanceStorePolicy,
			DataDisks:           dataDisks,
			SpotStrategy:        ecsutil.SpotStrategyForCapacityType(capacityType, nodeClass.Spec.SpotStrategy),
		}
		baseOpts.Tags = make(map[string]string, len(tags)+1)
		for key, value := range tags {
			baseOpts.Tags[key] = value
		}
		baseOpts.Tags[v1alpha1.TagResourceModel] = candidate.snapshot
		if nodeClass.Spec.LaunchTemplateID != nil {
			baseOpts.LaunchTemplateID = *nodeClass.Spec.LaunchTemplateID
			baseOpts.LaunchTemplateVersion = nodeClass.Spec.LaunchTemplateVersion
		}
		if capacityType == v1alpha1.CapacityTypeSpot && nodeClass.Spec.SpotPriceLimit != nil {
			baseOpts.SpotPriceLimit = *nodeClass.Spec.SpotPriceLimit
		}

		if c.clusterNetworkConfig != nil && c.clusterNetworkConfig.DualStack {
			one := int32(1)
			baseOpts.Ipv6AddressCount = &one
		}

		instanceID, err := vswitchFallbackCreate(ctx, baseOpts, []v1alpha1.VSwitch{candidate.vswitch}, func(ctx context.Context, opts instance.CreateOptions) (string, error) {
			if nodeClaim.UID == "" {
				return c.instanceProvider.Create(ctx, opts)
			}
			hash, err := nodeClass.Hash()
			if err != nil {
				return "", err
			}
			return c.launchInstance(ctx, nodeClaim, opts, hash)
		}, c.unavailableOfferings)
		if err == nil {
			return instanceID, nil
		}

		if errors.IsInsufficientCapacityError(err) {
			logger.Info("priced launch candidate unavailable, trying next candidate",
				"instanceType", it.Name, "error", err)
			lastErr = err
			continue
		}
		// Non-capacity errors (parameter, permission, etc.) fail fast — retrying
		// a different instance type won't fix a bad parameter.
		return "", fmt.Errorf("failed to create instance: %w", err)
	}
	return "", fmt.Errorf("all priced launch candidates exhausted (%d tried), last error: %w", len(candidates), lastErr)
}

// vswitchFallbackCreate tries vswitches in order, falling back to the next on capacity or IP-exhaustion
// errors. Non-retryable errors cause an immediate return.
func vswitchFallbackCreate(ctx context.Context, baseOpts instance.CreateOptions, vswitches []v1alpha1.VSwitch, createFn func(context.Context, instance.CreateOptions) (string, error), unavailable *cache.UnavailableOfferingsCache) (string, error) {
	capacityType := ecsutil.CapacityTypeFromInstance(ecsutil.ChargeTypePostPaid, baseOpts.SpotStrategy)
	sorted := make([]v1alpha1.VSwitch, len(vswitches))
	copy(sorted, vswitches)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].AvailableIPAddressCount > sorted[j].AvailableIPAddressCount
	})

	var lastErr error
	for _, vsw := range sorted {
		zone := vsw.ZoneID
		if zone == "" {
			zone = vsw.Zone
		}
		if unavailable != nil && zone != "" && !unavailable.IsAvailable(baseOpts.InstanceType, zone, capacityType) {
			lastErr = fmt.Errorf("%s: cached unavailable offering %s/%s/%s", errors.ErrCodeNoStock, baseOpts.InstanceType, zone, capacityType)
			continue
		}
		opts := baseOpts
		opts.VSwitchID = vsw.ID
		instanceID, err := createFn(ctx, opts)
		if err == nil {
			return instanceID, nil
		}
		if errors.IsInsufficientCapacityError(err) {
			if unavailable != nil && zone != "" && errors.IsInstanceTypeCapacityError(err) {
				unavailable.MarkUnavailable(baseOpts.InstanceType, zone, capacityType)
			}
			log.FromContext(ctx).Info("vSwitch capacity/IP exhausted, trying next",
				"vswitch", vsw.ID, "zone", vsw.Zone, "error", err)
			lastErr = err
			continue
		}
		return "", fmt.Errorf("failed to create instance: %w", err)
	}
	return "", fmt.Errorf("all vSwitches exhausted, last error: %w", lastErr)
}

func convertMetadataOptions(opts *v1alpha1.MetadataOptions) *instance.MetadataOptions {
	if opts == nil {
		return nil
	}
	converted := &instance.MetadataOptions{}
	if opts.HttpEndpoint != nil && *opts.HttpEndpoint != "" {
		endpoint := *opts.HttpEndpoint
		converted.HttpEndpoint = &endpoint
	}
	if opts.HttpTokens != "" {
		tokens := opts.HttpTokens
		converted.HttpTokens = &tokens
	}
	if opts.HttpPutResponseHopLimit != nil {
		hopLimit := *opts.HttpPutResponseHopLimit
		converted.HttpPutResponseHopLimit = &hopLimit
	}
	return converted
}

func validateRAMRoleForCreate(nodeClass *v1alpha1.ECSNodeClass) (string, error) {
	if nodeClass.Spec.Role == nil {
		return "", nil
	}
	if nodeClass.Status.RAMRole == nil {
		return "", fmt.Errorf("nodeclass status.ramRole is not resolved for spec.role %q", *nodeClass.Spec.Role)
	}
	if *nodeClass.Status.RAMRole != *nodeClass.Spec.Role {
		return "", fmt.Errorf("nodeclass status.ramRole %q does not match spec.role %q; status may be stale", *nodeClass.Status.RAMRole, *nodeClass.Spec.Role)
	}
	return *nodeClass.Status.RAMRole, nil
}

func (c *CloudProvider) convertInstanceToNodeClaim(ctx context.Context, inst *instance.Instance, original *coreapis.NodeClaim, instanceTypes []*instancetype.InstanceType, clusterID string) (*coreapis.NodeClaim, error) {
	// Find instance type info
	capacity, allocatable, err := c.instanceResources(ctx, inst)
	if err != nil {
		return nil, err
	}
	var matchedIT *instancetype.InstanceType

	for _, it := range instanceTypes {
		if it.Name == inst.InstanceType {
			matchedIT = it
			break
		}
	}

	// Copy original NodeClaim and update status
	nodeClaim := original.DeepCopy()
	nodeClaim.Status.ProviderID = fmt.Sprintf("%s.%s", inst.Region, inst.InstanceID)
	nodeClaim.Status.Capacity = capacity
	nodeClaim.Status.Allocatable = allocatable
	nodeClaim.Status.ImageID = inst.ImageID
	nodeClaim.Labels = instanceLabelsFromInstance(inst, matchedIT)
	return nodeClaim, nil
}

func setNodeClassHashAnnotation(nodeClaim *coreapis.NodeClaim, hash string) {
	if nodeClaim.Annotations == nil {
		nodeClaim.Annotations = make(map[string]string)
	}
	nodeClaim.Annotations[v1alpha1.AnnotationECSNodeClassHash] = hash
	nodeClaim.Annotations[v1alpha1.AnnotationECSNodeClassHashVersion] = v1alpha1.ECSNodeClassHashVersion
}

// convertKubeletConfigToBootstrap converts v1alpha1.KubeletConfiguration to bootstrap.KubeletConfiguration
func convertKubeletConfigToBootstrap(config *v1alpha1.KubeletConfiguration) *bootstrap.KubeletConfiguration {
	if config == nil {
		return nil
	}

	return &bootstrap.KubeletConfiguration{
		MaxPods:                     config.MaxPods,
		PodsPerCore:                 config.PodsPerCore,
		CPUCFSQuota:                 config.CPUCFSQuota,
		SystemReserved:              config.SystemReserved,
		KubeReserved:                config.KubeReserved,
		EvictionHard:                config.EvictionHard,
		EvictionSoft:                config.EvictionSoft,
		EvictionSoftGracePeriod:     config.EvictionSoftGracePeriod,
		EvictionMaxPodGracePeriod:   config.EvictionMaxPodGracePeriod,
		ImageGCHighThresholdPercent: config.ImageGCHighThresholdPercent,
		ImageGCLowThresholdPercent:  config.ImageGCLowThresholdPercent,
		ClusterDNS:                  config.ClusterDNS,
	}
}

// selectImageForArchitecture never guesses missing/unknown architectures and
// returns an empty image when no known compatible candidate is available.
func selectImageForArchitecture(images []v1alpha1.Image, targetArch string) v1alpha1.Image {
	target, known := ecsutil.KnownArchitecture(targetArch)
	if !known {
		return v1alpha1.Image{}
	}
	var selected v1alpha1.Image
	for _, img := range images {
		arch, known := ecsutil.KnownArchitecture(img.Architecture)
		if !known || arch != target || img.ID == "" {
			continue
		}
		if selected.ID == "" || imagefamily.PreferredImage(img, selected) {
			selected = img
		}
	}
	return selected
}

func filterInstanceTypesByImages(types []*instancetype.InstanceType, images []v1alpha1.Image) []*instancetype.InstanceType {
	result := make([]*instancetype.InstanceType, 0, len(types))
	for _, it := range types {
		if it != nil && selectImageForArchitecture(images, it.Architecture).ID != "" {
			result = append(result, it)
		}
	}
	return result
}

// Drift detection helper functions (from drift.go)

func isImageAllowed(imageID string, allowedImages []v1alpha1.Image) bool {
	for _, img := range allowedImages {
		if img.ID == imageID {
			return true
		}
	}
	return false
}

func isVSwitchAllowed(vswitchID string, allowedVSwitches []v1alpha1.VSwitch) bool {
	for _, vs := range allowedVSwitches {
		if vs.ID == vswitchID {
			return true
		}
	}
	return false
}

func isSecurityGroupAllowed(instanceSGs []string, allowedSGs []v1alpha1.SecurityGroup) bool {
	allowedIDs := make([]string, 0, len(allowedSGs))
	for _, sg := range allowedSGs {
		allowedIDs = append(allowedIDs, sg.ID)
	}
	return securitygroups.EqualIDs(instanceSGs, allowedIDs)
}

// parseLaunchTime parses instance creation time with fallback to current time
func parseLaunchTime(ctx context.Context, instanceID, creationTime string) time.Time {
	launchTime, err := time.Parse(time.RFC3339, creationTime)
	if err != nil {
		// Log warning and use current time as fallback
		log.FromContext(ctx).V(1).Info("Failed to parse instance creation time, using current time",
			"instanceID", instanceID,
			"creationTime", creationTime,
			"error", err)
		return time.Now()
	}
	return launchTime
}
