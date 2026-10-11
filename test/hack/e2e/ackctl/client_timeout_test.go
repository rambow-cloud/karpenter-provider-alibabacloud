package main

import (
	"testing"
	"time"
)

func TestACKClientConnectionTimeoutsUseMilliseconds(t *testing.T) {
	cfg := &Config{}
	cfg.AlibabaCloud.AccessKeyID = "synthetic-test-key"
	cfg.AlibabaCloud.AccessKeySecret = "synthetic-test-secret"
	cfg.AlibabaCloud.RegionID = "cn-hangzhou"
	csClient, err := newCSClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ecsClient, err := newECSClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	vpcClient, err := newVPCClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]*int{"CS": csClient.ConnectTimeout, "ECS": ecsClient.ConnectTimeout, "VPC": vpcClient.ConnectTimeout} {
		t.Run(name, func(t *testing.T) {
			if value == nil || time.Duration(*value)*time.Millisecond != 30*time.Second {
				t.Fatalf("ConnectTimeout must represent 30 seconds in milliseconds, got %v", value)
			}
		})
	}
}
