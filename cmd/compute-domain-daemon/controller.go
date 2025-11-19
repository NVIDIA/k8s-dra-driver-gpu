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

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/flags"
	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/workqueue"
)

// ManagerConfig holds the configuration for the compute domain manager.
type ManagerConfig struct {
	workQueue              *workqueue.WorkQueue
	clientsets             flags.ClientSets
	nodeName               string
	computeDomainUUID      string
	computeDomainName      string
	computeDomainNamespace string
	cliqueID               string
	podIP                  string
	podName                string
	podNamespace           string
	maxNodesPerIMEXDomain  int
}

// ControllerConfig holds the configuration for the controller.
type ControllerConfig struct {
	nodeName               string
	computeDomainUUID      string
	computeDomainName      string
	computeDomainNamespace string
	cliqueID               string
	podIP                  string
	podName                string
	podNamespace           string
	maxNodesPerIMEXDomain  int
}

// Controller manages the lifecycle of compute domain operations.
type Controller struct {
	computeDomainManager *ComputeDomainManager
	workQueue            *workqueue.WorkQueue
}

// NewController creates and initializes a new Controller instance.
func NewController(config *ControllerConfig) (*Controller, error) {
	// Create a new KubeConfig from flags
	kubeConfig := &flags.KubeClientConfig{}
	clientSets, err := kubeConfig.NewClientSets()
	if err != nil {
		return nil, fmt.Errorf("failed to create client sets: %v", err)
	}

	workQueue := workqueue.New(workqueue.DefaultCDDaemonRateLimiter())

	mc := &ManagerConfig{
		workQueue:              workQueue,
		clientsets:             clientSets,
		nodeName:               config.nodeName,
		computeDomainUUID:      config.computeDomainUUID,
		computeDomainName:      config.computeDomainName,
		computeDomainNamespace: config.computeDomainNamespace,
		cliqueID:               config.cliqueID,
		podIP:                  config.podIP,
		podName:                config.podName,
		podNamespace:           config.podNamespace,
		maxNodesPerIMEXDomain:  config.maxNodesPerIMEXDomain,
	}

	controller := &Controller{
		computeDomainManager: NewComputeDomainManager(mc),
		workQueue:            workQueue,
	}

	return controller, nil
}

// Run starts the controller's main loop and manages the lifecycle of its components.
// It initializes the work queue and handles graceful shutdown when the context is cancelled.
func (c *Controller) Run(ctx context.Context) error {
	// Start the compute domain manager
	if err := c.computeDomainManager.Start(ctx); err != nil {
		return fmt.Errorf("failed to start compute domain manager: %v", err)
	}

	// Start processing the workqueue
	c.workQueue.Run(ctx)

	// Stop the compute domain manager
	if err := c.computeDomainManager.Stop(); err != nil {
		return fmt.Errorf("failed to stop compute domain manager: %v", err)
	}

	return nil
}

// GetNodesUpdateChan() returns a channel that yields updates for the nodes
// currently present in the CD status. This is only a complete set of nodes
// (size `numNodes`) if IMEXDaemonsWithDNSNames=false.
func (c *Controller) GetNodesUpdateChan() chan []*nvapi.ComputeDomainNode {
	return c.computeDomainManager.GetNodesUpdateChan()
}
