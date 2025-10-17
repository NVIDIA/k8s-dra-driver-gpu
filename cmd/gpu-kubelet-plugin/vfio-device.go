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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"k8s.io/klog/v2"
	cdiapi "tags.cncf.io/container-device-interface/pkg/cdi"
	cdispec "tags.cncf.io/container-device-interface/specs-go"
)

const (
	hostNamespaceMount     = "/proc/1/ns/mnt"
	kernelIommuGroupPath   = "/sys/kernel/iommu_groups"
	vfioPciModule          = "vfio_pci"
	vfioPciDriver          = "vfio-pci"
	nvidiaDriver           = "nvidia"
	sysModulesRoot         = "/sys/module"
	pciDevicesRoot         = "/sys/bus/pci/devices"
	vfioDevicesRoot        = "/dev/vfio"
	unbindFromDriverScript = "/usr/bin/unbind_from_driver.sh"
	bindToDriverScript     = "/usr/bin/bind_to_driver.sh"
	driverResetRetries     = "5"
	gpuFreeCheckInterval   = 1 * time.Second
	gpuFreeCheckTimeout    = 60 * time.Second
)

type VfioPciManager struct {
	driverRoot string
	driver     string
}

func NewVfioPciManager(driverRoot string) *VfioPciManager {
	vm := &VfioPciManager{
		driverRoot: driverRoot,
		driver:     vfioPciDriver,
	}
	if !vm.isVfioPCIModuleLoaded() {
		err := vm.loadVfioPciModule()
		if err != nil {
			klog.Fatalf("failed to load vfio_pci module: %v", err)
		}
	}
	return vm
}

// PreChecks tests if vfio-pci device allocations can be used.
func (vm *VfioPciManager) Prechecks() error {
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
	modules, err := os.ReadDir(sysModulesRoot)
	if err != nil {
		return false
	}

	for _, module := range modules {
		if module.Name() == vfioPciModule {
			return true
		}
	}

	return false

}

func (vm *VfioPciManager) loadVfioPciModule() error {
	_, err := execCommandInHostNamespace("modprobe", []string{vfioPciModule}) //nolint:gosec
	if err != nil {
		return err
	}

	return nil
}

func (vm *VfioPciManager) WaitForGPUFree(info *VfioDeviceInfo) error {
	if info.parent == nil {
		return nil
	}
	timeout := time.After(gpuFreeCheckTimeout)
	ticker := time.NewTicker(gpuFreeCheckInterval)
	defer ticker.Stop()

	gpuDeviceNode := filepath.Join(vm.driverRoot, "dev", fmt.Sprintf("nvidia%d", info.parent.minor))
	for {
		select {
		case <-timeout:
			return fmt.Errorf("timed out waiting for gpu to be free")
		case <-ticker.C:
			out, err := execCommand("lsof", []string{gpuDeviceNode}) //nolint:gosec
			klog.Infof("lsof output: %s, err: %v", string(out), err)
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
					return nil
				}
				continue
			}
		}
	}
}

// Configure binds the GPU to the vfio-pci driver.
func (vm *VfioPciManager) Configure(info *VfioDeviceInfo) error {
	perGpuLock.Get(info.pcieBusID).Lock()
	defer perGpuLock.Get(info.pcieBusID).Unlock()

	driver, err := getDriver(pciDevicesRoot, info.pcieBusID)
	if err != nil {
		return err
	}
	if driver == vm.driver {
		return nil
	}
	err = vm.WaitForGPUFree(info)
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
func (vm *VfioPciManager) Unconfigure(info *VfioDeviceInfo) error {
	perGpuLock.Get(info.pcieBusID).Lock()
	defer perGpuLock.Get(info.pcieBusID).Unlock()

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

func GetVfioCommonCDIContainerEdits() *cdiapi.ContainerEdits {
	return &cdiapi.ContainerEdits{
		ContainerEdits: &cdispec.ContainerEdits{
			DeviceNodes: []*cdispec.DeviceNode{
				{
					Path: filepath.Join(vfioDevicesRoot, "vfio"),
				},
			},
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

func execCommandInHostNamespace(cmd string, args []string) ([]byte, error) {
	nsenterArgs := []string{"--mount=/proc/1/ns/mnt", cmd}
	nsenterArgs = append(nsenterArgs, args...)
	return exec.Command("nsenter", nsenterArgs...).CombinedOutput()
}

func execCommand(cmd string, args []string) ([]byte, error) {
	return exec.Command(cmd, args...).CombinedOutput()
}
