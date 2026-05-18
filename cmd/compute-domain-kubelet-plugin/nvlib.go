/*
 * Copyright (c) 2021, NVIDIA CORPORATION.  All rights reserved.
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
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"k8s.io/klog/v2"
	"k8s.io/mount-utils"

	"github.com/google/uuid"

	nvdev "github.com/NVIDIA/go-nvlib/pkg/nvlib/device"
	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

const (
	procDevicesPath                  = "/proc/devices"
	procDriverNvidiaPath             = "/proc/driver/nvidia"
	nvidiaCapsDeviceName             = "nvidia-caps"
	nvidiaCapsImexChannelsDeviceName = "nvidia-caps-imex-channels"
	nvidiaCapFabricImexMgmtPath      = "/proc/driver/nvidia/capabilities/fabric-imex-mgmt"
	hostDevContainerPath             = "/host/dev"
)

type deviceLib struct {
	nvdev.Interface
	nvmllib           nvml.Interface
	driverLibraryPath string
	devRoot           string
	nvidiaSMIPath     string
}

type nvcapDeviceInfo struct {
	major  int
	minor  int
	mode   int
	modify int
	path   string
}

func newDeviceLib(driverRoot root) (*deviceLib, error) {
	driverLibraryPath, err := driverRoot.getDriverLibraryPath()
	if err != nil {
		return nil, fmt.Errorf("failed to locate driver libraries: %w", err)
	}

	nvidiaSMIPath, err := driverRoot.getNvidiaSMIPath()
	if err != nil {
		return nil, fmt.Errorf("failed to locate nvidia-smi: %w", err)
	}

	// We construct an NVML library specifying the path to libnvidia-ml.so.1
	// explicitly so that we don't have to rely on the library path.
	nvmllib := nvml.New(
		nvml.WithLibraryPath(driverLibraryPath),
	)
	d := deviceLib{
		Interface:         nvdev.New(nvmllib),
		nvmllib:           nvmllib,
		driverLibraryPath: driverLibraryPath,
		devRoot:           driverRoot.getDevRoot(),
		nvidiaSMIPath:     nvidiaSMIPath,
	}

	if err := d.unmountRecursively(procDriverNvidiaPath); err != nil {
		return nil, fmt.Errorf("error recursively unmounting %s: %w", procDriverNvidiaPath, err)
	}

	if err := d.conditionallyBindMountHostDev(); err != nil {
		return nil, fmt.Errorf("error conditionally bind mounting host dev: %w", err)
	}

	return &d, nil
}

func (l deviceLib) init() error {
	ret := l.nvmllib.Init()
	if ret != nvml.SUCCESS {
		return fmt.Errorf("error initializing NVML: %v", ret)
	}
	return nil
}

func (l deviceLib) alwaysShutdown() {
	ret := l.nvmllib.Shutdown()
	if ret != nvml.SUCCESS {
		klog.Warningf("error shutting down NVML: %v", ret)
	}
}

func (l deviceLib) enumerateAllPossibleDevices(config *Config) (AllocatableDevices, error) {
	alldevices := make(AllocatableDevices)

	computeDomainChannels, err := l.enumerateComputeDomainChannels(config)
	if err != nil {
		return nil, fmt.Errorf("error enumerating ComputeDomain channel devices: %w", err)
	}
	for k, v := range computeDomainChannels {
		alldevices[k] = v
	}

	computeDomainDaemons, err := l.enumerateComputeDomainDaemons(config)
	if err != nil {
		return nil, fmt.Errorf("error enumerating ComputeDomain daemon devices: %w", err)
	}
	for k, v := range computeDomainDaemons {
		alldevices[k] = v
	}

	return alldevices, nil
}

func (l deviceLib) enumerateComputeDomainChannels(config *Config) (AllocatableDevices, error) {
	devices := make(AllocatableDevices)

	imexChannelCount, err := l.getImexChannelCount()
	if err != nil {
		return nil, fmt.Errorf("error getting IMEX channel count: %w", err)
	}
	for i := 0; i < imexChannelCount; i++ {
		computeDomainChannelInfo := &ComputeDomainChannelInfo{
			ID: i,
		}
		deviceInfo := &AllocatableDevice{
			Channel: computeDomainChannelInfo,
		}
		devices[computeDomainChannelInfo.CanonicalName()] = deviceInfo
	}

	return devices, nil
}

func (l deviceLib) enumerateComputeDomainDaemons(config *Config) (AllocatableDevices, error) {
	devices := make(AllocatableDevices)
	computeDomainDaemonInfo := &ComputeDomainDaemonInfo{
		ID: 0,
	}
	deviceInfo := &AllocatableDevice{
		Daemon: computeDomainDaemonInfo,
	}
	devices[computeDomainDaemonInfo.CanonicalName()] = deviceInfo
	return devices, nil
}

func (l deviceLib) getCliqueID() (string, error) {
	if err := l.init(); err != nil {
		return "", fmt.Errorf("error initializing deviceLib: %w", err)
	}
	defer l.alwaysShutdown()

	uniqueClusterUUIDs := make(map[string]struct{})
	uniqueCliqueIDs := make(map[string]struct{})

	err := l.VisitDevices(func(i int, d nvdev.Device) error {
		isFabricAttached, err := d.IsFabricAttached()
		if err != nil {
			return fmt.Errorf("error checking if device is fabric attached: %w", err)
		}
		if !isFabricAttached {
			return nil
		}

		info, ret := d.GetGpuFabricInfo()
		if ret != nvml.SUCCESS {
			return fmt.Errorf("failed to get GPU fabric info: %w", ret)
		}

		clusterUUID, err := uuid.FromBytes(info.ClusterUuid[:])
		if err != nil {
			return fmt.Errorf("invalid cluster UUID: %w", err)
		}

		cliqueID := fmt.Sprintf("%d", info.CliqueId)

		uniqueClusterUUIDs[clusterUUID.String()] = struct{}{}
		uniqueCliqueIDs[cliqueID] = struct{}{}

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("error getting fabric information from one or more devices: %w", err)
	}

	if len(uniqueClusterUUIDs) == 0 && len(uniqueCliqueIDs) == 0 {
		return "", nil
	}

	if len(uniqueClusterUUIDs) != 1 {
		return "", fmt.Errorf("unexpected number of unique ClusterUUIDs found on devices")
	}

	if len(uniqueCliqueIDs) != 1 {
		return "", fmt.Errorf("unexpected number of unique CliqueIDs found on devices")
	}

	for clusterUUID := range uniqueClusterUUIDs {
		for cliqueID := range uniqueCliqueIDs {
			return fmt.Sprintf("%s.%s", clusterUUID, cliqueID), nil
		}
	}

	return "", fmt.Errorf("unexpected return")
}

func (l deviceLib) getImexChannelCount() (int, error) {
	// TODO: Pull this value from /proc/driver/nvidia/params
	return 2048, nil
}

// getDeviceMajor searches for one "<integer> <name>" occurrence in the
// "Character devices" section of the /proc/devices file, and returns the
// integer.
func (l deviceLib) getDeviceMajor(name string) (int, error) {

	re := regexp.MustCompile(
		// The `(?s)` flag makes `.` match newlines. The greedy modifier in
		// `.*?` ensures to pick the first match after "Character devices".
		// Extract the number as capture group (the first and only group).
		"(?s)Character devices:.*?" +
			"([0-9]+) " + regexp.QuoteMeta(name) +
			// Require `name` to be newline-terminated (to not match on a device
			// that has `name` as prefix).
			"\n.*Block devices:",
	)

	data, err := os.ReadFile(procDevicesPath)
	if err != nil {
		return -1, fmt.Errorf("error reading '%s': %w", procDevicesPath, err)
	}

	// Expect precisely one match: first element is the total match, second
	// element corresponds to first capture group within that match (i.e., the
	// number of interest).
	matches := re.FindStringSubmatch(string(data))
	if len(matches) != 2 {
		return -1, fmt.Errorf("error parsing '%s': unexpected regex match: %v", procDevicesPath, matches)
	}

	// Convert capture group content to integer. Perform upper bound check:
	// value must fit into 32-bit integer (it's then also guaranteed to fit into
	// a 32-bit unsigned integer, which is the type that must be passed to
	// unix.Mkdev()).
	major, err := strconv.ParseInt(matches[1], 10, 32)
	if err != nil {
		return -1, fmt.Errorf("int conversion failed for '%v': %w", matches[1], err)
	}

	// ParseInt() always returns an integer of explicit type `int64`. We have
	// performed an upper bound check so it's safe to convert this to `int`
	// (which is documented as "int is a signed integer type that is at least 32
	// bits in size", so in theory it could be smaller than int64).
	return int(major), nil
}

func (l deviceLib) parseNVCapDeviceInfo(nvcapsFilePath string) (*nvcapDeviceInfo, error) {
	file, err := os.Open(nvcapsFilePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info := &nvcapDeviceInfo{}

	major, err := l.getDeviceMajor(nvidiaCapsDeviceName)
	if err != nil {
		return nil, fmt.Errorf("error getting device major: %w", err)
	}
	info.major = major

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "DeviceFileMinor":
			_, _ = fmt.Sscanf(value, "%d", &info.minor)
		case "DeviceFileMode":
			_, _ = fmt.Sscanf(value, "%d", &info.mode)
		case "DeviceFileModify":
			_, _ = fmt.Sscanf(value, "%d", &info.modify)
		}
	}
	info.path = fmt.Sprintf("/dev/nvidia-caps/nvidia-cap%d", info.minor)

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return info, nil
}

func (l deviceLib) createComputeDomainChannelDevice(channel int) error {
	// Construct the properties of the device node to create.
	path := fmt.Sprintf("/dev/nvidia-caps-imex-channels/channel%d", channel)
	path = filepath.Join(l.devRoot, path)
	mode := uint32(unix.S_IFCHR | 0666)

	// Get the IMEX channel major and build a /dev device from it
	major, err := l.getDeviceMajor(nvidiaCapsImexChannelsDeviceName)
	if err != nil {
		return fmt.Errorf("error getting IMEX channel major: %w", err)
	}
	dev := unix.Mkdev(uint32(major), uint32(channel))

	// Recursively create any parent directories of the channel.
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("error creating directory for IMEX channel device nodes: %w", err)
	}

	// Remove the channel if it already exists.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("error removing existing IMEX channel device node: %w", err)
	}

	// Create the device node using syscall.Mknod
	if err := unix.Mknod(path, mode, int(dev)); err != nil {
		return fmt.Errorf("mknod of IMEX channel failed: %w", err)
	}

	return nil
}

func (l deviceLib) createNvCapDevice(nvcapFilePath string) error {
	// Get the nvcapDeviceInfo for the nvcap file.
	deviceInfo, err := l.parseNVCapDeviceInfo(nvcapFilePath)
	if err != nil {
		return fmt.Errorf("error parsing nvcap file for fabric-imex-mgmt: %w", err)
	}

	// Construct the necessary information to create the device node
	path := filepath.Join(l.devRoot, deviceInfo.path)
	mode := unix.S_IFCHR | uint32(deviceInfo.mode)
	dev := unix.Mkdev(uint32(deviceInfo.major), uint32(deviceInfo.minor))

	// Recursively create any parent directories of the device.
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("error creating directory for nvcaps device nodes: %w", err)
	}

	// Remove the device if it already exists.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("error removing existing nvcap device node: %w", err)
	}

	// Create the device node using syscall.Mknod
	if err := unix.Mknod(path, mode, int(dev)); err != nil {
		return fmt.Errorf("mknod of nvcap device failed: %w", err)
	}

	return nil
}

func (l deviceLib) unmountRecursively(root string) error {
	// Get a reference to the mount executable.
	mountExecutable, err := exec.LookPath("mount")
	if err != nil {
		return fmt.Errorf("error looking up mpunt executable: %w", err)
	}
	mounter := mount.New(mountExecutable)

	// Build a recursive helper function to unmount depth-first.
	var helper func(path string) error
	helper = func(path string) error {
		// Read the directory contents of path.
		entries, err := os.ReadDir(path)
		if err != nil {
			return fmt.Errorf("failed to read directory %s: %w", path, err)
		}

		// Process each entry, recursively.
		for _, entry := range entries {
			subPath := filepath.Join(path, entry.Name())
			if entry.IsDir() {
				if err := helper(subPath); err != nil {
					return err
				}
			}
		}

		// After processing all children, unmount the current directory if it's a mount point.
		mounted, err := mounter.IsMountPoint(path)
		if err != nil {
			return fmt.Errorf("failed to check mount point %s: %w", path, err)
		}
		if mounted {
			if err := mounter.Unmount(path); err != nil {
				return fmt.Errorf("failed to unmount %s: %w", path, err)
			}
		}

		return nil
	}

	return helper(root)
}

// conditionallyBindMountHostDev bind mounts hostDevContainerPath over /dev when devRoot is "/".
// Introduced to address the issues described in https://github.com/NVIDIA/k8s-dra-driver-gpu/issues/477.
// TODO: Revisit with a more comprehensive solution as proposed in https://github.com/NVIDIA/k8s-dra-driver-gpu/pull/307.
func (l deviceLib) conditionallyBindMountHostDev() error {
	// If devRoot != "/" then we don't need to do the mount
	if l.devRoot != "/" {
		return nil
	}

	// Get a reference to the mount executable.
	mountExecutable, err := exec.LookPath("mount")
	if err != nil {
		return fmt.Errorf("error looking up mount executable: %w", err)
	}
	mounter := mount.New(mountExecutable)

	// Bind mount hostDevContainerPath over /dev
	if err := mounter.Mount(hostDevContainerPath, "/dev", "", []string{"bind"}); err != nil {
		return fmt.Errorf("failed to bind mount %s over /dev: %w", hostDevContainerPath, err)
	}

	return nil
}
