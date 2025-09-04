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
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"k8s.io/klog/v2"
	"k8s.io/mount-utils"

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
	cp "github.com/otiai10/copy"
)

const (
	hostsFilePath = "/etc/hosts"
	dnsNameFormat = "compute-domain-daemon-%d"
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

// UnmountEtcHosts unmounts /etc/hosts and replaces it with a regular file,
// retaining the contents from the mount source. Background: in a container,
// /etc/hosts normally is mounted in and managed by the container runtime (also
// see https://github.com/moby/moby/issues/22281) to take control of name
// resolution in the container. We however want to make sure that we are not
// affected by out-of-band updates to this file (especially after container
// startup, as this might interfere with our management of the file contents).
// The unmount ensures that this file is only updated from within the container,
// by logic that we control. Additionally, replacing the mount with a regular
// file enables atomically flipping the content of /etc/hosts via rename(): this
// system call does not work across a mount (it would fail with `/etc/hosts:
// device or resource busy`, also see
// https://github.com/moby/moby/issues/17190#issuecomment-149438280).
func (m *DNSNameManager) UnmountEtcHosts() error {
	hPath := "/etc/hosts"
	backupPath := "/etc/hosts_init_state"

	// Copy, retaining permissions.
	if err := cp.Copy(hPath, backupPath); err != nil {
		return fmt.Errorf("error copying %s to %s: %w", hPath, backupPath, err)
	}

	// Discover `mount` executable.
	mountExePath, err := exec.LookPath("mount")
	if err != nil {
		return fmt.Errorf("error looking up 'mount' executable: %w", err)
	}
	mounter := mount.New(mountExePath)

	// Trigger unmount. Requires privileged execution.
	if err := mounter.Unmount(hPath); err != nil {
		return fmt.Errorf("error unmounting %s: %w", hPath, err)
	}

	// Restore hosts file with original contents and permissions.
	if err := cp.Copy(backupPath, hPath); err != nil {
		return fmt.Errorf("error copying %s to %s: %w", backupPath, hPath, err)
	}

	// Read and log contents (helpful for debugging, and happens only during
	// startup).
	data, err := os.ReadFile(hPath)
	if err != nil {
		return fmt.Errorf("error reading %s: %w", hPath, err)
	}
	klog.Infof("Unmounted %s, retained initial state:\n%s", hPath, data)
	return nil
}

// UpdateDNSNameMappings updates the /etc/hosts file with any new IP to DNS name mappings.
func (m *DNSNameManager) UpdateDNSNameMappings(nodes []*nvapi.ComputeDomainNode) error {
	m.Lock()
	defer m.Unlock()

	// Make a local copy of the current ipToDNSName mappings
	ipToDNSName := maps.Clone(m.ipToDNSName)

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
	for ip := range ipToDNSName {
		if !currentIPs[ip] {
			delete(ipToDNSName, ip)
		}
	}

	// Add new IPs to map
	for _, node := range cliqueNodes {
		// If IP already has a DNS name, skip it
		if _, exists := ipToDNSName[node.IPAddress]; exists {
			continue
		}

		// Construct the DNS name from the node index
		dnsName, err := m.constructDNSName(node)
		if err != nil {
			return fmt.Errorf("failed to allocate DNS name for IP %s: %w", node.IPAddress, err)
		}

		// Assign the IP -> DNS name mapping
		ipToDNSName[node.IPAddress] = dnsName
	}

	// If the existing ipToDNSName mappings are unchanged, exit early
	if maps.Equal(ipToDNSName, m.ipToDNSName) {
		return nil
	}

	// Otherwise, update the cached ipToDNSName mapping
	m.ipToDNSName = ipToDNSName

	// And updated the hosts file with new mappings
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

	if err := updateFileAtomic(hostsFilePath, []byte(newHostsContent.String())); err != nil {
		return fmt.Errorf("failed hosts file update: %w", err)
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

	klog.Infof("Created static nodes config file with %d DNS names using format '%s'", m.maxNodesPerIMEXDomain, dnsNameFormat)

	return nil
}

// updateFileAtomic overwrites the file at `path` atomically with new contents
// given by `data`. Requires `path` to exist and be a regular file. Strategy: 1)
// create a temporary file in the same directory, 2) copy the permissions from
// the original file, 3) write `data`, and 4) issue rename().
func updateFileAtomic(path string, data []byte) error {
	s, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat on %s failed: %w", path, err)
	}
	if !s.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", path)
	}

	origPerms := s.Mode().Perm()
	origDirPath := filepath.Dir(path)

	// Create new file in the same directory (randomness in name and locked-down
	// perms). Get its path.
	f, err := os.CreateTemp(origDirPath, "tmp_dra-driver-*")
	if err != nil {
		return fmt.Errorf("could not create temp file in %s: %w", origDirPath, err)
	}
	tmpPath := f.Name()

	// Make new file's permissions equivalent to original file's permissions.
	if err := os.Chmod(tmpPath, origPerms); err != nil {
		return fmt.Errorf("failed to chmod %s (%s): %w", tmpPath, origPerms.String(), err)
	}

	// Write data to new file. The permission argument is ignored because the
	// file is known to exist. Note that `os.WriteFile()` itself may issue
	// multiple system calls, and hence is not an atomic operation.
	if err := os.WriteFile(tmpPath, data, origPerms); err != nil {
		return fmt.Errorf("failed to write %s: %w", tmpPath, err)
	}

	// Overwrite original file with the rename() system call which on Linux is
	// atomic (specifically, consumers of the original file path will either see
	// the old or the new version but nothing else).
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to rename %s to %s: %w", tmpPath, path, err)
	}

	klog.V(6).Infof("Updated /etc/hosts:\n%s", data)
	return nil
}
