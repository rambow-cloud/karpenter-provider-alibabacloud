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

package batcher

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/utils/securitygroups"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
)

// RunInstancesRequest represents a request to create an instance
type RunInstancesRequest struct {
	// ECS RunInstances request
	Request *ecs.RunInstancesRequest

	// Additional metadata for batching
	BatchKey string
}

// RunInstancesResult represents the result of creating an instance
type RunInstancesResult struct {
	InstanceID string
	LaunchTime time.Time
}

// ECSClientInterface defines the minimal interface needed for ECS operations
type ECSClientInterface interface {
	RunInstances(ctx context.Context, req *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error)
}

// RunInstancesExecutor executes batch instance creation
type RunInstancesExecutor struct {
	ecsClient ECSClientInterface
	region    string
}

// NewRunInstancesExecutor creates a new RunInstances batch executor
func NewRunInstancesExecutor(ecsClient ECSClientInterface, region string) *RunInstancesExecutor {
	return &RunInstancesExecutor{
		ecsClient: ecsClient,
		region:    region,
	}
}

// Execute executes a batch of RunInstances requests
// This implements the BatchExecutor interface
func (e *RunInstancesExecutor) Execute(ctx context.Context, items []*BatchItem) error {
	if len(items) == 0 {
		return nil
	}

	// All items in the same batch should have compatible configurations
	// We use the first request as the template and set Amount to len(items)
	firstRequest := items[0].Request.(*RunInstancesRequest)

	// Build batch request
	req, err := e.cloneRequest(firstRequest.Request)
	if err != nil {
		for _, item := range items {
			item.ResultCh <- &BatchResult{Error: err}
		}
		return err
	}
	req.Amount = tea.Int32(int32(len(items)))

	// Execute batch creation
	startTime := time.Now()
	resp, err := e.ecsClient.RunInstances(ctx, req)

	if err != nil {
		// Batch failed, notify all waiters
		for _, item := range items {
			item.ResultCh <- &BatchResult{
				Error: fmt.Errorf("batch RunInstances failed: %w", err),
			}
		}
		return err
	}

	// Check if we got enough instances
	var instanceIDs []string
	if resp != nil && resp.Body != nil && resp.Body.InstanceIdSets != nil {
		for _, id := range resp.Body.InstanceIdSets.InstanceIdSet {
			if value := tea.StringValue(id); value != "" {
				instanceIDs = append(instanceIDs, value)
			}
		}
	}
	if len(instanceIDs) < len(items) {
		err := fmt.Errorf("insufficient instances returned: requested %d, got %d", len(items), len(instanceIDs))

		// Distribute what we got, then error for the rest
		for i, item := range items {
			if i < len(instanceIDs) {
				item.ResultCh <- &BatchResult{
					Value: &RunInstancesResult{
						InstanceID: instanceIDs[i],
						LaunchTime: startTime,
					},
				}
			} else {
				item.ResultCh <- &BatchResult{
					Error: err,
				}
			}
		}
		return err
	}

	// Success! Distribute results to all waiters
	for i, item := range items {
		item.ResultCh <- &BatchResult{
			Value: &RunInstancesResult{
				InstanceID: instanceIDs[i],
				LaunchTime: startTime,
			},
		}
	}

	return nil
}

// cloneRequest creates a copy of the RunInstances request
func (e *RunInstancesExecutor) cloneRequest(req *ecs.RunInstancesRequest) (*ecs.RunInstancesRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("RunInstances request is required")
	}
	// Preserve every SDK field and isolate nested pointers/slices from callers.
	clone := &ecs.RunInstancesRequest{}
	if err := tea.Convert(req, clone); err != nil {
		return nil, fmt.Errorf("clone RunInstances request: %w", err)
	}
	if tea.StringValue(clone.RegionId) == "" {
		clone.RegionId = tea.String(e.region)
	}
	return clone, nil
}

