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
	hostsFilePath = "/etc/hosts"
	dnsNameFormat = "compute-domain-daemon-%d"
)

// DNSNameManager manages the allocation of static DNS names to IP addresses.
type DNSNameManager struct {
	sync.Mutex
	ipToDNSName           map[string]string
	cliqueID              string
	maxNodesPerIMEXDomain int
	nodesConfigPath       string
}

// NewDNSNameManager creates a new DNS name manager.
func NewDNSNameManager(cliqueID string, maxNodesPerIMEXDomain int, nodesConfigPath string) *DNSNameManager {
	return &DNSNameManager{
		ipToDNSName:           make(map[string]string),
		cliqueID:              cliqueID,
		maxNodesPerIMEXDomain: maxNodesPerIMEXDomain,
		nodesConfigPath:       nodesConfigPath,
	}
}

// UpdateDNSNameMappings updates the /etc/hosts file with IP to DNS name mappings.
func (m *DNSNameManager) UpdateDNSNameMappings(nodes []*nvapi.ComputeDomainNode) error {
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

	for ip := range m.ipToDNSName {
		if !currentIPs[ip] {
			delete(m.ipToDNSName, ip)
		}
	}

	// Add new IPs to map (filling in holes where others were removed)
	for _, node := range cliqueNodes {
		// If IP already has a DNS name, skip it
		if _, exists := m.ipToDNSName[node.IPAddress]; exists {
			continue
		}

		dnsName, err := m.allocateDNSName(node.IPAddress)
		if err != nil {
			return fmt.Errorf("failed to allocate DNS name for IP %s: %w", node.IPAddress, err)
		}
		m.ipToDNSName[node.IPAddress] = dnsName
	}

	// Update the hosts file with current mappings
	return m.updateHostsFile()
}

// LogDNSNameMappings logs the current compute-domain-daemon mappings from memory.
func (m *DNSNameManager) LogDNSNameMappings() {
	m.Lock()
	defer m.Unlock()

	if len(m.ipToDNSName) == 0 {
		klog.Infof("No compute-domain-daemon mappings found")
		return
	}

	klog.Infof("Current compute-domain-daemon mappings:")
	for ip, dnsName := range m.ipToDNSName {
		klog.Infof("  %s -> %s", ip, dnsName)
	}
}

// allocateDNSName allocates a DNS name for an IP address, reusing existing DNS names if possible.
func (m *DNSNameManager) allocateDNSName(ip string) (string, error) {
	// If IP already has a DNS name, return it
	if dnsName, exists := m.ipToDNSName[ip]; exists {
		return dnsName, nil
	}

	// Find the next available DNS name
	for i := 0; i < m.maxNodesPerIMEXDomain; i++ {
		dnsName := fmt.Sprintf(dnsNameFormat, i)
		// Check if this DNS name is already in use
		inUse := false
		for _, existingDNSName := range m.ipToDNSName {
			if existingDNSName == dnsName {
				inUse = true
				break
			}
		}
		if !inUse {
			m.ipToDNSName[ip] = dnsName
			return dnsName, nil
		}
	}

	// If all DNS names are used, return an error
	return "", fmt.Errorf("no DNS names available (max: %d)", m.maxNodesPerIMEXDomain)
}

// updateHostsFile updates the /etc/hosts file with current IP to DNS name mappings.
func (m *DNSNameManager) updateHostsFile() error {
	// Read hosts file
	hostsContent, err := os.ReadFile(hostsFilePath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", hostsFilePath, err)
	}

	// Grab any lines to preserve, skipping existing DNS name mappings
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

	// Add new DNS name mappings
	for ip, dnsName := range m.ipToDNSName {
		newHostsContent.WriteString(fmt.Sprintf("%s\t%s\n", ip, dnsName))
	}

	// Write the updated hosts file
	if err := os.WriteFile(hostsFilePath, []byte(newHostsContent.String()), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", hostsFilePath, err)
	}

	return nil
}

// WriteNodesConfig creates a static nodes config file with DNS names.
func (m *DNSNameManager) WriteNodesConfig() error {
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

	// Write static DNS names
	for i := 0; i < m.maxNodesPerIMEXDomain; i++ {
		dnsName := fmt.Sprintf(dnsNameFormat, i)
		if _, err := fmt.Fprintf(f, "%s\n", dnsName); err != nil {
			return fmt.Errorf("failed to write to nodes config file: %w", err)
		}
	}

	klog.Infof("Created static nodes config file with %d DNS names using format %s", m.maxNodesPerIMEXDomain, dnsNameFormat)

	return nil
}
