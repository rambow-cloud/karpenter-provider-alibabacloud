package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/nodepool"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/utils/kubernetesversion"
	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
)

const smokeRunTag = "testing/karpenter-run"

func validateSmokeRRSA(detail *cs.DescribeClusterDetailResponseBody, providerARN string) error {
	if detail == nil || detail.RrsaConfig == nil || !tea.BoolValue(detail.RrsaConfig.Enabled) || providerARN == "" || tea.StringValue(detail.RrsaConfig.OidcArn) != providerARN {
		return fmt.Errorf("ACK must have matching enabled RRSA provider")
	}
	// ACK reports the Kubernetes API's default token audience here, which can
	// be https://kubernetes.default.svc. It does not describe RAM's accepted
	// audiences. The Chart projects sts.aliyuncs.com explicitly and the admitted
	// controller Pod is checked separately by verifyRRSAPod.
	return nil
}

type smokePoolSnapshot struct {
	detail *cs.DescribeClusterNodePoolDetailResponse
}

func (s *smokePoolSnapshot) DescribeClusterNodePools(context.Context, string) (*cs.DescribeClusterNodePoolsResponse, error) {
	return nil, fmt.Errorf("smoke requires an explicit native pool")
}
func (s *smokePoolSnapshot) DescribeClusterNodePoolDetail(context.Context, string, string) (*cs.DescribeClusterNodePoolDetailResponse, error) {
	return s.detail, nil
}

type SmokeConfig struct {
	VSwitchIDs         []string `yaml:"vswitch_ids" json:"vswitch_ids"`
	SecurityGroupIDs   []string `yaml:"security_group_ids" json:"security_group_ids"`
	ImageID            string   `yaml:"image_id" json:"image_id"`
	ACKNodePoolID      string   `yaml:"ack_node_pool_id" json:"ack_node_pool_id"`
	WorkerRole         string   `yaml:"worker_role" json:"worker_role"`
	InstanceTypes      []string `yaml:"instance_types" json:"instance_types"`
	SystemDiskCategory string   `yaml:"system_disk_category" json:"system_disk_category"`
	SystemDiskSize     int      `yaml:"system_disk_size" json:"system_disk_size"`
	PodImage           string   `yaml:"pod_image" json:"pod_image"`
}

func validateSmokeConfig(cfg *Config) error {
	if cfg.AlibabaCloud.Authentication != "sts" {
		return fmt.Errorf("smoke requires explicit sts authentication")
	}
	if cfg.AlibabaCloud.ClusterID == "" || cfg.Smoke == nil {
		return fmt.Errorf("smoke requires an explicit existing ACK cluster and smoke configuration")
	}
	if err := kubernetesversion.Validate(cfg.Cluster.KubernetesVersion); err != nil {
		return err
	}
	if _, err := controllerRRSAValues(cfg, cfg.AlibabaCloud.ClusterID, "https://preflight.invalid"); err != nil {
		return err
	}
	s := cfg.Smoke
	if len(s.VSwitchIDs) == 0 || len(s.SecurityGroupIDs) == 0 || s.ImageID == "" || s.ACKNodePoolID == "" || s.WorkerRole == "" || len(s.InstanceTypes) == 0 || len(s.InstanceTypes) > 5 {
		return fmt.Errorf("smoke requires pinned network, ACK image, native node pool, worker role and 1..5 candidate types")
	}
	for _, ids := range [][]string{s.VSwitchIDs, s.SecurityGroupIDs, s.InstanceTypes} {
		seen := map[string]bool{}
		for _, id := range ids {
			if id == "" || strings.ContainsAny(id, " \t\r\n") || seen[id] {
				return fmt.Errorf("invalid or duplicate smoke identifiers")
			}
			seen[id] = true
		}
	}
	if s.SystemDiskSize < 40 || s.SystemDiskSize > 120 || s.SystemDiskCategory == "" {
		return fmt.Errorf("smoke system disk must be explicitly configured between 40 and 120 GiB")
	}
	if !regexp.MustCompile(`^[^\s]+@sha256:[0-9a-f]{64}$`).MatchString(s.PodImage) {
		return fmt.Errorf("smoke Pod image must be pinned to a digest")
	}
	return nil
}

