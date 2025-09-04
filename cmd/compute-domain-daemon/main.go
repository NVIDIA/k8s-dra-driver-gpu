/*
 * Copyright (c) 2025 NVIDIA CORPORATION.  All rights reserved.
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
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"k8s.io/klog/v2"

	"github.com/Masterminds/semver"
	"github.com/urfave/cli/v2"

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/featuregates"
	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/flags"
)

const (
	nodesConfigPath = "/etc/nvidia-imex/nodes_config.cfg"
	imexConfigPath  = "/etc/nvidia-imex/config.cfg"
	imexBinaryPath  = "/usr/bin/nvidia-imex"
	imexCtlPath     = "/usr/bin/nvidia-imex-ctl"
)

type Flags struct {
	cliqueID               string
	computeDomainUUID      string
	computeDomainName      string
	computeDomainNamespace string
	nodeName               string
	podIP                  string
	maxNodesPerIMEXDomain  int
	loggingConfig          *flags.LoggingConfig
	featureGateConfig      *flags.FeatureGateConfig
}

func main() {
	if err := newApp().Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newApp() *cli.App {
	flags := Flags{
		loggingConfig:     flags.NewLoggingConfig(),
		featureGateConfig: flags.NewFeatureGateConfig(),
	}

	// Create a wrapper that will be used to gracefully shut down all subcommands
	wrapper := func(ctx context.Context, f func(ctx context.Context, cancel context.CancelFunc, flags *Flags) error) error {
		// Create a cancelable context from the one passed in
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGTERM)
		go func() {
			<-sigChan
			klog.Infof("Received SIGTERM, initiate shutdown")
			cancel()
		}()

		// Call the wrapped function
		return f(ctx, cancel, &flags)
	}

	cliFlags := []cli.Flag{
		&cli.StringFlag{
			Name:        "cliqueid",
			Usage:       "The clique ID for this node.",
			EnvVars:     []string{"CLIQUE_ID"},
			Destination: &flags.cliqueID,
		},
		&cli.StringFlag{
			Name:        "compute-domain-uuid",
			Usage:       "The UUID of the ComputeDomain to manage.",
			EnvVars:     []string{"COMPUTE_DOMAIN_UUID"},
			Destination: &flags.computeDomainUUID,
		},
		&cli.StringFlag{
			Name:        "compute-domain-name",
			Usage:       "The name of the ComputeDomain to manage.",
			EnvVars:     []string{"COMPUTE_DOMAIN_NAME"},
			Destination: &flags.computeDomainName,
		},
		&cli.StringFlag{
			Name:        "compute-domain-namespace",
			Usage:       "The namespace of the ComputeDomain to manage.",
			Value:       "default",
			EnvVars:     []string{"COMPUTE_DOMAIN_NAMESPACE"},
			Destination: &flags.computeDomainNamespace,
		},
		&cli.StringFlag{
			Name:        "node-name",
			Usage:       "The name of this Kubernetes node.",
			EnvVars:     []string{"NODE_NAME"},
			Destination: &flags.nodeName,
		},
		&cli.StringFlag{
			Name:        "pod-ip",
			Usage:       "The IP address of this pod.",
			EnvVars:     []string{"POD_IP"},
			Destination: &flags.podIP,
		},
		&cli.IntFlag{
			Name:        "max-nodes-per-imex-domain",
			Usage:       "The maximum number of possible nodes per IMEX domain",
			EnvVars:     []string{"MAX_NODES_PER_IMEX_DOMAIN"},
			Destination: &flags.maxNodesPerIMEXDomain,
		},
	}
	cliFlags = append(cliFlags, flags.featureGateConfig.Flags()...)
	cliFlags = append(cliFlags, flags.loggingConfig.Flags()...)

	// Create the app
	app := &cli.App{
		Name:  "compute-domain-daemon",
		Usage: "compute-domain-daemon manages the IMEX daemon for NVIDIA compute domains.",
		Flags: cliFlags,
		Before: func(c *cli.Context) error {
			return flags.loggingConfig.Apply()
		},
		Commands: []*cli.Command{
			{
				Name:  "run",
				Usage: "Run the compute domain daemon",
				Action: func(c *cli.Context) error {
					return wrapper(c.Context, run)
				},
			},
			{
				Name:  "check",
				Usage: "Check if the node is IMEX capable and if the IMEX daemon is ready",
				Action: func(c *cli.Context) error {
					return wrapper(c.Context, check)
				},
			},
		},
	}

	return app
}

// Run invokes the IMEX daemon and manages its lifecycle.
func run(ctx context.Context, cancel context.CancelFunc, flags *Flags) error {
	// Support heterogeneous compute domain
	if flags.cliqueID == "" {
		fmt.Println("ClusterUUID and CliqueId are NOT set for GPUs on this node.")
		fmt.Println("The IMEX daemon will not be started.")
		fmt.Println("Sleeping forever...")
		<-ctx.Done()
		return nil
	}

	config := &ControllerConfig{
		cliqueID:               flags.cliqueID,
		computeDomainUUID:      flags.computeDomainUUID,
		computeDomainName:      flags.computeDomainName,
		computeDomainNamespace: flags.computeDomainNamespace,
		nodeName:               flags.nodeName,
		podIP:                  flags.podIP,
		maxNodesPerIMEXDomain:  flags.maxNodesPerIMEXDomain,
	}
	klog.Infof("config: %v", config)

	var dnsNameManager *DNSNameManager
	if featuregates.Enabled(featuregates.IMEXDaemonsWithDNSNames) {
		// Prepare DNS name manager
		dnsNameManager = NewDNSNameManager(flags.cliqueID, flags.maxNodesPerIMEXDomain, nodesConfigPath)

		if err := dnsNameManager.UnmountEtcHosts(); err != nil {
			return fmt.Errorf("failed to unmount /etc/hosts: %w", err)
		}

		// Create static nodes config file with DNS names
		if err := dnsNameManager.WriteNodesConfig(); err != nil {
			return fmt.Errorf("failed to create static nodes config: %w", err)
		}
	}

	// Prepare IMEX daemon process manager.
	daemonCommandLine := []string{imexBinaryPath, "-c", imexConfigPath}
	processManager := NewProcessManager(daemonCommandLine)

	// Prepare controller with CD manager (not invoking the controller yet).
	controller, err := NewController(config)
	if err != nil {
		return fmt.Errorf("error creating controller: %w", err)
	}

	var wg sync.WaitGroup

	// Start controller in goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := controller.Run(ctx); err != nil {
			klog.Errorf("controller failed, initiate shutdown: %s", err)
			cancel()
		}
	}()

	// Start IMEX daemon update loop in goroutine (watches for CD status
	// changes and manages IMEX daemon updates).
	wg.Add(1)
	go func() {
		defer wg.Done()
		if featuregates.Enabled(featuregates.IMEXDaemonsWithDNSNames) {
			// Use new DNS name-based functionality
			if err := IMEXDaemonUpdateLoopWithDNSNames(ctx, controller, processManager, dnsNameManager); err != nil {
				klog.Errorf("IMEXDaemonUpdateLoop failed, initiate shutdown: %s", err)
				cancel()
			}
		} else {
			// Use original IP-based functionality
			if err := IMEXDaemonUpdateLoopWithIPs(ctx, controller, flags.cliqueID, processManager); err != nil {
				klog.Errorf("IMEXDaemonUpdateLoop failed, initiate shutdown: %s", err)
				cancel()
			}
		}
	}()

	// Start child process watchdog in goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Watchdog restarts the IMEX daemon upon unexpected termination, and
		// shuts it down gracefully upon our own shutdown.
		if err := processManager.Watchdog(ctx); err != nil {
			klog.Errorf("watch failed, initiate shutdown: %s", err)
			cancel()
		}
	}()

	wg.Wait()

	// Let's not yet try to make exit code promises.
	return nil
}

// IMEXDaemonUpdateLoopWithIPs reacts to ComputeDomain status changes by updating the
// IMEX daemon nodes config file and (re)starting the IMEX daemon process.
func IMEXDaemonUpdateLoopWithIPs(ctx context.Context, controller *Controller, cliqueID string, pm *ProcessManager) error {
	for {
		klog.Infof("wait for nodes update")
		select {
		case <-ctx.Done():
			klog.Infof("shutdown: stop IMEXDaemonUpdateLoopWithIPs")
			return nil
		case nodes := <-controller.GetNodesUpdateChan():
			if err := writeNodesConfig(cliqueID, nodes); err != nil {
				return fmt.Errorf("writeNodesConfig failed: %w", err)
			}

			klog.Infof("Got update, (re)start IMEX daemon")
			if err := pm.Restart(); err != nil {
				// This might be a permanent problem, and retrying upon next update
				// might be pointless. Terminate us.
				return fmt.Errorf("error (re)starting IMEX daemon: %w", err)
			}
		}
	}
}

// IMEXDaemonUpdateLoopWithDNSNames reacts to ComputeDomain status changes by
// updating the /etc/hosts file with IP to DNS name mappings. This relies on
// the IMEX daemon to pick up these changes automatically (and quickly) --
// which it seems to do via grpc-based health-checking of individual
// connections. We only restart the IMEX daemon if it crashes (both
// unexpectedly and expectedly).
func IMEXDaemonUpdateLoopWithDNSNames(ctx context.Context, controller *Controller, processManager *ProcessManager, dnsNameManager *DNSNameManager) error {
	for {
		klog.Infof("wait for nodes update")
		select {
		case <-ctx.Done():
			klog.Infof("shutdown: stop IMEXDaemonUpdateLoopWithDNSNames")
			return nil
		case nodes := <-controller.GetNodesUpdateChan():
			if err := dnsNameManager.UpdateDNSNameMappings(nodes); err != nil {
				return fmt.Errorf("failed to update DNS name => IP mappings: %w", err)
			}
			if err := processManager.EnsureStarted(); err != nil {
				return fmt.Errorf("failed to ensure IMEX daemon is started: %w", err)
			}
			dnsNameManager.LogDNSNameMappings()
		}
	}
}

// check verifies if the node is IMEX capable and if so, checks if the IMEX daemon is ready.
// It returns an error if any step fails.
func check(ctx context.Context, cancel context.CancelFunc, flags *Flags) error {
	if flags.cliqueID == "" {
		fmt.Println("ClusterUUID and CliqueId are NOT set for GPUs on this node.")
		return nil
	}

	// Get IMEX version to determine which flags to use
	v, err := getIMEXVersion(ctx)
	if err != nil {
		return fmt.Errorf("error getting IMEX version: %w", err)
	}

	// Set flags based on version
	var args []string
	if v.LessThan(semver.MustParse("580.0.0")) {
		args = []string{"-q", "-i", "127.0.0.1", "50005"}
	} else {
		args = []string{"-q"}
	}

	// Check if IMEX daemon is ready
	cmd := exec.CommandContext(ctx, imexCtlPath, args...)

	// CombinedOutput captures both, stdout and stderr.
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error checking IMEX daemon status: %w", err)
	}

	if string(output) != "READY\n" {
		return fmt.Errorf("IMEX daemon not ready: %s", string(output))
	}

	return nil
}

// writeNodesConfig creates a nodesConfig file with IPs for nodes in the same clique.
func writeNodesConfig(cliqueID string, nodes []*nvapi.ComputeDomainNode) error {
	// Ensure the directory exists
	dir := filepath.Dir(nodesConfigPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Create or overwrite the nodesConfig file
	f, err := os.Create(nodesConfigPath)
	if err != nil {
		return fmt.Errorf("failed to create nodes config file: %w", err)
	}
	defer f.Close()

	// Write IPs for nodes in the same clique
	//
	// Note(JP): wo we need to apply this type of filtering also in the logic
	// that checks if an IMEX daemon restart is required?
	for _, node := range nodes {
		if node.CliqueID == cliqueID {
			if _, err := fmt.Fprintf(f, "%s\n", node.IPAddress); err != nil {
				return fmt.Errorf("failed to write to nodes config file: %w", err)
			}
		}
	}

	if err := logNodesConfig(); err != nil {
		return fmt.Errorf("logNodesConfig failed: %w", err)
	}
	return nil
}

// Read and log the contents of the nodes configuration file. Return an error if
// the file cannot be read.
func logNodesConfig() error {
	content, err := os.ReadFile(nodesConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read nodes config: %w", err)
	}
	klog.Infof("Current %s:\n%s", nodesConfigPath, string(content))
	return nil
}

// getIMEXVersion returns the version of the NVIDIA IMEX binary.
func getIMEXVersion(ctx context.Context) (*semver.Version, error) {
	cmd := exec.CommandContext(ctx, imexBinaryPath, "--version")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("error running nvidia-imex: %w", err)
	}

	// Split the output by spaces and get the last token
	version := string(output)
	tokens := strings.Fields(version)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("invalid version output: %s", version)
	}

	// Parse and normalize the version using semver
	versionStr := tokens[len(tokens)-1]
	v, err := semver.NewVersion(versionStr)
	if err != nil {
		return nil, fmt.Errorf("invalid version format: %w", err)
	}

	return v, nil
}