// ComputeRunInstancesBatchKey computes a batch key for RunInstances request
// Requests with the same key can be batched together
func ComputeRunInstancesBatchKey(req *ecs.RunInstancesRequest) string {
	if req == nil {
		return ""
	}
	// Hash the complete V2 request so fields newly exposed by the SDK cannot
	// accidentally share a batch. Amount is assigned by the batch executor.
	canonical := *req
	canonical.Amount = nil
	canonical.SecurityGroupIds = tea.StringSlice(securitygroups.NormalizeIDs(tea.StringSliceValue(req.SecurityGroupIds)))
	if tea.Float32Value(canonical.SpotPriceLimit) == 0 {
		canonical.SpotPriceLimit = nil
	}
	if req.SystemDisk != nil {
		disk := *req.SystemDisk
		disk.PerformanceLevel = optionalBatchString(normalizeBatchPerformanceLevel(tea.StringValue(disk.Category), tea.StringValue(disk.PerformanceLevel)))
		disk.Encrypted = optionalBatchString(normalizeBatchBool(tea.StringValue(disk.Encrypted), false))
		canonical.SystemDisk = &disk
	}
	canonical.DataDisk = make([]*ecs.RunInstancesRequestDataDisk, 0, len(req.DataDisk))
	for _, original := range req.DataDisk {
		if original == nil {
			continue
		}
		disk := *original
		disk.PerformanceLevel = optionalBatchString(normalizeBatchPerformanceLevel(tea.StringValue(disk.Category), tea.StringValue(disk.PerformanceLevel)))
		disk.Encrypted = optionalBatchString(normalizeBatchBool(tea.StringValue(disk.Encrypted), false))
		if disk.DeleteWithInstance == nil {
			disk.DeleteWithInstance = tea.Bool(true)
		}
		canonical.DataDisk = append(canonical.DataDisk, &disk)
	}
	canonical.Tag = make([]*ecs.RunInstancesRequestTag, 0, len(req.Tag))
	for _, tag := range req.Tag {
		if tag != nil {
			canonical.Tag = append(canonical.Tag, tag)
		}
	}
	sort.Slice(canonical.Tag, func(i, j int) bool {
		left, right := canonical.Tag[i], canonical.Tag[j]
		if tea.StringValue(left.Key) != tea.StringValue(right.Key) {
			return tea.StringValue(left.Key) < tea.StringValue(right.Key)
		}
		return tea.StringValue(left.Value) < tea.StringValue(right.Value)
	})
	data, err := json.Marshal(&canonical)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("run-instances-%x", sha256.Sum256(data))
}

func optionalBatchString(value string) *string {
	if value == "" {
		return nil
	}
	return tea.String(value)
}

func normalizeBatchPerformanceLevel(category, performanceLevel string) string {
	if category == "cloud_essd" {
		if performanceLevel == "" {
			return "PL0"
		}
		return performanceLevel
	}
	return ""
}

func normalizeBatchBool(value string, defaultValue bool) string {
	switch value {
	case "true", "True", "TRUE":
		if defaultValue {
			return ""
		}
		return "true"
	case "false", "False", "FALSE":
		if !defaultValue {
			return ""
		}
		return "false"
	default:
		return ""
	}
}

// InstanceBatcher provides batch instance creation with windowed batching
type InstanceBatcher struct {
	windowedBatcher *WindowedBatcher
	ecsClient       ECSClientInterface
	region          string
}

// NewInstanceBatcher creates a new instance batcher
// Uses 1ms idle / 10ms max windows aligned with optimized AWS Provider settings for faster response
func NewInstanceBatcher(ecsClient ECSClientInterface, region string) *InstanceBatcher {
	executorFactory := func(key string) BatchExecutor {
		return NewRunInstancesExecutor(ecsClient, region)
	}

	return &InstanceBatcher{
		windowedBatcher: NewWindowedBatcher(
			1*time.Millisecond,  // idle duration - 缩短空闲时间以提高响应速度
			10*time.Millisecond, // max duration - 缩短最大等待时间以加快批处理
			executorFactory,
		),
		ecsClient: ecsClient,
		region:    region,
	}
}

// CreateInstance creates an instance with batching
// Multiple concurrent calls with the same configuration will be batched together
func (b *InstanceBatcher) CreateInstance(
	ctx context.Context,
	req *ecs.RunInstancesRequest,
) (*RunInstancesResult, error) {
	if req == nil {
		return nil, fmt.Errorf("RunInstances request is required")
	}
	// Compute batch key
	batchKey := ComputeRunInstancesBatchKey(req)
	if batchKey == "" {
		return nil, fmt.Errorf("RunInstances request cannot be encoded")
	}

	// Create result channel
	resultCh := make(chan *BatchResult, 1)

	// Add to batch window
	request := &RunInstancesRequest{
		Request:  req,
		BatchKey: batchKey,
	}

	err := b.windowedBatcher.Batch(batchKey, request, resultCh)
	if err != nil {
		return nil, fmt.Errorf("failed to add to batch: %w", err)
	}

	// Wait for result
	select {
	case result := <-resultCh:
		if result.Error != nil {
			return nil, result.Error
		}
		return result.Value.(*RunInstancesResult), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close closes the batcher and releases resources
func (b *InstanceBatcher) Close() error {
	if b.windowedBatcher != nil {
		// Close the windowed batcher which will flush any pending items
		b.windowedBatcher.Close()
	}
	return nil
}

// Stats returns batcher statistics
func (b *InstanceBatcher) Stats() map[string]interface{} {
	if b.windowedBatcher != nil {
		return b.windowedBatcher.Stats()
	}
	return map[string]interface{}{}
}