func runSmokeConfig(args []string) error {
	fs := flag.NewFlagSet("smoke-config", flag.ContinueOnError)
	path := fs.String("config", "", "config path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := LoadConfig(*path)
	if err != nil {
		return err
	}
	if err = validateSmokeConfig(cfg); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]interface{}{"cluster_id": cfg.AlibabaCloud.ClusterID, "cluster_name": cfg.Cluster.Name, "region": cfg.AlibabaCloud.RegionID, "kubernetes_version": cfg.Cluster.KubernetesVersion, "smoke": cfg.Smoke, "system_selector": cfg.ControllerRRSA.SystemNodeSelector, "controller_image": cfg.ControllerRRSA.ImageRepository + ":" + cfg.ControllerRRSA.ImageTag, "controller_provider_arn": cfg.ControllerRRSA.ProviderARN, "controller_role_arn": cfg.ControllerRRSA.RoleARN})
}

type smokeInstanceAPI interface {
	DescribeInstances(*ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error)
	DeleteInstances(*ecs.DeleteInstancesRequest) (*ecs.DeleteInstancesResponse, error)
}

func cloudError(operation string, err error) error {
	var api *tea.SDKError
	if errors.As(err, &api) {
		return fmt.Errorf("%s failed: %s", operation, tea.StringValue(api.Code))
	}
	// Do not emit SDK response bodies, tokens or request userdata into reports.
	return fmt.Errorf("%s failed (transport or malformed response)", operation)
}

func dryRunAllowed(err error) bool {
	var api *tea.SDKError
	return errors.As(err, &api) && tea.StringValue(api.Code) == "DryRunOperation"
}

func validSmokeRun(run string) bool {
	return regexp.MustCompile(`^ack-[a-z0-9][a-z0-9-]{5,48}$`).MatchString(run)
}

func smokeSelectors(cluster, run string) map[string]string {
	return map[string]string{smokeRunTag: run, v1alpha1.TagClusterID: cluster, v1alpha1.TagManagedBy: v1alpha1.TagManagedByValue}
}

func strictSmokeOwnership(row map[string]interface{}, cluster, run string) bool {
	tags, _ := row["Tags"].(map[string]string)
	for key, value := range smokeSelectors(cluster, run) {
		if tags[key] != value {
			return false
		}
	}
	return true
}

func selectedJSON(value interface{}, keys ...string) (map[string]interface{}, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var source map[string]interface{}
	if err = json.Unmarshal(b, &source); err != nil {
		return nil, err
	}
	if source == nil {
		return nil, fmt.Errorf("missing cloud record")
	}
	result := map[string]interface{}{}
	for _, k := range keys {
		if v, ok := source[k]; ok {
			result[k] = v
		}
	}
	return result, nil
}

