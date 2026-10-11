package hash

import (
	"context"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestSharedHashControllerContract(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	n := &v1alpha1.ECSNodeClass{ObjectMeta: metav1.ObjectMeta{Name: "class"}, Spec: v1alpha1.ECSNodeClassSpec{ClusterID: "cluster-a", SpotStrategy: tea.String("SpotAsPriceGo")}}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(n).Build()
	controller := NewController(kubeClient)
	req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(n)}
	_, err := controller.Reconcile(ctx, req)
	require.NoError(t, err)
	updated := &v1alpha1.ECSNodeClass{}
	require.NoError(t, kubeClient.Get(ctx, req.NamespacedName, updated))
	expected, err := updated.Hash()
	require.NoError(t, err)
	require.Equal(t, expected, updated.Annotations[v1alpha1.AnnotationECSNodeClassHash])
	require.Equal(t, v1alpha1.ECSNodeClassHashVersion, updated.Annotations[v1alpha1.AnnotationECSNodeClassHashVersion])
	version := updated.ResourceVersion
	_, err = controller.Reconcile(ctx, req)
	require.NoError(t, err)
	require.NoError(t, kubeClient.Get(ctx, req.NamespacedName, updated))
	require.Equal(t, version, updated.ResourceVersion, "unchanged canonical hash must avoid an update")
}
