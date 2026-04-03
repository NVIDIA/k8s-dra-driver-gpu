/*
 * Copyright (c) 2026 NVIDIA CORPORATION.  All rights reserved.
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

package common

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// MaskNvidiaDriverParams conditionally masks the params file to prevent this container from
// recreating any missing GPU device nodes. This is necessary, for example, when running
// under nvkind to limit the set GPUs governed by the plugin even though it has cgroup
// access to all of them.
func MaskNvidiaDriverParams() error {
	if os.Getenv("MASK_NVIDIA_DRIVER_PARAMS") != "true" {
		return nil
	}

	src := "/proc/driver/nvidia/params"
	dst := "root/gpu-params"

	content, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read %s: %v", src, err)
	}

	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		if line == "ModifyDeviceFiles: 1" {
			lines[i] = "ModifyDeviceFiles: 0"
		}
	}
	newContent := strings.Join(lines, "\n")

	if err := os.WriteFile(dst, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %v", dst, err)
	}

	if err := syscall.Mount(dst, src, "", syscall.MS_BIND, ""); err != nil {
		return fmt.Errorf("failed to bind mount %s to %s: %v", dst, src, err)
	}

	return nil
}
