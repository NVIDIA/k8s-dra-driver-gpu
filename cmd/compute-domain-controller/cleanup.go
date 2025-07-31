/*
 * SPDX-FileCopyrightText: Copyright (c) 2025 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
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
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

const (
	cleanupInterval = 10 * time.Minute
)

type CleanupCallback[T metav1.Object] func(ctx context.Context, cdUID string) error

type CleanupManager[T metav1.Object] struct {
	waitGroup     sync.WaitGroup
	cancelContext context.CancelFunc

	informers        []cache.SharedIndexInformer
	getComputeDomain GetComputeDomainFunc
	callback         CleanupCallback[T]
	queue            chan struct{}
}

func NewCleanupManager[T metav1.Object](informers []cache.SharedIndexInformer, getComputeDomain GetComputeDomainFunc, callback CleanupCallback[T]) *CleanupManager[T] {
	return &CleanupManager[T]{
		informers:        informers,
		getComputeDomain: getComputeDomain,
		callback:         callback,
		// Buffered channel to implement a pragmatic fixed-size queue so that
		// we enqueue at most one cleanup operation.
		queue: make(chan struct{}, 1),
	}
}

func (m *CleanupManager[T]) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.periodicCleanup(ctx)
	}()

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.cleanupWorker(ctx)
	}()

	return nil
}

func (m *CleanupManager[T]) Stop() error {
	m.cancelContext()
	m.waitGroup.Wait()
	return nil
}

// EnqueueCleanup() submits a cleanup task if the queue is currently empty.
// Return a Boolean indicating whether the task was submitted or not.
func (m *CleanupManager[T]) EnqueueCleanup() bool {
	select {
	case m.queue <- struct{}{}:
		// Task submitted.
		return true
	default:
		// Channel full: one task already lined up, did not submit more.
		return false
	}
}

func (m *CleanupManager[T]) cleanup(ctx context.Context) {
	klog.V(6).Infof("Cleanup: perform for %T objects", *new(T))
	list := []interface{}{}
	for _, informer := range m.informers {
		list = append(list, informer.GetStore().List())
	}
	for _, item := range list {
		obj, ok := item.(T)
		if !ok {
			continue
		}

		labels := obj.GetLabels()
		if labels == nil {
			continue
		}

		uid, exists := labels[computeDomainLabelKey]
		if !exists {
			continue
		}

		computeDomain, err := m.getComputeDomain(uid)
		if err != nil {
			klog.Errorf("error getting ComputeDomain: %v", err)
			continue
		}

		if computeDomain != nil {
			continue
		}

		klog.Infof("Cleanup: stale %T found for ComputeDomain '%s', running callback", *new(T), uid)
		if err := m.callback(ctx, uid); err != nil {
			klog.Errorf("error running CleanupManager callback: %v", err)
			continue
		}
	}
}

// Run forever until context is canceled.
func (m *CleanupManager[T]) cleanupWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.queue:
			m.cleanup(ctx)
		}
	}
}

// Periodically submit a cleanup task.
func (m *CleanupManager[T]) periodicCleanup(ctx context.Context) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if m.EnqueueCleanup() {
				klog.V(6).Infof("Periodoc cleanup requested for %T objects", *new(T))
			}
		}
	}
}
