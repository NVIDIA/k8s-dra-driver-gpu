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

package workqueue

import (
	"math/rand"
	"time"

	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

// Jitter relative to the delay yielded by the inner limiter. Example: a factor
// of 0.1 translates to a jitter interval with a width of 10 % compared to the
// inner delay, and centered around the inner delay time (resulting in +/- 5 %
// deviation compared to the inner delay time).
type JitterRL[T comparable] struct {
	inner  workqueue.TypedRateLimiter[T]
	factor float64
}

func NewJitterRateLimiter[T comparable](inner workqueue.TypedRateLimiter[T], factor float64) workqueue.TypedRateLimiter[T] {
	if factor >= 1.0 {
		panic("factor must be < 1.0")
	}
	return &JitterRL[T]{inner: inner, factor: factor}
}

func (j *JitterRL[T]) When(item T) time.Duration {
	// Get inner limiter's delay.
	d := j.inner.When(item)

	// Calculate jitter interval width W_j relative to the delay time given by
	// the inner limiter.
	jitterWidthSeconds := d.Seconds() * j.factor

	// Get random number in the interval [-W_j/2, W_j/2).
	jitterSeconds := jitterWidthSeconds * (rand.Float64() - 0.5)

	delay := d + time.Duration(jitterSeconds*float64(time.Second))
	klog.V(7).Infof("inner: %.5f s, jittered: %.5f s", d.Seconds(), delay.Seconds())

	return delay
}

func (j *JitterRL[T]) Forget(item T) {
	j.inner.Forget(item)
}

func (j *JitterRL[T]) NumRequeues(item T) int {
	return j.inner.NumRequeues(item)
}
