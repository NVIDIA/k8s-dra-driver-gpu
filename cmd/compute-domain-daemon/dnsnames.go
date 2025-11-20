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
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"k8s.io/klog/v2"

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
)

const (
	hostsFilePath = "/etc/hosts"
	dnsNamePrefix = "compute-domain-daemon-"
	dnsNameFormat = dnsNamePrefix + "%d"
)

// IPToDNSNameMap holds a map of IP Addresses to DNS names.
type IPToDNSNameMap map[string]string

// DNSNameManager manages the allocation of static DNS names to IP addresses.
type DNSNameManager struct {
	sync.Mutex
	ipToDNSName           IPToDNSNameMap
	cliqueID              string
	maxNodesPerIMEXDomain int
	nodesConfigPath       string
}

// NewDNSNameManager creates a new DNS name manager.
func NewDNSNameManager(cliqueID string, maxNodesPerIMEXDomain int, nodesConfigPath string) *DNSNameManager {
	return &DNSNameManager{
		ipToDNSName:           make(IPToDNSNameMap),
		cliqueID:              cliqueID,
		maxNodesPerIMEXDomain: maxNodesPerIMEXDomain,
		nodesConfigPath:       nodesConfigPath,
	}
}

// UpdateDNSNameMappings updates the /etc/hosts file with any new IP to DNS name
// mappings. The boolean return value indicates whether the hosts file was
// updated or not (it must be ignored when the returned error is non-nil).
func (m *DNSNameManager) UpdateDNSNameMappings(nodes []*nvapi.ComputeDomainNode) (bool, error) {
	m.Lock()
	defer m.Unlock()

	// Make a local ipToDNSName mappings
	ipToDNSName := make(IPToDNSNameMap)

	// Prefilter nodes to only consider those with the matching cliqueID
	var cliqueNodes []*nvapi.ComputeDomainNode
	for _, node := range nodes {
		if node.CliqueID == m.cliqueID {
			cliqueNodes = append(cliqueNodes, node)
		}
	}

	// Add IPs to map
	for _, node := range cliqueNodes {
		// Construct the DNS name from the node index
		dnsName, err := m.constructDNSName(node)
		if err != nil {
			return false, fmt.Errorf("failed to allocate DNS name for IP %s: %w", node.IPAddress, err)
		}

		// Assign the IP -> DNS name mapping
		ipToDNSName[node.IPAddress] = dnsName
	}

	// If the existing ipToDNSName mappings are unchanged, exit early
	if maps.Equal(ipToDNSName, m.ipToDNSName) {
		return false, nil
	}

	// Otherwise, update the cached ipToDNSName mapping
	m.ipToDNSName = ipToDNSName

	// And update the hosts file with the new mapping
	return true, m.updateHostsFile()
}

// LogDNSNameMappings logs the current compute-domain-daemon mappings from memory.
func (m *DNSNameManager) LogDNSNameMappings() {
	m.Lock()
	defer m.Unlock()

	if len(m.ipToDNSName) == 0 {
		klog.Infof("Current compute-domain-daemon mappings: empty")
		return
	}

	klog.Infof("Current compute-domain-daemon mappings:")

	// Sort alphabetically by DNS name. Right-justify DNS names by adding
	// whitespace to the left.
	for _, ip := range slices.Sorted(maps.Keys(m.ipToDNSName)) {
		dnsname := m.ipToDNSName[ip]
		klog.Infof("%26s -> %s", dnsname, ip)
	}
}

// contructDNSName constructs a DNS name for a node based on its index field.
// Returns an error if the index is invalid or exceeds maxNodesPerIMEXDomain.
func (m *DNSNameManager) constructDNSName(node *nvapi.ComputeDomainNode) (string, error) {
	if node.Index < 0 {
		return "", fmt.Errorf("node %s has invalid index %d", node.Name, node.Index)
	}
	if node.Index >= m.maxNodesPerIMEXDomain {
		return "", fmt.Errorf("node %s has invalid index %d, must be less than %d", node.Name, node.Index, m.maxNodesPerIMEXDomain)
	}
	dnsName := fmt.Sprintf(dnsNameFormat, node.Index)
	return dnsName, nil
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

		// Skip existing compute-domain-daemon mappings
		if strings.Contains(line, dnsNamePrefix) {
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
