/*
Copyright 2025 The Kubernetes Authors.
Copyright 2025 NVIDIA Corporation.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admissionv1 "k8s.io/api/admission/v1"
	resourceapi "k8s.io/api/resource/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilversion "k8s.io/apimachinery/pkg/util/version"
	"k8s.io/utils/ptr"

	configapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/featuregates"
)

func TestReadyEndpoint(t *testing.T) {
	s := httptest.NewServer(newMux())
	t.Cleanup(s.Close)

	res, err := http.Get(s.URL + "/readyz")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.StatusCode)
}

func TestResourceClaimValidatingWebhook(t *testing.T) {

	tests := map[string]struct {
		featureGates         map[string]bool
		admissionReview      *admissionv1.AdmissionReview
		requestContentType   string
		expectedResponseCode int
		expectedAllowed      bool
		expectedMessage      string
	}{
		"bad contentType": {
			requestContentType:   "invalid type",
			expectedResponseCode: http.StatusUnsupportedMediaType,
		},
		"invalid AdmissionReview": {
			admissionReview:      &admissionv1.AdmissionReview{},
			expectedResponseCode: http.StatusBadRequest,
		},
		"valid GpuConfig in ResourceClaim": {
			featureGates: map[string]bool{
				string(featuregates.TimeSlicingSettings): true,
			},
			admissionReview: admissionReviewWithObject(
				resourceClaimWithGpuConfigs(
					resourceClaimResourceV1Beta1,
					&configapi.GpuConfig{
						Sharing: &configapi.GpuSharing{
							Strategy: configapi.TimeSlicingStrategy,
							TimeSlicingConfig: &configapi.TimeSlicingConfig{
								Interval: ptr.To(configapi.DefaultTimeSlice),
							},
						},
					},
				),
			),
			expectedAllowed: true,
		},
		"invalid GpuConfigs in ResourceClaim": {
			featureGates: map[string]bool{
				string(featuregates.TimeSlicingSettings): true,
				string(featuregates.MPSSupport):          true,
			},
			admissionReview: admissionReviewWithObject(
				resourceClaimWithGpuConfigs(
					resourceClaimResourceV1Beta1,
					&configapi.GpuConfig{
						Sharing: &configapi.GpuSharing{
							Strategy: configapi.TimeSlicingStrategy,
							TimeSlicingConfig: &configapi.TimeSlicingConfig{
								Interval: ptr.To(configapi.TimeSliceInterval("Invalid Interval")),
							},
						},
					},
					&configapi.GpuConfig{
						Sharing: &configapi.GpuSharing{
							Strategy: configapi.MpsStrategy,
							MpsConfig: &configapi.MpsConfig{
								DefaultActiveThreadPercentage: ptr.To(-1),
							},
						},
					},
				),
			),
			expectedAllowed: false,
			expectedMessage: "2 configs failed to validate: object at spec.devices.config[0].opaque.parameters is invalid: unknown time-slice interval: Invalid Interval; object at spec.devices.config[1].opaque.parameters is invalid: active thread percentage must not be negative",
		},
		"valid GpuConfig in ResourceClaimTemplate": {
			featureGates: map[string]bool{
				string(featuregates.TimeSlicingSettings): true,
			},
			admissionReview: admissionReviewWithObject(
				resourceClaimTemplateWithGpuConfigs(
					resourceClaimTemplateResourceV1Beta1,
					&configapi.GpuConfig{
						Sharing: &configapi.GpuSharing{
							Strategy: configapi.TimeSlicingStrategy,
							TimeSlicingConfig: &configapi.TimeSlicingConfig{
								Interval: ptr.To(configapi.DefaultTimeSlice),
							},
						},
					},
				),
			),
			expectedAllowed: true,
		},
		"invalid GpuConfigs in ResourceClaimTemplate": {
			admissionReview: admissionReviewWithObject(
				resourceClaimTemplateWithGpuConfigs(
					resourceClaimTemplateResourceV1Beta1,
					&configapi.GpuConfig{
						Sharing: &configapi.GpuSharing{
							Strategy: configapi.TimeSlicingStrategy,
							TimeSlicingConfig: &configapi.TimeSlicingConfig{
								Interval: ptr.To(configapi.TimeSliceInterval("Invalid Interval")),
							},
						},
					},
					&configapi.GpuConfig{
						Sharing: &configapi.GpuSharing{
							Strategy: configapi.MpsStrategy,
							MpsConfig: &configapi.MpsConfig{
								DefaultActiveThreadPercentage: ptr.To(-1),
							},
						},
					},
				),
			),
			expectedAllowed: false,
			expectedMessage: "2 configs failed to validate: object at spec.spec.devices.config[0].opaque.parameters is invalid: unknown time-slice interval: Invalid Interval; object at spec.spec.devices.config[1].opaque.parameters is invalid: active thread percentage must not be negative",
			featureGates: map[string]bool{
				string(featuregates.TimeSlicingSettings): true,
				string(featuregates.MPSSupport):          true,
			},
		},

		// v1 API version tests
		"valid GpuConfig in ResourceClaim v1": {
			admissionReview: admissionReviewWithObject(
				resourceClaimWithGpuConfigs(
					resourceClaimResourceV1,
					&configapi.GpuConfig{
						Sharing: &configapi.GpuSharing{
							Strategy: configapi.TimeSlicingStrategy,
							TimeSlicingConfig: &configapi.TimeSlicingConfig{
								Interval: ptr.To(configapi.DefaultTimeSlice),
							},
						},
					},
				),
			),
			expectedAllowed: true,
			featureGates: map[string]bool{
				string(featuregates.TimeSlicingSettings): true,
			},
		},
		"valid GpuConfig in ResourceClaimTemplate v1": {
			featureGates: map[string]bool{
				string(featuregates.TimeSlicingSettings): true,
			},
			admissionReview: admissionReviewWithObject(
				resourceClaimTemplateWithGpuConfigs(
					resourceClaimTemplateResourceV1,
					&configapi.GpuConfig{
						Sharing: &configapi.GpuSharing{
							Strategy: configapi.TimeSlicingStrategy,
							TimeSlicingConfig: &configapi.TimeSlicingConfig{
								Interval: ptr.To(configapi.DefaultTimeSlice),
							},
						},
					},
				),
			),
			expectedAllowed: true,
		},
		"invalid GpuConfig in ResourceClaim v1 (tests conversion)": {
			featureGates: map[string]bool{
				string(featuregates.TimeSlicingSettings): true,
			},
			admissionReview: admissionReviewWithObject(
				resourceClaimWithGpuConfigs(
					resourceClaimResourceV1,
					&configapi.GpuConfig{
						Sharing: &configapi.GpuSharing{
							Strategy: configapi.TimeSlicingStrategy,
							TimeSlicingConfig: &configapi.TimeSlicingConfig{
								Interval: ptr.To(configapi.TimeSliceInterval("Invalid Interval")),
							},
						},
					},
				),
			),
			expectedAllowed: false,
			expectedMessage: "1 configs failed to validate: object at spec.devices.config[0].opaque.parameters is invalid: unknown time-slice interval: Invalid Interval",
		},
		"invalid GpuConfig in ResourceClaimTemplate v1 (tests conversion)": {
			featureGates: map[string]bool{
				string(featuregates.MPSSupport): true,
			},
			admissionReview: admissionReviewWithObject(
				resourceClaimTemplateWithGpuConfigs(
					resourceClaimTemplateResourceV1,
					&configapi.GpuConfig{
						Sharing: &configapi.GpuSharing{
							Strategy: configapi.MpsStrategy,
							MpsConfig: &configapi.MpsConfig{
								DefaultActiveThreadPercentage: ptr.To(-1),
							},
						},
					},
				),
			),
			expectedAllowed: false,
			expectedMessage: "1 configs failed to validate: object at spec.spec.devices.config[0].opaque.parameters is invalid: active thread percentage must not be negative",
		},

		// v1beta2 API version tests
		"valid GpuConfig in ResourceClaim v1beta2": {
			featureGates: map[string]bool{
				string(featuregates.TimeSlicingSettings): true,
			},
			admissionReview: admissionReviewWithObject(
				resourceClaimWithGpuConfigs(
					resourceClaimResourceV1Beta2,
					&configapi.GpuConfig{
						Sharing: &configapi.GpuSharing{
							Strategy: configapi.TimeSlicingStrategy,
							TimeSlicingConfig: &configapi.TimeSlicingConfig{
								Interval: ptr.To(configapi.DefaultTimeSlice),
							},
						},
					},
				),
			),
			expectedAllowed: true,
		},
		"valid GpuConfig in ResourceClaimTemplate v1beta2": {
			featureGates: map[string]bool{
				string(featuregates.TimeSlicingSettings): true,
			},
			admissionReview: admissionReviewWithObject(
				resourceClaimTemplateWithGpuConfigs(
					resourceClaimTemplateResourceV1Beta2,
					&configapi.GpuConfig{
						Sharing: &configapi.GpuSharing{
							Strategy: configapi.TimeSlicingStrategy,
							TimeSlicingConfig: &configapi.TimeSlicingConfig{
								Interval: ptr.To(configapi.DefaultTimeSlice),
							},
						},
					},
				),
			),
			expectedAllowed: true,
		},
		"invalid GpuConfig in ResourceClaim v1beta2 (tests conversion)": {
			featureGates: map[string]bool{
				string(featuregates.MPSSupport): true,
			},
			admissionReview: admissionReviewWithObject(
				resourceClaimWithGpuConfigs(
					resourceClaimResourceV1Beta2,
					&configapi.GpuConfig{
						Sharing: &configapi.GpuSharing{
							Strategy: configapi.MpsStrategy,
							MpsConfig: &configapi.MpsConfig{
								DefaultActiveThreadPercentage: ptr.To(-1),
							},
						},
					},
				),
			),
			expectedAllowed: false,
			expectedMessage: "1 configs failed to validate: object at spec.devices.config[0].opaque.parameters is invalid: active thread percentage must not be negative",
		},
		"invalid GpuConfig in ResourceClaimTemplate v1beta2 (tests conversion)": {
			featureGates: map[string]bool{
				string(featuregates.TimeSlicingSettings): true,
			},
			admissionReview: admissionReviewWithObject(
				resourceClaimTemplateWithGpuConfigs(
					resourceClaimTemplateResourceV1Beta2,
					&configapi.GpuConfig{
						Sharing: &configapi.GpuSharing{
							Strategy: configapi.TimeSlicingStrategy,
							TimeSlicingConfig: &configapi.TimeSlicingConfig{
								Interval: ptr.To(configapi.TimeSliceInterval("Invalid Interval")),
							},
						},
					},
				),
			),
			expectedAllowed: false,
			expectedMessage: "1 configs failed to validate: object at spec.spec.devices.config[0].opaque.parameters is invalid: unknown time-slice interval: Invalid Interval",
		},

		// Feature gate disabled tests - these should fail when feature gates are off
		"TimeSlicingStrategy rejected when TimeSlicingSettings feature gate is disabled": {
			featureGates: map[string]bool{
				string(featuregates.TimeSlicingSettings): false,
			},
			admissionReview: admissionReviewWithObject(
				resourceClaimWithGpuConfigs(
					resourceClaimResourceV1Beta1,
					&configapi.GpuConfig{
						Sharing: &configapi.GpuSharing{
							Strategy: configapi.TimeSlicingStrategy,
							TimeSlicingConfig: &configapi.TimeSlicingConfig{
								Interval: ptr.To(configapi.DefaultTimeSlice),
							},
						},
					},
				),
			),
			expectedAllowed: false,
			expectedMessage: "1 configs failed to validate: object at spec.devices.config[0].opaque.parameters is invalid: unknown GPU sharing strategy: TimeSlicing",
		},
		"MpsStrategy rejected when MPSSupport feature gate is disabled": {
			featureGates: map[string]bool{
				string(featuregates.MPSSupport): false,
			},
			admissionReview: admissionReviewWithObject(
				resourceClaimWithGpuConfigs(
					resourceClaimResourceV1Beta1,
					&configapi.GpuConfig{
						Sharing: &configapi.GpuSharing{
							Strategy: configapi.MpsStrategy,
							MpsConfig: &configapi.MpsConfig{
								DefaultActiveThreadPercentage: ptr.To(50),
							},
						},
					},
				),
			),
			expectedAllowed: false,
			expectedMessage: "1 configs failed to validate: object at spec.devices.config[0].opaque.parameters is invalid: unknown GPU sharing strategy: MPS",
		},
		"TimeSlicingStrategy rejected in ResourceClaimTemplate when TimeSlicingSettings feature gate is disabled": {
			featureGates: map[string]bool{
				string(featuregates.TimeSlicingSettings): false,
			},
			admissionReview: admissionReviewWithObject(
				resourceClaimTemplateWithGpuConfigs(
					resourceClaimTemplateResourceV1Beta1,
					&configapi.GpuConfig{
						Sharing: &configapi.GpuSharing{
							Strategy: configapi.TimeSlicingStrategy,
							TimeSlicingConfig: &configapi.TimeSlicingConfig{
								Interval: ptr.To(configapi.DefaultTimeSlice),
							},
						},
					},
				),
			),
			expectedAllowed: false,
			expectedMessage: "1 configs failed to validate: object at spec.spec.devices.config[0].opaque.parameters is invalid: unknown GPU sharing strategy: TimeSlicing",
		},
		"MpsStrategy rejected in ResourceClaimTemplate when MPSSupport feature gate is disabled": {
			featureGates: map[string]bool{
				string(featuregates.MPSSupport): false,
			},
			admissionReview: admissionReviewWithObject(
				resourceClaimTemplateWithGpuConfigs(
					resourceClaimTemplateResourceV1Beta1,
					&configapi.GpuConfig{
						Sharing: &configapi.GpuSharing{
							Strategy: configapi.MpsStrategy,
							MpsConfig: &configapi.MpsConfig{
								DefaultActiveThreadPercentage: ptr.To(50),
							},
						},
					},
				),
			),
			expectedAllowed: false,
			expectedMessage: "1 configs failed to validate: object at spec.spec.devices.config[0].opaque.parameters is invalid: unknown GPU sharing strategy: MPS",
		},
	}

	s := httptest.NewServer(newMux())
	t.Cleanup(s.Close)

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			err := featuregates.FeatureGates.SetEmulationVersion(utilversion.MajorMinor(999, 999))
			require.NoError(t, err)

			if test.featureGates != nil {
				err := featuregates.FeatureGates.SetFromMap(test.featureGates)
				require.NoError(t, err)
			}

			requestBody, err := json.Marshal(test.admissionReview)
			require.NoError(t, err)

			contentType := test.requestContentType
			if contentType == "" {
				contentType = "application/json"
			}

			res, err := http.Post(s.URL+"/validate-resource-claim-parameters", contentType, bytes.NewReader(requestBody))
			require.NoError(t, err)
			expectedResponseCode := test.expectedResponseCode
			if expectedResponseCode == 0 {
				expectedResponseCode = http.StatusOK
			}
			assert.Equal(t, expectedResponseCode, res.StatusCode)
			if res.StatusCode != http.StatusOK {
				// We don't have an AdmissionReview to validate
				return
			}

			responseBody, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			res.Body.Close()

			responseAdmissionReview, err := readAdmissionReview(responseBody)
			assert.NoError(t, err)
			assert.Equal(t, test.expectedAllowed, responseAdmissionReview.Response.Allowed)
			if !test.expectedAllowed {
				assert.Equal(t, test.expectedMessage, string(responseAdmissionReview.Response.Result.Message))
			}
		})
	}
}

func admissionReviewWithObject(obj runtime.Object) *admissionv1.AdmissionReview {
	// Extract GVR from the object's GVK
	gvk := obj.GetObjectKind().GroupVersionKind()
	resource := metav1.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: strings.ToLower(gvk.Kind) + "s", // Convert Kind to resource name
	}

	requestedAdmissionReview := &admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			Resource: resource,
			Object: runtime.RawExtension{
				Object: obj,
			},
		},
	}
	requestedAdmissionReview.SetGroupVersionKind(admissionv1.SchemeGroupVersion.WithKind("AdmissionReview"))
	return requestedAdmissionReview
}

func resourceClaimWithGpuConfigs(gvr metav1.GroupVersionResource, gpuConfigs ...*configapi.GpuConfig) *resourceapi.ResourceClaim {
	resourceClaim := &resourceapi.ResourceClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: gvr.Group + "/" + gvr.Version,
			Kind:       "ResourceClaim",
		},
		Spec: resourceClaimSpecWithGpuConfigs(gpuConfigs...),
	}
	return resourceClaim
}

func resourceClaimTemplateWithGpuConfigs(gvr metav1.GroupVersionResource, gpuConfigs ...*configapi.GpuConfig) *resourceapi.ResourceClaimTemplate {
	resourceClaimTemplate := &resourceapi.ResourceClaimTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: gvr.Group + "/" + gvr.Version,
			Kind:       "ResourceClaimTemplate",
		},
		Spec: resourceapi.ResourceClaimTemplateSpec{
			Spec: resourceClaimSpecWithGpuConfigs(gpuConfigs...),
		},
	}
	return resourceClaimTemplate
}

func resourceClaimSpecWithGpuConfigs(gpuConfigs ...*configapi.GpuConfig) resourceapi.ResourceClaimSpec {
	resourceClaimSpec := resourceapi.ResourceClaimSpec{}
	for _, gpuConfig := range gpuConfigs {
		gpuConfig.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   configapi.GroupName,
			Version: configapi.Version,
			Kind:    "GpuConfig",
		})
		deviceConfig := resourceapi.DeviceClaimConfiguration{
			DeviceConfiguration: resourceapi.DeviceConfiguration{
				Opaque: &resourceapi.OpaqueDeviceConfiguration{
					Driver: DriverName,
					Parameters: runtime.RawExtension{
						Object: gpuConfig,
					},
				},
			},
		}
		resourceClaimSpec.Devices.Config = append(resourceClaimSpec.Devices.Config, deviceConfig)
	}
	return resourceClaimSpec
}
