package cloudprovider

import (
	"context"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/operator/options"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/imagefamily"
	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
)

type launchCompatibilityCS struct {
	clients.CSClient
	imageType string
}

func (c *launchCompatibilityCS) DescribeClusterDetail(context.Context, string) (*cs.DescribeClusterDetailResponse, error) {
	return &cs.DescribeClusterDetailResponse{Body: &cs.DescribeClusterDetailResponseBody{CurrentVersion: tea.String("1.34.10-aliyun.1"), ClusterType: tea.String("ManagedKubernetes")}}, nil
}
func (c *launchCompatibilityCS) DescribeKubernetesVersionMetadata(context.Context, *cs.DescribeKubernetesVersionMetadataRequest) ([]*cs.DescribeKubernetesVersionMetadataResponseBody, error) {
	return []*cs.DescribeKubernetesVersionMetadataResponseBody{{Version: tea.String("1.34.10-aliyun.1"), Images: []*cs.DescribeKubernetesVersionMetadataResponseBodyImages{{ImageId: tea.String("m-approved"), ImageType: tea.String(c.imageType), OsType: tea.String("Linux"), Architecture: tea.String("x86_64")}}}}, nil
}

type launchCompatibilityECS struct {
	clients.ECSClient
	os      string
	queries int
}

func (c *launchCompatibilityECS) DescribeImages(context.Context, []string, map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error) {
	c.queries++
	return []ecs.DescribeImagesResponseBodyImagesImage{{ImageId: tea.String("m-approved"), ImageOwnerAlias: tea.String("system"), OSType: tea.String(c.os), Architecture: tea.String("x86_64"), Status: tea.String("Available")}}, nil
}

func TestCreateRechecksLegacyImageStatusBeforeInstanceDiscovery(t *testing.T) {
	for _, tc := range []struct{ name, id, os, imageType string }{
		{"windows", "m-approved", "windows", "AliyunLinux3"},
		{"unknown OS", "m-approved", "", "AliyunLinux3"},
		{"missing ContainerOS disk", "m-approved", "linux", "ContainerOS4"},
		{"unapproved public pin", "m-outside-ack", "linux", "AliyunLinux3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := (&options.Options{ClusterID: "cluster-a"}).ToContext(context.Background())
			class := &v1alpha1.ECSNodeClass{ObjectMeta: metav1.ObjectMeta{Name: "class", Generation: 1}, Spec: v1alpha1.ECSNodeClassSpec{ClusterID: "cluster-a", ImageSelectorTerms: []v1alpha1.ImageSelectorTerm{{ID: tea.String(tc.id)}}}, Status: v1alpha1.ECSNodeClassStatus{Images: []v1alpha1.Image{{ID: tc.id}}, Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: 1}}}}
			scheme := runtime.NewScheme()
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			ecsClient := &launchCompatibilityECS{os: tc.os}
			images := imagefamily.NewProvider(ecsClient, imagefamily.WithACKCompatibility(&launchCompatibilityCS{imageType: tc.imageType}, "cn-hangzhou", "cluster-a"))
			// Missing type/instance/bootstrap providers make any path beyond the
			// compatibility gate panic, proving no RunInstances path is reached.
			provider := &CloudProvider{kubeClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(class).Build(), imageFamilyProvider: images}
			claim := &coreapis.NodeClaim{Spec: coreapis.NodeClaimSpec{NodeClassRef: &coreapis.NodeClassReference{Name: class.Name}}}
			_, err := provider.Create(ctx, claim)
			require.Error(t, err)
			require.Positive(t, ecsClient.queries)
		})
	}
}

func TestImagePolicyHashNormalizesDefaultAndTracksCustom(t *testing.T) {
	class := &v1alpha1.ECSNodeClass{}
	baseline := calculateNodeClassHash(class)
	class.Spec.ImageCompatibilityPolicy = "ACK"
	require.Equal(t, baseline, calculateNodeClassHash(class))
	class.Spec.ImageCompatibilityPolicy = "Custom"
	require.NotEqual(t, baseline, calculateNodeClassHash(class))
}
