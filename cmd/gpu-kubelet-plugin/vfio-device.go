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
	"strings"
	"time"

	"k8s.io/klog/v2"
	cdiapi "tags.cncf.io/container-device-interface/pkg/cdi"
	cdispec "tags.cncf.io/container-device-interface/specs-go"
)

const (
	kernelIommuGroupPath = "/sys/kernel/iommu_groups"
	vfioPciModule        = "vfio_pci"
	vfioPciDriver        = "vfio-pci"
	nvidiaDriver         = "nvidia"
	hostRoot             = "/host-root"
	sysModulesRoot       = "/sys/module"
	pciDevicesRoot       = "/sys/bus/pci/devices"
	vfioDevicesRoot      = "/dev/vfio"
	driverResetRetries   = "5"
	gpuFreeCheckInterval = 1 * time.Second
	gpuFreeCheckTimeout  = 60 * time.Second
)

type VfioPciManager struct {
	containerDriverRoot string
	hostDriverRoot      string
	driver              string
	nvlib               *deviceLib
	nvidiaEnabled       bool
}

func NewVfioPciManager(containerDriverRoot string, hostDriverRoot string, nvlib *deviceLib, nvidiaEnabled bool) *VfioPciManager {
	vm := &VfioPciManager{
		containerDriverRoot: containerDriverRoot,
		hostDriverRoot:      hostDriverRoot,
		driver:              vfioPciDriver,
		nvlib:               nvlib,
		nvidiaEnabled:       nvidiaEnabled,
	}
	if !vm.isVfioPCIModuleLoaded() {
		err := vm.loadVfioPciModule()
		if err != nil {
			klog.Fatalf("failed to load vfio_pci module: %v", err)
		}
	}

	return vm
}

// ValidatePassthroughSupport tests if vfio-pci device allocations can be used.
func (vm *VfioPciManager) ValidatePassthroughSupport() error {
	if !vm.isVfioPCIModuleLoaded() {
		return fmt.Errorf("vfio_pci module is not loaded")
	}
	iommuEnabled, err := vm.isIommuEnabled()
	if err != nil {
		return err
	}
	if !iommuEnabled {
		return fmt.Errorf("IOMMU is not enabled in the kernel")
	}
	return nil
}

func (vm *VfioPciManager) isIommuEnabled() (bool, error) {
	f, err := os.Open(kernelIommuGroupPath)
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

func (vm *VfioPciManager) isVfioPCIModuleLoaded() bool {
	f, err := os.Stat(filepath.Join(sysModulesRoot, vfioPciModule))
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		klog.Fatalf("Failed to check if vfio_pci module is loaded: %v", err)
	}

	if !f.IsDir() {
		return false
	}

	return true
}

func (vm *VfioPciManager) loadVfioPciModule() error {
	_, err := execCommandWithChroot(hostRoot, "modprobe", []string{vfioPciModule}) //nolint:gosec
	if err != nil {
		return err
	}

	return nil
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
				klog.Errorf("Unexpected error checking if gpu device %q is free: %v", info.pcieBusID, err)
				continue
			}
			klog.Infof("gpu device %q has open fds by process(es): %s", info.pcieBusID, string(out))
		}
	}
}

// Verify there are no VFs on the GPU.
func (vm *VfioPciManager) verifyDisabledVFs(pcieBusID string) error {
	gpu, err := vm.nvlib.nvpci.GetGPUByPciBusID(pcieBusID)
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
	perGpuLock.Get(info.pcieBusID).Lock()
	defer perGpuLock.Get(info.pcieBusID).Unlock()

	driver, err := getDriver(pciDevicesRoot, info.pcieBusID)
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
	err = vm.verifyDisabledVFs(info.pcieBusID)
	if err != nil {
		return err
	}
	err = vm.changeDriver(info.pcieBusID, vm.driver)
	if err != nil {
		return err
	}
	return nil
}

// Unconfigure binds the GPU to the nvidia driver.
func (vm *VfioPciManager) Unconfigure(ctx context.Context, info *VfioDeviceInfo) error {
	perGpuLock.Get(info.pcieBusID).Lock()
	defer perGpuLock.Get(info.pcieBusID).Unlock()

	// Do nothing if we dont expect to switch to nvidia driver.
	if !vm.nvidiaEnabled {
		return nil
	}

	driver, err := getDriver(pciDevicesRoot, info.pcieBusID)
	if err != nil {
		return err
	}
	if driver == nvidiaDriver {
		return nil
	}
	err = vm.changeDriver(info.pcieBusID, nvidiaDriver)
	if err != nil {
		return err
	}
	return nil
}

