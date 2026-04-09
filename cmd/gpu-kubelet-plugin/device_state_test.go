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
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
)

const vfioDevName DeviceName = "gpu-vfio-0"

// fakeEnumerator implements deviceEnumerator for tests.
// Each call to enumerateAllPossibleDevices consumes the next result in the slice,
// once exhausted, the last result is repeated.
type fakeEnumerator struct {
	results []enumerateResult
	calls   int
}

type enumerateResult struct {
	devices *PerGPUAllocatableDevices
	err     error
}

func (f *fakeEnumerator) enumerateAllPossibleDevices() (*PerGPUAllocatableDevices, error) {
	idx := f.calls
	if idx >= len(f.results) {
		idx = len(f.results) - 1
	}
	f.calls++
	return f.results[idx].devices, f.results[idx].err
}

func emptyDevices() *PerGPUAllocatableDevices {
	return &PerGPUAllocatableDevices{allocatablesMap: map[PCIBusID]AllocatableDevices{}}
}

func oneDevice() *PerGPUAllocatableDevices {
	return &PerGPUAllocatableDevices{
		allocatablesMap: map[PCIBusID]AllocatableDevices{
			"0000:00:04.0": {"gpu-0": {Gpu: &GpuInfo{UUID: "GPU-fake-uuid"}}},
		},
	}
}

// vfioOnlyDevices returns a PerGPUAllocatableDevices with a single vfio-type
// device whose parent is nil — PCI enumeration found the GPU but nvml has not yet initialized it.
func vfioOnlyDevices(vfioName DeviceName) *PerGPUAllocatableDevices {
	return &PerGPUAllocatableDevices{
		allocatablesMap: map[PCIBusID]AllocatableDevices{
			"0000:00:04.0": {
				vfioName: {Vfio: &VfioDeviceInfo{
					UUID:     "vfio-fake-uuid",
					PciBusID: "0000:00:04.0",
					// parent intentionally nil: nvml has not seen this GPU yet
				}},
			},
		},
	}
}

// vfioOnlyDevicesWithParent returns a PerGPUAllocatableDevices with a single
// vfio-type device whose parent GPU is set — normal steady-state when a GPU is prepared as vfio.
func vfioOnlyDevicesWithParent(vfioName DeviceName) *PerGPUAllocatableDevices {
	parent := &GpuInfo{UUID: "GPU-fake-uuid", pciBusID: "0000:00:04.0"}
	return &PerGPUAllocatableDevices{
		allocatablesMap: map[PCIBusID]AllocatableDevices{
			"0000:00:04.0": {
				vfioName: {Vfio: &VfioDeviceInfo{
					UUID:     "vfio-fake-uuid",
					PciBusID: "0000:00:04.0",
					parent:   parent,
				}},
			},
		},
	}
}

// mixedDevices returns a PerGPUAllocatableDevices with two GPUs:
//   - GPU at 0000:00:04.0: a vfio-type device whose parent is nil (nvml has not yet initialized it)
//   - GPU at 0000:00:05.0: a gpu-type device returned normally by nvml
func mixedDevices(vfioName DeviceName) *PerGPUAllocatableDevices {
	return &PerGPUAllocatableDevices{
		allocatablesMap: map[PCIBusID]AllocatableDevices{
			"0000:00:04.0": {
				vfioName: {Vfio: &VfioDeviceInfo{
					UUID:     "vfio-fake-uuid",
					PciBusID: "0000:00:04.0",
					// parent intentionally nil: nvml has not seen this GPU yet
				}},
			},
			"0000:00:05.0": {"gpu-1": {Gpu: &GpuInfo{UUID: "GPU-fake-uuid-2"}}},
		},
	}
}

// prepareCompletedCheckpoint builds a Checkpoint with a single PrepareCompleted
// claim for the given vfio device name.
func prepareCompletedCheckpoint(vfioName DeviceName) *Checkpoint {
	return &Checkpoint{
		V2: &CheckpointV2{
			PreparedClaims: PreparedClaimsByUID{
				"claim-uid-1": {
					CheckpointState: ClaimCheckpointStatePrepareCompleted,
					PreparedDevices: PreparedDevices{
						{Devices: PreparedDeviceList{
							{Vfio: &PreparedVfioDevice{
								Info:   &VfioDeviceInfo{UUID: "vfio-fake-uuid"},
								Device: &kubeletplugin.Device{DeviceName: vfioName},
							}},
						}},
					},
				},
			},
		},
	}
}

