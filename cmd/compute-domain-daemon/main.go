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
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"text/template"
	"time"

	"k8s.io/klog/v2"

	"github.com/urfave/cli/v2"

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
	"github.com/NVIDIA/k8s-dra-driver-gpu/internal/common"
	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/featuregates"
	pkgflags "github.com/NVIDIA/k8s-dra-driver-gpu/pkg/flags"
)

const (
	imexDaemonConfigDirPath   = "/imexd"
	imexDaemonConfigPath      = imexDaemonConfigDirPath + "/imexd.cfg"
	imexDaemonConfigTmplPath  = imexDaemonConfigDirPath + "/imexd.cfg.tmpl"
	imexDaemonNodesConfigPath = imexDaemonConfigDirPath + "/nodes.cfg"
	imexDaemonBinaryName      = "nvidia-imex"
	imexCtlBinaryName         = "nvidia-imex-ctl"
)

type Flags struct {
	cliqueID               string
	computeDomainUUID      string
	computeDomainName      string
	computeDomainNamespace string
	computeDomainNumNodes  int
	nodeName               string
	podIP                  string
	podName                string
	podNamespace           string
	maxNodesPerIMEXDomain  int
}

type IMEXConfigTemplateData struct {
	IMEXCmdBindInterfaceIP    string
	IMEXDaemonNodesConfigPath string
}

