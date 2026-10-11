package imagefamily

import (
	"sort"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
)

// PreferredImage ranks by known creation time (newest first), then by ID for
// deterministic ties. Candidate membership and explicit pins are unchanged.
func PreferredImage(candidate, current v1alpha1.Image) bool {
	a, aErr := time.Parse(time.RFC3339Nano, candidate.CreationTime)
	b, bErr := time.Parse(time.RFC3339Nano, current.CreationTime)
	if (aErr == nil) != (bErr == nil) {
		return aErr == nil
	}
	if aErr == nil && !a.Equal(b) {
		return a.After(b)
	}
	if candidate.ID != current.ID {
		return candidate.ID < current.ID
	}
	return candidate.Name < current.Name
}

func removeDuplicateImages(images []v1alpha1.Image) []v1alpha1.Image {
	byID := make(map[string]v1alpha1.Image, len(images))
	for _, img := range images {
		if img.ID == "" {
			continue
		}
		if current, found := byID[img.ID]; !found || PreferredImage(img, current) {
			byID[img.ID] = img
		}
	}
	result := make([]v1alpha1.Image, 0, len(byID))
	for _, img := range byID {
		result = append(result, img)
	}
	sort.Slice(result, func(i, j int) bool { return PreferredImage(result[i], result[j]) })
	return result
}
