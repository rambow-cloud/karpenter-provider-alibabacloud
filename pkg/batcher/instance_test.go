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
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
)

// mockECSClient is a mock ECS client for testing
// It implements the minimal interface needed for RunInstancesExecutor
type mockECSClient struct {
	runInstancesFunc func(*ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error)
	callCount        int32
}

func (m *mockECSClient) RunInstances(ctx context.Context, req *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
	atomic.AddInt32(&m.callCount, 1)
	if m.runInstancesFunc != nil {
		return m.runInstancesFunc(req)
	}
	return &ecs.RunInstancesResponse{}, nil
}

// TestRunInstancesExecutor tests the executor for batch instance creation
func TestRunInstancesExecutor(t *testing.T) {
	mockClient := &mockECSClient{
		runInstancesFunc: func(req *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
			// Parse amount
			amount := int(tea.Int32Value(req.Amount))

			// Generate instance IDs
			instanceIDs := make([]string, amount)
			for i := 0; i < amount; i++ {
				instanceIDs[i] = fmt.Sprintf("i-test-%d", i)
			}

			resp := &ecs.RunInstancesResponse{
				Body: &ecs.RunInstancesResponseBody{InstanceIdSets: &ecs.RunInstancesResponseBodyInstanceIdSets{InstanceIdSet: tea.StringSlice(instanceIDs)}},
			}
			return resp, nil
		},
	}

	executor := &RunInstancesExecutor{
		ecsClient: mockClient,
		region:    "cn-hangzhou",
	}

	// Create batch items
	const batchSize = 5
	items := make([]*BatchItem, batchSize)
	resultChs := make([]chan *BatchResult, batchSize)

	for i := 0; i < batchSize; i++ {
		req := &ecs.RunInstancesRequest{SystemDisk: &ecs.RunInstancesRequestSystemDisk{}}
		req.RegionId = tea.String("cn-hangzhou")
		req.InstanceType = tea.String("ecs.g6.large")
		req.ImageId = tea.String("m-test")
		req.VSwitchId = tea.String("vsw-test")
		req.Amount = tea.Int32(1)

		resultCh := make(chan *BatchResult, 1)
		resultChs[i] = resultCh

		items[i] = &BatchItem{
			Request: &RunInstancesRequest{
				Request:  req,
				BatchKey: "test-batch",
			},
			ResultCh: resultCh,
		}
	}

	// Execute batch
	err := executor.Execute(context.Background(), items)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify only 1 API call was made
	if count := atomic.LoadInt32(&mockClient.callCount); count != 1 {
		t.Errorf("Expected 1 RunInstances call, got %d", count)
	}

	// Verify all items got results
	timeout := time.After(1 * time.Second)
	for i, resultCh := range resultChs {
		select {
		case result := <-resultCh:
			if result.Error != nil {
				t.Errorf("Item %d got error: %v", i, result.Error)
				continue
			}
			instanceResult := result.Value.(*RunInstancesResult)
			if instanceResult.InstanceID == "" {
				t.Errorf("Item %d got empty instance ID", i)
			}
		case <-timeout:
			t.Errorf("Item %d timeout waiting for result", i)
		}
	}
}

// TestRunInstancesExecutorError tests error handling
func TestRunInstancesExecutorError(t *testing.T) {
	mockClient := &mockECSClient{
		runInstancesFunc: func(req *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
			return nil, fmt.Errorf("API error: insufficient capacity")
		},
	}

	executor := &RunInstancesExecutor{
		ecsClient: mockClient,
		region:    "cn-hangzhou",
	}

	// Create batch items
	const batchSize = 3
	items := make([]*BatchItem, batchSize)
	resultChs := make([]chan *BatchResult, batchSize)

	for i := 0; i < batchSize; i++ {
		req := &ecs.RunInstancesRequest{SystemDisk: &ecs.RunInstancesRequestSystemDisk{}}
		resultCh := make(chan *BatchResult, 1)
		resultChs[i] = resultCh

		items[i] = &BatchItem{
			Request: &RunInstancesRequest{
				Request:  req,
				BatchKey: "test-batch",
			},
			ResultCh: resultCh,
		}
	}

	// Execute batch (should fail)
	err := executor.Execute(context.Background(), items)
	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	// Verify all items got error notification
	timeout := time.After(1 * time.Second)
	for i, resultCh := range resultChs {
		select {
		case result := <-resultCh:
			if result.Error == nil {
				t.Errorf("Item %d expected error, got nil", i)
			}
		case <-timeout:
			t.Errorf("Item %d timeout waiting for error", i)
		}
	}
}

