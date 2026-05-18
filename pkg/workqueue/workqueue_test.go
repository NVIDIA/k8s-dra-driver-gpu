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

package workqueue

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestEnqueue(t *testing.T) {
	// Create a WorkQueue using the default rate limiter.
	defaultRateLimiter := DefaultControllerRateLimiter()
	wq := New(defaultRateLimiter)

	require.NotNil(t, wq)
	require.NotNil(t, wq.queue)

	// Create a context with timeout for processing.
	// use DefaultTypedControllerRateLimiter Base delay: 5ms
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	// Test EnqueueRaw
	t.Run("EnqueueRaw", func(t *testing.T) {
		var called int32
		callback := func(ctx context.Context, obj any) error {
			atomic.StoreInt32(&called, 1)
			return nil
		}
		wq.EnqueueRaw("AnyObject", callback)
		wq.processNextWorkItem(ctx)

		if atomic.LoadInt32(&called) != 1 {
			t.Error("EnqueueRaw callback was not invoked")
		}
	})
	// Test Enqueue with valid and invalid runtime.Object and  nil callback
	// TODO: Implement a proper claim spec that needs to be processed
	t.Run("EnqueueValid", func(t *testing.T) {
		var called int32
		callback := func(ctx context.Context, obj any) error {
			_, ok := obj.(runtime.Object)
			if !ok {
				t.Errorf("Expected runtime.Object, got %T", obj)
			}
			atomic.StoreInt32(&called, 1)
			return nil
		}
		validObj := &runtime.Unknown{}
		wq.Enqueue(validObj, callback)
		wq.processNextWorkItem(ctx)

		if atomic.LoadInt32(&called) != 1 {
			t.Error("Enqueue callback was not invoked")
		}
	})

	t.Run("EnqueueInvalid", func(t *testing.T) {
		callback := func(ctx context.Context, obj any) error { return nil }
		wq.Enqueue("NotRuntimeObject", callback)
	})

	t.Run("NilCallback", func(t *testing.T) {
		validObj := &runtime.Unknown{}
		wq.Enqueue(validObj, nil)
		wq.processNextWorkItem(ctx)
	})
}