func smokeInstances(client smokeInstanceAPI, region, cluster, run string, ids []string, vpcs ...string) ([]map[string]interface{}, error) {
	vpc := ""
	if len(vpcs) > 0 {
		vpc = vpcs[0]
	}
	if run != "" && !validSmokeRun(run) {
		return nil, fmt.Errorf("invalid run ID")
	}
	if run == "" && len(ids) == 0 && vpc == "" {
		return nil, fmt.Errorf("instance query requires run ownership or exact IDs")
	}
	req := &ecs.DescribeInstancesRequest{RegionId: tea.String(region), MaxResults: tea.Int32(100), DryRun: tea.Bool(true)}
	if vpc != "" {
		if !strings.HasPrefix(vpc, "vpc-") {
			return nil, fmt.Errorf("invalid exact VPC identity")
		}
		req.VpcId = tea.String(vpc)
	}
	if run != "" {
		req.Tag = ecsInstanceTags(smokeSelectors(cluster, run))
	}
	if len(ids) > 0 {
		data, _ := json.Marshal(ids)
		req.InstanceIds = tea.String(string(data))
	}
	if _, err := client.DescribeInstances(req); !dryRunAllowed(err) {
		return nil, fmt.Errorf("DescribeInstances authorization dry-run did not succeed")
	}
	req.DryRun = tea.Bool(false)
	rows := []map[string]interface{}{}
	seenTokens := map[string]bool{}
	seenIDs := map[string]bool{}
	for {
		resp, err := client.DescribeInstances(req)
		if err != nil {
			return nil, cloudError("DescribeInstances", err)
		}
		if resp == nil || resp.Body == nil || resp.Body.Instances == nil {
			return nil, fmt.Errorf("incomplete DescribeInstances response")
		}
		for _, inst := range resp.Body.Instances.Instance {
			if vpc != "" && (inst == nil || inst.VpcAttributes == nil || tea.StringValue(inst.VpcAttributes.VpcId) != vpc) {
				return nil, fmt.Errorf("foreign or incomplete VPC instance returned")
			}
			row, err := selectedJSON(inst, "InstanceId", "RegionId", "ZoneId", "Status", "InstanceChargeType", "SpotStrategy", "ImageId", "InstanceType", "Cpu", "VpcAttributes", "SecurityGroupIds")
			if err != nil {
				return nil, err
			}
			id, _ := row["InstanceId"].(string)
			if id == "" || seenIDs[id] {
				return nil, fmt.Errorf("missing or duplicate cloud instance identity")
			}
			seenIDs[id] = true
			tags := map[string]string{}
			if inst.Tags != nil {
				for _, tag := range inst.Tags.Tag {
					if tag != nil {
						k := tea.StringValue(tag.TagKey)
						if _, ok := smokeSelectors(cluster, run)[k]; ok || k == v1alpha1.TagNodePool || k == v1alpha1.TagNodeClaim || k == v1alpha1.TagNodeClaimUID || k == "ack.aliyun.com" || k == "ack.alibabacloud.com/nodepool-id" || k == "acs:autoscaling:scalingGroupId" {
							tags[k] = tea.StringValue(tag.TagValue)
						}
					}
				}
			}
			row["Tags"] = tags
			if run != "" && !strictSmokeOwnership(row, cluster, run) {
				return nil, fmt.Errorf("foreign instance returned by ownership query")
			}
			rows = append(rows, row)
		}
		next := tea.StringValue(resp.Body.NextToken)
		if next == "" {
			return rows, nil
		}
		if seenTokens[next] {
			return nil, fmt.Errorf("cloud pagination repeated token")
		}
		seenTokens[next] = true
		req.NextToken = tea.String(next)
	}
}

func releaseSmokeInstance(client smokeInstanceAPI, region, cluster, run, id string) error {
	if !validSmokeRun(run) || !strings.HasPrefix(id, "i-") {
		return fmt.Errorf("release requires a valid run and exact instance ID")
	}
	rows, err := smokeInstances(client, region, cluster, "", []string{id})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	if len(rows) != 1 || rows[0]["InstanceId"] != id || !strictSmokeOwnership(rows[0], cluster, run) {
		return fmt.Errorf("refusing release outside exact run ownership")
	}
	tags, _ := rows[0]["Tags"].(map[string]string)
	if tags["ack.alibabacloud.com/nodepool-id"] != "" || tags["acs:autoscaling:scalingGroupId"] != "" {
		return fmt.Errorf("native ACK/ESS instance requires controller-coordinated removal; direct fallback refused")
	}
	_, err = client.DeleteInstances(&ecs.DeleteInstancesRequest{RegionId: tea.String(region), InstanceId: []*string{tea.String(id)}, Force: tea.Bool(true)})
	if err != nil {
		return cloudError("DeleteInstances", err)
	}
	return nil
}

