/*
 * Copyright (c) 2025 NVIDIA CORPORATION.  All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

func uidIndexer[T metav1.ObjectMetaAccessor](obj any) ([]string, error) {
	d, ok := obj.(T)
	if !ok {
		return nil, fmt.Errorf("expected a %T but got %T", *new(T), obj)
	}
	return []string{string(d.GetObjectMeta().GetUID())}, nil
}

func addComputeDomainLabelIndexer[T metav1.ObjectMetaAccessor](informer cache.SharedIndexInformer) error {
	return informer.AddIndexers(cache.Indexers{
		"computeDomainLabel": func(obj any) ([]string, error) {
			d, ok := obj.(T)
			if !ok {
				return nil, fmt.Errorf("expected a %T but got %T", *new(T), obj)
			}
			labels := d.GetObjectMeta().GetLabels()
			if value, exists := labels[computeDomainLabelKey]; exists {
				return []string{value}, nil
			}
			klog.V(2).Info("No object found with ComputeDomain Label")
			return nil, nil
		},
	})
}

func getByComputeDomainUID[T1 *T2, T2 any](ctx context.Context, informer cache.SharedIndexInformer, cdUID string) ([]T1, error) {
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return nil, fmt.Errorf("cache sync failed for %T", *new(T1))
	}

	objs, err := informer.GetIndexer().ByIndex("computeDomainLabel", cdUID)
	if err != nil {
		return nil, fmt.Errorf("error getting %T via ComputeDomain label: %w", *new(T1), err)
	}
	if len(objs) == 0 {
		klog.V(2).Infof("No object found with ComputeDomain Label with UID %s", cdUID)
		return nil, nil
	}

	var ds []T1
	for _, obj := range objs {
		d, ok := obj.(T1)
		if !ok {
			return nil, fmt.Errorf("failed to cast to %T", *new(T1))
		}
		ds = append(ds, d)
	}

	klog.V(2).Infof("Found %d objects with ComputeDomain Label with UID %s", len(ds), cdUID)
	return ds, nil
}
