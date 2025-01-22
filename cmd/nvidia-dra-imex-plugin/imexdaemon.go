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
	"text/template"

	cdiapi "tags.cncf.io/container-device-interface/pkg/cdi"
	cdispec "tags.cncf.io/container-device-interface/specs-go"
)

const (
	ImexDaemonSettingsRoot       = DriverPluginPath + "/imex"
	ImexDaemonConfigTemplatePath = "/templates/imex-daemon-config.tmpl.cfg"
)

type ImexDaemonSettingsManager struct {
	config          *Config
	configFilesRoot string
}

type ImexDaemonSettings struct {
	manager         *ImexDaemonSettingsManager
	domain          string
	rootDir         string
	configPath      string
	nodesConfigPath string
}

func NewImexDaemonSettingsManager(config *Config, configFilesRoot string) *ImexDaemonSettingsManager {
	return &ImexDaemonSettingsManager{
		config:          config,
		configFilesRoot: configFilesRoot,
	}
}

func (m *ImexDaemonSettingsManager) NewSettings(domain string) *ImexDaemonSettings {
	return &ImexDaemonSettings{
		manager:         m,
		domain:          domain,
		rootDir:         fmt.Sprintf("%s/%s", m.configFilesRoot, domain),
		configPath:      fmt.Sprintf("%s/%s/%s", m.configFilesRoot, domain, "config.cfg"),
		nodesConfigPath: fmt.Sprintf("%s/%s/%s", m.configFilesRoot, domain, "nodes_config.cfg"),
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
	nodeIPs, err := s.GetNodeIPs(ctx)
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

func (s *ImexDaemonSettings) GetNodeIPs(ctx context.Context) ([]string, error) {
	ips := []string{"10.136.206.41", "10.136.206.42", "10.136.206.43", "10.136.206.44"}
	return ips, nil
}
