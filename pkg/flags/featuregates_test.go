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
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
	"k8s.io/component-base/featuregate"

	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/featuregates"
)

// Test feature constants for different scenarios.
var (
	// testValidDisabledFeature is a feature that is disabled by default
	// This can be changed to any other valid disabled feature for testing purposes.
	testValidDisabledFeature = featuregate.Feature("AllAlpha")

	// testValidEnabledFeature is a feature that is enabled by default in our system
	// This can be changed to any other valid enabled feature for testing purposes.
	testValidEnabledFeature = featuregate.Feature("AllBeta")
)

// =============================================================================
// Core Functionality Tests
// =============================================================================

func TestFeatureGateConfigBasicFunctionality(t *testing.T) {
	// Test that the config provides basic feature gate functionality
	fg := featuregates.GetFeatureGates()

	// Test that the feature gate is functional
	require.NotNil(t, fg, "FeatureGate should not be nil")

	// Test that we can query known features
	knownFeatures := fg.KnownFeatures()
	require.NotEmpty(t, knownFeatures, "Should have some known features available")

	// Test that the feature gate system is working by testing with a real project feature
	// This tests integration without depending on specific feature values
	require.NotPanics(t, func() {
		// Query an actual test feature that exists in our codebase
		// We don't care about the value, just that the system works
		_ = featuregates.Enabled(testValidDisabledFeature)
	}, "Should be able to query actual project features without panic")

	// Test that the Enabled method works correctly and is consistent
	require.NotPanics(t, func() {
		_ = featuregates.Enabled(testValidDisabledFeature)
	}, "Should be able to query existing features")

	// The result should be consistent between calls
	result1 := featuregates.Enabled(testValidDisabledFeature)
	result2 := featuregates.Enabled(testValidDisabledFeature)
	require.Equal(t, result1, result2, "Enabled method should return consistent results")
}

func TestFeatureGateConfigSeparationOfConcerns(t *testing.T) {
	// Test that FeatureGateConfig owns feature gates and LoggingConfig consumes them
	featureGateConfig := NewFeatureGateConfig()
	loggingConfig := NewLoggingConfig()

	// FeatureGateConfig should provide feature gate flags
	featureFlags := featureGateConfig.Flags()
	require.NotEmpty(t, featureFlags, "FeatureGateConfig should provide feature gate flags")

	// LoggingConfig should provide logging flags (not feature gate flags)
	loggingFlags := loggingConfig.Flags()
	require.NotNil(t, loggingFlags, "LoggingConfig should provide logging flags")
}

// =============================================================================
// Feature Gate Operations Tests
// =============================================================================

func TestFeatureGateConfigSetFromMap(t *testing.T) {
	t.Run("EnableDisabledFeature", func(t *testing.T) {
		// Test that we can enable a disabled feature
		fg := featuregates.GetFeatureGates()

		// Test enabling a disabled feature - we know AllAlpha starts as false
		require.False(t, featuregates.Enabled(testValidDisabledFeature), "AllAlpha feature should start as false")

		// Enable it through SetFromMap (simulating CLI input)
		err := fg.SetFromMap(map[string]bool{
			string(testValidDisabledFeature): true,
		})
		require.NoError(t, err, "Should be able to enable disabled feature through SetFromMap")

		// Verify the config reflects the change
		require.True(t, featuregates.Enabled(testValidDisabledFeature), "Feature should be enabled after SetFromMap")

		// Reset to original disabled state
		err = fg.SetFromMap(map[string]bool{
			string(testValidDisabledFeature): false,
		})
		require.NoError(t, err, "Should be able to reset feature to disabled state")
	})

	t.Run("DisableEnabledFeature", func(t *testing.T) {
		// Test that we can disable an enabled feature
		fg := featuregates.GetFeatureGates()

		// First enable AllBeta so we can test disabling it
		err := fg.SetFromMap(map[string]bool{
			string(testValidEnabledFeature): true,
		})
		require.NoError(t, err, "Should be able to enable AllBeta feature")

		// Verify it's enabled
		require.True(t, featuregates.Enabled(testValidEnabledFeature), "AllBeta feature should be enabled")

		// Now test disabling it through SetFromMap (simulating CLI input)
		err = fg.SetFromMap(map[string]bool{
			string(testValidEnabledFeature): false,
		})
		require.NoError(t, err, "Should be able to disable enabled feature through SetFromMap")

		// Verify the config reflects the change
		require.False(t, featuregates.Enabled(testValidEnabledFeature), "Feature should be disabled after SetFromMap")

		// Reset to original disabled state
		err = fg.SetFromMap(map[string]bool{
			string(testValidEnabledFeature): false,
		})
		require.NoError(t, err, "Should be able to reset feature to disabled state")
	})
}

