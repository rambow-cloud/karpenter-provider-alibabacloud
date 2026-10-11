// Copyright 2026 The Alibaba Cloud Karpenter Authors.
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

func TestSecurityFloorsUseEffectiveReplacement(t *testing.T) {
	for _, test := range []struct {
		name, data string
		wantError  bool
	}{
		{"old version rejected", `{"Path":"google.golang.org/grpc","Version":"v1.72.2"}`, true},
		{"fixed replacement accepted", `{"Path":"google.golang.org/grpc","Version":"v1.72.2","Replace":{"Path":"google.golang.org/grpc","Version":"v1.83.2"}}`, false},
		{"unsafe replacement rejected", `{"Path":"google.golang.org/grpc","Version":"v1.83.2","Replace":{"Path":"google.golang.org/grpc","Version":"v1.72.2"}}`, true},
		{"unversioned local replacement rejected", `{"Path":"google.golang.org/grpc","Version":"v1.83.2","Replace":{"Path":"../grpc"}}`, true},
		{"unverified fork rejected", `{"Path":"google.golang.org/grpc","Version":"v1.83.2","Replace":{"Path":"example.com/grpc","Version":"v1.83.2"}}`, true},
		{"prerelease before fix rejected", `{"Path":"golang.org/x/net","Version":"v0.60.0-rc.1"}`, true},
		{"newer fixed version accepted", `{"Path":"golang.org/x/net","Version":"v0.61.0"}`, false},
		{"removed tracked modules allowed", `{"Path":"example.com/unrelated","Version":"v1.0.0"}`, false},
		{"legacy SDK rejected", `{"Path":"github.com/aliyun/alibaba-cloud-sdk-go","Version":"v1.63.107"}`, true},
		{"legacy PDF dependency rejected", `{"Path":"rsc.io/pdf","Version":"v0.1.1"}`, true},
		{"empty list rejected", ``, true},
		{"broken list rejected", `{`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := checkModules([]byte(test.data)); (err != nil) != test.wantError {
				t.Fatalf("checkModules() error = %v, wantError = %v", err, test.wantError)
			}
		})
	}
}
