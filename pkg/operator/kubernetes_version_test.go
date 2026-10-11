package operator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery/fake"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	coreoperator "sigs.k8s.io/karpenter/pkg/operator"
)

func TestOperatorRejectsOldAPIServerBeforeInitializingCloudProviders(t *testing.T) {
	client := kubernetesfake.NewClientset()
	client.Discovery().(*fake.FakeDiscovery).FakedServerVersion = &version.Info{GitVersion: "v1.33.9-aliyun.1"}
	// No manager or cloud options: the support check must run before either is used.
	op, err := NewOperator(context.Background(), &coreoperator.Operator{KubernetesInterface: client})
	require.Nil(t, op)
	require.ErrorContains(t, err, "requires Kubernetes >= 1.34")
}
