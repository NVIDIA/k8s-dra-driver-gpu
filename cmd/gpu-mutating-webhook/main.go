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
)

const (
	tlsDir      = `/etc/webhook/tls`
	tlsCertFile = `tls.crt`
	tlsKeyFile  = `tls.key`
)

var (
	podResource     = metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	gpuClaimName    = "nvidia-gpu-resourceclaim"
	gpuTemplateName = "nvidia-gpu-resourceclaim-template"
)

func applyGPUMutation(req *admissionv1.AdmissionRequest) ([]patchOperation, error) {
	// Only mutate if the incoming resource is a Pod CREATE request.
	if req.Resource != podResource {
		log.Printf("applyGPUMutation invoked for a non-Pod resource: %v", req.Resource)
		return nil, nil
	}
	if req.Operation != admissionv1.Create {
		log.Printf("applyGPUMutation invoked for operation %s, ignoring", req.Operation)
		return nil, nil
	}

	raw := req.Object.Raw
	var pod corev1.Pod
	if _, _, err := universalDeserializer.Decode(raw, nil, &pod); err != nil {
		return nil, fmt.Errorf("could not deserialize pod object: %v", err)
	}

	var patches []patchOperation

	// Check if the Pod already has a resource claim
	hasGPUClaim := false
	for _, rc := range pod.Spec.ResourceClaims {
		if rc.Name == gpuClaimName {
			hasGPUClaim = true
			break
		}
	}

	// Escape "nvidia.com/gpu" for JSON Patch
	escapedGPUKey := strings.ReplaceAll(strings.ReplaceAll("nvidia.com/gpu", "~", "~0"), "/", "~1")

	for i, c := range pod.Spec.Containers {
		foundGPU := false

		if _, ok := c.Resources.Requests["nvidia.com/gpu"]; ok {
			foundGPU = true
			patches = append(patches, patchOperation{
				Op:   "remove",
				Path: fmt.Sprintf("/spec/containers/%d/resources/requests/%s", i, escapedGPUKey),
			})
		}

		if _, ok := c.Resources.Limits["nvidia.com/gpu"]; ok {
			foundGPU = true
			patches = append(patches, patchOperation{
				Op:   "remove",
				Path: fmt.Sprintf("/spec/containers/%d/resources/limits/%s", i, escapedGPUKey),
			})
		}

		if foundGPU {
			gpuClaimPresent := false
			for _, claimRef := range c.Resources.Claims {
				if claimRef.Name == gpuClaimName {
					gpuClaimPresent = true
					break
				}
			}
			if !gpuClaimPresent {
				if c.Resources.Claims == nil {
					patches = append(patches, patchOperation{
						Op:   "add",
						Path: fmt.Sprintf("/spec/containers/%d/resources/claims", i),
						Value: []map[string]string{
							{"name": gpuClaimName},
						},
					})
				} else {
					patches = append(patches, patchOperation{
						Op:    "add",
						Path:  fmt.Sprintf("/spec/containers/%d/resources/claims/-", i),
						Value: map[string]string{"name": gpuClaimName},
					})
				}
			}
		}
	}

	if len(patches) > 0 && !hasGPUClaim {
		newClaim := map[string]string{
			"name":                      gpuClaimName,
			"resourceClaimTemplateName": gpuTemplateName,
		}

		if pod.Spec.ResourceClaims == nil {
			patches = append(patches, patchOperation{
				Op:   "add",
				Path: "/spec/resourceClaims",
				Value: []map[string]string{
					newClaim,
				},
			})
		} else {
			patches = append(patches, patchOperation{
				Op:    "add",
				Path:  "/spec/resourceClaims/-",
				Value: newClaim,
			})
		}
		log.Printf("Added ResourceClaim %q referencing template %q to Pod %q",
			gpuClaimName, gpuTemplateName, pod.Name)
	}

	return patches, nil
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
	log.Printf("Starting webhook server on %s", server.Addr)
	log.Fatal(server.ListenAndServeTLS(certPath, keyPath))
}
