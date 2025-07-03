/*
 * Copyright (c) 2022-2023 NVIDIA CORPORATION.  All rights reserved.
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
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/urfave/cli/v2"

	"k8s.io/klog/v2"

	"github.com/NVIDIA/k8s-dra-driver-gpu/internal/info"
	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/flags"
)

const (
	DriverName                         = "compute-domain.nvidia.com"
	DriverPluginPath                   = "/var/lib/kubelet/plugins/" + DriverName
	DriverPluginCheckpointFileBasename = "checkpoint.json"
)

type Flags struct {
	kubeClientConfig  flags.KubeClientConfig
	loggingConfig     *flags.LoggingConfig
	featureGateConfig *flags.FeatureGateConfig

	nodeName            string
	namespace           string
	cdiRoot             string
	containerDriverRoot string
	hostDriverRoot      string
	nvidiaCDIHookPath   string
}

type Config struct {
	flags      *Flags
	clientsets flags.ClientSets
}

func main() {
	if err := newApp().Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newApp() *cli.App {
	flags := &Flags{
		loggingConfig:     flags.NewLoggingConfig(),
		featureGateConfig: flags.NewFeatureGateConfig(),
	}
	cliFlags := []cli.Flag{
		&cli.StringFlag{
			Name:        "node-name",
			Usage:       "The name of the node to be worked on.",
			Required:    true,
			Destination: &flags.nodeName,
			EnvVars:     []string{"NODE_NAME"},
		},
		&cli.StringFlag{
			Name:        "namespace",
			Usage:       "The namespace used for the custom resources.",
			Value:       "default",
			Destination: &flags.namespace,
			EnvVars:     []string{"NAMESPACE"},
		},
		&cli.StringFlag{
			Name:        "cdi-root",
			Usage:       "Absolute path to the directory where CDI files will be generated.",
			Value:       "/etc/cdi",
			Destination: &flags.cdiRoot,
			EnvVars:     []string{"CDI_ROOT"},
		},
		&cli.StringFlag{
			Name:        "nvidia-driver-root",
			Aliases:     []string{"host_driver-root"},
			Value:       "/",
			Usage:       "the root path for the NVIDIA driver installation on the host (typical values are '/' or '/run/nvidia/driver')",
			Destination: &flags.hostDriverRoot,
			EnvVars:     []string{"NVIDIA_DRIVER_ROOT", "HOST_DRIVER_ROOT"},
		},
		&cli.StringFlag{
			Name:        "container-driver-root",
			Value:       "/driver-root",
			Usage:       "the path where the NVIDIA driver root is mounted in the container; used for generating CDI specifications",
			Destination: &flags.containerDriverRoot,
			EnvVars:     []string{"CONTAINER_DRIVER_ROOT"},
		},
		&cli.StringFlag{
			Name:        "nvidia-cdi-hook-path",
			Usage:       "Absolute path to the nvidia-cdi-hook executable in the host file system. Used in the generated CDI specification.",
			Destination: &flags.nvidiaCDIHookPath,
			EnvVars:     []string{"NVIDIA_CDI_HOOK_PATH"},
		},
	}
	cliFlags = append(cliFlags, flags.kubeClientConfig.Flags()...)
	cliFlags = append(cliFlags, flags.featureGateConfig.Flags()...)
	cliFlags = append(cliFlags, flags.loggingConfig.Flags()...)

	app := &cli.App{
		Name:            "compute-domain-kubelet-plugin",
		Usage:           "compute-domain-kubelet-plugin implements a DRA driver plugin for NVIDIA compute domains.",
		ArgsUsage:       " ",
		HideHelpCommand: true,
		Flags:           cliFlags,
		Before: func(c *cli.Context) error {
			if c.Args().Len() > 0 {
				return fmt.Errorf("arguments not supported: %v", c.Args().Slice())
			}
			return flags.loggingConfig.Apply()
		},
		Action: func(c *cli.Context) error {
			ctx := c.Context

			clientSets, err := flags.kubeClientConfig.NewClientSets()
			if err != nil {
				return fmt.Errorf("create client: %w", err)
			}

			config := &Config{
				flags:      flags,
				clientsets: clientSets,
			}

			return StartPlugin(ctx, config)
		},
		Version: info.GetVersionString(),
	}

	// We remove the -v alias for the version flag so as to not conflict with the -v flag used for klog.
	f, ok := cli.VersionFlag.(*cli.BoolFlag)
	if ok {
		f.Aliases = nil
	}

	return app
}

// StartPlugin initializes and runs the compute domain kubelet plugin.
func StartPlugin(ctx context.Context, config *Config) error {
	// Create the plugin directory
	err := os.MkdirAll(DriverPluginPath, 0750)
	if err != nil {
		return err
	}

	// Setup nvidia-cdi-hook binary
	if err := config.flags.setNvidiaCDIHookPath(); err != nil {
		return fmt.Errorf("error setting up nvidia-cdi-hook: %w", err)
	}

	// Initialize CDI root directory
	info, err := os.Stat(config.flags.cdiRoot)
	switch {
	case err != nil && os.IsNotExist(err):
		err := os.MkdirAll(config.flags.cdiRoot, 0750)
		if err != nil {
			return err
		}
	case err != nil:
		return err
	case !info.IsDir():
		return fmt.Errorf("path for cdi file generation is not a directory: '%v'", config.flags.cdiRoot)
	}

	// Setup signal handling for graceful shutdown
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	// Create a cancellable context for cleanup
	var driver *driver
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		if err := driver.Shutdown(); err != nil {
			klog.Errorf("Unable to cleanly shutdown driver: %v", err)
		}
	}()

	// Create and start the driver
	driver, err = NewDriver(ctx, config)
	if err != nil {
		return fmt.Errorf("error creating driver: %w", err)
	}

	// Wait for shutdown signal
	<-sigs

	return nil
}

// setNvidiaCDIHookPath ensures the proper flag is set with the host path for the nvidia-cdi-hook binary.
// If 'f.nvidiaCDIHookPath' is already set (from the command line), do nothing.
// If 'f.nvidiaCDIHookPath' is empty, it copies the nvidia-cdi-hook binary from
// /usr/bin/nvidia-cdi-hook to DriverPluginPath and sets 'f.nvidiaCDIHookPath'
// to this path. The /usr/bin/nvidia-cdi-hook is present in the current
// container image because it is copied from the toolkit image into this
// container at build time.
func (f *Flags) setNvidiaCDIHookPath() error {
	if f.nvidiaCDIHookPath != "" {
		return nil
	}

	sourcePath := "/usr/bin/nvidia-cdi-hook"
	targetPath := filepath.Join(DriverPluginPath, "nvidia-cdi-hook")

	input, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("error reading nvidia-cdi-hook: %w", err)
	}

	if err := os.WriteFile(targetPath, input, 0755); err != nil {
		return fmt.Errorf("error copying nvidia-cdi-hook: %w", err)
	}

	f.nvidiaCDIHookPath = targetPath

	return nil
}