func main() {
	if err := newApp().Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newApp() *cli.App {
	loggingConfig := pkgflags.NewLoggingConfig()
	featureGateConfig := pkgflags.NewFeatureGateConfig()
	flags := &Flags{}

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
		return f(ctx, cancel, flags)
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
		&cli.IntFlag{
			Name:        "compute-domain-num-nodes",
			Usage:       "",
			EnvVars:     []string{"COMPUTE_DOMAIN_NUM_NODES"},
			Destination: &flags.computeDomainNumNodes,
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
		&cli.StringFlag{
			Name:        "pod-name",
			Usage:       "The name of this pod.",
			EnvVars:     []string{"POD_NAME"},
			Destination: &flags.podName,
		},
		&cli.StringFlag{
			Name:        "pod-namespace",
			Usage:       "The namespace of this pod.",
			EnvVars:     []string{"POD_NAMESPACE"},
			Destination: &flags.podNamespace,
		},
		&cli.IntFlag{
			Name:        "max-nodes-per-imex-domain",
			Usage:       "The maximum number of possible nodes per IMEX domain",
			EnvVars:     []string{"MAX_NODES_PER_IMEX_DOMAIN"},
			Destination: &flags.maxNodesPerIMEXDomain,
		},
	}
	cliFlags = append(cliFlags, featureGateConfig.Flags()...)
	cliFlags = append(cliFlags, loggingConfig.Flags()...)

	// Create the app
	app := &cli.App{
		Name:  "compute-domain-daemon",
		Usage: "compute-domain-daemon manages the IMEX daemon for NVIDIA compute domains.",
		Flags: cliFlags,
		Before: func(c *cli.Context) error {
			// `loggingConfig` must be applied before doing any logging
			err := loggingConfig.Apply()
			pkgflags.LogStartupConfig(flags, loggingConfig)
			return err
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
	common.StartDebugSignalHandlers()

	config := &ControllerConfig{
		cliqueID:               flags.cliqueID,
		computeDomainUUID:      flags.computeDomainUUID,
		computeDomainName:      flags.computeDomainName,
		computeDomainNamespace: flags.computeDomainNamespace,
		computeDomainNumNodes:  flags.computeDomainNumNodes,
		nodeName:               flags.nodeName,
		podIP:                  flags.podIP,
		podName:                flags.podName,
		podNamespace:           flags.podNamespace,
		maxNodesPerIMEXDomain:  flags.maxNodesPerIMEXDomain,
	}

	// Support heterogeneous ComputeDomains. That means that a CD may contain
	// nodes that do not take part in Multi-Node NVLink communication. On such
	// nodes, this program is started with an empty NVLink clique ID
	// configuration parameter. In this mode, do not start the IMEX daemon but
	// otherwise keep business logic intact. In particular, continuously update
	// this node's state in the CD object.
	if flags.cliqueID == "" {
		klog.Infof("no cliqueID: register with ComputeDomain, but do not run IMEX daemon")
	}

	// large-N-simulation hack
	if config.cliqueID == "none" {
		config.cliqueID = calcCliqueID(config.nodeName, config.computeDomainNumNodes)
	}

	// Render and write the IMEX daemon config with the current pod IP
	// if err := writeIMEXConfig(flags.podIP); err != nil {
	// 	return fmt.Errorf("writeIMEXConfig failed: %w", err)
	// }

	// Prepare IMEX daemon process manager (not invoking the process yet).
	var dnsNameManager *DNSNameManager
	if featuregates.Enabled(featuregates.IMEXDaemonsWithDNSNames) {
		// Prepare DNS name manager
		dnsNameManager = NewDNSNameManager(flags.cliqueID, flags.maxNodesPerIMEXDomain, imexDaemonNodesConfigPath)

		// Create static nodes config file with DNS names
		if err := dnsNameManager.WriteNodesConfig(); err != nil {
			return fmt.Errorf("failed to create static nodes config: %w", err)
		}
	}

	// Prepare IMEX daemon process manager.
	//daemonCommandLine := []string{imexDaemonBinaryName, "-c", imexDaemonConfigPath}
	daemonCommandLine := []string{
		"bash",
		"-c",
		"trap 'echo \"Received SIGUSR1\"' SIGUSR1; echo; echo; echo START; while true; touch /running; echo running; do sleep 5; done",
	}
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
		klog.Infof("Terminated: controller task")
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
		klog.Infof("Terminated: IMEX daemon update task")
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
		klog.Infof("Terminated: process manager")
	}()

	wg.Wait()

	// Let's not yet try to make exit code promises.
	klog.Infof("Exiting")
	return nil
}

// IMEXDaemonUpdateLoopWithIPs reacts to ComputeDomain status changes by updating the
// IMEX daemon nodes config file and (re)starting the IMEX daemon process.
func IMEXDaemonUpdateLoopWithIPs(ctx context.Context, controller *Controller, cliqueID string, pm *ProcessManager) error {
	for {
		klog.V(1).Infof("wait for nodes update")
		select {
		case <-ctx.Done():
			klog.Infof("shutdown: stop IMEXDaemonUpdateLoopWithIPs")
			return nil
		case nodes := <-controller.GetNodesUpdateChan():
			if err := writeNodesConfig(cliqueID, nodes); err != nil {
				return fmt.Errorf("writeNodesConfig failed: %w", err)
			}

			if cliqueID == "" {
				klog.V(1).Infof("empty cliqueID: do not start IMEX daemon")
				break
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
	t0 := time.Now()
	firstupdate := true

	for {
		klog.V(1).Infof("wait for nodes update")

		select {
		case <-ctx.Done():
			klog.Infof("shutdown: stop IMEXDaemonUpdateLoopWithDNSNames")
			return nil
		case nodes := <-controller.GetNodesUpdateChan():
			if firstupdate {
				klog.V(6).Infof("t_process_start %.3f s", time.Since(t0).Seconds())
				firstupdate = false
			}

			updated, err := dnsNameManager.UpdateDNSNameMappings(nodes, controller.computeDomainManager.config.cliqueID)
			if err != nil {
				return fmt.Errorf("failed to update DNS name => IP mappings: %w", err)
			}

			if controller.computeDomainManager.config.cliqueID == "" {
				klog.V(1).Infof("empty cliqueID: do not start IMEX daemon")
				break
			}

			fresh, err := processManager.EnsureStarted()
			if err != nil {
				return fmt.Errorf("failed to ensure IMEX daemon is started: %w", err)
			}

			dnsNameManager.LogDNSNameMappings()

			// Skip sending SIGUSR1 when the process is fresh (has newly been
			// created) or when this was a noop update. TODO: review skipping
			// this also if the new set of IP addresses only strictly removes
			// addresses compared to the old set (then we don't need to force
			// the daemon to re-resolve & re-connect).
			if !updated || fresh {
				break
			}

			// Actively ask the IMEX daemon to re-read its config and to
			// re-connect to its peers (involving DNS name re-resolution).
			klog.Infof("updated DNS/IP mapping, old process: send SIGUSR1")
			if err := processManager.Signal(syscall.SIGUSR1); err != nil {
				// Only log (ignore this error for now: if the process went away
				// unexpectedly, the process manager will handle that. If any
				// other error resulted in bad signal delivery, we may get away
				// with it).
				klog.Errorf("failed to send SIGUSR1 to child process: %s", err)
			}
		}
	}
}

// check verifies if the node is IMEX capable and if so, checks if the IMEX daemon is ready.
// It returns an error if any step fails.
func check(ctx context.Context, cancel context.CancelFunc, flags *Flags) error {
	if _, err := os.Stat("/running"); err == nil {
		fmt.Println("/running found")
		return nil
	}

	fmt.Println("/running not found yet")
	return fmt.Errorf("/running not found yet")
}

// writeIMEXConfig renders the config template with the pod IP and writes it to the final config file.
func writeIMEXConfig(podIP string) error {
	configTemplateData := IMEXConfigTemplateData{
		IMEXCmdBindInterfaceIP:    podIP,
		IMEXDaemonNodesConfigPath: imexDaemonNodesConfigPath,
	}

	tmpl, err := template.ParseFiles(imexDaemonConfigTmplPath)
	if err != nil {
		return fmt.Errorf("error parsing template file: %w", err)
	}

	var configFile bytes.Buffer
	if err := tmpl.Execute(&configFile, configTemplateData); err != nil {
		return fmt.Errorf("error executing template: %w", err)
	}

	// Ensure the directory exists
	dir := filepath.Dir(imexDaemonConfigPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	if err := os.WriteFile(imexDaemonConfigPath, configFile.Bytes(), 0644); err != nil {
		return fmt.Errorf("error writing config file %v: %w", imexDaemonConfigPath, err)
	}

	klog.Infof("Rendered IMEX daemon config file with: %v", configTemplateData)
	return nil
}

// writeNodesConfig creates a nodesConfig file with IPs for nodes in the same clique.
func writeNodesConfig(cliqueID string, nodes []*nvapi.ComputeDomainNode) error {
	// Ensure the directory exists
	dir := filepath.Dir(imexDaemonNodesConfigPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Create or overwrite the nodesConfig file
	f, err := os.Create(imexDaemonNodesConfigPath)
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
	content, err := os.ReadFile(imexDaemonNodesConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read nodes config: %w", err)
	}
	klog.Infof("Current %s:\n%s", imexDaemonNodesConfigPath, string(content))
	return nil
}

func calcCliqueID(nodename string, numnodes int) string {
	// 1. Determine number of cliques
	// To round up NUMNODES / 18, we use integer math: (n + divisor - 1) / divisor
	// Example: 19 nodes / 18 = 2 cliques
	ccount := (numnodes + 17) / 18
	if ccount == 0 {
		ccount = 1
	}

	// 2. Calculate clique assignment
	// We hash the node name and map it to one of the clique indices
	cliqueIndex := getCliqueAssignment(nodename, ccount)
	// overwrite assigned clique ID (so far, it was noop)
	cliqueID := strconv.Itoa(cliqueIndex)
	klog.Infof("identified clique count: %d, picked cliqueID %s", ccount, cliqueID)
	return cliqueID
}

// Each node (simulated in a pod) must independently self-assign a clique using
// only its name and the total node count -- the most robust "uniform" strategy
// is Modulo Hashing. getCliqueAssignment hashes a string to a range [0, max-1]
func getCliqueAssignment(key string, max int) int {
	// FNV-1a is a non-cryptographic hash function that is fast
	// and has excellent distribution properties for short strings.
	h := fnv.New32a()
	h.Write([]byte(key))
	hashValue := h.Sum32()

	// Modulo operation maps the massive hash value to our specific range
	return int(hashValue % uint32(max))
}