func TestFeatureGateConfigKnownFeatures(t *testing.T) {
	fg := featuregates.GetFeatureGates()

	knownFeatures := fg.KnownFeatures()
	require.NotEmpty(t, knownFeatures, "Should have some known features")

	// Test that the known features list is functional
	// Check that it contains descriptive strings (not just feature names)
	foundDescriptive := false
	for _, feature := range knownFeatures {
		if strings.Contains(feature, "=") && (strings.Contains(feature, "ALPHA") || strings.Contains(feature, "BETA") || strings.Contains(feature, "GA")) {
			foundDescriptive = true
			break
		}
	}
	require.True(t, foundDescriptive, "KnownFeatures should return descriptive feature information")
}

func TestFeatureGateConfigErrorHandling(t *testing.T) {
	fg := featuregates.GetFeatureGates()

	// Test error handling with invalid features
	err := fg.SetFromMap(map[string]bool{
		"NonExistentFeature": true,
	})
	require.Error(t, err, "Should error when trying to set non-existent feature")

	// Test that we can still query valid features after error
	require.NotPanics(t, func() {
		_ = featuregates.Enabled(testValidDisabledFeature)
	}, "Should be able to query valid features even after error")
}

// =============================================================================
// CLI/Flags Integration Tests
// =============================================================================

func TestFeatureGateConfigFlagsGeneration(t *testing.T) {
	config := NewFeatureGateConfig()
	flags := config.Flags()

	require.NotEmpty(t, flags, "Should generate feature gate flags")

	// Find the feature-gates flag
	var featureGatesFlag *cli.Flag
	for i := range flags {
		flag := flags[i]
		// Check if this is our feature-gates flag by examining the usage
		if strings.Contains(flag.String(), "feature-gates") {
			featureGatesFlag = &flag
			break
		}
	}

	require.NotNil(t, featureGatesFlag, "Should have a feature-gates flag")
	require.Contains(t, (*featureGatesFlag).String(), "alpha/experimental", "Feature gates flag should mention alpha/experimental features")
}

func TestFeatureGateConfigCLIFlagIntegration(t *testing.T) {
	// Test that the generated CLI flags properly describe available features
	config := NewFeatureGateConfig()
	flags := config.Flags()

	require.NotEmpty(t, flags, "Should generate CLI flags")

	// The flag should describe available features
	var flagUsage string
	for _, flag := range flags {
		flagUsage = flag.String()
		if strings.Contains(flagUsage, "feature-gates") {
			break
		}
	}

	require.NotEmpty(t, flagUsage, "Should have feature-gates flag usage")

	// Should mention key concepts
	require.Contains(t, flagUsage, "alpha/experimental", "Should mention alpha/experimental features")
	require.Contains(t, flagUsage, "key=value", "Should mention key=value format")
}

// Test Feature Gate Map Conversion.
func TestFeatureGateConfigToMap(t *testing.T) {
	// Test with default configuration (should include known features)
	result := featuregates.ToMap()

	// Should not be empty since we have known features
	require.NotEmpty(t, result, "ToMap should not return empty map when features are known")

	// Should be a proper map[string]bool
	require.IsType(t, map[string]bool{}, result, "ToMap should return map[string]bool")

	// Verify each entry has correct types and values
	for featureName, enabled := range result {
		require.IsType(t, "", featureName, "Feature name should be string")
		require.IsType(t, true, enabled, "Feature value should be bool")

		// Verify the feature name is actually known
		allFeatures := featuregates.ToMap()
		_, found := allFeatures[featureName]
		require.True(t, found, "Feature name should be in known features map")
	}

	// Test specific features we know about
	require.Contains(t, result, "AllAlpha", "Should contain AllAlpha")
	require.Equal(t, false, result["AllAlpha"], "AllAlpha should have default value false")
	require.Contains(t, result, "AllBeta", "Should contain AllBeta")
	require.Equal(t, false, result["AllBeta"], "AllBeta should have default value false")
}
