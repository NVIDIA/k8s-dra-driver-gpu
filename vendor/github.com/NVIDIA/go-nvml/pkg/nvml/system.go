// Copyright (c) 2020, NVIDIA CORPORATION.  All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package nvml

// nvml.SystemGetDriverVersion()
func (l *library) SystemGetDriverVersion() (string, Return) {
	Version := make([]byte, SYSTEM_DRIVER_VERSION_BUFFER_SIZE)
	ret := nvmlSystemGetDriverVersion(&Version[0], SYSTEM_DRIVER_VERSION_BUFFER_SIZE)
	return string(Version[:clen(Version)]), ret
}

// nvml.SystemGetNVMLVersion()
func (l *library) SystemGetNVMLVersion() (string, Return) {
	Version := make([]byte, SYSTEM_NVML_VERSION_BUFFER_SIZE)
	ret := nvmlSystemGetNVMLVersion(&Version[0], SYSTEM_NVML_VERSION_BUFFER_SIZE)
	return string(Version[:clen(Version)]), ret
}

// nvml.SystemGetCudaDriverVersion()
func (l *library) SystemGetCudaDriverVersion() (int, Return) {
	var CudaDriverVersion int32
	ret := nvmlSystemGetCudaDriverVersion(&CudaDriverVersion)
	return int(CudaDriverVersion), ret
}

// nvml.SystemGetCudaDriverVersion_v2()
func (l *library) SystemGetCudaDriverVersion_v2() (int, Return) {
	var CudaDriverVersion int32
	ret := nvmlSystemGetCudaDriverVersion_v2(&CudaDriverVersion)
	return int(CudaDriverVersion), ret
}

// nvml.SystemGetProcessName()
func (l *library) SystemGetProcessName(pid int) (string, Return) {
	name := make([]byte, SYSTEM_PROCESS_NAME_BUFFER_SIZE)
	ret := nvmlSystemGetProcessName(uint32(pid), &name[0], SYSTEM_PROCESS_NAME_BUFFER_SIZE)
	return string(name[:clen(name)]), ret
}

// nvml.SystemGetHicVersion()
func (l *library) SystemGetHicVersion() ([]HwbcEntry, Return) {
	var hwbcCount uint32 = 1 // Will be reduced upon returning
	for {
		hwbcEntries := make([]HwbcEntry, hwbcCount)
		ret := nvmlSystemGetHicVersion(&hwbcCount, &hwbcEntries[0])
		if ret == SUCCESS {
			return hwbcEntries[:hwbcCount], ret
		}
		if ret != ERROR_INSUFFICIENT_SIZE {
			return nil, ret
		}
		hwbcCount *= 2
	}
}

// nvml.SystemGetTopologyGpuSet()
func (l *library) SystemGetTopologyGpuSet(cpuNumber int) ([]Device, Return) {
	var count uint32
	ret := nvmlSystemGetTopologyGpuSet(uint32(cpuNumber), &count, nil)
	if ret != SUCCESS {
		return nil, ret
	}
	if count == 0 {
		return []Device{}, ret
	}
	deviceArray := make([]nvmlDevice, count)
	ret = nvmlSystemGetTopologyGpuSet(uint32(cpuNumber), &count, &deviceArray[0])
	return convertSlice[nvmlDevice, Device](deviceArray), ret
}

// nvml.SystemGetConfComputeCapabilities()
func (l *library) SystemGetConfComputeCapabilities() (ConfComputeSystemCaps, Return) {
	var capabilities ConfComputeSystemCaps
	ret := nvmlSystemGetConfComputeCapabilities(&capabilities)
	return capabilities, ret
}

// nvml.SystemGetConfComputeState()
func (l *library) SystemGetConfComputeState() (ConfComputeSystemState, Return) {
	var state ConfComputeSystemState
	ret := nvmlSystemGetConfComputeState(&state)
	return state, ret
}

// nvml.SystemGetConfComputeGpusReadyState()
func (l *library) SystemGetConfComputeGpusReadyState() (uint32, Return) {
	var isAcceptingWork uint32
	ret := nvmlSystemGetConfComputeGpusReadyState(&isAcceptingWork)
	return isAcceptingWork, ret
}

// nvml.SystemSetConfComputeGpusReadyState()
func (l *library) SystemSetConfComputeGpusReadyState(isAcceptingWork uint32) Return {
	return nvmlSystemSetConfComputeGpusReadyState(isAcceptingWork)
}

// nvml.SystemSetNvlinkBwMode()
func (l *library) SystemSetNvlinkBwMode(nvlinkBwMode uint32) Return {
	return nvmlSystemSetNvlinkBwMode(nvlinkBwMode)
}

// nvml.SystemGetNvlinkBwMode()
func (l *library) SystemGetNvlinkBwMode() (uint32, Return) {
	var nvlinkBwMode uint32
	ret := nvmlSystemGetNvlinkBwMode(&nvlinkBwMode)
	return nvlinkBwMode, ret
}

// nvml.SystemGetConfComputeKeyRotationThresholdInfo()
func (l *library) SystemGetConfComputeKeyRotationThresholdInfo() (ConfComputeGetKeyRotationThresholdInfo, Return) {
	var keyRotationThresholdInfo ConfComputeGetKeyRotationThresholdInfo
	keyRotationThresholdInfo.Version = STRUCT_VERSION(keyRotationThresholdInfo, 1)
	ret := nvmlSystemGetConfComputeKeyRotationThresholdInfo(&keyRotationThresholdInfo)
	return keyRotationThresholdInfo, ret
}

// nvml.SystemGetConfComputeSettings()
func (l *library) SystemGetConfComputeSettings() (SystemConfComputeSettings, Return) {
	var settings SystemConfComputeSettings
	settings.Version = STRUCT_VERSION(settings, 1)
	ret := nvmlSystemGetConfComputeSettings(&settings)
	return settings, ret
}

// nvml.SystemSetConfComputeKeyRotationThresholdInfo()
func (l *library) SystemSetConfComputeKeyRotationThresholdInfo(keyRotationThresholdInfo ConfComputeSetKeyRotationThresholdInfo) Return {
	return nvmlSystemSetConfComputeKeyRotationThresholdInfo(&keyRotationThresholdInfo)
}

// nvml.SystemGetDriverBranch()
func (l *library) SystemGetDriverBranch() (SystemDriverBranchInfo, Return) {
	var branchInfo SystemDriverBranchInfo
	branchInfo.Version = STRUCT_VERSION(branchInfo, 1)
	ret := nvmlSystemGetDriverBranch(&branchInfo, SYSTEM_DRIVER_VERSION_BUFFER_SIZE)
	return branchInfo, ret
}
