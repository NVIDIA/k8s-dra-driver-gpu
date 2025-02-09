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
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"text/template"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	cdiapi "tags.cncf.io/container-device-interface/pkg/cdi"
	cdispec "tags.cncf.io/container-device-interface/specs-go"

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
	nvinformers "github.com/NVIDIA/k8s-dra-driver-gpu/pkg/nvidia.com/informers/externalversions"
)

const (
	computeDomainLabelKey = "resource.nvidia.com/computeDomain"

	informerResyncPeriod = 10 * time.Minute

	ComputeDomainDaemonSettingsRoot       = DriverPluginPath + "/domains"
	ComputeDomainDaemonConfigTemplatePath = "/templates/compute-domain-daemon-config.tmpl.cfg"
)

type ComputeDomainManager struct {
	config        *Config
	waitGroup     sync.WaitGroup
	cancelContext context.CancelFunc

	factory  nvinformers.SharedInformerFactory
	informer cache.SharedIndexInformer

	configFilesRoot string
	cliqueID        string
}

type ComputeDomainDaemonSettings struct {
	manager         *ComputeDomainManager
	domain          string
	rootDir         string
	configPath      string
	nodesConfigPath string
}

func NewComputeDomainManager(config *Config, configFilesRoot, cliqueID string) *ComputeDomainManager {
	factory := nvinformers.NewSharedInformerFactory(config.clientsets.Nvidia, informerResyncPeriod)
	informer := factory.Resource().V1beta1().ComputeDomains().Informer()

	m := &ComputeDomainManager{
		config:          config,
		factory:         factory,
		informer:        informer,
		configFilesRoot: configFilesRoot,
		cliqueID:        cliqueID,
	}

	return m
}

func (m *ComputeDomainManager) Start(ctx context.Context) (rerr error) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	defer func() {
		if rerr != nil {
			if err := m.Stop(); err != nil {
				klog.Errorf("error stopping ComputeDomainManager: %v", err)
			}
		}
	}()

	err := m.informer.AddIndexers(cache.Indexers{
		"computeDomainUID": uidIndexer[*nvapi.ComputeDomain],
	})
	if err != nil {
		return fmt.Errorf("error adding indexer for UIDs: %w", err)
	}

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.factory.Start(ctx.Done())
	}()

	if !cache.WaitForCacheSync(ctx.Done(), m.informer.HasSynced) {
		return fmt.Errorf("informer cache sync for ComputeDomains failed")
	}

	return nil
}

func (m *ComputeDomainManager) Stop() error {
	m.cancelContext()
	m.waitGroup.Wait()
	return nil
}

func (m *ComputeDomainManager) NewSettings(domain string) *ComputeDomainDaemonSettings {
	return &ComputeDomainDaemonSettings{
		manager:         m,
		domain:          domain,
		rootDir:         fmt.Sprintf("%s/%s", m.configFilesRoot, domain),
		configPath:      fmt.Sprintf("%s/%s/%s", m.configFilesRoot, domain, "config.cfg"),
		nodesConfigPath: fmt.Sprintf("%s/%s/%s", m.configFilesRoot, domain, "nodes_config.cfg"),
	}
}

func (m *ComputeDomainManager) GetComputeDomainChannelContainerEdits(devRoot string, info *ComputeDomainChannelInfo) *cdiapi.ContainerEdits {
	if m.cliqueID == "" {
		return nil
	}

	channelPath := fmt.Sprintf("/dev/nvidia-caps-imex-channels/channel%d", info.ID)

	return &cdiapi.ContainerEdits{
		ContainerEdits: &cdispec.ContainerEdits{
			DeviceNodes: []*cdispec.DeviceNode{
				{
					Path:     channelPath,
					HostPath: filepath.Join(devRoot, channelPath),
				},
			},
		},
	}
}

func (s *ComputeDomainDaemonSettings) GetDomain() string {
	return s.domain
}

func (s *ComputeDomainDaemonSettings) GetCDIContainerEdits() *cdiapi.ContainerEdits {
	return &cdiapi.ContainerEdits{
		ContainerEdits: &cdispec.ContainerEdits{
			Mounts: []*cdispec.Mount{
				{
					ContainerPath: "/etc/nvidia-imex",
					HostPath:      s.rootDir,
					Options:       []string{"rw", "nosuid", "nodev", "bind"},
				},
			},
		},
	}
}

func (s *ComputeDomainDaemonSettings) Prepare(ctx context.Context) error {
	if err := os.MkdirAll(s.rootDir, 0755); err != nil {
		return fmt.Errorf("error creating directory %v: %w", s.rootDir, err)
	}

	if err := s.WriteConfigFile(ctx); err != nil {
		return fmt.Errorf("error writing config file %v: %w", s.configPath, err)
	}

	if err := s.WriteNodesConfigFile(ctx); err != nil {
		return fmt.Errorf("error writing nodes config file %v: %w", s.nodesConfigPath, err)
	}

	return nil
}

func (s *ComputeDomainDaemonSettings) Unprepare(ctx context.Context) error {
	if err := os.RemoveAll(s.rootDir); err != nil {
		return fmt.Errorf("error removing directory %v: %w", s.rootDir, err)
	}
	return nil
}

