package clients

import (
	"encoding/json"
	"fmt"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	openapiutil "github.com/alibabacloud-go/openapi-util/service"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
)

// ECS SDK v5.1.0 omits the documented Usable request/response field. Use its
// signed RPC transport and retain that field locally rather than losing it in
// conversion to the generated model. The exported ECSClient contract is unchanged.
type availableImage struct {
	ecs.DescribeImagesResponseBodyImagesImage
	Usable *bool `json:"Usable"`
}
type imageAvailabilityResponse struct {
	Body *struct {
		TotalCount *int32 `json:"TotalCount"`
		Images     *struct {
			Image []*availableImage `json:"Image"`
		} `json:"Images"`
	} `json:"body"`
}

func describeUsableImages(native *ecs.Client, request *ecs.DescribeImagesRequest) (*imageAvailabilityResponse, error) {
	query := tea.ToMap(request)
	query["Usable"] = true
	params := &openapi.Params{Action: tea.String("DescribeImages"), Version: tea.String("2014-05-26"), Protocol: tea.String("HTTPS"), Pathname: tea.String("/"), Method: tea.String("POST"), AuthType: tea.String("AK"), Style: tea.String("RPC"), ReqBodyType: tea.String("formData"), BodyType: tea.String("json")}
	raw, err := native.CallApi(params, &openapi.OpenApiRequest{Query: openapiutil.Query(query)}, &util.RuntimeOptions{})
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode image availability: %w", err)
	}
	var response imageAvailabilityResponse
	if err := json.Unmarshal(encoded, &response); err != nil {
		return nil, fmt.Errorf("decode image availability: %w", err)
	}
	return &response, nil
}

func launchableImage(img *availableImage) bool {
	if img == nil || tea.StringValue(img.ImageId) == "" || tea.StringValue(img.Status) != "Available" || !tea.BoolValue(img.Usable) {
		return false
	}
	// IsSubscribed means acceptance of marketplace terms, not proof that an
	// image's usage period is unexpired. Do not silently accept paid terms.
	return tea.StringValue(img.ImageOwnerAlias) != "marketplace" || tea.BoolValue(img.IsSubscribed)
}