func getDriver(pciDevicesRoot, pciAddress string) (string, error) {
	driverPath, err := os.Readlink(filepath.Join(pciDevicesRoot, pciAddress, "driver"))
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

func (vm *VfioPciManager) acquireUnbindLock(gpu string) error {
	lockRetries := 5
	unbindLockFile := filepath.Join("/proc/driver/nvidia/gpus", gpu, "unbindLock")

	if _, err := os.Stat(unbindLockFile); err != nil {
		// If the lock file doesn't exist, we assume no lock is needed.
		return nil
	}

	for attempt := 1; attempt <= lockRetries; attempt++ {
		klog.Infof("[retry %d/%d] Attempting to acquire unbindLock for %s", attempt, lockRetries, gpu)

		// Try to write 1 to acquire the lock
		err := os.WriteFile(unbindLockFile, []byte("1\n"), 0200)
		if err != nil {
			klog.Warningf("failed to write to unbindLock file %s: %v", unbindLockFile, err)
		}

		// Read the lock file to verify
		content, err := os.ReadFile(unbindLockFile)
		if err == nil {
			val := strings.TrimSpace(string(content))
			if val == "1" {
				klog.Infof("UnbindLock acquired for %s", gpu)
				return nil
			}
		}

		time.Sleep(time.Duration(attempt) * time.Second)
	}

	return fmt.Errorf("cannot obtain unbindLock for %s", gpu)
}

func (vm *VfioPciManager) unbindFromDriver(pciAddress string) error {
	driverPath := filepath.Join(pciDevicesRoot, pciAddress, "driver")
	if _, err := os.Stat(driverPath); err != nil {
		// Not bound to any driver
		return nil
	}

	existingDriver, err := filepath.EvalSymlinks(driverPath)
	if err != nil {
		return fmt.Errorf("failed to resolve driver symlink for %s: %v", pciAddress, err)
	}

	existingDriverName := filepath.Base(existingDriver)
	if existingDriverName == "nvidia" {
		if err := vm.acquireUnbindLock(pciAddress); err != nil {
			return err
		}
	}

	unbindFile := filepath.Join(existingDriver, "unbind")
	if err := os.WriteFile(unbindFile, []byte(pciAddress+"\n"), 0200); err != nil {
		klog.Errorf("Attempting to unbind %s from its driver failed; err: %v", pciAddress, err)
		return err
	}
	return nil
}

func (vm *VfioPciManager) bindToDriver(pciAddress, driver string) error {
	driversPath := "/sys/bus/pci/drivers"
	driverOverrideFile := filepath.Join(pciDevicesRoot, pciAddress, "driver_override")
	bindFile := filepath.Join(driversPath, driver, "bind")

	if _, err := os.Stat(driverOverrideFile); err != nil {
		klog.Errorf("'%s' file does not exist", driverOverrideFile)
		return fmt.Errorf("driver_override file not found: %v", err)
	}

	if err := os.WriteFile(driverOverrideFile, []byte(driver+"\n"), 0200); err != nil {
		klog.Errorf("failed to write '%s' to %s", driver, driverOverrideFile)
		return fmt.Errorf("failed to write to driver_override: %v", err)
	}

	if _, err := os.Stat(bindFile); err != nil {
		klog.Errorf("'%s' file does not exist", bindFile)
		return fmt.Errorf("bind file not found: %v", err)
	}

	if err := os.WriteFile(bindFile, []byte(pciAddress+"\n"), 0200); err != nil {
		klog.Errorf("Attempting to bind %s to %s driver failed; err: %v", pciAddress, driver, err)
		// attempt to revert driver_override
		_ = os.WriteFile(driverOverrideFile, []byte("\n"), 0200)
		return fmt.Errorf("failed to write to bind file: %v", err)
	}
	return nil
}

func GetVfioCommonCDIContainerEdits() *cdiapi.ContainerEdits {
	return &cdiapi.ContainerEdits{
		ContainerEdits: &cdispec.ContainerEdits{
			DeviceNodes: []*cdispec.DeviceNode{
				{
					Path: filepath.Join(vfioDevicesRoot, "vfio"),
				},
			},
			// Make sure that NVIDIA_VISIBLE_DEVICES is set to void to avoid the
			// nvidia-container-runtime honoring it in addition to the underlying
			// runtime honoring CDI.
			Env: []string{"NVIDIA_VISIBLE_DEVICES=void"},
		},
	}
}

// GetCDIContainerEdits returns the CDI spec for a container to have access to the GPU while bound on vfio-pci driver.
func GetVfioCDIContainerEdits(info *VfioDeviceInfo) *cdiapi.ContainerEdits {
	vfioDevicePath := filepath.Join(vfioDevicesRoot, fmt.Sprintf("%d", info.iommuGroup))
	return &cdiapi.ContainerEdits{
		ContainerEdits: &cdispec.ContainerEdits{
			DeviceNodes: []*cdispec.DeviceNode{
				{
					Path: vfioDevicePath,
				},
			},
		},
	}
}

func execCommandWithChroot(fsRoot, cmd string, args []string) ([]byte, error) {
	chrootArgs := []string{fsRoot, cmd}
	chrootArgs = append(chrootArgs, args...)
	return exec.Command("chroot", chrootArgs...).CombinedOutput()
}

func execCommand(cmd string, args []string) ([]byte, error) {
	return exec.Command(cmd, args...).CombinedOutput()
}