func (s *ComputeDomainDaemonSettings) WriteConfigFile(ctx context.Context) error {
	configTemplateData := struct{}{}

	tmpl, err := template.ParseFiles(ComputeDomainDaemonConfigTemplatePath)
	if err != nil {
		return fmt.Errorf("error parsing template file: %w", err)
	}

	var configFile bytes.Buffer
	if err := tmpl.Execute(&configFile, configTemplateData); err != nil {
		return fmt.Errorf("error executing template: %w", err)
	}

	if err := os.WriteFile(s.configPath, configFile.Bytes(), 0644); err != nil {
		return fmt.Errorf("error writing config file %v: %w", s.configPath, err)
	}

	return nil
}

func (s *ComputeDomainDaemonSettings) WriteNodesConfigFile(ctx context.Context) error {
	nodeIPs, err := s.manager.GetNodeIPs(ctx, s.domain)
	if err != nil {
		return fmt.Errorf("error getting node IPs: %w", err)
	}

	var nodesConfigFile bytes.Buffer
	for _, ip := range nodeIPs {
		nodesConfigFile.WriteString(fmt.Sprintf("%s\n", ip))
	}

	if err := os.WriteFile(s.nodesConfigPath, nodesConfigFile.Bytes(), 0644); err != nil {
		return fmt.Errorf("error writing config file %v: %w", s.configPath, err)
	}

	return nil
}

func (m *ComputeDomainManager) AssertComputeDomainReady(ctx context.Context, cdUID string) error {
	if err := m.UpdateComputeDomainDeployment(ctx, cdUID); err != nil {
		return fmt.Errorf("error updating Deployment for ComputeDomain: %w", err)
	}

	cd, err := m.GetComputeDomain(ctx, cdUID)
	if err != nil {
		return fmt.Errorf("error getting ComputeDomain: %w", err)
	}
	if cd == nil {
		return fmt.Errorf("ComputeDomain not found: %s", cdUID)
	}

	if cd.Status.Status != nvapi.ComputeDomainStatusReady {
		return fmt.Errorf("ComputeDomain not Ready")
	}

	return nil
}

func (m *ComputeDomainManager) GetNodeIPs(ctx context.Context, cdUID string) ([]string, error) {
	cd, err := m.GetComputeDomain(ctx, cdUID)
	if err != nil {
		return nil, fmt.Errorf("error getting ComputeDomain: %w", err)
	}
	if cd == nil {
		return nil, fmt.Errorf("ComputeDomain not found: %s", cdUID)
	}

	if cd.Status.Nodes == nil {
		return nil, fmt.Errorf("error getting status of nodes in ComputeDomain: %w", err)
	}

	var ips []string
	for _, node := range cd.Status.Nodes {
		if m.cliqueID == node.CliqueID {
			ips = append(ips, node.IPAddress)
		}
	}
	return ips, nil
}

func (m *ComputeDomainManager) UpdateComputeDomainDeployment(ctx context.Context, cdUID string) error {
	cd, err := m.GetComputeDomain(ctx, cdUID)
	if err != nil {
		return fmt.Errorf("error getting ComputeDomain: %w", err)
	}
	if cd == nil {
		return fmt.Errorf("ComputeDomain not found: %s", cdUID)
	}

	d, err := m.GetComputeDomainDeployment(ctx, cdUID)
	if err != nil || d == nil {
		return fmt.Errorf("error getting Deployment for ComputeDomain: %w", err)
	}

	newD := d.DeepCopy()

	if newD.Spec.Template.Spec.Affinity == nil {
		newD.Spec.Template.Spec.Affinity = &corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "kubernetes.io/hostname",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{},
								},
							},
						},
					},
				},
			},
		}
	}

	values := []string{m.config.flags.nodeName}
	for _, value := range newD.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0].Values {
		if value == m.config.flags.nodeName {
			return nil
		}
		values = append(values, value)
	}
	newD.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0].Values = values

	if len(values) == cd.Spec.NumNodes {
		newD.Spec.Replicas = ptr.To(int32(len(values)))
	}

	if _, err := m.config.clientsets.Core.AppsV1().Deployments(newD.Namespace).Update(ctx, newD, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("error updating Deployment for ComputeDomain: %w", err)
	}

	return nil
}

func (m *ComputeDomainManager) GetComputeDomain(ctx context.Context, cdUID string) (*nvapi.ComputeDomain, error) {
	cds, err := m.informer.GetIndexer().ByIndex("computeDomainUID", cdUID)
	if err != nil {
		return nil, fmt.Errorf("error retrieving ComputeDomain by UID: %w", err)
	}
	if len(cds) == 0 {
		return nil, nil
	}
	if len(cds) != 1 {
		return nil, fmt.Errorf("multiple ComputeDomains with the same UID")
	}
	cd, ok := cds[0].(*nvapi.ComputeDomain)
	if !ok {
		return nil, fmt.Errorf("failed to cast to ComputeDomain")
	}
	return cd, nil
}

func (m *ComputeDomainManager) GetComputeDomainDeployment(ctx context.Context, cdUID string) (*appsv1.Deployment, error) {
	labelSelector := &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      computeDomainLabelKey,
				Operator: metav1.LabelSelectorOpIn,
				Values:   []string{cdUID},
			},
		},
	}

	listOptions := metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(labelSelector),
	}

	ds, err := m.config.clientsets.Core.AppsV1().Deployments(m.config.flags.namespace).List(ctx, listOptions)
	if err != nil {
		return nil, fmt.Errorf("error listing Deployments for ComputeDomain: %w", err)
	}
	if len(ds.Items) == 0 {
		return nil, nil
	}
	if len(ds.Items) != 1 {
		return nil, fmt.Errorf("multiple Deployments for ComputeDomain with the same UID")
	}

	return &ds.Items[0], nil
}
