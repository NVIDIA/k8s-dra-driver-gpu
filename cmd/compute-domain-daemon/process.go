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

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"k8s.io/klog/v2"
)

type ProcessManager struct {
	sync.Mutex
	handle      *exec.Cmd
	cmd         []string
	waitResChan chan error
}

func NewProcessManager(cmd []string) *ProcessManager {
	m := &ProcessManager{
		handle:      nil,
		cmd:         cmd,
		waitResChan: make(chan error, 1),
	}
	return m
}

// Restart starts or restarts the process.
func (m *ProcessManager) Restart() error {
	if m.handle != nil {
		if err := m.stop(); err != nil {
			return fmt.Errorf("restart: stop failed: %w", err)
		}
	}
	return m.start()
}

// EnsureStarted starts the process if it is not already running. If the process
// is already started, this is a no-op. The boolean return value indicates
// `new`, i.e. it is `true` if the process was _newly_ started. It must be
// ignored when the returned error is non-nil.
func (m *ProcessManager) EnsureStarted() (bool, error) {
	if m.handle != nil {
		return false, nil
	}
	return true, m.start()
}

// Signal() attempts to send the provided signal to the managed child process.
// Any error is emitted to the caller and must be handled there.
func (m *ProcessManager) Signal(s os.Signal) error {
	m.Lock()
	defer m.Unlock()

	if m.handle == nil {
		return fmt.Errorf("pm: sending signal %s failed: not started", s)
	}
	return m.handle.Process.Signal(s)
}

func (m *ProcessManager) start() error {
	m.Lock()
	defer m.Unlock()

	if m.handle != nil {
		return fmt.Errorf("pm: start failed: already started")
	}

	klog.Infof("Start: %s", strings.Join(m.cmd, " "))

	// Child inherits stdout/err: output of child will interleave with output of
	// parent. In practice, individual log lines typically stay intact (data
	// written in one write() syscall typically is written as an atomic unit).
	cmd := exec.Command(m.cmd[0], m.cmd[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Start()

	// For pre-start problems like invalid path or permission error.
	if err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}
	m.handle = cmd

	// Start blocking wait() system call reaping this process once it exits. The
	// result is written to a buffered channel (that can be peeked into; used to
	// detect unexpected child termination in Watchdog()). It's OK to give up
	// control over this goroutine; but it's critical that `m.wait()` is called
	// at some point during each child's lifecycle to read from the channel.
	go func() {
		m.waitResChan <- m.handle.Wait()
	}()

	klog.Infof("Started process with pid %d", cmd.Process.Pid)
	return nil
}

// Wait for Wait() syscall result to appear on channel. Expected to block if
// called from stop(). Log exit code and reset `m.handle` (after which it is
// safe to call start() again). If the wait() system call that feeds the channel
// returns an error: fatal.
func (m *ProcessManager) wait() error {
	werr := <-m.waitResChan
	if werr == nil {
		klog.Infof("Child exited with code 0")
	} else {
		if exitError, ok := werr.(*exec.ExitError); ok {
			klog.Warningf("Child exited with code %d", exitError.ExitCode())
		} else {
			return fmt.Errorf("pm: wait() failed: %w", werr)
		}
	}

	// Status reaped, we can drop reference to the child.
	m.handle = nil
	return nil
}

func (m *ProcessManager) stop() error {
	// Two places may call stop(): i) Restart(), ii) concext cancel handler
	m.Lock()
	defer m.Unlock()

	if m.handle == nil {
		return fmt.Errorf("pm: stop failed: not started")
	}

	klog.Infof("Stop: send SIGTERM to pid %d", m.handle.Process.Pid)
	err := m.handle.Process.Signal(syscall.SIGTERM)
	if err != nil {
		return fmt.Errorf("pm: stop: could not send SIGTERM to child: %w", err)
	}

	// Wait for process to gracefully shut down. TODO: apply timeout, send
	// SIGKILL upon timeout, wait again. Update: it's reasonable to leave this
	// to the k8s orchestration layer.
	klog.Infof("Wait() for child")
	if err := m.wait(); err != nil {
		return fmt.Errorf("pm: stop: wait failed: %w", err)
	}

	return nil
}

// Watchdog() supervises the process: unexpected termination is handled by
// logging a corresponding message, and by restarting the process. Canceling the
// injected context is the intended way to gracefully stop the child (and to
// also terminate the watchdog).
func (m *ProcessManager) Watchdog(ctx context.Context) error {
	// Maybe use SIGCHLD handler instead to make this ticker-less
	ticker := time.NewTicker(1000 * time.Millisecond)
	defer ticker.Stop()

	klog.Infof("Start watchdog")
	for {
		select {
		case <-ctx.Done():
			klog.Infof("Watchdog: context canceled, attempt to stop child process")
			if err := m.stop(); err != nil {
				return fmt.Errorf("watchdog: stop failed: %w", err)
			}
			return nil
		case <-ticker.C:
			if !m.lost() {
				continue
			}

			klog.Warningf("Watchdog: child terminated unexpectedly")
			// `m.wait()` is known to not block at this point.
			if err := m.wait(); err != nil {
				return fmt.Errorf("watchdog: process lost, wait failed, treat fatal: %w", err)
			}

			klog.Warningf("Watchdog: start process again")
			if err := m.Restart(); err != nil {
				return fmt.Errorf("watchdog: process lost, restart failed, treat fatal: %w", err)
			}

		}
	}
}

// Detect if process terminated unexpectedly.
func (m *ProcessManager) lost() bool {
	if !m.TryLock() {
		// Start or stop is in progress; do not inspect state.
		return false
	}
	defer m.Unlock()

	if m.handle == nil {
		// Not yet or not currently started.
		return false
	}

	if len(m.waitResChan) == 0 {
		// Currently running: background wait() did not return.
		return false
	}

	return true
}
