package cloudprovider

import (
	"context"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/operator/options"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
	coreprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

func TestCreateAndSchedulingRejectUnresolvedGeneration(t *testing.T) {
	ctx := (&options.Options{ClusterID: "cluster-a"}).ToContext(context.Background())
	for _, test := range []struct {
		name       string
		conditions []metav1.Condition
	}{
		{"missing", nil},
		{"old successful", []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: 1}}},
		{"legacy successful", []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}}},
		{"current failure", []metav1.Condition{{Type: "Ready", Status: metav1.ConditionFalse, ObservedGeneration: 2}}},
		{"current unknown", []metav1.Condition{{Type: "Ready", Status: metav1.ConditionUnknown, ObservedGeneration: 2}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			class := &v1alpha1.ECSNodeClass{ObjectMeta: metav1.ObjectMeta{Name: "class", Generation: 2}, Spec: v1alpha1.ECSNodeClassSpec{ClusterID: "cluster-a"}, Status: v1alpha1.ECSNodeClassStatus{Conditions: test.conditions}}
			scheme := runtime.NewScheme()
			if err := v1alpha1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			// No cloud providers: reaching ECS/type discovery would panic.
			provider := &CloudProvider{kubeClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(class).Build()}
			claim := &coreapis.NodeClaim{Spec: coreapis.NodeClaimSpec{NodeClassRef: &coreapis.NodeClassReference{Name: class.Name}}}
			pool := &coreapis.NodePool{}
			pool.Spec.Template.Spec.NodeClassRef = claim.Spec.NodeClassRef
			if _, err := provider.Create(ctx, claim); !coreprovider.IsNodeClassNotReadyError(err) {
				t.Fatalf("Create error = %v", err)
			}
			if _, err := provider.GetInstanceTypes(ctx, pool); !coreprovider.IsNodeClassNotReadyError(err) {
				t.Fatalf("GetInstanceTypes error = %v", err)
			}
			if drift, err := provider.IsDrifted(ctx, claim); drift != "" || !coreprovider.IsNodeClassNotReadyError(err) {
				t.Fatalf("unresolved NodeClass must defer drift before ECS lookup: drift=%s err=%v", drift, err)
			}
			class.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: 2}}
			if err := nodeClassReadinessError(class); err != nil {
				t.Fatalf("resolved generation did not recover: %v", err)
			}
		})
	}
}
