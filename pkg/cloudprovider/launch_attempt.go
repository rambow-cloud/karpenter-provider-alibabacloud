package cloudprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/errors"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"sigs.k8s.io/controller-runtime/pkg/client"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
)

// Persist only opaque digests and IDs, never userdata or bootstrap credentials.
type launchAttempt struct {
	Fingerprint string `json:"fingerprint"`
	Token       string `json:"token"`
	Hash        string `json:"hash"`
	Number      int    `json:"number"`
	Rejected    bool   `json:"rejected,omitempty"`
	InstanceID  string `json:"instanceID,omitempty"`
}

func (c *CloudProvider) readLaunch(ctx context.Context, claim *coreapis.NodeClaim) (*coreapis.NodeClaim, launchAttempt, error) {
	current := &coreapis.NodeClaim{}
	// Production injects the uncached API reader for read-after-write and CAS.
	reader := c.launchReader
	if reader == nil {
		reader = c.kubeClient
	}
	if err := reader.Get(ctx, client.ObjectKeyFromObject(claim), current); err != nil {
		return nil, launchAttempt{}, err
	}
	if claim.UID == "" || current.UID != claim.UID {
		return nil, launchAttempt{}, fmt.Errorf("NodeClaim identity changed")
	}
	var attempt launchAttempt
	if raw := current.Annotations[v1alpha1.AnnotationLaunchAttempt]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &attempt); err != nil {
			return nil, attempt, fmt.Errorf("invalid launch attempt: %w", err)
		}
		if len(attempt.Token) != 64 || len(attempt.Fingerprint) != 64 || len(attempt.Hash) != 64 || attempt.Number < 1 {
			return nil, attempt, fmt.Errorf("incomplete launch attempt; refusing a new instance")
		}
	}
	return current, attempt, nil
}

func (c *CloudProvider) writeLaunch(ctx context.Context, claim *coreapis.NodeClaim, expected string, attempt launchAttempt) error {
	current, old, err := c.readLaunch(ctx, claim)
	if err != nil {
		return err
	}
	if old.Token != expected {
		return fmt.Errorf("launch attempt changed concurrently")
	}
	original := current.DeepCopy()
	raw, err := json.Marshal(attempt)
	if err != nil {
		return err
	}
	if current.Annotations == nil {
		current.Annotations = map[string]string{}
	}
	current.Annotations[v1alpha1.AnnotationLaunchAttempt] = string(raw)
	return c.kubeClient.Patch(ctx, current, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{}))
}

func (c *CloudProvider) launchInstance(ctx context.Context, claim *coreapis.NodeClaim, opts instance.CreateOptions, hash string) (string, error) {
	_, attempt, err := c.readLaunch(ctx, claim)
	if err != nil {
		return "", err
	}
	if attempt.InstanceID != "" {
		return attempt.InstanceID, nil
	}
	opts.ClientToken = ""
	data, err := json.Marshal(opts)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	fingerprint := hex.EncodeToString(digest[:])
	if attempt.Token != "" && !attempt.Rejected && attempt.Fingerprint != fingerprint {
		return "", fmt.Errorf("launch result unresolved; parameters changed, refusing to create another instance")
	}
	if attempt.Token == "" || attempt.Rejected {
		previous := attempt.Token
		number := attempt.Number + 1
		token := sha256.Sum256([]byte(fmt.Sprintf("%s/%d/%s", claim.UID, number, fingerprint)))
		attempt = launchAttempt{Fingerprint: fingerprint, Token: hex.EncodeToString(token[:]), Hash: hash, Number: number}
		if err := c.writeLaunch(ctx, claim, previous, attempt); err != nil {
			return "", fmt.Errorf("persist launch intent: %w", err)
		}
	}
	opts.ClientToken = attempt.Token
	id, err := c.instanceProvider.Create(ctx, opts)
	if err != nil {
		// Only a structured, definite capacity/IP rejection authorizes fallback.
		if errors.IsCreateCapacityRejection(err) {
			attempt.Rejected = true
			if patchErr := c.writeLaunch(ctx, claim, attempt.Token, attempt); patchErr != nil {
				return "", fmt.Errorf("record rejected launch: %v", patchErr)
			}
		}
		return "", err
	}
	attempt.InstanceID = id
	if err := c.writeLaunch(ctx, claim, attempt.Token, attempt); err != nil {
		return "", fmt.Errorf("persist created instance ID: %w", err)
	}
	return id, nil
}

func (c *CloudProvider) recoverLaunch(ctx context.Context, claim *coreapis.NodeClaim, tags map[string]string) (*coreapis.NodeClaim, error) {
	_, attempt, err := c.readLaunch(ctx, claim)
	if err != nil {
		return nil, err
	}
	if attempt.Token == "" || attempt.Rejected {
		return nil, nil
	}
	if attempt.InstanceID == "" {
		ownership := map[string]string{}
		for _, key := range []string{v1alpha1.TagManagedBy, v1alpha1.TagClusterID, v1alpha1.TagNodeClaim, v1alpha1.TagNodeClaimUID} {
			if tags[key] == "" {
				return nil, fmt.Errorf("missing launch recovery ownership tag %s", key)
			}
			ownership[key] = tags[key]
		}
		ids, err := c.instanceProvider.FindCreation(ctx, ownership)
		if err != nil {
			return nil, err
		}
		if len(ids) > 1 {
			return nil, fmt.Errorf("multiple instances exist for this NodeClaim UID; refusing another launch")
		}
		if len(ids) == 0 {
			return nil, nil
		}
		attempt.InstanceID = ids[0]
		if err := c.writeLaunch(ctx, claim, attempt.Token, attempt); err != nil {
			return nil, err
		}
	}
	inst, err := c.instanceProvider.Get(ctx, attempt.InstanceID, true)
	if err != nil {
		return nil, fmt.Errorf("recover created instance: %w", err)
	}
	for _, key := range []string{v1alpha1.TagManagedBy, v1alpha1.TagClusterID, v1alpha1.TagNodeClaim, v1alpha1.TagNodeClaimUID} {
		if inst.Tags[key] != tags[key] {
			return nil, fmt.Errorf("created instance ownership mismatch for %s", key)
		}
	}
	types, err := c.instanceTypeProvider.List(ctx)
	if err != nil {
		return nil, err
	}
	result := c.convertInstanceToNodeClaim(ctx, inst, claim, types, tags[v1alpha1.TagClusterID])
	setNodeClassHashAnnotation(result, attempt.Hash)
	return result, nil
}
