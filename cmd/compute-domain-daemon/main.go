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
	"syscall"

	"github.com/urfave/cli/v2"
)

const (
	nodesConfig = "/etc/nvidia-imex/nodes_config.cfg"
	imexConfig  = "/etc/nvidia-imex/config.cfg"
	imexLog     = "/var/log/nvidia-imex.log"
	imexBinary  = "/usr/bin/nvidia-imex"
	imexCtl     = "/usr/bin/nvidia-imex-ctl"
)

type Flags struct {
	cliqueID string
}

func main() {
	if err := newApp().Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newApp() *cli.App {
	// Create local flags variable
	var flags Flags

	// Create a wrapper that will be used to gracefully shut down all subcommands
	wrapper := func(ctx context.Context, f func(ctx context.Context, flags *Flags) error) error {
		// Create a cancelable context from the one passed in
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Handle SIGTERM
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGTERM)
		go func() {
			<-sigChan
			cancel()
		}()

		// Call the wrapped function
		return f(ctx, &flags)
	}

	// Create the app
	app := &cli.App{
		Name:  "compute-domain-daemon",
		Usage: "compute-domain-daemon manages the IMEX daemon for NVIDIA compute domains.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "cliqueid",
				Usage:       "The clique ID for this node.",
				EnvVars:     []string{"CLIQUE_ID"},
				Destination: &flags.cliqueID,
			},
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

// run runs the compute domain daemon, checking IMEX capability and managing the IMEX daemon lifecycle.
// It returns an error if any step fails.
func run(ctx context.Context, flags *Flags) error {
	if flags.cliqueID == "" {
		fmt.Println("ClusterUUID and CliqueId are NOT set for GPUs on this node.")
		fmt.Println("The IMEX daemon will not be started.")
		fmt.Println("Sleeping forever...")
		<-ctx.Done()
		return nil
	}

	// Print nodes config
	if err := printNodesConfig(ctx); err != nil {
		return fmt.Errorf("error printing nodes config: %w", err)
	}

	// Run IMEX daemon
	if err := runIMEXDaemon(ctx, imexConfig); err != nil {
		return fmt.Errorf("error running IMEX daemon: %w", err)
	}

	// Tail the log file
	if err := tail(ctx, imexLog); err != nil {
		return fmt.Errorf("error tailing log file: %w", err)
	}

	return nil
}

// check verifies if the node is IMEX capable and if so, checks if the IMEX daemon is ready.
// It returns an error if any step fails.
func check(ctx context.Context, flags *Flags) error {
	if flags.cliqueID == "" {
		fmt.Println("ClusterUUID and CliqueId are NOT set for GPUs on this node.")
		return nil
	}

	// Check if IMEX daemon is ready
	cmd := exec.CommandContext(ctx, imexCtl, "-q", "-i", "127.0.0.1", "50005")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("error checking IMEX daemon status: %w", err)
	}

	if string(output) != "READY\n" {
		return fmt.Errorf("IMEX daemon not ready: %s", string(output))
	}

	return nil
}

// printNodesConfig reads and prints the contents of the nodes configuration file.
// It returns an error if the file cannot be read.
func printNodesConfig(ctx context.Context) error {
	fmt.Printf("%s:\n", nodesConfig)
	content, err := os.ReadFile(nodesConfig)
	if err != nil {
		return fmt.Errorf("failed to read nodes config: %w", err)
	}
	fmt.Println(string(content))
	return nil
}

// runIMEXDaemon starts the IMEX daemon with the specified configuration file.
// It returns an error if the daemon fails to start or exits unexpectedly.
func runIMEXDaemon(ctx context.Context, config string) error {
	cmd := exec.CommandContext(ctx, imexBinary, "-c", config)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// tail continuously reads and prints new lines from the specified file using the system's tail command.
// It starts from the beginning of the file (-n +1) and follows new lines (-f).
// It blocks until the context is cancelled or an error occurs.
func tail(ctx context.Context, path string) error {
	cmd := exec.CommandContext(ctx, "tail", "-n", "+1", "-f", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
