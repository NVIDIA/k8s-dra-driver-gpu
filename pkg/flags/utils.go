/*
 * Copyright 2025 NVIDIA CORPORATION.
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

package flags

import (
	"strings"

	"github.com/spf13/pflag"
	"github.com/urfave/cli/v2"
	"k8s.io/apimachinery/pkg/util/dump"
	"k8s.io/klog/v2"

	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/featuregates"
)

func pflagToCLI(flag *pflag.Flag, category string) cli.Flag {
	return &cli.GenericFlag{
		Name:        flag.Name,
		Category:    category,
		Usage:       flag.Usage,
		Value:       flag.Value,
		Destination: flag.Value,
		EnvVars:     []string{strings.ToUpper(strings.ReplaceAll(flag.Name, "-", "_"))},
	}
}

func LogStartupConfig(parsedFlags any, loggingConfig *LoggingConfig) {
	// Always log component startup config (level 0).
	klog.Infof("\nFeature gates: %#v\nVerbosity: %d\nFlags: %s",
		// Flat boolean map -- no pretty-printing needed.
		featuregates.ToMap(),
		loggingConfig.config.Verbosity,
		// Based on go-spew's Sdump(), with indentation. Type information is
		// always displayed (cannot be disabled).
		dump.Pretty(parsedFlags),
	)

	// This is a complex object, comprised of largely static default klog
	// component configuration. Various parts can be overridden via environment
	// variables or CLI flags: it makes sense to log the interpolated config,
	// but only on a high verbosity level.
	klog.V(6).Infof("Logging config: %s", dump.Pretty(loggingConfig))
}
