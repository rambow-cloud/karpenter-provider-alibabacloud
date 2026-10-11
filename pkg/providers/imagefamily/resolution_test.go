package imagefamily

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
)

type discoveryClient struct {
	clients.ECSClient
	calls int
	query func([]string, map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error)
}

func (c *discoveryClient) DescribeImages(_ context.Context, ids []string, filters map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error) {
	c.calls++
	return c.query(ids, filters)
}

func TestImageVersionsUseCreationTimeAndPreservePins(t *testing.T) {
	client := &discoveryClient{query: func(ids []string, _ map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error) {
		images := []ecs.DescribeImagesResponseBodyImagesImage{
			{ImageId: tea.String("m-a-old"), Architecture: tea.String("x86_64"), CreationTime: tea.String("2025-01-01T00:00:00Z")},
			{ImageId: tea.String("m-z-new"), Architecture: tea.String("x86_64"), CreationTime: tea.String("2026-10-01T00:00:00Z")},
			{ImageId: tea.String("m-arm"), Architecture: tea.String("arm64"), CreationTime: tea.String("2026-09-01T00:00:00Z")},
			{ImageId: tea.String("m-unknown"), Architecture: tea.String("x86_64")},
		}
		if len(ids) > 0 {
			return images[:1], nil
		}
		return append(images, images[1]), nil
	}}
	p := NewProvider(client)
	images, err := p.Resolve(context.Background(), []v1alpha1.ImageSelectorTerm{{Name: tea.String("app-*")}})
	require.NoError(t, err)
	require.Equal(t, []string{"m-z-new", "m-arm", "m-a-old", "m-unknown"}, imageIDs(images))
	require.Equal(t, "2026-10-01T00:00:00Z", images[0].CreationTime)
	pinned, err := p.Resolve(context.Background(), []v1alpha1.ImageSelectorTerm{{ID: tea.String("m-a-old")}})
	require.NoError(t, err)
	require.Equal(t, []string{"m-a-old"}, imageIDs(pinned))

	images = removeDuplicateImages([]v1alpha1.Image{
		{ID: "m-b", CreationTime: "2026-10-01T08:00:00+08:00"},
		{ID: "m-a", CreationTime: "2026-10-01T00:00:00Z"},
		{ID: "m-invalid", CreationTime: "not-a-date"},
		{ID: "m-empty"},
		{ID: "m-a"}, // Incomplete duplicate must not discard a known timestamp.
	})
	require.Equal(t, []string{"m-a", "m-b", "m-empty", "m-invalid"}, imageIDs(images))
}

func imageIDs(images []v1alpha1.Image) []string {
	ids := make([]string, 0, len(images))
	for _, image := range images {
		ids = append(ids, image.ID)
	}
	return ids
}

func TestSemanticImageCacheAndReturnIsolation(t *testing.T) {
	client := &discoveryClient{query: func(_ []string, filters map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error) {
		return []ecs.DescribeImagesResponseBodyImagesImage{{ImageId: tea.String("m-" + filters["ImageName"])}}, nil
	}}
	p := NewProvider(client)
	name := "same"
	terms := []v1alpha1.ImageSelectorTerm{{Name: &name, Tags: map[string]string{"env": "prod", "app": "web"}}}
	first, err := p.Resolve(context.Background(), terms)
	require.NoError(t, err)
	first[0].ID = "caller-mutated"
	equivalent := []v1alpha1.ImageSelectorTerm{{Name: tea.String("same"), Tags: map[string]string{"app": "web", "env": "prod"}}}
	second, err := p.Resolve(context.Background(), equivalent)
	require.NoError(t, err)
	require.Equal(t, "m-same", second[0].ID)
	require.Equal(t, 1, client.calls)
	name = "changed"
	changed, err := p.Resolve(context.Background(), terms)
	require.NoError(t, err)
	require.Equal(t, "m-changed", changed[0].ID)
	require.Equal(t, 2, client.calls)

	a := v1alpha1.ImageSelectorTerm{Name: tea.String("a")}
	b := v1alpha1.ImageSelectorTerm{Name: tea.String("b")}
	_, err = p.Resolve(context.Background(), []v1alpha1.ImageSelectorTerm{a, b})
	require.NoError(t, err)
	count := client.calls
	_, err = p.Resolve(context.Background(), []v1alpha1.ImageSelectorTerm{b, a, a})
	require.NoError(t, err)
	require.Equal(t, count, client.calls, "selector order and duplicate OR alternatives do not change query semantics")
}

func TestImageCacheRecoversEmptyFailuresAndReclaimsExpiredEntries(t *testing.T) {
	available, failure := false, false
	client := &discoveryClient{query: func(_ []string, filters map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error) {
		if failure {
			return nil, fmt.Errorf("API unavailable")
		}
		if !available {
			return nil, nil
		}
		return []ecs.DescribeImagesResponseBodyImagesImage{{ImageId: tea.String("m-" + filters["ImageName"])}}, nil
	}}
	p := NewProvider(client)
	terms := []v1alpha1.ImageSelectorTerm{{Name: tea.String("new-release")}}
	images, err := p.Resolve(context.Background(), terms)
	require.NoError(t, err)
	require.Empty(t, images)
	available = true
	images, err = p.Resolve(context.Background(), terms)
	require.NoError(t, err)
	require.Len(t, images, 1)
	require.Equal(t, 2, client.calls)
	p.cache[imageCacheKey(terms)].ExpiresAt = time.Now().Add(-time.Second)
	failure = true
	_, err = p.Resolve(context.Background(), []v1alpha1.ImageSelectorTerm{{Name: tea.String("different")}})
	require.Error(t, err)
	require.Empty(t, p.cache, "expired selectors are reclaimed even when a different key is queried")
	failure = false
	_, err = p.Resolve(context.Background(), terms)
	require.NoError(t, err)
	require.Equal(t, 4, client.calls)
}

func TestImageCacheCapacityIsBounded(t *testing.T) {
	client := &discoveryClient{query: func(_ []string, filters map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error) {
		return []ecs.DescribeImagesResponseBodyImagesImage{{ImageId: tea.String("m-" + filters["ImageName"])}}, nil
	}}
	p := NewProvider(client)
	for i := 0; i < maxImageCacheEntries+1; i++ {
		_, err := p.Resolve(context.Background(), []v1alpha1.ImageSelectorTerm{{Name: tea.String(fmt.Sprintf("selector-%d", i))}})
		require.NoError(t, err)
	}
	require.Len(t, p.cache, maxImageCacheEntries)
	count := client.calls
	_, err := p.Resolve(context.Background(), []v1alpha1.ImageSelectorTerm{{Name: tea.String("selector-0")}})
	require.NoError(t, err)
	require.Equal(t, count+1, client.calls, "oldest positive entry is evicted at the capacity limit")
	require.Len(t, p.cache, maxImageCacheEntries)
}
