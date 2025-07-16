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

// Package main implements a Kubernetes Device Resource Allocation (DRA) driver controller
package main

import (
	"context"
	"fmt"

	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/flags"
	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/workqueue"
)

// ManagerConfig defines the common configuration options shared across all managers.
// It contains essential fields for driver identification, Kubernetes client access,
// and work queue management.
type ManagerConfig struct {
	// driverName is the unique identifier for this DRA driver
	driverName string

	// driverNamespace is the Kubernetes namespace where the driver operates
	driverNamespace string

	// imageName is the full image name to use when rendering templates
	imageName string

	// maxNodesPerIMEXDomain is the maximum number of nodes per IMEX domain to allocate
	maxNodesPerIMEXDomain int

	// clientsets provides access to various Kubernetes API client interfaces
	clientsets flags.ClientSets

	// workQueue manages the asynchronous processing of tasks
	workQueue *workqueue.WorkQueue

	// additionalNamespaces is a list of additional namespaces
	// where the driver can manage resources
	additionalNamespaces []string
}

// Controller manages the lifecycle of the DRA driver and its components.
type Controller struct {
	// config holds the controller's configuration settings
	config *Config
}

// NewController creates and initializes a new Controller instance with the provided configuration.
func NewController(config *Config) *Controller {
	return &Controller{config: config}
}

// Run starts the controller's main loop and manages the lifecycle of its components.
// It initializes the work queue, starts the ComputeDomain manager, and handles
// graceful shutdown when the context is cancelled.
func (c *Controller) Run(ctx context.Context) error {
	workQueue := workqueue.New(workqueue.DefaultControllerRateLimiter())

	managerConfig := &ManagerConfig{
		driverName:            c.config.driverName,
		driverNamespace:       c.config.flags.namespace,
		additionalNamespaces:  c.config.flags.additionalNamespaces.Value(),
		imageName:             c.config.flags.imageName,
		maxNodesPerIMEXDomain: c.config.flags.maxNodesPerIMEXDomain,
		clientsets:            c.config.clientsets,
		workQueue:             workQueue,
	}

	cdManager := NewComputeDomainManager(managerConfig)

	if err := cdManager.Start(ctx); err != nil {
		return fmt.Errorf("error starting ComputeDomain manager: %w", err)
	}

	workQueue.Run(ctx)

	if err := cdManager.Stop(); err != nil {
		return fmt.Errorf("error stopping ComputeDomain manager: %w", err)
	}

	return nil
}
