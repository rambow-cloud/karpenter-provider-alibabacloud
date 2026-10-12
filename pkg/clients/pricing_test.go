package clients

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
)

type priceHTTP struct {
	called bool
	form   url.Values
	value  interface{}
}

func (f *priceHTTP) Call(r *http.Request, _ *http.Transport) (*http.Response, error) {
	f.called = true
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	f.form = r.Form
	f.value = r.Context().Value(offlineContextKey{})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"PriceInfo":{"Price":{"Currency":"CNY","TradePrice":0.75}}}`))}, nil
}
func TestOfferingPricePreservesNativeSDKBillingFieldsAndContext(t *testing.T) {
	native, err := ecs.NewClient(localSDKConfig("http://offline.invalid"))
	require.NoError(t, err)
	transport := &priceHTTP{}
	native.HttpClient = transport
	adapter := NewDefaultECSClient(native, "cn-hangzhou").(*DefaultECSClient)
	ctx := context.WithValue(context.Background(), offlineContextKey{}, "present")
	r, err := adapter.DescribeOfferingPrice(ctx, &ecs.DescribePriceRequest{RegionId: tea.String("cn-hangzhou"), ResourceType: tea.String("instance"), InstanceType: tea.String("ecs.g7.large"), ZoneId: tea.String("cn-hangzhou-i"), ImageId: tea.String("m-test"), PriceUnit: tea.String("Hour"), Period: tea.Int32(1), Amount: tea.Int32(1), SpotStrategy: tea.String("SpotAsPriceGo"), SpotDuration: tea.Int32(0), SystemDisk: &ecs.DescribePriceRequestSystemDisk{Category: tea.String("cloud_essd"), Size: tea.Int32(80), PerformanceLevel: tea.String("PL1")}, DataDisk: []*ecs.DescribePriceRequestDataDisk{{Category: tea.String("cloud_ssd"), Size: tea.Int64(100)}}})
	require.NoError(t, err)
	require.Equal(t, "present", transport.value)
	for k, v := range map[string]string{"Action": "DescribePrice", "InstanceType": "ecs.g7.large", "ZoneId": "cn-hangzhou-i", "ImageId": "m-test", "PriceUnit": "Hour", "Period": "1", "Amount": "1", "SpotStrategy": "SpotAsPriceGo", "SpotDuration": "0", "SystemDisk.Category": "cloud_essd", "SystemDisk.Size": "80", "SystemDisk.PerformanceLevel": "PL1", "DataDisk.1.Category": "cloud_ssd", "DataDisk.1.Size": "100"} {
		require.Equal(t, v, transport.form.Get(k), k)
	}
	require.EqualValues(t, .75, *r.Body.PriceInfo.Price.TradePrice)
	transport.called = false
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = adapter.DescribeOfferingPrice(canceled, &ecs.DescribePriceRequest{})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, transport.called)
}
