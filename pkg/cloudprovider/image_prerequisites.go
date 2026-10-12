package cloudprovider

import (
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/imagefamily"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
)

func selectBootImage(images []v1alpha1.Image, it *instancetype.InstanceType, nc *v1alpha1.ECSNodeClass) v1alpha1.Image {
	eligible := []v1alpha1.Image{}
	for _, img := range images {
		if imagefamily.ValidateHardware(nc.Spec, img, it) == nil {
			eligible = append(eligible, img)
		}
	}
	return selectImageForArchitecture(eligible, it.Architecture)
}

func filterImagesForNodeLabels(images []v1alpha1.Image, labels map[string]string) []v1alpha1.Image {
	result := []v1alpha1.Image{}
	for _, img := range images {
		if imagefamily.ValidateNodeLabels(img, labels) == nil {
			result = append(result, img)
		}
	}
	return result
}
func filterBootInstanceTypes(types []*instancetype.InstanceType, images []v1alpha1.Image, nc *v1alpha1.ECSNodeClass) []*instancetype.InstanceType {
	result := []*instancetype.InstanceType{}
	for _, it := range types {
		if it != nil && selectBootImage(images, it, nc).ID != "" {
			result = append(result, it)
		}
	}
	return result
}
