package cloudprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"sigs.k8s.io/controller-runtime/pkg/client"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
	coreprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

const ackRemovalAnnotation = "karpenter.alibabacloud.com/ack-removal"

type ackRemoval struct {
	TaskID     string `json:"taskID,omitempty"`
	ClusterID  string `json:"clusterID"`
	PoolID     string `json:"poolID"`
	InstanceID string `json:"instanceID"`
	ClaimUID   string `json:"claimUID"`
}

// deleteACKInstance coordinates native desired capacity before deleting ECS.
// The receipt survives reconcile retries/controller restarts; a pending or failed
// ACK operation must never fall through to a direct ECS deletion.
func (c *CloudProvider) deleteACKInstance(ctx context.Context, claim *coreapis.NodeClaim, id string) error {
	if c.kubeClient == nil || claim.UID == "" {
		return fmt.Errorf("ACK termination requires a persisted NodeClaim identity")
	}
	live := &coreapis.NodeClaim{}
	reader := c.launchReader
	if reader == nil {
		reader = c.kubeClient
	}
	if err := reader.Get(ctx, client.ObjectKeyFromObject(claim), live); err != nil {
		return fmt.Errorf("read ACK termination receipt: %w", err)
	}
	if live.UID != claim.UID || live.Status.ProviderID != claim.Status.ProviderID {
		return fmt.Errorf("NodeClaim termination identity changed")
	}
	var receipt ackRemoval
	if raw := live.Annotations[ackRemovalAnnotation]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &receipt); err != nil || receipt.PoolID == "" || receipt.InstanceID != id || receipt.ClaimUID != string(claim.UID) || receipt.ClusterID != c.ackNodePoolProvider.ClusterID() {
			return fmt.Errorf("ACK termination receipt identity is invalid")
		}
		if receipt.TaskID == "" {
			// The controller may have stopped after submitting the request but
			// before saving its task ID. ACK removal has no client token; do not
			// retry a possibly successful decrement. Preserve the intent for repair.
			return fmt.Errorf("ACK removal outcome is unknown; inspect the ACK task and restore its taskID in %s before retrying", ackRemovalAnnotation)
		}
		complete, err := c.ackNodePoolProvider.RemovalComplete(ctx, receipt.TaskID)
		if err != nil || !complete {
			return err
		}
	}
	member, err := c.ackNodePoolProvider.LookupInstance(ctx, id)
	if err != nil {
		return err
	}
	inst, getErr := c.instanceProvider.Get(ctx, id, true)
	if getErr != nil && !coreprovider.IsNodeClaimNotFoundError(getErr) && !instance.IsNotFoundError(getErr) {
		return getErr
	}
	if inst != nil {
		if claim.Status.ProviderID != inst.Region+"."+inst.InstanceID {
			return fmt.Errorf("ECS instance region/identity does not match NodeClaim")
		}
		for key, value := range map[string]string{
			v1alpha1.TagManagedBy: v1alpha1.TagManagedByValue, v1alpha1.TagClusterID: c.ackNodePoolProvider.ClusterID(),
			v1alpha1.TagNodeClaim: claim.Name, v1alpha1.TagNodeClaimUID: string(claim.UID),
		} {
			if inst.Tags[key] != value {
				return fmt.Errorf("refusing ACK termination: instance ownership mismatch for %s", key)
			}
		}
	}
	if member != nil {
		if receipt.TaskID != "" {
			return fmt.Errorf("ACK removal task succeeded but native membership remains; ECS termination deferred")
		}
		if inst == nil {
			return fmt.Errorf("ACK membership remains for missing ECS; retain NodeClaim for capacity reconciliation")
		}
		if cluster := inst.Tags["ack.aliyun.com"]; cluster != "" && cluster != c.ackNodePoolProvider.ClusterID() {
			return fmt.Errorf("instance belongs to another ACK cluster")
		}
		if pool := inst.Tags["ack.alibabacloud.com/nodepool-id"]; pool != "" && pool != member.PoolID {
			return fmt.Errorf("ACK pool membership does not match instance tags")
		}
		if state := strings.ToLower(member.State); state == "removing" || state == "initial" || state == "pending" {
			return nil
		}
		// Persist intent with an optimistic lock BEFORE the non-idempotent API.
		// This also prevents two reconcilers submitting the same decrement.
		receipt = ackRemoval{ClusterID: c.ackNodePoolProvider.ClusterID(), PoolID: member.PoolID, InstanceID: id, ClaimUID: string(claim.UID)}
		if err := c.persistACKRemoval(ctx, live, receipt); err != nil {
			return err
		}
		taskID, err := c.ackNodePoolProvider.RemoveInstance(ctx, member.PoolID, id)
		if err != nil {
			return err
		}
		receipt.TaskID = taskID
		return c.persistACKRemoval(ctx, live, receipt)
	}
	if inst == nil {
		return coreprovider.NewNodeClaimNotFoundError(fmt.Errorf("instance %s and native ACK membership are absent", id))
	}
	if receipt.TaskID == "" && (inst.Tags["ack.alibabacloud.com/nodepool-id"] != "" || inst.Tags["acs:autoscaling:scalingGroupId"] != "") {
		return fmt.Errorf("native adoption/removal is unresolved; ECS termination deferred")
	}
	return c.instanceProvider.Delete(ctx, id)
}

func (c *CloudProvider) persistACKRemoval(ctx context.Context, live *coreapis.NodeClaim, receipt ackRemoval) error {
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	before := live.DeepCopy()
	if live.Annotations == nil {
		live.Annotations = map[string]string{}
	}
	live.Annotations[ackRemovalAnnotation] = string(data)
	if err := c.kubeClient.Patch(ctx, live, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("persist ACK removal receipt; ECS termination deferred: %w", err)
	}
	return nil
}
