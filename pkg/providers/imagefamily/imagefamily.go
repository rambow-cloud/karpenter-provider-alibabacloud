/*
Copyright 2024 The Alibaba Cloud Karpenter Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package imagefamily

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
)

// Provider handles image operations for Alibaba Cloud
type Provider struct {
	ecsClient clients.ECSClient
	cache     map[string]*CacheEntry
	cacheMu   sync.RWMutex
	cacheTTL  time.Duration
	ack       *ackCompatibility
}

type ImageQuery struct {
	ID              string
	ImageFamily     string
	Name            string
	ImageOwnerAlias string
	ImageOwnerID    string
	Tags            map[string]string
}

// CacheEntry represents a cached result with expiration
type CacheEntry struct {
	Value     interface{}
	ExpiresAt time.Time
}

// Image represents an ECS image
type Image struct {
	ID           string
	Name         string
	OSType       string
	Architecture string
	CreationTime time.Time
}

// NewProvider creates a new image provider
func NewProvider(ecsClient clients.ECSClient, opts ...Option) *Provider {
	p := &Provider{
		ecsClient: ecsClient,
		cache:     make(map[string]*CacheEntry),
		cacheTTL:  5 * time.Minute, // Bound discovery staleness; empty results are not cached.
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// SetCacheTTL sets the cache TTL duration
func (p *Provider) SetCacheTTL(ttl time.Duration) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	p.cacheTTL = ttl
}

const maxImageCacheEntries = 256

// pruneExpiredLocked also reclaims entries whose selectors are never reused.
func (p *Provider) pruneExpiredLocked(now time.Time) {
	for key, entry := range p.cache {
		if !now.Before(entry.ExpiresAt) {
			delete(p.cache, key)
		}
	}
}

func (p *Provider) getCachedValue(key string) (interface{}, bool) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	p.pruneExpiredLocked(time.Now())
	entry, exists := p.cache[key]
	if !exists {
		return nil, false
	}
	return append([]v1alpha1.Image(nil), entry.Value.([]v1alpha1.Image)...), true
}

func (p *Provider) setCachedValue(key string, value interface{}) {
	images := value.([]v1alpha1.Image)
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	now := time.Now()
	p.pruneExpiredLocked(now)
	if len(images) == 0 || p.cacheTTL <= 0 {
		return
	}
	if _, exists := p.cache[key]; !exists && len(p.cache) >= maxImageCacheEntries {
		oldest := ""
		for candidate, entry := range p.cache {
			if oldest == "" || entry.ExpiresAt.Before(p.cache[oldest].ExpiresAt) || (entry.ExpiresAt.Equal(p.cache[oldest].ExpiresAt) && candidate < oldest) {
				oldest = candidate
			}
		}
		delete(p.cache, oldest)
	}
	p.cache[key] = &CacheEntry{Value: append([]v1alpha1.Image(nil), images...), ExpiresAt: now.Add(p.cacheTTL)}
}

// Selectors are OR alternatives. Normalize values, map order, duplicate terms
// and term order so DeepCopy/reallocation cannot change the cache identity.
func imageCacheKey(terms []v1alpha1.ImageSelectorTerm) string {
	unique := map[string]bool{}
	for _, term := range terms {
		query := imageQueryFromTerm(term)
		if len(query.Tags) == 0 {
			query.Tags = nil
		}
		encoded, _ := json.Marshal(query) // Only strings and string maps.
		unique[string(encoded)] = true
	}
	canonical := make([]string, 0, len(unique))
	for query := range unique {
		canonical = append(canonical, query)
	}
	sort.Strings(canonical)
	encoded, _ := json.Marshal(canonical)
	return string(encoded)
}

// Resolve resolves image selectors to actual images
func (p *Provider) Resolve(ctx context.Context, terms []v1alpha1.ImageSelectorTerm) ([]v1alpha1.Image, error) {
	if err := v1alpha1.ValidateImageSelectorTerms(terms); err != nil {
		return nil, fmt.Errorf("resolve images: %w", err)
	}

	// Create cache key from terms
	cacheKey := imageCacheKey(terms)

	// Check cache first
	if images, exists := p.getCachedValue(cacheKey); exists {
		return images.([]v1alpha1.Image), nil
	}

	var result []v1alpha1.Image

	for i, term := range terms {
		query := imageQueryFromTerm(term)
		images, err := p.getByQuery(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("resolve imageSelectorTerms[%d]: %w", i, err)
		}
		result = append(result, images...)
	}

	// Remove duplicates
	result = removeDuplicateImages(result)

	// Cache the result
	p.setCachedValue(cacheKey, result)

	return result, nil
}

func imageQueryFromTerm(term v1alpha1.ImageSelectorTerm) ImageQuery {
	query := ImageQuery{Tags: term.Tags}
	if term.ID != nil {
		query.ID = *term.ID
	}
	if term.ImageFamily != nil {
		query.ImageFamily = *term.ImageFamily
	}
	if term.Name != nil {
		query.Name = *term.Name
	}
	if term.ImageOwnerAlias != nil {
		query.ImageOwnerAlias = *term.ImageOwnerAlias
	}
	if term.ImageOwnerID != nil {
		query.ImageOwnerID = *term.ImageOwnerID
	}
	return query
}

func (p *Provider) getByQuery(ctx context.Context, query ImageQuery) ([]v1alpha1.Image, error) {
	imageIDs := []string(nil)
	filters := map[string]string{}
	if query.ID != "" {
		imageIDs = []string{query.ID}
	}
	if query.ImageFamily != "" {
		filters["ImageFamily"] = query.ImageFamily
	}
	if query.Name != "" {
		filters["ImageName"] = query.Name
	}
	if query.ImageOwnerAlias != "" {
		filters["ImageOwnerAlias"] = query.ImageOwnerAlias
	}
	if query.ImageOwnerID != "" {
		filters["ImageOwnerID"] = query.ImageOwnerID
	}
	for k, v := range query.Tags {
		filters["tag:"+k] = v
	}
	if len(filters) == 0 {
		filters = nil
	}

	images, err := p.ecsClient.DescribeImages(ctx, imageIDs, filters)
	if err != nil {
		return nil, fmt.Errorf("failed to describe images: %w", err)
	}

	result := make([]v1alpha1.Image, 0, len(images))
	for _, img := range images {
		if img.ImageId == nil {
			continue
		}
		image := v1alpha1.Image{ID: *img.ImageId}
		if img.CreationTime != nil {
			image.CreationTime = *img.CreationTime
		}
		if img.ImageName != nil {
			image.Name = *img.ImageName
		}
		if img.Architecture != nil {
			image.Architecture = *img.Architecture
		}
		if img.OSType != nil {
			image.OSType = *img.OSType
		}
		result = append(result, image)
	}
	return result, nil
}

// ClearCache clears the image cache
func (p *Provider) ClearCache() {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	p.cache = make(map[string]*CacheEntry)
}
