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
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"text/template"

	"k8s.io/klog/v2"

	"github.com/urfave/cli/v2"

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/flags"
)

const (
	nodesConfigPath    = "/etc/nvidia-imex/nodes_config.cfg"
	imexConfigPath     = "/etc/nvidia-imex/config.cfg"
	imexConfigTmplPath = "/etc/nvidia-imex/config.tmpl.cfg"
	imexBinaryName     = "nvidia-imex"
	imexCtlBinaryName  = "nvidia-imex-ctl"
)

type Flags struct {
	cliqueID               string
	computeDomainUUID      string
	computeDomainName      string
	computeDomainNamespace string
	nodeName               string
	podIP                  string
	loggingConfig          *flags.LoggingConfig
}

type IMEXConfigTemplateData struct {
	IMEXCmdBindInterfaceIP string
}

func main() {
	if err := newApp().Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newApp() *cli.App {
	flags := Flags{
		loggingConfig: flags.NewLoggingConfig(),
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
	}
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
	}
	klog.Infof("config: %v", config)

	// Write the IMEX config with the current pod IP before starting the daemon
	if err := writeIMEXConfig(flags.podIP); err != nil {
		return fmt.Errorf("writeIMEXConfig failed: %w", err)
	}

	// Prepare IMEX daemon process manager (not invoking the process yet).
	daemonCommandLine := []string{imexBinaryName, "-c", imexConfigPath}
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

	// Start IMEXDaemonUpdateLoop() in goroutine (watches for CD status
	// changes, and restarts the IMEX daemon as needed).
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := IMEXDaemonUpdateLoop(ctx, controller, flags.cliqueID, processManager); err != nil {
			klog.Errorf("IMEXDaemonUpdateLoop failed, initiate shutdown: %s", err)
			cancel()
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

// IMEXDaemonUpdateLoop() reacts to ComputeDomain status changes by updating the
// IMEX daemon nodes config file and (re)starting the IMEX daemon process.
func IMEXDaemonUpdateLoop(ctx context.Context, controller *Controller, cliqueID string, pm *ProcessManager) error {
	for {
		klog.Infof("wait for nodes update")
		select {
		case <-ctx.Done():
			klog.Infof("shutdown: stop IMEXDaemonUpdateLoop")
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

// check verifies if the node is IMEX capable and if so, checks if the IMEX daemon is ready.
// It returns an error if any step fails.
func check(ctx context.Context, cancel context.CancelFunc, flags *Flags) error {
	if flags.cliqueID == "" {
		fmt.Println("ClusterUUID and CliqueId are NOT set for GPUs on this node.")
		return nil
	}

	// Check if IMEX daemon is ready
	cmd := exec.CommandContext(ctx, imexCtlBinaryName, "-q")

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

// writeIMEXConfig renders the config template with the pod IP and writes it to the final config file.
func writeIMEXConfig(podIP string) error {
	configTemplateData := IMEXConfigTemplateData{
		IMEXCmdBindInterfaceIP: podIP,
	}

	tmpl, err := template.ParseFiles(imexConfigTmplPath)
	if err != nil {
		return fmt.Errorf("error parsing template file: %w", err)
	}

	var configFile bytes.Buffer
	if err := tmpl.Execute(&configFile, configTemplateData); err != nil {
		return fmt.Errorf("error executing template: %w", err)
	}

	if err := os.WriteFile(imexConfigPath, configFile.Bytes(), 0644); err != nil {
		return fmt.Errorf("error writing config file %v: %w", imexConfigPath, err)
	}

	klog.Infof("Updated IMEX config file with pod IP: %s", podIP)
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