func TestEnumerateDevicesWithRetry(t *testing.T) {
	t.Parallel()

	// fastBackoff is a zero-jitter, millisecond-interval backoff used across
	// test cases so retries are deterministic and tests finish quickly.
	fastBackoff := func(steps int) wait.Backoff {
		return wait.Backoff{
			Duration: 1 * time.Millisecond,
			Factor:   1.0,
			Jitter:   0.0,
			Steps:    steps,
		}
	}

	tests := map[string]struct {
		enumerator         *fakeEnumerator
		checkpoint         *Checkpoint
		ctxFn              func() (context.Context, context.CancelFunc)
		backoff            wait.Backoff
		passthroughEnabled bool
		wantErr            error
		wantDeviceCount    int
		wantCalls          int
	}{
		"devices found on first call": {
			enumerator:      &fakeEnumerator{results: []enumerateResult{{devices: oneDevice()}}},
			backoff:         fastBackoff(10),
			wantDeviceCount: 1,
			wantCalls:       1,
		},
		"devices found after retries": {
			enumerator: &fakeEnumerator{results: []enumerateResult{
				{devices: emptyDevices()},
				{devices: emptyDevices()},
				{devices: oneDevice()},
			}},
			backoff:         fastBackoff(10),
			wantDeviceCount: 1,
			wantCalls:       3,
		},
		// A pre-cancelled context short-circuits ExponentialBackoffWithContext before the condition
		// runs, so the enumerator is never invoked regardless of the backoff budget.
		"context cancelled returns context error": {
			enumerator: &fakeEnumerator{results: []enumerateResult{{devices: emptyDevices()}}},
			ctxFn: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			backoff:   fastBackoff(10),
			wantErr:   context.Canceled,
			wantCalls: 0,
		},
		"retry budget exhausted returns sentinel error to force crashloop": {
			enumerator: &fakeEnumerator{results: []enumerateResult{{devices: emptyDevices()}}},
			backoff:    fastBackoff(3),
			wantErr:    ErrDeviceEnumerationTimeout,
			wantCalls:  3,
		},
		"transient NVML error retried and then succeeds": {
			enumerator: &fakeEnumerator{results: []enumerateResult{
				{err: fmt.Errorf("ensureNVML failed: %w", nvml.ERROR_UNINITIALIZED)},
				{err: fmt.Errorf("ensureNVML failed: %w", nvml.ERROR_DRIVER_NOT_LOADED)},
				{devices: oneDevice()},
			}},
			backoff:         fastBackoff(10),
			wantDeviceCount: 1,
			wantCalls:       3,
		},
		"non-transient NVML error fails immediately": {
			enumerator: &fakeEnumerator{results: []enumerateResult{
				{err: fmt.Errorf("ensureNVML failed: %w", nvml.ERROR_GPU_IS_LOST)},
			}},
			backoff:   fastBackoff(10),
			wantErr:   nvml.ERROR_GPU_IS_LOST,
			wantCalls: 1,
		},
		"empty then non-transient error propagates mid-retry": {
			enumerator: &fakeEnumerator{results: []enumerateResult{
				{devices: emptyDevices()},
				{err: fmt.Errorf("ensureNVML failed: %w", nvml.ERROR_GPU_IS_LOST)},
			}},
			backoff:   fastBackoff(10),
			wantErr:   nvml.ERROR_GPU_IS_LOST,
			wantCalls: 2,
		},
		"passthrough: orphan vfio with empty checkpoint retries until gpu appears": {
			enumerator: &fakeEnumerator{results: []enumerateResult{
				{devices: vfioOnlyDevices(vfioDevName)},
				{devices: vfioOnlyDevices(vfioDevName)},
				{devices: oneDevice()},
			}},
			passthroughEnabled: true,
			backoff:            fastBackoff(10),
			wantDeviceCount:    1,
			wantCalls:          3,
		},
		"passthrough: orphan vfio covered by PrepareCompleted checkpoint does not retry": {
			enumerator: &fakeEnumerator{results: []enumerateResult{
				{devices: vfioOnlyDevices(vfioDevName)},
			}},
			checkpoint:         prepareCompletedCheckpoint(vfioDevName),
			passthroughEnabled: true,
			backoff:            fastBackoff(10),
			wantDeviceCount:    1,
			wantCalls:          1,
		},
		"passthrough: mixed devices with orphan vfio covered by PrepareCompleted checkpoint does not retry": {
			enumerator: &fakeEnumerator{results: []enumerateResult{
				{devices: mixedDevices(vfioDevName)},
			}},
			checkpoint:         prepareCompletedCheckpoint(vfioDevName),
			passthroughEnabled: true,
			backoff:            fastBackoff(10),
			wantDeviceCount:    2,
			wantCalls:          1,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			if tc.ctxFn != nil {
				var cancel context.CancelFunc
				ctx, cancel = tc.ctxFn()
				defer cancel()
			}

			cp := tc.checkpoint
			if cp == nil {
				cp = &Checkpoint{V2: &CheckpointV2{PreparedClaims: PreparedClaimsByUID{}}}
			}
			got, err := enumerateDevicesWithRetry(ctx, tc.enumerator, tc.backoff, cp, tc.passthroughEnabled)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
				assert.Len(t, got.allocatablesMap, tc.wantDeviceCount)
			}
			assert.Equal(t, tc.wantCalls, tc.enumerator.calls, "enumerator call count")
		})
	}
}

