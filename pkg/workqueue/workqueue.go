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
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

type WorkQueue struct {
	sync.RWMutex
	queue     workqueue.TypedRateLimitingInterface[any]
	activeOps map[string]*WorkItem
}

type WorkItem struct {
	Key      string
	Object   any
	Callback func(ctx context.Context, obj any) error
}

func DefaultControllerRateLimiter() workqueue.TypedRateLimiter[any] {
	return workqueue.DefaultTypedControllerRateLimiter[any]()
}

func New(r workqueue.TypedRateLimiter[any]) *WorkQueue {
	queue := workqueue.NewTypedRateLimitingQueue(r)
	return &WorkQueue{
		queue:     queue,
		activeOps: make(map[string]*WorkItem),
	}
}

func (q *WorkQueue) Run(ctx context.Context) {
	go func() {
		<-ctx.Done()
		q.queue.ShutDown()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		default:
			q.processNextWorkItem(ctx)
		}
	}
}

func (q *WorkQueue) EnqueueRaw(obj any, callback func(ctx context.Context, obj any) error) {
	workItem := &WorkItem{
		Object:   obj,
		Callback: callback,
	}
	q.queue.AddRateLimited(workItem)
}

func (q *WorkQueue) Enqueue(obj any, callback func(ctx context.Context, obj any) error) {
	runtimeObj, ok := obj.(runtime.Object)
	if !ok {
		klog.Warningf("unexpected object type %T: runtime.Object required", obj)
		return
	}

	workItem := &WorkItem{
		Object:   runtimeObj.DeepCopyObject(),
		Callback: callback,
	}

	q.queue.AddRateLimited(workItem)
}

func (q *WorkQueue) EnqueueRawWithKey(obj any, key string, callback func(ctx context.Context, obj any) error) {
	workItem := &WorkItem{
		Key:      key,
		Object:   obj,
		Callback: callback,
	}

	q.Lock()
	q.activeOps[key] = workItem
	q.queue.AddRateLimited(workItem)
	q.Unlock()
}

func (q *WorkQueue) EnqueueWithKey(obj any, key string, callback func(ctx context.Context, obj any) error) {
	runtimeObj, ok := obj.(runtime.Object)
	if !ok {
		klog.Warningf("unexpected object type %T: runtime.Object required", obj)
		return
	}

	workItem := &WorkItem{
		Key:      key,
		Object:   runtimeObj.DeepCopyObject(),
		Callback: callback,
	}

	q.Lock()
	q.activeOps[key] = workItem
	q.queue.AddRateLimited(workItem)
	q.Unlock()
}

func (q *WorkQueue) processNextWorkItem(ctx context.Context) {
	item, shutdown := q.queue.Get()
	if shutdown {
		return
	}
	defer q.queue.Done(item)

	workItem, ok := item.(*WorkItem)
	if !ok {
		klog.Errorf("Unexpected item in queue: %v", item)
		return
	}

	err := q.reconcile(ctx, workItem)
	if err != nil {
		klog.Errorf("Failed to reconcile work item: %v", err)
		// Only retry if we're still the current operation for this key
		q.Lock()
		if q.activeOps[workItem.Key] != nil && q.activeOps[workItem.Key] != workItem {
			klog.Errorf("Work item with key '%s' has been replaced with a newer enqueued one, not retrying", workItem.Key)
			q.queue.Forget(workItem)
		} else {
			q.queue.AddRateLimited(workItem)
		}
		q.Unlock()
	} else {
		// Only clean up activeOps if this item is still the current one for this key
		q.Lock()
		if workItem == q.activeOps[workItem.Key] {
			delete(q.activeOps, workItem.Key)
		}
		q.queue.Forget(workItem)
		q.Unlock()
	}
}

func (q *WorkQueue) reconcile(ctx context.Context, workItem *WorkItem) error {
	if workItem.Callback == nil {
		return fmt.Errorf("no callback to process work item: %+v", workItem)
	}
	return workItem.Callback(ctx, workItem.Object)
}
