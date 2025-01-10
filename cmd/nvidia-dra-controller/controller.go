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
)

type Controller struct {
	ImexManager *ImexManager
}

// StartController starts a Controller.
func StartController(ctx context.Context, config *Config) (*Controller, error) {
	if !config.flags.deviceClasses.Has(ImexChannelType) {
		return nil, nil
	}

	imexManager, err := StartImexManager(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("error starting IMEX manager: %w", err)
	}

	m := &Controller{
		ImexManager: imexManager,
	}

	return m, nil
}

// Stop stops a running Controller.
func (m *Controller) Stop() error {
	if m == nil {
		return nil
	}
	return m.ImexManager.Stop()
}