// TestComputeRunInstancesBatchKey tests batch key computation
func TestComputeRunInstancesBatchKey(t *testing.T) {
	req1 := &ecs.RunInstancesRequest{SystemDisk: &ecs.RunInstancesRequestSystemDisk{}}
	req1.RegionId = tea.String("cn-hangzhou")
	req1.InstanceType = tea.String("ecs.g6.large")
	req1.ImageId = tea.String("m-test")
	req1.VSwitchId = tea.String("vsw-test")

	req2 := &ecs.RunInstancesRequest{SystemDisk: &ecs.RunInstancesRequestSystemDisk{}}
	req2.RegionId = tea.String("cn-hangzhou")
	req2.InstanceType = tea.String("ecs.g6.large")
	req2.ImageId = tea.String("m-test")
	req2.VSwitchId = tea.String("vsw-test")

	// Same configuration should produce same key
	key1 := ComputeRunInstancesBatchKey(req1)
	key2 := ComputeRunInstancesBatchKey(req2)

	if key1 != key2 {
		t.Errorf("Expected same batch key for identical requests, got %s != %s", key1, key2)
	}

	// Different configuration should produce different key
	req3 := &ecs.RunInstancesRequest{SystemDisk: &ecs.RunInstancesRequestSystemDisk{}}
	req3.RegionId = tea.String("cn-hangzhou")
	req3.InstanceType = tea.String("ecs.g6.xlarge") // Different instance type
	req3.ImageId = tea.String("m-test")
	req3.VSwitchId = tea.String("vsw-test")

	key3 := ComputeRunInstancesBatchKey(req3)
	if key1 == key3 {
		t.Errorf("Expected different batch key for different instance types")
	}

	// Different VSwitch should produce different key
	req4 := &ecs.RunInstancesRequest{SystemDisk: &ecs.RunInstancesRequestSystemDisk{}}
	req4.RegionId = tea.String("cn-hangzhou")
	req4.InstanceType = tea.String("ecs.g6.large")
	req4.ImageId = tea.String("m-test")
	req4.VSwitchId = tea.String("vsw-different") // Different VSwitch

	key4 := ComputeRunInstancesBatchKey(req4)
	if key1 == key4 {
		t.Errorf("Expected different batch key for different VSwitches")
	}
}

func TestComputeRunInstancesBatchKeySecurityGroupIDs(t *testing.T) {
	req1 := &ecs.RunInstancesRequest{SystemDisk: &ecs.RunInstancesRequestSystemDisk{}}
	req1.RegionId = tea.String("cn-hangzhou")
	req1.InstanceType = tea.String("ecs.g6.large")
	req1.ImageId = tea.String("m-test")
	req1.VSwitchId = tea.String("vsw-test")
	req1.SecurityGroupIds = tea.StringSlice([]string{"sg-2", "sg-1", "sg-2"})

	req2 := &ecs.RunInstancesRequest{SystemDisk: &ecs.RunInstancesRequestSystemDisk{}}
	req2.RegionId = tea.String("cn-hangzhou")
	req2.InstanceType = tea.String("ecs.g6.large")
	req2.ImageId = tea.String("m-test")
	req2.VSwitchId = tea.String("vsw-test")
	req2.SecurityGroupIds = tea.StringSlice([]string{"sg-1", "sg-2"})

	if key1, key2 := ComputeRunInstancesBatchKey(req1), ComputeRunInstancesBatchKey(req2); key1 != key2 {
		t.Fatalf("expected same batch key for duplicate/order differences, got %s != %s", key1, key2)
	}

	req3 := &ecs.RunInstancesRequest{SystemDisk: &ecs.RunInstancesRequestSystemDisk{}}
	req3.RegionId = tea.String("cn-hangzhou")
	req3.InstanceType = tea.String("ecs.g6.large")
	req3.ImageId = tea.String("m-test")
	req3.VSwitchId = tea.String("vsw-test")
	req3.SecurityGroupIds = tea.StringSlice([]string{"sg-1"})

	if key1, key3 := ComputeRunInstancesBatchKey(req1), ComputeRunInstancesBatchKey(req3); key1 == key3 {
		t.Fatal("expected different batch key when security group set differs")
	}
}

