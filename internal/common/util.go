/*
 * SPDX-FileCopyrightText: Copyright (c) 2025 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
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
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"k8s.io/klog/v2"
)

const dumpPath = "/tmp/goroutine-stacks.dump"

// Set up SIGUSR2 handler: if triggered, acquire stack traces for all goroutines
// in this process. Dump to file, and fall back to emitting to stderr if file
// output didn't work.
func StartDebugSignalHandlers() {
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGUSR2)
		for range c {
			// Pre-allocate a large-ish buffer for Stack() to write to (output
			// is truncated if buffer isn't big enough).
			buf := make([]byte, 1024*1024*2)
			written := runtime.Stack(buf, true)

			// Try to detect truncated output. Checking for `written == size`
			// may not be sufficient. For example, if the dump routine finds
			// that there isn't enough space for the next full record (full
			// goroutine stack, or full line), it might stop writing rather than
			// writing a partial line. Just add a warning to the file if we got
			// close-ish to the buffer boundary.
			if written > len(buf)-5000 {
				suffix := "\n\nwarning: runtime.Stack() output may be truncated"
				buf = append(buf, suffix...)
				written += len(suffix)
			}

			err := os.WriteFile(dumpPath, buf[:written], 0666)
			if err == nil {
				klog.Infof("Wrote: %s", dumpPath)
				continue
			}

			klog.Errorf("Could not write %s: %s", dumpPath, err)
			chunks := net.Buffers{[]byte("\n"), buf[:written], []byte("\n")}
			_, _ = chunks.WriteTo(os.Stderr)
		}
	}()

	klog.Infof("Started debug signal handler(s)")
}
