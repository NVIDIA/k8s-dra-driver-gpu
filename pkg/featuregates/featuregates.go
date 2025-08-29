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

package featuregates

import (
	"strings"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/version"
	"k8s.io/component-base/featuregate"
	logsapi "k8s.io/component-base/logs/api/v1"

	"github.com/NVIDIA/k8s-dra-driver-gpu/internal/info"
)

const (
	// TimeSlicingSettings allows timeslicing settings to be customized.
	TimeSlicingSettings featuregate.Feature = "TimeSlicingSettings"

	// MPSSupport allows MPS (Multi-Process Service) settings to be specified.
	MPSSupport featuregate.Feature = "MPSSupport"

	// IMEXDaemonsWithDNSNames allows using DNS names instead of raw IPs for IMEX daemons.
	IMEXDaemonsWithDNSNames featuregate.Feature = "IMEXDaemonsWithDNSNames"
)

// FeatureGates is a singleton representing the set of all feature gates and their values.
// It contains both project-specific feature gates and standard Kubernetes logging feature gates.
var FeatureGates featuregate.MutableVersionedFeatureGate

// defaultFeatureGates contains the default settings for all project-specific feature gates.
// These will be registered with the standard Kubernetes feature gate system.
var defaultFeatureGates = map[featuregate.Feature]featuregate.VersionedSpecs{
	TimeSlicingSettings: {
		{
			Default:    false,
			PreRelease: featuregate.Alpha,
			Version:    version.MajorMinor(25, 8),
		},
	},
	MPSSupport: {
		{
			Default:    false,
			PreRelease: featuregate.Alpha,
			Version:    version.MajorMinor(25, 8),
		},
	},
	IMEXDaemonsWithDNSNames: {
		{
			Default:    false,
			PreRelease: featuregate.Alpha,
			Version:    version.MajorMinor(25, 8),
		},
	},
}

// init instantiates and sets the singleton 'FeatureGates' variable with newFeatureGates().
func init() {
	FeatureGates = newFeatureGates(parseProjectVersion())
}

// parseProjectVersion parses the project version string and returns major.minor version.
func parseProjectVersion() *version.Version {
	versionStr := info.GetVersionParts()[0]
	v := version.MustParse(strings.TrimPrefix(versionStr, "v"))
	return version.MajorMinor(v.Major(), v.Minor())
}

// newFeatureGates instantiates a new set of feature gates with both standard Kubernetes
// logging feature gates and project-specific feature gates, along with appropriate default values.
// Mostly used for testing.
func newFeatureGates(version *version.Version) featuregate.MutableVersionedFeatureGate {
	// Create a versioned feature gate with the specified version
	// This ensures proper version handling for our feature gates
	fg := featuregate.NewVersionedFeatureGate(version)

	// Add standard Kubernetes logging feature gates
	utilruntime.Must(logsapi.AddFeatureGates(fg))

	// Add project-specific feature gates
	utilruntime.Must(fg.AddVersioned(defaultFeatureGates))

	// Override default logging feature gate values
	loggingOverrides := map[string]bool{
		string(logsapi.ContextualLogging): true,
	}
	utilruntime.Must(fg.SetFromMap(loggingOverrides))

	return fg
}

// Enabled returns true if the specified feature gate is enabled in the global FeatureGates singleton.
// This is a convenience function that uses the global feature gate registry.
func Enabled(feature featuregate.Feature) bool {
	return FeatureGates.Enabled(feature)
}

// KnownFeatures returns a list of known feature gates with their descriptions.
func KnownFeatures() []string {
	return FeatureGates.KnownFeatures()
}

// ToMap returns all known feature gates as a map[string]bool suitable for
// template rendering (e.g., {"FeatureA": true, "FeatureB": false}).
// Returns an empty map if no feature gates are configured.
func ToMap() map[string]bool {
	result := make(map[string]bool)
	for feature := range FeatureGates.GetAll() {
		result[string(feature)] = FeatureGates.Enabled(feature)
	}
	return result
}
