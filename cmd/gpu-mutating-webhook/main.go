/**
# Copyright 2025 NVIDIA CORPORATION
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
**/

package main

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

const (
	tlsDir          = `/etc/webhook/tls`
	tlsCertFile     = `tls.crt`
	tlsKeyFile      = `tls.key`
	gpuResourceName = "nvidia.com/gpu"
	gpuClaimName    = "nvidia-gpu-resourceclaim"
	gpuTemplateName = "nvidia-gpu-resourceclaim-template"
)

var (
	podResource = metav1.GroupVersionResource{Version: "v1", Resource: "pods"}
)

func applyGPUMutation(req *admissionv1.AdmissionRequest) ([]patchOperation, error) {
	// Only mutate Pod CREATE
	// Swati: may be add UPDATE
	if req.Resource != podResource || req.Operation != admissionv1.Create {
		klog.Infof("skip mutation for %v/%v", req.Resource, req.Operation)
		return nil, nil
	}

	var pod corev1.Pod
	if _, _, err := universalDeserializer.Decode(req.Object.Raw, nil, &pod); err != nil {
		klog.Errorf("failed to decode Pod: %v", err)
		return nil, fmt.Errorf("could not deserialize pod: %w", err)
	}

	key := escapeJSONPointer(gpuResourceName)
	var patches []patchOperation
	var ctrGPUResourceClaims []string

	// Iterate on all containers and check for "nvidia.com/gpu" limits
	// using the logic described here for prefering limits over requests
	// GPUs are only supposed to be specified in the limits section, meaning
	// - can specify GPU limits without specifying requests. limit will be used as request value by default
	// - can specify GPU in both limits and requests but they must be equal
	// - cannot specify GPU requests without specifying limits
	// refer: https://kubernetes.io/docs/tasks/manage-gpus/scheduling-gpus/#using-device-plugins
	for ci, ctr := range pod.Spec.Containers {
		ctrName := ctr.Name
		limitCount, limitOk := ctr.Resources.Limits[gpuResourceName]

		// skip if no GPUs in limits
		if !limitOk || limitCount.Value() < 1 {
			continue
		}
		gpuCount := limitCount.Value()

		// check any GPUs in requests
		// it must be equal to limits
		if reqCount, reqOK := ctr.Resources.Requests[gpuResourceName]; reqOK {
			if reqCount.Value() != gpuCount {
				klog.Warningf("container[%q]: gpu request (%d) != limit (%d), skipping mutation", ctrName, reqCount.Value(), gpuCount)
				continue
			}
			reqPatch := removeResourceRequest(ci, "requests", key)
			patches = append(patches, reqPatch)
			klog.Infof("removed container[%q].Resources.Requests: %v", ctrName, reqPatch)
		}
		limitPatch := removeResourceRequest(ci, "limits", key)
		patches = append(patches, limitPatch)
		klog.Infof("removed container[%q].Resources.Limits: %v", ctrName, limitPatch)

		// ensure container-claims slice exists
		// this is JSON way to first creating the field if it does not exist and append later with "-"
		if len(ctr.Resources.Claims) == 0 {
			createPatch := createClaimPatch(fmt.Sprintf("/spec/containers/%d/resources/claims", ci))
			patches = append(patches, createPatch)
			klog.Infof("created container[%q] empty claims array: %v", ctrName, createPatch)
		}

		// append one claim per GPU
		for i := int64(0); i < gpuCount; i++ {
			claimName := fmt.Sprintf("%s-%d", gpuClaimName, i)
			ctrGPUResourceClaims = append(ctrGPUResourceClaims, claimName)
			appendPatch := appendClaimPatch(
				fmt.Sprintf("/spec/containers/%d/resources/claims", ci),
				map[string]string{"name": claimName},
			)
			patches = append(patches, appendPatch)
			klog.Infof("added to container[%q].Resources.Claims: %v", ctrName, appendPatch)
		}
	}

	// Add claims pod-level
	podName := pod.Name
	if len(ctrGPUResourceClaims) > 0 {
		// ensure pod-claims slice exists
		if len(pod.Spec.ResourceClaims) == 0 {
			createPatch := createClaimPatch("/spec/resourceClaims")
			patches = append(patches, createPatch)
			klog.Infof("created pod[%q] empty claims array: %v", podName, createPatch)
		}

		// append each container GPU claim at pod-level
		for _, name := range ctrGPUResourceClaims {
			appendPatch := appendClaimPatch(
				"/spec/resourceClaims",
				map[string]string{
					"name":                      name,
					"resourceClaimTemplateName": gpuTemplateName,
				},
			)
			patches = append(patches, appendPatch)
			klog.Infof("added ResourceClaim %q (template=%q) to %q: %v", name, gpuTemplateName, podName, appendPatch)
		}
	}

	return patches, nil
}

// escapeJSONPointer replace "/" with "~1"
// refer: https://github.com/json-patch/json-patch-tests/issues/42
// needed for "nvidia.com/gpu". otherwise JSON will treat "/" as a path delimiter and treat "gpu" as new field
func escapeJSONPointer(s string) string {
	return strings.ReplaceAll(s, "/", "~1")
}

// removeResourceRequest removes either .resources.requests or .resources.limits
func removeResourceRequest(ci int, field, key string) patchOperation {
	return patchOperation{
		Op:   "remove",
		Path: fmt.Sprintf("/spec/containers/%d/resources/%s/%s", ci, field, key),
	}
}

// createClaimPatch creates an empty slice at the given path
func createClaimPatch(path string) patchOperation {
	return patchOperation{
		Op:    "add",
		Path:  path,
		Value: []map[string]string{},
	}
}

// appendClaimPatch appends to the slice at path
// "-" is JSON way to inserting at the end of the array when no index is specified.
// refer: https://datatracker.ietf.org/doc/html/rfc6902
func appendClaimPatch(path string, entry map[string]string) patchOperation {
	return patchOperation{
		Op:    "add",
		Path:  path + "/-",
		Value: entry,
	}
}

func main() {
	certPath := filepath.Join(tlsDir, tlsCertFile)
	keyPath := filepath.Join(tlsDir, tlsKeyFile)

	mux := http.NewServeMux()
	mux.Handle("/mutate", admitFuncHandler(applyGPUMutation))

	server := &http.Server{
		Addr:    ":8443",
		Handler: mux,
	}

	if err := server.ListenAndServeTLS(certPath, keyPath); err != nil {
		// Swati: need better error handling here
		log.Fatalf("Failed to start server: %v", err)
	}
	klog.Infof("Started gpu-mutating-webhook server at %s", server.Addr)
}
