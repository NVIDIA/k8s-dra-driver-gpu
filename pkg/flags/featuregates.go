/*
 * Copyright 2024 NVIDIA CORPORATION.
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

	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/featuregates"
)

// FeatureGateConfig manages the unified feature gate registry containing both
// project-specific and standard Kubernetes feature gates.
type FeatureGateConfig struct{}

// NewFeatureGateConfig creates a new unified feature gate configuration.
func NewFeatureGateConfig() *FeatureGateConfig {
	return &FeatureGateConfig{}
}

// Flags returns the CLI flags for the unified feature gate configuration.
func (f *FeatureGateConfig) Flags() []cli.Flag {
	var fs pflag.FlagSet

	// Add the unified feature gates flag containing both project and logging features
	fs.AddFlag(&pflag.Flag{
		Name: "feature-gates",
		Usage: "A set of key=value pairs that describe feature gates for alpha/experimental features. " +
			"Options are:\n     " + strings.Join(featuregates.KnownFeatures(), "\n     "),
		Value: featuregates.FeatureGates().(pflag.Value), //nolint:forcetypeassert // No need for type check: FeatureGates is a *featuregate.featureGate, which implements pflag.Value.
	})

	var flags []cli.Flag
	fs.VisitAll(func(flag *pflag.Flag) {
		flags = append(flags, pflagToCLI(flag, "Feature Gates:"))
	})
	return flags
}