func TestHasOrphanVfioDevices(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		perGPU             *PerGPUAllocatableDevices
		checkpoint         *Checkpoint
		passthroughEnabled bool
		wantOrphan         bool
	}{
		"passthrough disabled — always false": {
			perGPU:             vfioOnlyDevices(vfioDevName),
			checkpoint:         &Checkpoint{V2: &CheckpointV2{PreparedClaims: PreparedClaimsByUID{}}},
			passthroughEnabled: false,
			wantOrphan:         false,
		},
		"no devices": {
			perGPU:             emptyDevices(),
			checkpoint:         &Checkpoint{V2: &CheckpointV2{PreparedClaims: PreparedClaimsByUID{}}},
			passthroughEnabled: true,
			wantOrphan:         false,
		},
		"gpu-type device only": {
			perGPU:             oneDevice(),
			checkpoint:         &Checkpoint{V2: &CheckpointV2{PreparedClaims: PreparedClaimsByUID{}}},
			passthroughEnabled: true,
			wantOrphan:         false,
		},
		"orphan vfio with empty checkpoint": {
			perGPU:             vfioOnlyDevices(vfioDevName),
			checkpoint:         &Checkpoint{V2: &CheckpointV2{PreparedClaims: PreparedClaimsByUID{}}},
			passthroughEnabled: true,
			wantOrphan:         true,
		},
		"orphan vfio covered by PrepareCompleted checkpoint": {
			perGPU:             vfioOnlyDevices(vfioDevName),
			checkpoint:         prepareCompletedCheckpoint(vfioDevName),
			passthroughEnabled: true,
			wantOrphan:         false,
		},
		"vfio with parent — not an orphan": {
			perGPU:             vfioOnlyDevicesWithParent(vfioDevName),
			checkpoint:         &Checkpoint{V2: &CheckpointV2{PreparedClaims: PreparedClaimsByUID{}}},
			passthroughEnabled: true,
			wantOrphan:         false,
		},
		"nil checkpoint treated as empty": {
			perGPU:             vfioOnlyDevices(vfioDevName),
			checkpoint:         nil,
			passthroughEnabled: true,
			wantOrphan:         true,
		},
		"mixed devices: orphan vfio covered by PrepareCompleted checkpoint": {
			perGPU:             mixedDevices(vfioDevName),
			checkpoint:         prepareCompletedCheckpoint(vfioDevName),
			passthroughEnabled: true,
			wantOrphan:         false,
		},
		"mixed devices: orphan vfio with empty checkpoint": {
			perGPU:             mixedDevices(vfioDevName),
			checkpoint:         &Checkpoint{V2: &CheckpointV2{PreparedClaims: PreparedClaimsByUID{}}},
			passthroughEnabled: true,
			wantOrphan:         true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := hasOrphanVfioDevices(tc.perGPU, tc.checkpoint, tc.passthroughEnabled)
			assert.Equal(t, tc.wantOrphan, got)
		})
	}
}

func TestEnumerateDevices(t *testing.T) {
	tests := map[string]struct {
		enumerator         *fakeEnumerator
		checkpoint         *Checkpoint
		passthroughEnabled bool
		wantNil            bool
		wantErr            error
		wantDeviceCount    int
	}{
		"devices found — returns them": {
			enumerator:      &fakeEnumerator{results: []enumerateResult{{devices: oneDevice()}}},
			wantDeviceCount: 1,
		},
		"empty result — returns nil to defer to background": {
			enumerator: &fakeEnumerator{results: []enumerateResult{{devices: emptyDevices()}}},
			wantNil:    true,
		},
		"transient NVML error — returns nil to defer to background": {
			enumerator: &fakeEnumerator{results: []enumerateResult{
				{err: fmt.Errorf("ensureNVML failed: %w", nvml.ERROR_UNINITIALIZED)},
			}},
			wantNil: true,
		},
		"non-transient NVML error — propagates immediately": {
			enumerator: &fakeEnumerator{results: []enumerateResult{
				{err: fmt.Errorf("ensureNVML failed: %w", nvml.ERROR_GPU_IS_LOST)},
			}},
			wantErr: nvml.ERROR_GPU_IS_LOST,
		},
		"passthrough: orphan vfio, nil checkpoint — returns nil to defer to background": {
			enumerator:         &fakeEnumerator{results: []enumerateResult{{devices: vfioOnlyDevices(vfioDevName)}}},
			checkpoint:         nil,
			passthroughEnabled: true,
			wantNil:            true,
		},
		"passthrough: orphan vfio covered by PrepareCompleted checkpoint — returns devices": {
			enumerator:         &fakeEnumerator{results: []enumerateResult{{devices: vfioOnlyDevices(vfioDevName)}}},
			checkpoint:         prepareCompletedCheckpoint(vfioDevName),
			passthroughEnabled: true,
			wantDeviceCount:    1,
		},
		"passthrough: vfio with parent — returns devices": {
			enumerator:         &fakeEnumerator{results: []enumerateResult{{devices: vfioOnlyDevicesWithParent(vfioDevName)}}},
			passthroughEnabled: true,
			wantDeviceCount:    1,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := enumerateDevices(tc.enumerator, tc.checkpoint, tc.passthroughEnabled)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			if tc.wantNil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.Len(t, got.allocatablesMap, tc.wantDeviceCount)
			}
		})
	}
}
