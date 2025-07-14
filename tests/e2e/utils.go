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
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
	computeDomainV1beta1 "github.com/NVIDIA/k8s-dra-driver-gpu/pkg/nvidia.com/clientset/versioned"
	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/nvidia.com/clientset/versioned/scheme"

	v1Core "k8s.io/api/core/v1"
	k8sV1beta1 "k8s.io/api/resource/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	clientset "k8s.io/client-go/kubernetes"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
)

var packagePath string

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

func CreateOrUpdateComputeDomainsFromFile(ctx context.Context, cli computeDomainV1beta1.Interface, filename, namespace string) ([]string, error) {
	computeDomains, err := newComputeDomainFromFile(filepath.Join(packagePath, "data", filename))
	if err != nil {
		return nil, fmt.Errorf("failed to create ComputeDomain from file: %w", err)
	}

	names := make([]string, len(computeDomains))
	for i, computeDomain := range computeDomains {
		computeDomain.Namespace = namespace

		names[i] = computeDomain.Name

		_, err := cli.ResourceV1beta1().ComputeDomains(namespace).Create(ctx, computeDomain, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to create ComputeDomain: %w", err)
		}
	}
	return names, nil
}

func CreateOrUpdateResourceClaimTemplatesFromFile(ctx context.Context, cli clientset.Interface, filename, namespace string) ([]string, error) {
	rcts, err := newResourceClaimTemplateFromFile(filepath.Join(packagePath, "data", filename))
	if err != nil {
		return nil, fmt.Errorf("failed to create ResourceClaimTemplate from file: %w", err)
	}

	names := make([]string, len(rcts))
	for i, rct := range rcts {
		rct.Namespace = namespace

		names[i] = rct.Name

		_, err := cli.ResourceV1beta1().ResourceClaimTemplates(namespace).Create(ctx, rct, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to create ResourceClaimTemplate: %w", err)
		}
	}
	return names, nil
}

func CreateOrUpdatePodsFromFile(ctx context.Context, cli clientset.Interface, filename, namespace string) ([]string, error) {
	pods, err := newPodFromfile(filepath.Join(packagePath, "data", filename))
	if err != nil {
		return nil, fmt.Errorf("failed to create Pod from file: %w", err)
	}

	names := make([]string, len(pods))
	for i, pod := range pods {
		pod.Namespace = namespace

		names[i] = pod.Name

		_, err := cli.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to create Pod: %w", err)
		}
	}
	return names, nil
}

func newComputeDomainFromFile(path string) ([]*v1beta1.ComputeDomain, error) {
	objs, err := apiObjsFromFile(path, scheme.Codecs.UniversalDeserializer())
	if err != nil {
		return nil, err
	}

	crs := make([]*v1beta1.ComputeDomain, len(objs))

	for i, obj := range objs {
		var ok bool
		crs[i], ok = obj.(*v1beta1.ComputeDomain)
		if !ok {
			return nil, fmt.Errorf("unexpected type %t when reading %q", obj, path)
		}
	}

	return crs, nil
}

func newPodFromfile(path string) ([]*v1Core.Pod, error) {
	objs, err := apiObjsFromFile(path, k8sscheme.Codecs.UniversalDeserializer())
	if err != nil {
		return nil, err
	}

	pods := make([]*v1Core.Pod, len(objs))

	for i, obj := range objs {
		var ok bool
		pods[i], ok = obj.(*v1Core.Pod)
		if !ok {
			return nil, fmt.Errorf("unexpected type %t when reading %q", obj, path)
		}
	}

	return pods, nil
}

func newResourceClaimTemplateFromFile(path string) ([]*k8sV1beta1.ResourceClaimTemplate, error) {
	objs, err := apiObjsFromFile(path, k8sscheme.Codecs.UniversalDeserializer())
	if err != nil {
		return nil, err
	}

	rcts := make([]*k8sV1beta1.ResourceClaimTemplate, len(objs))

	for i, obj := range objs {
		var ok bool
		rcts[i], ok = obj.(*k8sV1beta1.ResourceClaimTemplate)
		if !ok {
			return nil, fmt.Errorf("unexpected type %t when reading %q", obj, path)
		}
	}

	return rcts, nil
}

func apiObjsFromFile(path string, decoder apiruntime.Decoder) ([]apiruntime.Object, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// TODO: find out a nicer way to decode multiple api objects from a single
	// file (K8s must have that somewhere)
	split := bytes.Split(data, []byte("---"))
	objs := []apiruntime.Object{}

	for _, slice := range split {
		if len(slice) == 0 {
			continue
		}
		obj, _, err := decoder.Decode(slice, nil, nil)
		if err != nil {
			return nil, err
		}
		objs = append(objs, obj)
	}
	return objs, err
}

func init() {
	_, thisFile, _, _ := runtime.Caller(0)
	packagePath = filepath.Dir(thisFile)
}