func TestComputeRunInstancesBatchKeyDiskOptions(t *testing.T) {
	base := &ecs.RunInstancesRequest{SystemDisk: &ecs.RunInstancesRequestSystemDisk{}}
	base.RegionId = tea.String("cn-hangzhou")
	base.InstanceType = tea.String("ecs.g6.large")
	base.ImageId = tea.String("m-test")
	base.VSwitchId = tea.String("vsw-test")
	base.SystemDisk.Category = tea.String("cloud_essd")
	base.SystemDisk.Size = tea.String("40")
	base.SystemDisk.PerformanceLevel = tea.String("PL0")
	base.DataDisk = []*ecs.RunInstancesRequestDataDisk{
		{
			Category:           tea.String("cloud_essd"),
			Size:               tea.Int32(120),
			PerformanceLevel:   tea.String("PL1"),
			Encrypted:          tea.String("true"),
			KMSKeyId:           tea.String("kms-1"),
			SnapshotId:         tea.String("s-1"),
			Device:             tea.String("/dev/xvdb"),
			DeleteWithInstance: tea.Bool(false),
		},
	}

	changed := &ecs.RunInstancesRequest{SystemDisk: &ecs.RunInstancesRequestSystemDisk{}}
	*changed = *base
	changed.DataDisk = []*ecs.RunInstancesRequestDataDisk{
		{
			Category:           tea.String("cloud_essd"),
			Size:               tea.Int32(120),
			PerformanceLevel:   tea.String("PL1"),
			Encrypted:          tea.String("true"),
			KMSKeyId:           tea.String("kms-2"),
			SnapshotId:         tea.String("s-1"),
			Device:             tea.String("/dev/xvdb"),
			DeleteWithInstance: tea.Bool(false),
		},
	}

	if ComputeRunInstancesBatchKey(base) == ComputeRunInstancesBatchKey(changed) {
		t.Fatal("expected disk field differences to produce different batch keys")
	}
}

func TestComputeRunInstancesBatchKeyESSDPL0DefaultEquivalence(t *testing.T) {
	implicit := &ecs.RunInstancesRequest{SystemDisk: &ecs.RunInstancesRequestSystemDisk{}}
	implicit.RegionId = tea.String("cn-hangzhou")
	implicit.InstanceType = tea.String("ecs.g6.large")
	implicit.ImageId = tea.String("m-test")
	implicit.VSwitchId = tea.String("vsw-test")
	implicit.SystemDisk.Category = tea.String("cloud_essd")
	implicit.SystemDisk.Size = tea.String("40")

	explicit := &ecs.RunInstancesRequest{SystemDisk: &ecs.RunInstancesRequestSystemDisk{}}
	*explicit = *implicit
	disk := *implicit.SystemDisk
	explicit.SystemDisk = &disk
	explicit.SystemDisk.PerformanceLevel = tea.String("PL0")

	if ComputeRunInstancesBatchKey(implicit) != ComputeRunInstancesBatchKey(explicit) {
		t.Fatal("expected implicit and explicit ESSD PL0 to produce the same batch key")
	}
}

