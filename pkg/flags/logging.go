/*
 * Copyright 2023 The Kubernetes Authors.
 * Copyright 2023 NVIDIA CORPORATION.
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
	"github.com/spf13/pflag"
	"github.com/urfave/cli/v2"

	logsapi "k8s.io/component-base/logs/api/v1"

	_ "k8s.io/component-base/logs/json/register" // for JSON log output support

	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/featuregates"
)

type LoggingConfig struct {
	config *logsapi.LoggingConfiguration
}

// NewLoggingConfig creates a new logging configuration.
func NewLoggingConfig() *LoggingConfig {
	return &LoggingConfig{
		config: logsapi.NewLoggingConfiguration(),
	}
}

// Apply should be called in a cli.App.Before directly after parsing command
// line flags and before running any code which emits log entries.
// It uses the global feature gate singleton.
func (l *LoggingConfig) Apply() error {
	return logsapi.ValidateAndApply(l.config, featuregates.FeatureGates())
}

// Flags returns the flags for logging configuration (NOT including feature gates).
func (l *LoggingConfig) Flags() []cli.Flag {
	var fs pflag.FlagSet

	// This also registers klog configuration flags (such as -v).
	logsapi.AddFlags(l.config, &fs)

	// Note: We do NOT add the feature-gates flag here anymore.
	// That's handled by FeatureGateConfig to maintain proper separation of concerns.

	var flags []cli.Flag
	fs.VisitAll(func(flag *pflag.Flag) {
		flags = append(flags, pflagToCLI(flag, "Logging:"))
	})
	return flags
}
