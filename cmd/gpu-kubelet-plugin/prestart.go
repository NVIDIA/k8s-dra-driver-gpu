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

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"k8s.io/klog/v2"
)

// Main intent: help users to self-troubleshoot when the GPU driver is not set up
// properly before installing this DRA driver. In that case, the log of the init
// container running this script is meant to yield an actionable error message.
// For now, rely on k8s to implement a high-level retry with back-off.
func runPrestartInit(ctx context.Context) error {
	// Design goal: long-running init container that retries at constant frequency,
	// and leaves only upon success (with code 0).
	waitS := 10 * time.Second
	attempt := 0

	nvidiaDriverRoot := os.Getenv("NVIDIA_DRIVER_ROOT")
	if nvidiaDriverRoot == "" {
		// Not set, or set to empty string (not distinguishable).
		// Normalize to "/" (treated as such elsewhere).
		nvidiaDriverRoot = "/"
	}

	driverRootParent := "/driver-root-parent"
	// Remove trailing slash (if existing) and get last path element.
	driverRootPath := filepath.Join(driverRootParent, filepath.Base(nvidiaDriverRoot))

	// Ensure the /driver-root-parent directory exists
	if err := os.MkdirAll(driverRootParent, 0755); err != nil {
		klog.Warningf("Failed to create %s: %v", driverRootParent, err)
	}

	// Create in-container path /driver-root as a symlink. Expectation: link may be
	// broken initially (e.g., if the GPU operator isn't deployed yet. The link heals
	// once the driver becomes mounted (e.g., once GPU operator provides the driver
	// on the host at /run/nvidia/driver).
	fmt.Printf("create symlink: /driver-root -> %s\n", driverRootPath)
	_ = os.Remove("/driver-root")
	if err := os.Symlink(driverRootPath, "/driver-root"); err != nil {
		klog.Warningf("Failed to create symlink: %v", err)
	}

	for {
		if validateAndExitOnSuccess(ctx, nvidiaDriverRoot, attempt) {
			return nil
		}

		select {
		case <-ctx.Done():
			// DS pods may get deleted (terminated with SIGTERM) and re-created when the GPU
			// Operator driver container creates a mount at /run/nvidia. Make that explicit.
			fmt.Printf("%s: received SIGTERM\n", time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
			return nil
		case <-time.After(waitS):
			attempt++
		}
	}
}

func emitCommonErr(nvidiaDriverRoot string) {
	fmt.Printf("Check failed. Has the NVIDIA GPU driver been set up? "+
		"It is expected to be installed under "+
		"NVIDIA_DRIVER_ROOT (currently set to '%s') "+
		"in the host filesystem. If that path appears to be unexpected: "+
		"review the DRA driver's 'nvidiaDriverRoot' Helm chart variable. "+
		"Otherwise, review if the GPU driver has "+
		"actually been installed under that path.\n", nvidiaDriverRoot)
}

func validateAndExitOnSuccess(ctx context.Context, nvidiaDriverRoot string, attempt int) bool {
	fmt.Printf("%s  /driver-root (%s on host): ", time.Now().UTC().Format("2006-01-02T15:04:05Z"), nvidiaDriverRoot)

	// Search specific set of directories (not recursively: not required, and
	// /driver-root may be a big tree). Limit to first result (multiple results
	// are a bit of a pathological state, but continue with validation logic).
	nvPath := findFirstFile(
		"nvidia-smi",
		"/driver-root/opt/bin",
		"/driver-root/usr/bin",
		"/driver-root/usr/sbin",
		"/driver-root/bin",
		"/driver-root/sbin",
	)

	// Follow symlinks (-L), because `libnvidia-ml.so.1` is typically a link.
	nvLibPath := findFirstFile(
		"libnvidia-ml.so.1",
		"/driver-root/usr/lib64",
		"/driver-root/usr/lib/x86_64-linux-gnu",
		"/driver-root/usr/lib/aarch64-linux-gnu",
		"/driver-root/lib64",
		"/driver-root/lib/x86_64-linux-gnu",
		"/driver-root/lib/aarch64-linux-gnu",
	)

	if nvPath == "" {
		fmt.Printf("nvidia-smi: not found, ")
	} else {
		fmt.Printf("nvidia-smi: '%s', ", nvPath)
	}

	if nvLibPath == "" {
		fmt.Printf("libnvidia-ml.so.1: not found, ")
	} else {
		fmt.Printf("libnvidia-ml.so.1: '%s', ", nvLibPath)
	}

	// Log top-level entries in /driver-root (this may be valuable debug info).
	entries, _ := os.ReadDir("/driver-root")
	var entryNames string
	for i, e := range entries {
		if i > 0 {
			entryNames += " "
		}
		entryNames += e.Name()
	}
	fmt.Printf("current contents: [%s].\n", entryNames)

	if nvPath != "" && nvLibPath != "" {
		// Run with clean environment (only LD_PRELOAD; nvidia-smi has only this
		// dependency). Emit message before invocation (nvidia-smi may be slow or
		// hang).
		fmt.Printf("invoke: env -i LD_PRELOAD=%s %s\n", nvLibPath, nvPath)

		cmd := exec.CommandContext(ctx, nvPath)
		cmd.Env = []string{"LD_PRELOAD=" + nvLibPath}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		err := cmd.Run()
		// For checking GPU driver health: rely on nvidia-smi's exit code. Rely
		// on code 0 signaling that the driver is properly set up. See section
		// 'RETURN VALUE' in the nvidia-smi man page for meaning of error codes.
		if err == nil {
			fmt.Printf("nvidia-smi returned with code 0: success, leave\n")
			return true
		}

		if exitErr, ok := err.(*exec.ExitError); ok {
			fmt.Printf("exit code: %d\n", exitErr.ExitCode())
		} else {
			fmt.Printf("execution failed: %v, exit code: -1\n", err)
		}
	}

	// Reduce log volume: log hints only every Nth attempt.
	if attempt%6 != 0 {
		return false
	}

	// nvidia-smi binaries not found, or execution failed. First, provide generic
	// error message. Then, try to provide actionable hints for common problems.
	fmt.Println()
	emitCommonErr(nvidiaDriverRoot)

	// For host-provided driver not at / provide feedback for two special cases.
	if nvidiaDriverRoot != "/" {
		if len(entries) == 0 {
			fmt.Printf("Hint: Directory %s on the host is empty\n", nvidiaDriverRoot)
		} else {
			// Not empty, but at least one of the binaries not found: this is a
			// rather pathological state.
			if nvPath == "" || nvLibPath == "" {
				fmt.Printf("Hint: Directory %s is not empty but at least one of the binaries wasn't found.\n", nvidiaDriverRoot)
			}
		}
	}

	// Common mistake: driver container, but forgot `--set nvidiaDriverRoot`
	if nvidiaDriverRoot == "/" {
		if _, err := os.Stat("/driver-root/run/nvidia/driver/usr/bin/nvidia-smi"); err == nil {
			fmt.Printf("Hint: '/run/nvidia/driver/usr/bin/nvidia-smi' exists on the host, you " +
				"may want to re-install the DRA driver Helm chart with " +
				"--set nvidiaDriverRoot=/run/nvidia/driver\n")
		}
	}

	if nvidiaDriverRoot == "/run/nvidia/driver" {
		fmt.Printf("Hint: NVIDIA_DRIVER_ROOT is set to '/run/nvidia/driver' " +
			"which typically means that the NVIDIA GPU Operator " +
			"manages the GPU driver. Make sure that the GPU Operator " +
			"is deployed and healthy.\n")
	}
	fmt.Println()

	return false
}

func findFirstFile(filename string, dirs ...string) string {
	for _, dir := range dirs {
		path := filepath.Join(dir, filename)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}
