/*
Copyright The Kubernetes Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"k8s.io/klog/v2"
)

const (
	kernelIommuGroupPath   = "/sys/kernel/iommu_groups"
	vfioPciModule          = "vfio_pci"
	vfioPciDriver          = "vfio-pci"
	nvidiaDriver           = "nvidia"
	hostRoot               = "/host-root"
	sysModulePath          = "/sys/module"
	pciDevicesPath         = "/sys/bus/pci/devices"
	vfioDevicesRoot        = "/dev/vfio"
	vfioDevicesPath        = "/dev/vfio/devices"
	iommuDevicePath        = "/dev/iommu"
	unbindFromDriverScript = "/usr/bin/unbind_from_driver.sh"
	bindToDriverScript     = "/usr/bin/bind_to_driver.sh"
	gpuFreeCheckInterval   = 1 * time.Second
	gpuFreeCheckTimeout    = 60 * time.Second
)

type VfioPciManager struct {
	containerDriverRoot string
	hostDriverRoot      string
	driver              string
	nvlib               *deviceLib
	nvidiaEnabled       bool
}

func NewVfioPciManager(containerDriverRoot string, hostDriverRoot string, nvlib *deviceLib, nvidiaEnabled bool) (*VfioPciManager, error) {
	if loaded, err := checkVfioPCIModuleLoaded(); err == nil {
		if !loaded {
			err = loadVfioPciModule()
			if err != nil {
				return nil, fmt.Errorf("failed to load vfio_pci module: %w", err)
			}
		}
	} else {
		return nil, fmt.Errorf("error checking if vfio_pci module is loaded: %w", err)
	}

	iommuEnabled, err := checkIommuEnabled()
	if err != nil {
		return nil, fmt.Errorf("error checking if IOMMU is enabled: %w", err)
	}
	if !iommuEnabled {
		return nil, fmt.Errorf("IOMMU is not enabled in the kernel")
	}

	if _, err := os.Stat(hostRoot); os.IsNotExist(err) {
		return nil, fmt.Errorf("%s volume mount is missing: the kubelet-plugin container requires a hostPath volume "+
			"mounted at %s to check GPU device availability via fuser. Ensure the helm chart has "+
			"featureGates.PassthroughSupport=true or add the volume mount manually", hostRoot, hostRoot)
	}

	vm := &VfioPciManager{
		containerDriverRoot: containerDriverRoot,
		hostDriverRoot:      hostDriverRoot,
		driver:              vfioPciDriver,
		nvlib:               nvlib,
		nvidiaEnabled:       nvidiaEnabled,
	}

	return vm, nil
}

func (vm *VfioPciManager) WaitForGPUFree(ctx context.Context, info *VfioDeviceInfo) error {
	if info.parent == nil {
		return nil
	}
	timeout := time.After(gpuFreeCheckTimeout)
	ticker := time.NewTicker(gpuFreeCheckInterval)
	defer ticker.Stop()

	gpuDeviceNode := filepath.Join(vm.hostDriverRoot, "dev", fmt.Sprintf("nvidia%d", info.parent.minor))
	for {
		select {
		case <-timeout:
			return fmt.Errorf("timed out waiting for gpu to be free")
		case <-ticker.C:
			out, err := execCommandWithChroot(hostRoot, "fuser", []string{gpuDeviceNode}) //nolint:gosec
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
					return nil
				}
				klog.Errorf("Error checking if gpu device %q is free (exit code may indicate fuser/chroot misconfiguration; "+
					"verify %s is mounted and contains fuser): %v, output: %s", info.PciBusID, hostRoot, err, string(out))
				continue
			}
			klog.Infof("gpu device %q has open fds by process(es): %s -- external processes (e.g. dcgm-exporter) "+
				"holding GPU device handles will block VFIO passthrough (see issue #1076)", info.PciBusID, string(out))
		}
	}
}

// Verify there are no VFs on the GPU.
func (vm *VfioPciManager) verifyDisabledVFs(pciBusID string) error {
	gpu, err := vm.nvlib.nvpci.GetGPUByPciBusID(pciBusID)
	if err != nil {
		return err
	}
	numVFs := gpu.SriovInfo.PhysicalFunction.NumVFs
	if numVFs > 0 {
		return fmt.Errorf("gpu has %d VFs, cannot unbind", numVFs)
	}
	return nil
}

// Configure binds the GPU to the vfio-pci driver.
func (vm *VfioPciManager) Configure(ctx context.Context, info *VfioDeviceInfo) error {
	perGpuLock.Get(info.PciBusID).Lock()
	defer perGpuLock.Get(info.PciBusID).Unlock()

	driver, err := getDriver(pciDevicesPath, info.PciBusID)
	if err != nil {
		return err
	}
	if driver == vm.driver {
		return nil
	}
	// Only support vfio-pci or nvidia (if vm.nvidiaEnabled) driver.
	if !vm.nvidiaEnabled || driver != nvidiaDriver {
		return fmt.Errorf("gpu is bound to %q driver, expected %q or %q", driver, vm.driver, nvidiaDriver)
	}
	err = vm.WaitForGPUFree(ctx, info)
	if err != nil {
		return err
	}
	err = vm.verifyDisabledVFs(info.PciBusID)
	if err != nil {
		return err
	}
	err = vm.changeDriver(info.PciBusID, vm.driver)
	if err != nil {
		return err
	}
	return nil
}

// Unconfigure binds the GPU to the nvidia driver.
func (vm *VfioPciManager) Unconfigure(ctx context.Context, info *VfioDeviceInfo) error {
	perGpuLock.Get(info.PciBusID).Lock()
	defer perGpuLock.Get(info.PciBusID).Unlock()

	// Do nothing if we dont expect to switch to nvidia driver.
	if !vm.nvidiaEnabled {
		return nil
	}

	driver, err := getDriver(pciDevicesPath, info.PciBusID)
	if err != nil {
		return err
	}
	if driver == nvidiaDriver {
		return nil
	}
	err = vm.changeDriver(info.PciBusID, nvidiaDriver)
	if err != nil {
		return err
	}
	return nil
}

func getDriver(pciDevicesPath, pciAddress string) (string, error) {
	driverPath, err := os.Readlink(filepath.Join(pciDevicesPath, pciAddress, "driver"))
	if err != nil {
		return "", err
	}
	_, driver := filepath.Split(driverPath)
	return driver, nil
}

func (vm *VfioPciManager) changeDriver(pciAddress, driver string) error {
	err := vm.unbindFromDriver(pciAddress)
	if err != nil {
		return err
	}
	err = vm.bindToDriver(pciAddress, driver)
	if err != nil {
		return err
	}
	return nil
}

func (vm *VfioPciManager) unbindFromDriver(pciAddress string) error {
	out, err := execCommand(unbindFromDriverScript, []string{pciAddress}) //nolint:gosec
	if err != nil {
		klog.Errorf("Attempting to unbind %s from its driver failed; stdout: %s, err: %v", pciAddress, string(out), err)
		return err
	}
	return nil
}

func (vm *VfioPciManager) bindToDriver(pciAddress, driver string) error {
	out, err := execCommand(bindToDriverScript, []string{pciAddress, driver}) //nolint:gosec
	if err != nil {
		klog.Errorf("Attempting to bind %s to %s driver failed; stdout: %s, err: %v", pciAddress, driver, string(out), err)
		return err
	}
	return nil
}

// Check if the vfio_pci module is loaded.
func checkVfioPCIModuleLoaded() (bool, error) {
	f, err := os.Stat(filepath.Join(hostRoot, sysModulePath, vfioPciModule))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check if vfio_pci module is loaded: %w", err)
	}

	if !f.IsDir() {
		return false, nil
	}

	return true, nil
}

// Load the vfio_pci module.
func loadVfioPciModule() error {
	_, err := execCommandWithChroot(hostRoot, "modprobe", []string{vfioPciModule}) //nolint:gosec
	if err != nil {
		return err
	}

	return nil
}

// Check if IOMMU is enabled.
func checkIommuEnabled() (bool, error) {
	f, err := os.Open(filepath.Join(hostRoot, kernelIommuGroupPath))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = f.Readdirnames(1)
	if err == io.EOF {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	return true, nil
}

// Check if IOMMUFD is enabled.
// We correlate the IOMMUFD support with the presence of the /dev/iommu API device.
func checkIommuFDEnabled() (bool, error) {
	_, err := os.Stat(filepath.Join(hostRoot, iommuDevicePath))
	if err != nil {
		if os.IsNotExist(err) {
			klog.Infof("IOMMUFD is not enabled, /dev/iommu device node does not exist")
			return false, nil
		}
		return false, fmt.Errorf("error checking if iommu device node exists: %w", err)
	}
	return true, nil
}

// Execute a command with chroot.
func execCommandWithChroot(fsRoot, cmd string, args []string) ([]byte, error) {
	chrootArgs := []string{fsRoot, cmd}
	chrootArgs = append(chrootArgs, args...)
	return exec.Command("chroot", chrootArgs...).CombinedOutput()
}

// Execute a command.
func execCommand(cmd string, args []string) ([]byte, error) {
	return exec.Command(cmd, args...).CombinedOutput()
}