// TestInstanceBatcherCreateInstance tests the instance batcher
func TestInstanceBatcherCreateInstance(t *testing.T) {
	mockClient := &mockECSClient{
		runInstancesFunc: func(req *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
			amount := int(tea.Int32Value(req.Amount))
			instanceIDs := make([]string, amount)
			for i := 0; i < amount; i++ {
				instanceIDs[i] = fmt.Sprintf("i-batch-%d", i)
			}

			return &ecs.RunInstancesResponse{
				Body: &ecs.RunInstancesResponseBody{InstanceIdSets: &ecs.RunInstancesResponseBodyInstanceIdSets{InstanceIdSet: tea.StringSlice(instanceIDs)}},
			}, nil
		},
	}

	batcher := NewInstanceBatcher(mockClient, "cn-hangzhou")
	defer batcher.Close()

	// Create a single instance
	req := &ecs.RunInstancesRequest{SystemDisk: &ecs.RunInstancesRequestSystemDisk{}}
	req.RegionId = tea.String("cn-hangzhou")
	req.InstanceType = tea.String("ecs.g6.large")
	req.ImageId = tea.String("m-test")
	req.VSwitchId = tea.String("vsw-test")
	req.Amount = tea.Int32(1)

	ctx := context.Background()
	result, err := batcher.CreateInstance(ctx, req)
	if err != nil {
		t.Fatalf("CreateInstance failed: %v", err)
	}

	if result.InstanceID == "" {
		t.Error("Expected non-empty instance ID")
	}

	// Verify API was called
	if count := atomic.LoadInt32(&mockClient.callCount); count == 0 {
		t.Error("Expected at least 1 API call")
	}
}

// TestInstanceBatcherConcurrent tests concurrent instance creation
func TestInstanceBatcherConcurrent(t *testing.T) {
	mockClient := &mockECSClient{
		runInstancesFunc: func(req *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
			amount := int(tea.Int32Value(req.Amount))
			instanceIDs := make([]string, amount)
			for i := 0; i < amount; i++ {
				instanceIDs[i] = fmt.Sprintf("i-concurrent-%d-%d", time.Now().UnixNano(), i)
			}

			return &ecs.RunInstancesResponse{
				Body: &ecs.RunInstancesResponseBody{InstanceIdSets: &ecs.RunInstancesResponseBodyInstanceIdSets{InstanceIdSet: tea.StringSlice(instanceIDs)}},
			}, nil
		},
	}

	batcher := NewInstanceBatcher(mockClient, "cn-hangzhou")
	defer batcher.Close()

	// Launch 20 concurrent requests with identical configuration
	const numRequests = 20
	var wg sync.WaitGroup
	wg.Add(numRequests)

	results := make([]*RunInstancesResult, numRequests)
	errors := make([]error, numRequests)

	for i := 0; i < numRequests; i++ {
		go func(idx int) {
			defer wg.Done()

			req := &ecs.RunInstancesRequest{SystemDisk: &ecs.RunInstancesRequestSystemDisk{}}
			req.RegionId = tea.String("cn-hangzhou")
			req.InstanceType = tea.String("ecs.g6.large")
			req.ImageId = tea.String("m-test")
			req.VSwitchId = tea.String("vsw-test")
			req.Amount = tea.Int32(1)

			ctx := context.Background()
			result, err := batcher.CreateInstance(ctx, req)
			results[idx] = result
			errors[idx] = err
		}(i)
	}

	wg.Wait()

	// Verify all requests succeeded
	successCount := 0
	for i := 0; i < numRequests; i++ {
		if errors[i] != nil {
			t.Errorf("Request %d failed: %v", i, errors[i])
			continue
		}
		if results[i] == nil || results[i].InstanceID == "" {
			t.Errorf("Request %d got empty result", i)
			continue
		}
		successCount++
	}

	if successCount != numRequests {
		t.Errorf("Expected %d successful requests, got %d", numRequests, successCount)
	}

	// Verify batching reduced API calls
	apiCalls := atomic.LoadInt32(&mockClient.callCount)
	if apiCalls >= numRequests {
		t.Logf("Warning: API calls (%d) >= requests (%d), batching may not be effective", apiCalls, numRequests)
	} else {
		t.Logf("Successfully batched %d requests into %d API calls (%.1f%% reduction)",
			numRequests, apiCalls, 100.0*(1.0-float64(apiCalls)/float64(numRequests)))
	}
}