func runSmokeCloud(args []string) error {
	fs := flag.NewFlagSet("smoke-cloud", flag.ContinueOnError)
	path := fs.String("config", "", "config path")
	op := fs.String("operation", "", "cluster, kubeconfig, types, instances, disks, enis, capacity or release")
	run := fs.String("run-id", "", "run ownership ID")
	idsInput := fs.String("ids", "[]", "JSON exact IDs")
	instance := fs.String("instance-id", "", "instance filter/release")
	output := fs.String("output", "", "private kubeconfig output")
	zone := fs.String("zone", "", "capacity zone")
	strategy := fs.String("strategy", "NoSpot", "capacity strategy")
	vpc := fs.String("vpc-id", "", "exact VPC inventory scope")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := LoadConfig(*path)
	if err != nil {
		return err
	}
	if err = validateSmokeConfig(cfg); err != nil {
		return err
	}
	var ids []string
	if err = json.Unmarshal([]byte(*idsInput), &ids); err != nil {
		return fmt.Errorf("ids must be a JSON array")
	}
	for _, id := range ids {
		if id == "" {
			return fmt.Errorf("empty exact ID")
		}
	}
	var result interface{}
	if *op == "cluster" || *op == "kubeconfig" || *op == "native-pool" || *op == "native-capacity" {
		client, err := newCSClient(cfg)
		if err != nil {
			return err
		}
		if *op == "native-pool" {
			detail, err := client.DescribeClusterNodePoolDetail(tea.String(cfg.AlibabaCloud.ClusterID), tea.String(cfg.Smoke.ACKNodePoolID))
			if err != nil {
				return cloudError("native ACK pool", err)
			}
			if detail == nil || detail.Body == nil {
				return fmt.Errorf("missing native ACK pool")
			}
			if detail.Body.AutoScaling != nil && tea.BoolValue(detail.Body.AutoScaling.Enable) {
				return fmt.Errorf("native pool autoscaling must be disabled for smoke isolation")
			}
			_, err = nodepool.New(cfg.AlibabaCloud.ClusterID, &smokePoolSnapshot{detail: detail}).Resolve(context.Background(), &v1alpha1.ECSNodeClass{Spec: v1alpha1.ECSNodeClassSpec{ClusterID: cfg.AlibabaCloud.ClusterID, ACKNodePoolID: tea.String(cfg.Smoke.ACKNodePoolID)}})
			if err != nil {
				return fmt.Errorf("native ACK pool lacks a supported resource/bootstrap model")
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"nodepool_id": cfg.Smoke.ACKNodePoolID, "result": "supported"})
		}
		if *op == "native-capacity" {
			capacity, err := smokeNativeCapacity(client, cfg.AlibabaCloud.ClusterID, cfg.Smoke.ACKNodePoolID)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(capacity)
		}
		resp, err := client.DescribeClusterDetail(tea.String(cfg.AlibabaCloud.ClusterID))
		if err != nil {
			return cloudError("ACK detail", err)
		}
		if resp == nil || resp.Body == nil || tea.StringValue(resp.Body.State) != "running" {
			return fmt.Errorf("ACK must be running")
		}
		if *op == "cluster" {
			if err := validateSmokeRRSA(resp.Body, cfg.ControllerRRSA.ProviderARN); err != nil {
				return err
			}
		}
		if err = kubernetesversion.Validate(tea.StringValue(resp.Body.CurrentVersion)); err != nil {
			return err
		}
		result, err = selectedJSON(resp.Body, "cluster_id", "name", "region_id", "vpc_id", "current_version", "state", "rrsa_config")
		if err != nil {
			return err
		}
		if *op == "kubeconfig" {
			if *output == "" {
				return fmt.Errorf("private kubeconfig output required")
			}
			data, err := getKubeconfig(client, cfg.AlibabaCloud.ClusterID)
			if err != nil {
				return cloudError("temporary kubeconfig", err)
			}
			if err = os.WriteFile(*output, []byte(data), 0600); err != nil {
				return err
			}
			result = map[string]string{"endpoint": kubeconfigEndpoint([]byte(data))}
		}
	} else {
		client, err := newECSClient(cfg)
		if err != nil {
			return err
		}
		region := cfg.AlibabaCloud.RegionID
		switch *op {
		case "instances":
			result, err = smokeInstances(client, region, cfg.AlibabaCloud.ClusterID, *run, ids)
		case "vpc-instances":
			if *vpc == "" {
				return fmt.Errorf("exact VPC identity required")
			}
			result, err = smokeInstances(client, region, cfg.AlibabaCloud.ClusterID, "", nil, *vpc)
		case "release":
			csSDK, e := newCSClient(cfg)
			if e != nil {
				return e
			}
			member, e := nodepool.New(cfg.AlibabaCloud.ClusterID, clients.NewDefaultCSClient(csSDK)).LookupInstance(context.Background(), *instance)
			if e != nil {
				return fmt.Errorf("fallback cannot verify native membership")
			}
			if member != nil {
				return fmt.Errorf("native ACK member cannot be released by direct ECS fallback")
			}
			err = releaseSmokeInstance(client, region, cfg.AlibabaCloud.ClusterID, *run, *instance)
			result = map[string]bool{"release_requested": err == nil}
		case "types":
			resp, e := client.DescribeInstanceTypes(&ecs.DescribeInstanceTypesRequest{InstanceTypes: stringPtrs(cfg.Smoke.InstanceTypes)})
			err = e
			if err == nil {
				if resp == nil || resp.Body == nil || resp.Body.InstanceTypes == nil {
					return fmt.Errorf("missing instance types")
				}
				rows := []map[string]interface{}{}
				for _, v := range resp.Body.InstanceTypes.InstanceType {
					row, e := selectedJSON(v, "InstanceTypeId", "CpuCoreCount", "CpuArchitecture", "MemorySize")
					if e != nil {
						return e
					}
					rows = append(rows, row)
				}
				result = rows
			}
		case "capacity":
			if *zone == "" || (*strategy != "NoSpot" && *strategy != "SpotAsPriceGo") {
				return fmt.Errorf("capacity requires zone and explicit strategy")
			}
			resp, e := client.DescribeAvailableResource(&ecs.DescribeAvailableResourceRequest{RegionId: tea.String(region), ZoneId: zone, DestinationResource: tea.String("InstanceType"), InstanceChargeType: tea.String("PostPaid"), SpotStrategy: strategy, SystemDiskCategory: tea.String(cfg.Smoke.SystemDiskCategory)})
			err = e
			if err == nil {
				if resp == nil || resp.Body == nil || resp.Body.AvailableZones == nil {
					return fmt.Errorf("missing capacity inventory")
				}
				result = resp.Body.AvailableZones
			}
		case "disks", "baseline-disks":
			if *op == "disks" && *instance == "" && len(ids) == 0 {
				return fmt.Errorf("disk query needs exact instance or recorded disk IDs")
			}
			rows := []map[string]interface{}{}
			next := ""
			seen := map[string]bool{}
			for {
				req := &ecs.DescribeDisksRequest{RegionId: tea.String(region), MaxResults: tea.Int32(100), NextToken: tea.String(next)}
				if *instance != "" {
					req.InstanceId = instance
				}
				if len(ids) > 0 {
					b, _ := json.Marshal(ids)
					req.DiskIds = tea.String(string(b))
				}
				resp, e := client.DescribeDisks(req)
				if e != nil {
					return cloudError("DescribeDisks", e)
				}
				if resp == nil || resp.Body == nil || resp.Body.Disks == nil {
					return fmt.Errorf("missing disk inventory")
				}
				for _, v := range resp.Body.Disks.Disk {
					row, e := selectedJSON(v, "DiskId", "InstanceId", "Status", "DeleteWithInstance", "Type")
					if e != nil {
						return e
					}
					rows = append(rows, row)
				}
				next = tea.StringValue(resp.Body.NextToken)
				if next == "" {
					break
				}
				if seen[next] {
					return fmt.Errorf("disk pagination repeated")
				}
				seen[next] = true
			}
			result = rows
		case "enis", "baseline-enis":
			if *op == "enis" && *instance == "" && len(ids) == 0 {
				return fmt.Errorf("ENI query needs exact instance or recorded ENI IDs")
			}
			rows := []map[string]interface{}{}
			next := ""
			seen := map[string]bool{}
			for {
				req := &ecs.DescribeNetworkInterfacesRequest{RegionId: tea.String(region), MaxResults: tea.Int32(100), NextToken: tea.String(next)}
				if *instance != "" {
					req.InstanceId = instance
				}
				if len(ids) > 0 {
					req.NetworkInterfaceId = stringPtrs(ids)
				}
				resp, e := client.DescribeNetworkInterfaces(req)
				if e != nil {
					return cloudError("DescribeNetworkInterfaces", e)
				}
				if resp == nil || resp.Body == nil || resp.Body.NetworkInterfaceSets == nil {
					return fmt.Errorf("missing ENI inventory")
				}
				for _, v := range resp.Body.NetworkInterfaceSets.NetworkInterfaceSet {
					row, e := selectedJSON(v, "NetworkInterfaceId", "InstanceId", "Status", "Type")
					if e != nil {
						return e
					}
					rows = append(rows, row)
				}
				next = tea.StringValue(resp.Body.NextToken)
				if next == "" {
					break
				}
				if seen[next] {
					return fmt.Errorf("ENI pagination repeated")
				}
				seen[next] = true
			}
			result = rows
		default:
			return fmt.Errorf("unsupported smoke cloud operation")
		}
		if err != nil {
			return cloudError(*op, err)
		}
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
