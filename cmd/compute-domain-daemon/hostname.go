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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"k8s.io/klog/v2"

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
)

const (
	maxHostnames   = 18
	hostsFilePath  = "/etc/hosts"
	hostnameFormat = "compute-domain-daemon-%d"
)

// HostnameManager manages the allocation of static hostnames to IP addresses.
type HostnameManager struct {
	sync.Mutex
	ipToHostname    map[string]string
	cliqueID        string
	nodesConfigPath string
}

// NewHostnameManager creates a new hostname manager.
func NewHostnameManager(cliqueID string, nodesConfigPath string) *HostnameManager {
	return &HostnameManager{
		ipToHostname:    make(map[string]string),
		cliqueID:        cliqueID,
		nodesConfigPath: nodesConfigPath,
	}
}

// UpdateHostnameMappings updates the /etc/hosts file with IP to hostname mappings.
func (m *HostnameManager) UpdateHostnameMappings(nodes []*nvapi.ComputeDomainNode) error {
	m.Lock()
	defer m.Unlock()

	// Prefilter nodes to only consider those with the matching cliqueID
	var cliqueNodes []*nvapi.ComputeDomainNode
	for _, node := range nodes {
		if node.CliqueID == m.cliqueID {
			cliqueNodes = append(cliqueNodes, node)
		}
	}

	// Find and remove stale IPs from map
	currentIPs := make(map[string]bool)
	for _, node := range cliqueNodes {
		currentIPs[node.IPAddress] = true
	}

	for ip := range m.ipToHostname {
		if !currentIPs[ip] {
			delete(m.ipToHostname, ip)
		}
	}

	// Add new IPs to map (filling in holes where others were removed)
	for _, node := range cliqueNodes {
		// If IP already has a hostname, skip it
		if _, exists := m.ipToHostname[node.IPAddress]; exists {
			continue
		}

		hostname, err := m.allocateHostname(node.IPAddress)
		if err != nil {
			return fmt.Errorf("failed to allocate hostname for IP %s: %w", node.IPAddress, err)
		}
		m.ipToHostname[node.IPAddress] = hostname
	}

	// Update the hosts file with current mappings
	return m.updateHostsFile()
}

// LogHostnameMappings logs the current compute-domain-daemon mappings from memory.
func (m *HostnameManager) LogHostnameMappings() {
	m.Lock()
	defer m.Unlock()

	if len(m.ipToHostname) == 0 {
		klog.Infof("No compute-domain-daemon mappings found")
		return
	}

	klog.Infof("Current compute-domain-daemon mappings:")
	for ip, hostname := range m.ipToHostname {
		klog.Infof("  %s -> %s", ip, hostname)
	}
}

// allocateHostname allocates a hostname for an IP address, reusing existing hostnames if possible.
func (m *HostnameManager) allocateHostname(ip string) (string, error) {
	// If IP already has a hostname, return it
	if hostname, exists := m.ipToHostname[ip]; exists {
		return hostname, nil
	}

	// Find the next available hostname
	for i := 0; i < maxHostnames; i++ {
		hostname := fmt.Sprintf(hostnameFormat, i)
		// Check if this hostname is already in use
		inUse := false
		for _, existingHostname := range m.ipToHostname {
			if existingHostname == hostname {
				inUse = true
				break
			}
		}
		if !inUse {
			m.ipToHostname[ip] = hostname
			return hostname, nil
		}
	}

	// If all hostnames are used, return an error
	return "", fmt.Errorf("no hostnames available (max: %d)", maxHostnames)
}

// updateHostsFile updates the /etc/hosts file with current IP to hostname mappings.
func (m *HostnameManager) updateHostsFile() error {
	// Read hosts file
	hostsContent, err := os.ReadFile(hostsFilePath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", hostsFilePath, err)
	}

	// Grab any lines to preserve, skipping existing hostname mappings
	var preservedLines []string
	for _, line := range strings.Split(string(hostsContent), "\n") {
		line = strings.TrimSpace(line)

		// Keep empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			preservedLines = append(preservedLines, line)
			continue
		}

		// Skip existing compute-domain-daemon mappings
		if strings.Contains(line, "compute-domain-daemon-") {
			continue
		}

		// Keep all other lines
		preservedLines = append(preservedLines, line)
	}

	// Add preserved lines
	var newHostsContent strings.Builder
	for _, line := range preservedLines {
		newHostsContent.WriteString(line)
		newHostsContent.WriteString("\n")
	}

	// Add a separator comment
	newHostsContent.WriteString("# Compute Domain Daemon mappings\n")

	// Add new hostname mappings
	for ip, hostname := range m.ipToHostname {
		newHostsContent.WriteString(fmt.Sprintf("%s\t%s\n", ip, hostname))
	}

	// Write the updated hosts file
	if err := os.WriteFile(hostsFilePath, []byte(newHostsContent.String()), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", hostsFilePath, err)
	}

	return nil
}

// WriteNodesConfig creates a static nodes config file with hostnames.
func (m *HostnameManager) WriteNodesConfig() error {
	// Ensure the directory exists
	dir := filepath.Dir(m.nodesConfigPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Create or overwrite the nodesConfig file
	f, err := os.Create(m.nodesConfigPath)
	if err != nil {
		return fmt.Errorf("failed to create nodes config file: %w", err)
	}
	defer f.Close()

	// Write static hostnames
	for i := 0; i < maxHostnames; i++ {
		hostname := fmt.Sprintf(hostnameFormat, i)
		if _, err := fmt.Fprintf(f, "%s\n", hostname); err != nil {
			return fmt.Errorf("failed to write to nodes config file: %w", err)
		}
	}

	klog.Infof("Created static nodes config file with %d hostnames using format %s", maxHostnames, hostnameFormat)
	return nil
}
