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
	"slices"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	nvapi "github.com/NVIDIA/k8s-dra-driver/api/nvidia.com/resource/v1beta1"
)

const (
	CliqueIDLabelKey = "nvidia.com/gpu.clique"
)

type DeploymentPodManager struct {
	config        *ManagerConfig
	waitGroup     sync.WaitGroup
	cancelContext context.CancelFunc

	factory  informers.SharedInformerFactory
	informer cache.SharedInformer
	lister   corev1listers.PodLister

	getComputeDomain   GetComputeDomainFunc
	computeDomainNodes []*nvapi.ComputeDomainNode
	numPods            int
}

func NewDeploymentPodManager(config *ManagerConfig, labelSelector *metav1.LabelSelector, numPods int, getComputeDomain GetComputeDomainFunc) *DeploymentPodManager {
	factory := informers.NewSharedInformerFactoryWithOptions(
		config.clientsets.Core,
		informerResyncPeriod,
		informers.WithNamespace(config.driverNamespace),
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.LabelSelector = metav1.FormatLabelSelector(labelSelector)
		}),
	)

	informer := factory.Core().V1().Pods().Informer()
	lister := factory.Core().V1().Pods().Lister()

	m := &DeploymentPodManager{
		config:           config,
		factory:          factory,
		informer:         informer,
		lister:           lister,
		getComputeDomain: getComputeDomain,
		numPods:          numPods,
	}

	return m
}

func (m *DeploymentPodManager) Start(ctx context.Context) (rerr error) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	defer func() {
		if rerr != nil {
			if err := m.Stop(); err != nil {
				klog.Errorf("error stopping DeploymentPod manager: %v", err)
			}
		}
	}()

	_, err := m.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			m.config.workQueue.Enqueue(obj, m.onPodAddOrUpdate)
		},
		UpdateFunc: func(objOld, objNew any) {
			m.config.workQueue.Enqueue(objNew, m.onPodAddOrUpdate)
		},
	})
	if err != nil {
		return fmt.Errorf("error adding event handlers for pod informer: %w", err)
	}

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.factory.Start(ctx.Done())
	}()

	if !cache.WaitForCacheSync(ctx.Done(), m.informer.HasSynced) {
		return fmt.Errorf("error syncing pod informer: %w", err)
	}

	return nil
}

func (m *DeploymentPodManager) Stop() error {
	m.cancelContext()
	m.waitGroup.Wait()
	return nil
}

func (m *DeploymentPodManager) onPodAddOrUpdate(ctx context.Context, obj any) error {
	p, ok := obj.(*corev1.Pod)
	if !ok {
		return fmt.Errorf("failed to cast to Pod")
	}

	p, err := m.lister.Pods(p.Namespace).Get(p.Name)
	if err != nil && errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("erroring retreiving Pod: %w", err)
	}

	klog.Infof("Processing added or updated Pod: %s/%s", p.Namespace, p.Name)

	cd, err := m.getComputeDomain(p.Labels[computeDomainLabelKey])
	if err != nil {
		return fmt.Errorf("error getting ComputeDomain: %w", err)
	}
	if cd == nil {
		return nil
	}

	if p.Spec.NodeName == "" {
		return fmt.Errorf("pod not yet scheduled: %s/%s", p.Namespace, p.Name)
	}

	var nodeNames []string
	for _, node := range m.computeDomainNodes {
		nodeNames = append(nodeNames, node.Name)
	}

	if !slices.Contains(nodeNames, p.Spec.NodeName) {
		node, err := m.GetComputeDomainNode(ctx, p.Spec.NodeName)
		if err != nil {
			return fmt.Errorf("error getting ComputeDomainNode: %w", err)
		}
		nodeNames = append(nodeNames, node.Name)
		m.computeDomainNodes = append(m.computeDomainNodes, node)
	}

	if len(nodeNames) != m.numPods {
		return fmt.Errorf("not all pods scheduled yet")
	}

	cd.Status.Nodes = m.computeDomainNodes
	cd.Status.Status = nvapi.ComputeDomainStatusNotReady
	if _, err = m.config.clientsets.Nvidia.ResourceV1beta1().ComputeDomains(cd.Namespace).UpdateStatus(ctx, cd, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("error updating nodes in ComputeDomain status: %w", err)
	}

	return nil
}

func (m *DeploymentPodManager) GetComputeDomainNode(ctx context.Context, nodeName string) (*nvapi.ComputeDomainNode, error) {
	node, err := m.config.clientsets.Core.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("error getting Node '%s': %w", nodeName, err)
	}

	var ipAddress string
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP {
			ipAddress = addr.Address
			break
		}
	}

	n := &nvapi.ComputeDomainNode{
		Name:      nodeName,
		IPAddress: ipAddress,
		CliqueID:  node.Labels[CliqueIDLabelKey],
	}

	return n, nil
}