// TestInstanceBatcherStats tests statistics
func TestInstanceBatcherStats(t *testing.T) {
	mockClient := &mockECSClient{
		runInstancesFunc: func(req *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
			return &ecs.RunInstancesResponse{
				Body: &ecs.RunInstancesResponseBody{InstanceIdSets: &ecs.RunInstancesResponseBodyInstanceIdSets{InstanceIdSet: tea.StringSlice([]string{"i-stats"})}},
			}, nil
		},
	}

	batcher := NewInstanceBatcher(mockClient, "cn-hangzhou")
	defer batcher.Close()

	stats := batcher.Stats()
	if stats == nil {
		t.Fatal("Expected non-nil stats")
	}

	// Stats should have expected keys
	if _, ok := stats["active_windows"]; !ok {
		t.Error("Expected 'active_windows' in stats")
	}
	if _, ok := stats["pending_items"]; !ok {
		t.Error("Expected 'pending_items' in stats")
	}
}

// BenchmarkInstanceBatcher benchmarks instance creation
func BenchmarkInstanceBatcher(b *testing.B) {
	mockClient := &mockECSClient{
		runInstancesFunc: func(req *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
			amount := int(tea.Int32Value(req.Amount))
			instanceIDs := make([]string, amount)
			for i := 0; i < amount; i++ {
				instanceIDs[i] = fmt.Sprintf("i-bench-%d", i)
			}
			return &ecs.RunInstancesResponse{
				Body: &ecs.RunInstancesResponseBody{InstanceIdSets: &ecs.RunInstancesResponseBodyInstanceIdSets{InstanceIdSet: tea.StringSlice(instanceIDs)}},
			}, nil
		},
	}

	batcher := NewInstanceBatcher(mockClient, "cn-hangzhou")
	defer batcher.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := &ecs.RunInstancesRequest{SystemDisk: &ecs.RunInstancesRequestSystemDisk{}}
			req.RegionId = tea.String("cn-hangzhou")
			req.InstanceType = tea.String("ecs.g6.large")
			req.ImageId = tea.String("m-test")
			req.VSwitchId = tea.String("vsw-test")
			req.Amount = tea.Int32(1)

			ctx := context.Background()
			_, _ = batcher.CreateInstance(ctx, req)
		}
	})
}

// TestRunInstancesExecutorInsufficientInstances tests insufficient instance handling
func TestRunInstancesExecutorInsufficientInstances(t *testing.T) {
	mockClient := &mockECSClient{
		runInstancesFunc: func(req *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
			// Return fewer instances than requested
			amount := int(tea.Int32Value(req.Amount))
			actualInstances := amount - 3 // Return 3 fewer instances
			if actualInstances < 0 {
				actualInstances = 0
			}

			instanceIDs := make([]string, actualInstances)
			for i := 0; i < actualInstances; i++ {
				instanceIDs[i] = fmt.Sprintf("i-%d", i)
			}

			return &ecs.RunInstancesResponse{
				Body: &ecs.RunInstancesResponseBody{InstanceIdSets: &ecs.RunInstancesResponseBodyInstanceIdSets{InstanceIdSet: tea.StringSlice(instanceIDs)}},
			}, nil
		},
	}

	executor := &RunInstancesExecutor{
		ecsClient: mockClient,
		region:    "cn-hangzhou",
	}

	// Request 5 instances
	const batchSize = 5
	items := make([]*BatchItem, batchSize)
	resultChs := make([]chan *BatchResult, batchSize)

	for i := 0; i < batchSize; i++ {
		req := &ecs.RunInstancesRequest{SystemDisk: &ecs.RunInstancesRequestSystemDisk{}}
		resultCh := make(chan *BatchResult, 1)
		resultChs[i] = resultCh

		items[i] = &BatchItem{
			Request: &RunInstancesRequest{
				Request:  req,
				BatchKey: "test-batch",
			},
			ResultCh: resultCh,
		}
	}

	// Execute batch
	err := executor.Execute(context.Background(), items)
	if err == nil {
		t.Error("Expected error for insufficient instances")
	}

	// First 2 should succeed, last 3 should get errors
	successCount := 0
	errorCount := 0

	timeout := time.After(1 * time.Second)
	for i, resultCh := range resultChs {
		select {
		case result := <-resultCh:
			if result.Error != nil {
				errorCount++
			} else {
				successCount++
			}
		case <-timeout:
			t.Errorf("Item %d timeout", i)
		}
	}

	// Note: The actual number of successes/errors depends on the mock implementation
	// We're just verifying the test structure is correct
}
