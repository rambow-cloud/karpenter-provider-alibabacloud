package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/stretchr/testify/require"
)

func TestNativeImageAvailabilityWireAndFiltering(t *testing.T) {
	for _, exact := range []bool{true, false} {
		t.Run(fmt.Sprintf("exact=%t", exact), func(t *testing.T) {
			requests := make(chan url.Values, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				r.Form.Set("_action", r.Header.Get("x-acs-action"))
				r.Form.Set("_authorization", r.Header.Get("Authorization"))
				requests <- r.Form
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"TotalCount":8,"Images":{"Image":[
                  {"ImageId":"lifsea_3_x64_5G_alibase_20260519.qcow2","Status":"Available","Usable":true,"ImageOwnerAlias":"system","Architecture":"x86_64"},
                  {"ImageId":"m-unusable","Status":"Available","Usable":false},
                  {"ImageId":"m-unknown-usability","Status":"Available"},
                  {"ImageId":"m-deprecated","Status":"Deprecated","Usable":true},
                  {"ImageId":"m-unaccepted-terms","Status":"Available","Usable":true,"ImageOwnerAlias":"marketplace","IsSubscribed":false},
                  {"ImageId":"m-accepted-terms","Status":"Available","Usable":true,"ImageOwnerAlias":"marketplace","IsSubscribed":true},
                  {"ImageId":"m-unknown-status","Usable":true},
                  {"ImageId":"m-private","Status":"Available","Usable":true,"ImageOwnerAlias":"self","IsSubscribed":false}
                ]}}`))
			}))
			defer server.Close()
			native, err := ecs.NewClient(localSDKConfig(server.URL))
			require.NoError(t, err)
			adapter := NewDefaultECSClient(native, "cn-hangzhou")
			var ids []string
			filters := map[string]string{"ImageName": "app-*", "tag:env": "prod", "ImageOwnerAlias": "self"}
			if exact {
				ids = []string{"lifsea_3_x64_5G_alibase_20260519.qcow2"}
				filters = nil
			}
			result, err := adapter.DescribeImages(context.Background(), ids, filters)
			require.NoError(t, err)
			names := []string{}
			for _, image := range result {
				names = append(names, *image.ImageId)
			}
			require.ElementsMatch(t, []string{"lifsea_3_x64_5G_alibase_20260519.qcow2", "m-accepted-terms", "m-private"}, names)
			require.Equal(t, "x86_64", *result[0].Architecture, "extended decode must preserve generated SDK fields")
			wire := <-requests
			require.Equal(t, "true", wire.Get("Usable"), "SDK model omission must not drop the signed parameter")
			require.Equal(t, "Available", wire.Get("Status"))
			require.Equal(t, fmt.Sprintf("%t", exact), wire.Get("ShowExpired"))
			require.Equal(t, "DescribeImages", wire.Get("_action"))
			require.NotEmpty(t, wire.Get("_authorization"), "native SDK still signs requests")
			if exact {
				require.Equal(t, ids[0], wire.Get("ImageId"))
			} else {
				require.Equal(t, "app-*", wire.Get("ImageName"))
				require.Equal(t, "env", wire.Get("Tag.1.Key"))
				require.Equal(t, "prod", wire.Get("Tag.1.Value"))
			}
		})
	}
}

func TestNativeImageAvailabilityPaginationAndIncompleteResponses(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(fmt.Sprintf("broken=%t", broken), func(t *testing.T) {
			pages := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				pages++
				rows := []map[string]interface{}{}
				if !broken {
					count := 100
					if pages == 2 {
						count = 1
					}
					for n := 0; n < count; n++ {
						rows = append(rows, map[string]interface{}{"ImageId": fmt.Sprintf("m-%d-%d", pages, n), "Status": "Available", "Usable": true})
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"TotalCount": 101, "Images": map[string]interface{}{"Image": rows}})
			}))
			defer server.Close()
			native, err := ecs.NewClient(localSDKConfig(server.URL))
			require.NoError(t, err)
			images, err := NewDefaultECSClient(native, "cn-hangzhou").DescribeImages(context.Background(), nil, map[string]string{"ImageFamily": "app"})
			if broken {
				require.ErrorContains(t, err, "incomplete")
				require.Nil(t, images)
			} else {
				require.NoError(t, err)
				require.Len(t, images, 101)
				require.Equal(t, 2, pages)
			}
		})
	}
}
