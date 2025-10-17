/*
 * Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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
	"sync"
)

type PerGPUMutex struct {
	sync.Mutex
	submutex map[string]*sync.Mutex
}

var perGpuLock *PerGPUMutex

func init() {
	perGpuLock = &PerGPUMutex{
		submutex: make(map[string]*sync.Mutex),
	}
}

func (pgm *PerGPUMutex) Get(gpu string) *sync.Mutex {
	pgm.Lock()
	defer pgm.Unlock()
	if pgm.submutex[gpu] == nil {
		pgm.submutex[gpu] = &sync.Mutex{}
	}
	return pgm.submutex[gpu]
}
