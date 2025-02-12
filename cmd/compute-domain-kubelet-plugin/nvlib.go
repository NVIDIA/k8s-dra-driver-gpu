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
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	nvdev "github.com/NVIDIA/go-nvlib/pkg/nvlib/device"
	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

const (
	procDevicesPath                  = "/proc/devices"
	nvidiaCapsImexChannelsDeviceName = "nvidia-caps-imex-channels"
)

type deviceLib struct {
	nvdev.Interface
	nvmllib           nvml.Interface
	driverLibraryPath string
	devRoot           string
	nvidiaSMIPath     string
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
	return &d, nil
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

func (l deviceLib) getImexChannelCount() (int, error) {
	// TODO: Pull this value from /proc/driver/nvidia/params
	return 2048, nil
}

func (l deviceLib) getImexChannelMajor() (int, error) {
	file, err := os.Open(procDevicesPath)
	if err != nil {
		return -1, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	foundCharDevices := false

	for scanner.Scan() {
		line := scanner.Text()

		// Ignore empty lines
		if line == "" {
			continue
		}

		// Check for any line with text followed by a colon (header)
		if strings.Contains(line, ":") {
			// Stop if we've already found the character devices section and reached another section
			if foundCharDevices {
				break
			}
			// Check if we entered the character devices section
			if strings.HasSuffix(line, ":") && strings.HasPrefix(line, "Character") {
				foundCharDevices = true
			}
			// Continue to the next line, regardless
			continue
		}

		// If we've passed the character devices section, check for nvidiaCapsImexChannelsDeviceName
		if foundCharDevices {
			parts := strings.Fields(line)
			if len(parts) == 2 && parts[1] == nvidiaCapsImexChannelsDeviceName {
				return strconv.Atoi(parts[0])
			}
		}
	}

	return -1, scanner.Err()
}

func (l deviceLib) createComputeDomainChannelDevice(channel int) error {
	// Construct the properties of the device node to create.
	path := fmt.Sprintf("/dev/nvidia-caps-imex-channels/channel%d", channel)
	path = filepath.Join(l.devRoot, path)
	mode := uint32(unix.S_IFCHR | 0666)

	// Get the IMEX channel major and build a /dev device from it
	major, err := l.getImexChannelMajor()
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
