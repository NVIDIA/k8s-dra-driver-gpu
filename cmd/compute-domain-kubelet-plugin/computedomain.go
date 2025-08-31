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
	cleanupInterval      = 10 * time.Minute

	ComputeDomainDaemonConfigFilesDirName = "domains"
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

func NewComputeDomainManager(config *Config, cliqueID string) *ComputeDomainManager {
	factory := nvinformers.NewSharedInformerFactory(config.clientsets.Nvidia, informerResyncPeriod)
	informer := factory.Resource().V1beta1().ComputeDomains().Informer()
	configFilesRoot := filepath.Join(config.DriverPluginPath(), ComputeDomainDaemonConfigFilesDirName)

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

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.periodicCleanup(ctx)
	}()

	if !cache.WaitForCacheSync(ctx.Done(), m.informer.HasSynced) {
		return fmt.Errorf("informer cache sync for ComputeDomains failed")
	}

	return nil
}

func (m *ComputeDomainManager) Stop() error {
	if m.cancelContext != nil {
		m.cancelContext()
	}
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

func (m *ComputeDomainManager) GetComputeDomainChannelContainerEdits(devRoot string, info *nvcapDeviceInfo) *cdiapi.ContainerEdits {
	return &cdiapi.ContainerEdits{
		ContainerEdits: &cdispec.ContainerEdits{
			DeviceNodes: []*cdispec.DeviceNode{
				{
					Path:     info.path,
					Type:     "c",
					FileMode: ptr.To(os.FileMode(info.mode)),
					Major:    int64(info.major),
					Minor:    int64(info.minor),
				},
			},
		},
	}
}

func (s *ComputeDomainDaemonSettings) GetDomain() string {
	return s.domain
}

func (s *ComputeDomainDaemonSettings) GetCDIContainerEdits(ctx context.Context, devRoot string, info *nvcapDeviceInfo) (*cdiapi.ContainerEdits, error) {
	cd, err := s.manager.GetComputeDomain(ctx, s.domain)
	if err != nil {
		return nil, fmt.Errorf("error getting compute domain: %w", err)
	}
	if cd == nil {
		return nil, fmt.Errorf("compute domain not found: %s", s.domain)
	}

	edits := &cdiapi.ContainerEdits{
		ContainerEdits: &cdispec.ContainerEdits{
			Env: []string{
				fmt.Sprintf("CLIQUE_ID=%s", s.manager.cliqueID),
				fmt.Sprintf("COMPUTE_DOMAIN_UUID=%s", cd.UID),
				fmt.Sprintf("COMPUTE_DOMAIN_NAME=%s", cd.Name),
				fmt.Sprintf("COMPUTE_DOMAIN_NAMESPACE=%s", cd.Namespace),
			},
			Mounts: []*cdispec.Mount{
				{
					ContainerPath: "/etc/nvidia-imex",
					HostPath:      s.rootDir,
					Options:       []string{"rw", "nosuid", "nodev", "bind"},
				},
			},
			DeviceNodes: []*cdispec.DeviceNode{
				{
					Path:     info.path,
					Type:     "c",
					FileMode: ptr.To(os.FileMode(info.mode)),
					Major:    int64(info.major),
					Minor:    int64(info.minor),
				},
			},
		},
	}

	return edits, nil
}

func (s *ComputeDomainDaemonSettings) Prepare(ctx context.Context) error {
	if err := os.MkdirAll(s.rootDir, 0755); err != nil {
		return fmt.Errorf("error creating directory %v: %w", s.rootDir, err)
	}

	if err := s.WriteConfigFile(ctx); err != nil {
		return fmt.Errorf("error writing config file %v: %w", s.configPath, err)
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

func (m *ComputeDomainManager) AssertComputeDomainReady(ctx context.Context, cdUID string) error {
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

func (m *ComputeDomainManager) AssertComputeDomainNamespace(ctx context.Context, claimNamespace, cdUID string) error {
	cd, err := m.GetComputeDomain(ctx, cdUID)
	if err != nil {
		return fmt.Errorf("error getting ComputeDomain: %w", err)
	}
	if cd == nil {
		return fmt.Errorf("ComputeDomain not found: %s", cdUID)
	}

	if cd.Namespace != claimNamespace {
		return fmt.Errorf("the ResourceClaim's namespace is different than the ComputeDomain's namespace")
	}

	return nil
}

func (m *ComputeDomainManager) AddNodeLabel(ctx context.Context, cdUID string) error {
	node, err := m.config.clientsets.Core.CoreV1().Nodes().Get(ctx, m.config.flags.nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error retrieving Node: %w", err)
	}

	currentValue, exists := node.Labels[computeDomainLabelKey]
	if exists && currentValue != cdUID {
		return fmt.Errorf("label already exists for a different ComputeDomain")
	}

	if exists && currentValue == cdUID {
		return nil
	}

	newNode := node.DeepCopy()
	if newNode.Labels == nil {
		newNode.Labels = make(map[string]string)
	}
	newNode.Labels[computeDomainLabelKey] = cdUID

	if _, err = m.config.clientsets.Core.CoreV1().Nodes().Update(ctx, newNode, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("error updating Node with label: %w", err)
	}

	return nil
}

func (m *ComputeDomainManager) RemoveNodeLabel(ctx context.Context, cdUID string) error {
	node, err := m.config.clientsets.Core.CoreV1().Nodes().Get(ctx, m.config.flags.nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error retrieving Node: %w", err)
	}

	if _, exists := node.Labels[computeDomainLabelKey]; !exists {
		return nil
	}

	if node.Labels[computeDomainLabelKey] != cdUID {
		return nil
	}

	newNode := node.DeepCopy()
	delete(newNode.Labels, computeDomainLabelKey)

	if _, err := m.config.clientsets.Core.CoreV1().Nodes().Update(ctx, newNode, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("error updating Node to remove label: %w", err)
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

func (m *ComputeDomainManager) periodicCleanup(ctx context.Context) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			klog.V(6).Infof("Running periodic sync to remove artifacts owned by stale ComputeDomain")

			_, err := os.Stat(m.configFilesRoot)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				klog.Errorf("error checking for existence of directory '%s': %v", m.configFilesRoot, err)
				continue
			}

			entries, err := os.ReadDir(m.configFilesRoot)
			if err != nil {
				klog.Errorf("error reading entries under directory '%s': %v", m.configFilesRoot, err)
				continue
			}

			for _, e := range entries {
				if !e.IsDir() {
					continue
				}

				uid := e.Name()
				path := filepath.Join(m.configFilesRoot, e.Name())

				computeDomain, err := m.GetComputeDomain(ctx, uid)
				if err != nil {
					klog.Errorf("error getting ComputeDomain: %v", err)
					continue
				}

				if computeDomain != nil {
					continue
				}

				klog.Infof("Stale artifacts found for ComputeDomain '%s', running cleanup", uid)

				if err := os.RemoveAll(path); err != nil {
					klog.Errorf("error removing artifacts directory for ComputeDomain '%s': %v", uid, err)
					continue
				}

				if err := m.RemoveNodeLabel(ctx, uid); err != nil {
					klog.Errorf("error removing Node label for ComputeDomain '%s': %v", uid, err)
					continue
				}
			}
		case <-ctx.Done():
			return
		}
	}
}
