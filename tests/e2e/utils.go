/*
 * SPDX-FileCopyrightText: Copyright (c) 2025 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
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

package e2e

import (
	"bufio"
	"regexp"
	"strings"

	"github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"

	v1Core "k8s.io/api/core/v1"
	k8sV1beta1 "k8s.io/api/resource/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newGpuResourceClaimTemplate returns a new ResourceClaimTemplate prepopulated
func newGpuResourceClaimTemplate(name, namespace string) *k8sV1beta1.ResourceClaimTemplate {
	return &k8sV1beta1.ResourceClaimTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "resource.k8s.io/v1beta1",
			Kind:       "ResourceClaimTemplate",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: k8sV1beta1.ResourceClaimTemplateSpec{
			Spec: k8sV1beta1.ResourceClaimSpec{
				Devices: k8sV1beta1.DeviceClaim{
					Requests: []k8sV1beta1.DeviceRequest{
						{
							Name:            "gpu",
							DeviceClassName: "gpu.nvidia.com",
						},
					},
				},
			},
		},
	}
}

// newComputeDomain returns a new ComputeDomain prepopulated
func newComputeDomain(name, namespace string) *v1beta1.ComputeDomain {
	return &v1beta1.ComputeDomain{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "resource.k8s.io/v1beta1",
			Kind:       "ComputeDomain",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1beta1.ComputeDomainSpec{
			NumNodes: 1,
			Channel: &v1beta1.ComputeDomainChannelSpec{
				ResourceClaimTemplate: v1beta1.ComputeDomainResourceClaimTemplate{
					Name: "test-channel-0",
				},
			},
		},
	}
}

// createPod creates a new Pod with the specified name and namespace
func createPod(namespace, podName string) *v1Core.Pod {
	// Define a minimal Pod spec.
	return &v1Core.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.nvidia.com": "k8s-dra-driver-gpu-test-app",
			},
		},
		Spec: v1Core.PodSpec{
			Containers: []v1Core.Container{
				{
					Name:  "dra-test-container",
					Image: "ubuntu:22.04",
					Ports: []v1Core.ContainerPort{
						{ContainerPort: 80},
					},
				},
			},
		},
	}
}

// validatePodLogs checks if each non-empty line in the provided logs
// matches the expected GPU log format:
// e.g., "GPU 0: Tesla T4 (UUID: GPU-0f8bf69e-2250-1558-891a-7b16b85ab251)"
func validatePodLogs(logs string) bool {
	// Regex:
	// ^GPU\s+\d+:       -> "GPU", spaces, digits, colon.
	// \s+.+\s+          -> spaces, GPU name, spaces.
	// \(UUID:\s+GPU-[0-9a-fA-F-]+\)$ -> "(UUID:" spaces, "GPU-" and the UUID format until ")"
	pattern := `^GPU\s+\d+:\s+.+\s+\(UUID:\s+GPU-[0-9a-fA-F-]+\)$`
	re := regexp.MustCompile(pattern)

	scanner := bufio.NewScanner(strings.NewReader(logs))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue // Skip empty lines.
		}
		if !re.MatchString(line) {
			return false
		}
	}
	if err := scanner.Err(); err != nil {
		return false
	}
	return true
}
