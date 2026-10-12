package nodepool

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
	"testing"
)

type poolAPI struct {
	list, detail string
	err          error
	calls        int
	pool         string
	ctx          context.Context
}

func (f *poolAPI) DescribeClusterNodePools(ctx context.Context, _ string) (*cs.DescribeClusterNodePoolsResponse, error) {
	f.calls++
	f.ctx = ctx
	r := &cs.DescribeClusterNodePoolsResponse{}
	err := json.Unmarshal([]byte(f.list), r)
	if f.err != nil {
		err = f.err
	}
	return r, err
}
func (f *poolAPI) DescribeClusterNodePoolDetail(ctx context.Context, _, pool string) (*cs.DescribeClusterNodePoolDetailResponse, error) {
	f.calls++
	f.ctx = ctx
	f.pool = pool
	r := &cs.DescribeClusterNodePoolDetailResponse{}
	err := json.Unmarshal([]byte(f.detail), r)
	if f.err != nil {
		err = f.err
	}
	return r, err
}

const defaultList = `{"body":{"nodepools":[{"nodepool_info":{"nodepool_id":"np-test","is_default":true}}]}}`
const validDetail = `{"body":{"nodepool_info":{"nodepool_id":"np-test","type":"ess"},"status":{"state":"active"},"node_config":{"kubelet_configuration":{"maxPods":20,"kubeReserved":{"cpu":"200m","memory":"200Mi"},"systemReserved":{"cpu":"100m"},"evictionHard":{"memory.available":"100Mi"}}}}}`

func TestResolvePinsNativePoolResourceConfiguration(t *testing.T) {
	f := &poolAPI{list: defaultList, detail: validDetail}
	ctx := context.WithValue(context.Background(), struct{}{}, "test")
	p, err := New("c-test", f).Resolve(ctx, &v1alpha1.ECSNodeClass{})
	require.NoError(t, err)
	require.Equal(t, "np-test", p.ID)
	require.Equal(t, "c-test", p.ClusterID)
	require.EqualValues(t, 20, *p.Kubelet.MaxPods)
	require.Equal(t, "200m", p.Kubelet.KubeReserved["cpu"])
	require.Same(t, ctx, f.ctx)
	require.Equal(t, 2, f.calls)
	f.calls = 0
	_, err = New("c-test", f).Resolve(ctx, &v1alpha1.ECSNodeClass{Spec: v1alpha1.ECSNodeClassSpec{ACKNodePoolID: tea.String("np-test")}})
	require.NoError(t, err)
	require.Equal(t, 1, f.calls)
}
func TestPoolResolutionFailsClosed(t *testing.T) {
	for name, detail := range map[string]string{
		"inactive":            `{"body":{"nodepool_info":{"nodepool_id":"np-test","type":"ess"},"status":{"state":"updating"}}}`,
		"mismatched":          `{"body":{"nodepool_info":{"nodepool_id":"np-other","type":"ess"},"status":{"state":"active"}}}`,
		"script":              `{"body":{"nodepool_info":{"nodepool_id":"np-test","type":"ess"},"status":{"state":"active"},"kubernetes_config":{"pre_user_data":"private-script"}}}`,
		"numeric reservation": `{"body":{"nodepool_info":{"nodepool_id":"np-test","type":"ess"},"status":{"state":"active"},"node_config":{"kubelet_configuration":{"kubeReserved":{"cpu":200}}}}}`,
		"memory manager":      `{"body":{"nodepool_info":{"nodepool_id":"np-test","type":"ess"},"status":{"state":"active"},"node_config":{"kubelet_configuration":{"memoryManagerPolicy":"Static"}}}}`,
		"empty":               "{}",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New("c-test", &poolAPI{list: defaultList, detail: detail}).Resolve(context.Background(), &v1alpha1.ECSNodeClass{})
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-script")
		})
	}
	for _, list := range []string{`{}`, `{"body":{"nodepools":[]}}`, `{"body":{"nodepools":[{"nodepool_info":{"nodepool_id":"np-test"}}]}}`, `{"body":{"nodepools":[{"nodepool_info":{"nodepool_id":"np-test","is_default":true}},{"nodepool_info":{"nodepool_id":"np-other","is_default":true}}]}}`} {
		_, err := New("c-test", &poolAPI{list: list, detail: validDetail}).Resolve(context.Background(), &v1alpha1.ECSNodeClass{})
		require.Error(t, err)
	}
	denied := errors.New("denied")
	_, err := New("c-test", &poolAPI{err: denied}).Resolve(context.Background(), &v1alpha1.ECSNodeClass{})
	require.ErrorIs(t, err, denied)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &poolAPI{}
	_, err = New("c-test", f).Resolve(ctx, &v1alpha1.ECSNodeClass{})
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, f.calls)
}
