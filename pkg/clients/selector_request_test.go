package clients

import (
	"context"
	"testing"
)

func TestBuildDescribeVSwitchesRequestMapsSelectorFields(t *testing.T) {
	req := buildDescribeVSwitchesRequest("cn-hangzhou", "vsw-1", map[string]string{"env": "prod"}, "cn-hangzhou-h")

	if req.RegionId == nil || *req.RegionId != "cn-hangzhou" {
		t.Fatalf("expected region to be mapped")
	}
	if req.VSwitchId == nil || *req.VSwitchId != "vsw-1" {
		t.Fatalf("expected VSwitchId to be mapped")
	}
	if req.ZoneId == nil || *req.ZoneId != "cn-hangzhou-h" {
		t.Fatalf("expected ZoneId to be mapped")
	}
	if len(req.Tag) != 1 || req.Tag[0].Key == nil || *req.Tag[0].Key != "env" || req.Tag[0].Value == nil || *req.Tag[0].Value != "prod" {
		t.Fatalf("expected tag filter to be mapped, got %#v", req.Tag)
	}
}

func TestBuildDescribeSecurityGroupsRequestMapsSelectorFields(t *testing.T) {
	req := buildDescribeSecurityGroupsRequest("cn-hangzhou", "sg-1", "app-sg", map[string]string{"env": "prod"})

	if req.SecurityGroupId == nil || *req.SecurityGroupId != "sg-1" {
		t.Fatalf("expected SecurityGroupId to be mapped")
	}
	if req.SecurityGroupName == nil || *req.SecurityGroupName != "app-sg" {
		t.Fatalf("expected SecurityGroupName to be mapped")
	}
	if len(req.Tag) != 1 || req.Tag[0].Key == nil || *req.Tag[0].Key != "env" || req.Tag[0].Value == nil || *req.Tag[0].Value != "prod" {
		t.Fatalf("expected tag filter to be mapped, got %#v", req.Tag)
	}
}

func TestBuildDescribeImagesRequestMapsSelectorFields(t *testing.T) {
	req, err := buildDescribeImagesRequest("cn-hangzhou", nil, map[string]string{
		"ImageFamily":     "aliyun_3",
		"ImageName":       "app-*",
		"ImageOwnerAlias": "others",
		"ImageOwnerID":    "1234567890123456",
		"tag:env":         "prod",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if req.ImageFamily == nil || *req.ImageFamily != "aliyun_3" {
		t.Fatalf("expected ImageFamily to be mapped")
	}
	if req.ImageName == nil || *req.ImageName != "app-*" {
		t.Fatalf("expected ImageName to be mapped")
	}
	if req.ImageOwnerAlias == nil || *req.ImageOwnerAlias != "others" {
		t.Fatalf("expected ImageOwnerAlias to be mapped")
	}
	if req.ImageOwnerId == nil || *req.ImageOwnerId != 1234567890123456 {
		t.Fatalf("expected ImageOwnerId to be mapped")
	}
	if len(req.Tag) != 1 || req.Tag[0].Key == nil || *req.Tag[0].Key != "env" || req.Tag[0].Value == nil || *req.Tag[0].Value != "prod" {
		t.Fatalf("expected image tag filter to be mapped, got %#v", req.Tag)
	}
}

func TestBuildDescribeImagesRequestRejectsInvalidOwnerID(t *testing.T) {
	if _, err := buildDescribeImagesRequest("cn-hangzhou", nil, map[string]string{"ImageOwnerID": "owner-1"}); err == nil {
		t.Fatal("expected invalid owner ID error")
	}
}

func TestBuildDescribeImagesRequestRejectsQueriesThatWouldWiden(t *testing.T) {
	for name, filters := range map[string]map[string]string{
		"no selector":          nil,
		"empty tag value":      {"tag:env": ""},
		"empty tag key":        {"tag:": "prod"},
		"unknown filter":       {"UnknownFilter": "prod"},
		"empty name":           {"ImageName": ""},
		"invalid alias":        {"ImageOwnerAlias": "public"},
		"owner without alias":  {"ImageName": "app", "ImageOwnerID": "123456"},
		"owner on self":        {"ImageOwnerAlias": "self", "ImageOwnerID": "123456"},
		"owner on system":      {"ImageOwnerAlias": "system", "ImageOwnerID": "123456"},
		"owner on marketplace": {"ImageOwnerAlias": "marketplace", "ImageOwnerID": "123456"},
		"overflow":             {"ImageOwnerAlias": "others", "ImageOwnerID": "9223372036854775808"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := buildDescribeImagesRequest("cn-hangzhou", nil, filters); err == nil {
				t.Fatal("invalid request was accepted")
			}
			// A nil SDK transport proves rejection happens before any API call.
			client := &DefaultECSClient{region: "cn-hangzhou"}
			if _, err := client.DescribeImages(context.Background(), nil, filters); err == nil {
				t.Fatal("invalid request reached the SDK")
			}
		})
	}
	if _, err := buildDescribeImagesRequest("cn-hangzhou", []string{"m-test"}, map[string]string{"ImageFamily": "app"}); err == nil {
		t.Fatal("mixed ID query accepted")
	}
	if _, err := buildDescribeImagesRequest("cn-hangzhou", []string{"m-test", "m-another"}, nil); err != nil {
		t.Fatalf("valid ID list rejected: %v", err)
	}
	req, err := buildDescribeImagesRequest("cn-hangzhou", nil, map[string]string{"ImageOwnerAlias": "others", "ImageOwnerID": "9223372036854775807"})
	if err != nil || req.ImageOwnerId == nil || *req.ImageOwnerId != 9223372036854775807 {
		t.Fatalf("valid owner boundary rejected: %v", err)
	}
}

func TestBuildDescribeImagesRequestAlwaysSetsShowExpired(t *testing.T) {
	// ShowExpired must always be true to allow querying ContainerOS / LifseaOS
	// images that are hidden by default. See GH issue #13.
	tests := []struct {
		name     string
		imageIDs []string
		filters  map[string]string
	}{
		{
			name:     "with image ID",
			imageIDs: []string{"lifsea_3_x64_5G_alibase_20260519.qcow2"},
			filters:  nil,
		},
		{
			name:     "with ImageFamily",
			imageIDs: nil,
			filters:  map[string]string{"ImageFamily": "acs:lifsea_os_3_x64"},
		},
		{
			name:    "with multiple filters",
			filters: map[string]string{"ImageOwnerAlias": "system", "ImageFamily": "acs:alibaba_cloud_linux_3_2104_lts_x64"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := buildDescribeImagesRequest("cn-hangzhou", tt.imageIDs, tt.filters)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if req.ShowExpired == nil {
				t.Fatal("ShowExpired must be set")
			}
			if *req.ShowExpired != true {
				t.Fatalf("ShowExpired must be true, got %v", *req.ShowExpired)
			}
		})
	}
}
