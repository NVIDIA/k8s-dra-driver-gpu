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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

// Indexer is a minimal interface that only provides the ByIndex method.
type Indexer interface {
	ByIndex(indexName, indexedValue string) ([]interface{}, error)
}

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
			return nil, nil
		},
	})
}

func getByComputeDomainUID[T1 *T2, T2 any](ctx context.Context, indexer Indexer, cdUID string) ([]T1, error) {
	objs, err := indexer.ByIndex("computeDomainLabel", cdUID)
	if err != nil {
		return nil, fmt.Errorf("error getting %T via ComputeDomain label: %w", *new(T1), err)
	}
	if len(objs) == 0 {
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

	return ds, nil
}

func addComputeDomainNodePodInexer(informer cache.SharedIndexInformer) error {
	return informer.AddIndexers(cache.Indexers{
		"computeDomainNode": func(obj any) ([]string, error) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				return nil, fmt.Errorf("expected a *corev1.Pod but got %T", obj)
			}
			labels := pod.GetObjectMeta().GetLabels()
			if value, exists := labels[computeDomainLabelKey]; exists {
				nodeName := pod.Spec.NodeName
				if len(nodeName) != 0 {
					return []string{fmt.Sprintf("%s/%s", value, nodeName)}, nil
				}
			}
			return nil, nil
		},
	})
}

func getByComputeDomainUIDAndNode(ctx context.Context, indexer Indexer, cdUID string, nodeName string) (*corev1.Pod, error) {
	key := fmt.Sprintf("%s/%s", cdUID, nodeName)
	objs, err := indexer.ByIndex("computeDomainNode", key)
	if err != nil {
		return nil, fmt.Errorf("error getting Pod via ComputeDomain+Node index: %w", err)
	}
	if len(objs) == 0 {
		return nil, nil
	}
	if len(objs) > 1 {
		return nil, fmt.Errorf("multiple DaemonSetPods found for cdUID %s and nodeName %s", cdUID, nodeName)
	}
	pod, ok := objs[0].(*corev1.Pod)
	if !ok {
		return nil, fmt.Errorf("failed to cast to *corev1.Pod")
	}
	return pod, nil
}
