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

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	cdiapi "tags.cncf.io/container-device-interface/pkg/cdi"
	cdispec "tags.cncf.io/container-device-interface/specs-go"

	nvapi "github.com/NVIDIA/k8s-dra-driver/api/nvidia.com/resource/v1beta1"
	nvinformers "github.com/NVIDIA/k8s-dra-driver/pkg/nvidia.com/informers/externalversions"
)

const (
	informerResyncPeriod = 10 * time.Minute

	ImexDaemonSettingsRoot       = DriverPluginPath + "/imex"
	ImexDaemonConfigTemplatePath = "/templates/imex-daemon-config.tmpl.cfg"
)

type ImexManager struct {
	config        *Config
	waitGroup     sync.WaitGroup
	cancelContext context.CancelFunc

	factory  nvinformers.SharedInformerFactory
	informer cache.SharedIndexInformer

	configFilesRoot string
	cliqueID        string
}

type ImexDaemonSettings struct {
	manager         *ImexManager
	domain          string
	rootDir         string
	configPath      string
	nodesConfigPath string
}

func NewImexManager(config *Config, configFilesRoot, cliqueID string) *ImexManager {
	factory := nvinformers.NewSharedInformerFactory(config.clientsets.Nvidia, informerResyncPeriod)
	informer := factory.Resource().V1beta1().ComputeDomains().Informer()

	m := &ImexManager{
		config:          config,
		factory:         factory,
		informer:        informer,
		configFilesRoot: configFilesRoot,
		cliqueID:        cliqueID,
	}

	return m
}

func (m *ImexManager) Start(ctx context.Context) (rerr error) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	defer func() {
		if rerr != nil {
			if err := m.Stop(); err != nil {
				klog.Errorf("error stopping ImexDaemonSettings manager: %v", err)
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

func (m *ImexManager) Stop() error {
	m.cancelContext()
	m.waitGroup.Wait()
	return nil
}

func (m *ImexManager) NewSettings(domain string) *ImexDaemonSettings {
	return &ImexDaemonSettings{
		manager:         m,
		domain:          domain,
		rootDir:         fmt.Sprintf("%s/%s", m.configFilesRoot, domain),
		configPath:      fmt.Sprintf("%s/%s/%s", m.configFilesRoot, domain, "config.cfg"),
		nodesConfigPath: fmt.Sprintf("%s/%s/%s", m.configFilesRoot, domain, "nodes_config.cfg"),
	}
}

func (m *ImexManager) GetImexChannelContainerEdits(devRoot string, info *ImexChannelInfo) *cdiapi.ContainerEdits {
	channelPath := fmt.Sprintf("/dev/nvidia-caps-imex-channels/channel%d", info.Channel)

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

func (s *ImexDaemonSettings) GetDomain() string {
	return s.domain
}

func (s *ImexDaemonSettings) GetCDIContainerEdits() *cdiapi.ContainerEdits {
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

func (s *ImexDaemonSettings) Prepare(ctx context.Context) error {
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

func (s *ImexDaemonSettings) Unprepare(ctx context.Context) error {
	if err := os.RemoveAll(s.rootDir); err != nil {
		return fmt.Errorf("error removing directory %v: %w", s.rootDir, err)
	}
	return nil
}

func (s *ImexDaemonSettings) WriteConfigFile(ctx context.Context) error {
	configTemplateData := struct{}{}

	tmpl, err := template.ParseFiles(ImexDaemonConfigTemplatePath)
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

func (s *ImexDaemonSettings) WriteNodesConfigFile(ctx context.Context) error {
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

func (m *ImexManager) GetNodeIPs(ctx context.Context, cdUID string) ([]string, error) {
	// TODO: Move away from a retry solution and instead register a callback
	// and react immediately when the desired ComputeDomain has its
	// Status.Nodes field populated.
	backoff := wait.Backoff{
		Duration: time.Microsecond, // Initial delay
		Factor:   3,                // Factor to multiply duration each iteration
		Jitter:   0,                // Jitter factor for randomness
		Steps:    16,               // Maximum number of steps
		Cap:      45 * time.Second, // Maximum backoff duration
	}

	var nodes []*nvapi.ComputeDomainNode
	err := wait.ExponentialBackoff(backoff, func() (bool, error) {
		cd, err := m.GetComputeDomain(ctx, cdUID)
		if err != nil {
			return false, fmt.Errorf("error getting ComputeDomain: %w", err)
		}
		if cd == nil || cd.Status.Nodes == nil {
			return false, nil
		}
		nodes = cd.Status.Nodes
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("error getting status of nodes in ComputeDomain: %w", err)
	}

	var ips []string
	for _, node := range nodes {
		if m.cliqueID == node.CliqueID {
			ips = append(ips, node.IPAddress)
		}
	}
	return ips, nil
}

func (m *ImexManager) GetComputeDomain(ctx context.Context, cdUID string) (*nvapi.ComputeDomain, error) {
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
