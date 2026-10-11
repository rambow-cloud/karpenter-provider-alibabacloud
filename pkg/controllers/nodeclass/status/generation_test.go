package status

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/imagefamily"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/securitygroup"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/vswitch"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	vpc "github.com/alibabacloud-go/vpc-20160428/v7/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type generationECSClient struct {
	clients.ECSClient
	failImages bool
	imageCalls int
}

func (c *generationECSClient) DescribeImages(_ context.Context, ids []string, _ map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error) {
	c.imageCalls++
	if c.failImages {
		return nil, fmt.Errorf("temporary image resolution failure")
	}
	return []ecs.DescribeImagesResponseBodyImagesImage{{ImageId: tea.String(ids[0]), Architecture: tea.String("x86_64")}}, nil
}
func (c *generationECSClient) DescribeSecurityGroups(context.Context, string, string, map[string]string) (*ecs.DescribeSecurityGroupsResponse, error) {
	return &ecs.DescribeSecurityGroupsResponse{Body: &ecs.DescribeSecurityGroupsResponseBody{SecurityGroups: &ecs.DescribeSecurityGroupsResponseBodySecurityGroups{SecurityGroup: []*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{{SecurityGroupId: tea.String("sg-test")}}}}}, nil
}

type generationVPCClient struct{ clients.VPCClient }

func (*generationVPCClient) DescribeVSwitches(_ context.Context, id string, _ map[string]string, _ string) (*vpc.DescribeVSwitchesResponse, error) {
	return &vpc.DescribeVSwitchesResponse{Body: &vpc.DescribeVSwitchesResponseBody{VSwitches: &vpc.DescribeVSwitchesResponseBodyVSwitches{VSwitch: []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{{VSwitchId: tea.String(id), ZoneId: tea.String("cn-hangzhou-h")}}}}}, nil
}

func TestGenerationResolutionFailureAndRecovery(t *testing.T) {
	ctx := context.Background()
	class := &v1alpha1.ECSNodeClass{ObjectMeta: metav1.ObjectMeta{Name: "generation-test", Generation: 1}, Spec: v1alpha1.ECSNodeClassSpec{
		ImageSelectorTerms:         []v1alpha1.ImageSelectorTerm{{ID: tea.String("m-old")}},
		VSwitchSelectorTerms:       []v1alpha1.VSwitchSelectorTerm{{ID: tea.String("vsw-old")}},
		SecurityGroupSelectorTerms: []v1alpha1.SecurityGroupSelectorTerm{{ID: tea.String("sg-test")}},
	}}
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(class).WithObjects(class).Build()
	ecsClient := &generationECSClient{}
	controller := NewController(kube, vswitch.NewProvider("cn-hangzhou", &generationVPCClient{}), securitygroup.NewProvider("cn-hangzhou", ecsClient), imagefamily.NewProvider(ecsClient), nil, nil)
	request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(class)}
	if _, err := controller.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := kube.Get(ctx, request.NamespacedName, class); err != nil {
		t.Fatal(err)
	}
	if !controller.isReady(class) {
		t.Fatal("initial generation did not resolve")
	}
	initialTransition := controller.getCondition(class, v1alpha1.ConditionTypeVSwitchResolved).LastTransitionTime
	class.Generation = 2 // fake client does not increment generation on spec changes.
	class.Spec.ImageSelectorTerms[0].ID = tea.String("m-new")
	class.Spec.VSwitchSelectorTerms[0].ID = tea.String("vsw-new")
	if err := kube.Update(ctx, class); err != nil {
		t.Fatal(err)
	}
	if controller.isReady(class) {
		t.Fatal("old successful conditions accepted for new generation")
	}
	ecsClient.failImages = true
	if _, err := controller.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := kube.Get(ctx, request.NamespacedName, class); err != nil {
		t.Fatal(err)
	}
	ready := controller.getCondition(class, v1alpha1.ConditionTypeReady)
	if ready.Status != metav1.ConditionFalse || ready.ObservedGeneration != 2 {
		t.Fatalf("failed resolution promoted old status: %v", ready)
	}
	if class.Status.Images[0].ID != "m-old" || class.Status.VSwitches[0].ID != "vsw-new" {
		t.Fatal("test did not exercise partial resolution with old images")
	}
	vs := controller.getCondition(class, v1alpha1.ConditionTypeVSwitchResolved)
	if vs.ObservedGeneration != 2 || !vs.LastTransitionTime.Equal(&initialTransition) {
		t.Fatal("unchanged successful condition must update generation and preserve transition time")
	}
	ecsClient.failImages = false
	if _, err := controller.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := kube.Get(ctx, request.NamespacedName, class); err != nil {
		t.Fatal(err)
	}
	ready = controller.getCondition(class, v1alpha1.ConditionTypeReady)
	if !controller.isReady(class) || ready.Status != metav1.ConditionTrue || ready.ObservedGeneration != 2 || class.Status.Images[0].ID != "m-new" {
		t.Fatal("current generation did not recover")
	}
	for _, cond := range class.Status.Conditions {
		if cond.ObservedGeneration != 2 {
			t.Fatalf("stale condition: %v", cond)
		}
	}
}

func TestGenerationStatusCannotOverwriteNewSpecOrReplacement(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprintf("replacement=%t", replace), func(t *testing.T) {
			old := &v1alpha1.ECSNodeClass{ObjectMeta: metav1.ObjectMeta{Name: "race", Generation: 1, UID: "old"}}
			newer := old.DeepCopy()
			if replace {
				newer.UID = "replacement"
			} else {
				newer.Generation = 2
			}
			scheme := runtime.NewScheme()
			if err := v1alpha1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(newer).WithObjects(newer).Build()
			controller := &Controller{client: kube}
			controller.setCondition(old, v1alpha1.ConditionTypeReady, metav1.ConditionTrue, "Ready", "old spec")
			if err := controller.updateStatus(context.Background(), old); err == nil {
				t.Fatal("old resolution overwrote a changed object")
			}
			if err := kube.Get(context.Background(), client.ObjectKeyFromObject(newer), newer); err != nil {
				t.Fatal(err)
			}
			if len(newer.Status.Conditions) != 0 {
				t.Fatal("old status was persisted")
			}
		})
	}
}

func TestGenerationConditionTransitionTime(t *testing.T) {
	class := &v1alpha1.ECSNodeClass{ObjectMeta: metav1.ObjectMeta{Generation: 2}, Status: v1alpha1.ECSNodeClassStatus{Conditions: []metav1.Condition{{Type: v1alpha1.ConditionTypeReady, Status: metav1.ConditionTrue, ObservedGeneration: 1, LastTransitionTime: metav1.NewTime(time.Unix(1, 0))}}}}
	controller := &Controller{}
	controller.setCondition(class, v1alpha1.ConditionTypeReady, metav1.ConditionFalse, "Failed", "failed")
	cond := class.Status.Conditions[0]
	if cond.ObservedGeneration != 2 || cond.Status != metav1.ConditionFalse || cond.LastTransitionTime.Unix() == 1 {
		t.Fatalf("transition not updated: %v", cond)
	}
}
