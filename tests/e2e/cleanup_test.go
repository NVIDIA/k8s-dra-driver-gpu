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
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

// waitForDeletion polls the provided checkFunc until a NotFound error is returned,
// confirming that the resource is deleted.
func waitForDeletion(resourceName string, checkFunc func() error) error {
	timeout := 2 * time.Minute
	interval := 5 * time.Second
	start := time.Now()
	for {
		err := checkFunc()
		if err != nil && errors.IsNotFound(err) {
			return nil
		}
		if time.Since(start) > timeout {
			return fmt.Errorf("timed out waiting for deletion of %s", resourceName)
		}
		time.Sleep(interval)
	}
}

// cleanupCRDs deletes specific CRDs used during testing.
func cleanupCRDs() {
	crds := []string{
		"computedomains.resource.nvidia.com",
	}
	if EnableGFD {
		crds = append(crds, "nodefeatures.nfd.k8s-sigs.io", "nodefeaturerules.nfd.k8s-sigs.io")
	}

	for _, crd := range crds {
		err := extClient.ApiextensionsV1().CustomResourceDefinitions().Delete(ctx, crd, metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())

		_ = waitForDeletion(crd, func() error {
			_, err := extClient.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, crd, metav1.GetOptions{})
			return err
		})
	}
}

// cleanupNamespaceResources removes all resources in the specified namespace.
func cleanupNamespaceResources(namespace string) {
	err := cleanupTestPods(namespace)
	Expect(err).NotTo(HaveOccurred())

	err = cleanUpComputeDomains(namespace)
	Expect(err).NotTo(HaveOccurred())

	err = cleanupResourceClaims(namespace)
	Expect(err).NotTo(HaveOccurred())

	err = cleanupDeviceClasses()
	Expect(err).NotTo(HaveOccurred())

	err = cleanupResourceClaimTemplates(namespace)
	Expect(err).NotTo(HaveOccurred())

	err = cleanupHelmDeployments(namespace)
	Expect(err).NotTo(HaveOccurred())

	cleanupCRDs()

	cleanupNodeLabels()
}

// cleanUpComputeDomains deletes all ComputeDomains in the specified namespace.
func cleanUpComputeDomains(namespace string) error {
	domains, err := resourceClient.ResourceV1beta1().ComputeDomains(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	zero := int64(0)
	policy := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{GracePeriodSeconds: &zero, PropagationPolicy: &policy}
	for _, cd := range domains.Items {
		if err = resourceClient.ResourceV1beta1().ComputeDomains(namespace).Delete(ctx, cd.Name, deleteOptions); err != nil {
			return err
		}
		if err = waitForDeletion(cd.Name, func() error {
			_, err := resourceClient.ResourceV1beta1().ComputeDomains(namespace).Get(ctx, cd.Name, metav1.GetOptions{})
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// cleanupResourceClaims deletes all ResourceClaims.
func cleanupResourceClaims(namespace string) error {
	claims, err := clientSet.ResourceV1beta1().ResourceClaims(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	zero := int64(0)
	policy := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{GracePeriodSeconds: &zero, PropagationPolicy: &policy}
	for _, rc := range claims.Items {
		if err = clientSet.ResourceV1beta1().ResourceClaims(namespace).Delete(ctx, rc.Name, deleteOptions); err != nil {
			return err
		}
		if err = waitForDeletion(rc.Name, func() error {
			_, err := clientSet.ResourceV1beta1().ResourceClaims(namespace).Get(ctx, rc.Name, metav1.GetOptions{})
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// cleanupResourceClaimTemplates deletes all ResourceClaimTemplates.
func cleanupResourceClaimTemplates(namespace string) error {
	templates, err := clientSet.ResourceV1beta1().ResourceClaimTemplates(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	zero := int64(0)
	policy := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{GracePeriodSeconds: &zero, PropagationPolicy: &policy}
	for _, tmpl := range templates.Items {
		if err = clientSet.ResourceV1beta1().ResourceClaimTemplates(namespace).Delete(ctx, tmpl.Name, deleteOptions); err != nil {
			return err
		}
		if err = waitForDeletion(tmpl.Name, func() error {
			_, err := clientSet.ResourceV1beta1().ResourceClaimTemplates(namespace).Get(ctx, tmpl.Name, metav1.GetOptions{})
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// cleanupDeviceClasses deletes only the four specified DeviceClasses.
func cleanupDeviceClasses() error {
	// List all DeviceClasses (cluster-scoped resource).
	deviceClasses, err := clientSet.ResourceV1beta1().DeviceClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	// Define the set of DeviceClasses to remove.
	targets := map[string]bool{
		"compute-domain-daemon.nvidia.com":          true,
		"compute-domain-default-channel.nvidia.com": true,
		"gpu.nvidia.com":                            true,
		"mig.nvidia.com":                            true,
	}

	// Configure deletion options: immediate deletion with foreground propagation.
	zero := int64(0)
	policy := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{
		GracePeriodSeconds: &zero,
		PropagationPolicy:  &policy,
	}

	// Iterate through each DeviceClass and delete if it's one of the targets.
	for _, dc := range deviceClasses.Items {
		if targets[dc.Name] {
			if err = clientSet.ResourceV1beta1().DeviceClasses().Delete(ctx, dc.Name, deleteOptions); err != nil {
				return err
			}
			// Wait for deletion to be completed.
			if err = waitForDeletion(dc.Name, func() error {
				_, err := clientSet.ResourceV1beta1().DeviceClasses().Get(ctx, dc.Name, metav1.GetOptions{})
				return err
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// cleanupTestPods deletes all test Pods in the namespace that have the label "app.nvidia.com=k8s-dra-driver-gpu-test-app".
func cleanupTestPods(namespace string) error {
	labelSelector := "app.nvidia.com=k8s-dra-driver-gpu-test-app"
	podList, err := clientSet.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return err
	}

	zero := int64(0)
	deleteOptions := metav1.DeleteOptions{GracePeriodSeconds: &zero}
	for _, pod := range podList.Items {
		if err = clientSet.CoreV1().Pods(namespace).Delete(ctx, pod.Name, deleteOptions); err != nil {
			return err
		}
		if err = waitForDeletion(pod.Name, func() error {
			_, err := clientSet.CoreV1().Pods(namespace).Get(ctx, pod.Name, metav1.GetOptions{})
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// cleanupNodeLabels removes the "resource.nvidia.com/computeDomain" label from all nodes,
// and if EnableGFD is true, removes any labels with prefixes "feature.node.kubernetes.io/" or "nvidia.com/"
// set by NFD and GFD during the test execution.
func cleanupNodeLabels() error {
	nodes, err := clientSet.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	for _, node := range nodes.Items {
		changed := false

		// Remove the specific computeDomain label.
		if _, exists := node.Labels["resource.nvidia.com/computeDomain"]; exists {
			delete(node.Labels, "resource.nvidia.com/computeDomain")
			changed = true
		}

		// Conditionally remove additional labels if EnableGFD is true.
		if EnableGFD {
			for key := range node.Labels {
				if strings.HasPrefix(key, "feature.node.kubernetes.io/") || strings.HasPrefix(key, "nvidia.com/") {
					delete(node.Labels, key)
					changed = true
				}
			}
		}

		// Update node only if any label was removed.
		if changed {
			_, err := clientSet.CoreV1().Nodes().Update(ctx, &node, metav1.UpdateOptions{})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// cleanupTestResources deletes the pod, computeDomain, and resource claim template,
// and then removes the "resource.nvidia.com/computeDomain" label from all nodes.
// It returns an error if any step fails.
func cleanupTestResources(namespace, podName, cdName, rctName string) error {
	// Delete the Pod with default deletion options.
	if err := clientSet.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{}); err != nil {
		return err
	}
	_ = waitForDeletion(podName, func() error {
		_, err := clientSet.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		return err
	})

	// Delete the computeDomain.
	if err := resourceClient.ResourceV1beta1().ComputeDomains(namespace).Delete(ctx, cdName, metav1.DeleteOptions{}); err != nil {
		return err
	}
	_ = waitForDeletion(cdName, func() error {
		_, err := resourceClient.ResourceV1beta1().ComputeDomains(namespace).Get(ctx, cdName, metav1.GetOptions{})
		return err
	})

	// Delete the resourceClaimTemplate
	if err := clientSet.ResourceV1beta1().ResourceClaimTemplates(namespace).Delete(ctx, rctName, metav1.DeleteOptions{}); err != nil {
		return err
	}
	_ = waitForDeletion(rctName, func() error {
		_, err := clientSet.ResourceV1beta1().ResourceClaimTemplates(namespace).Get(ctx, rctName, metav1.GetOptions{})
		return err
	})

	return nil
}

// cleanupHelmDeployments uninstalls all deployed Helm releases in the specified namespace.
func cleanupHelmDeployments(namespace string) error {
	releases, err := helmClient.ListDeployedReleases()
	if err != nil {
		return fmt.Errorf("failed to list deployed releases: %w", err)
	}

	for _, release := range releases {
		// Check if the release is deployed in the target namespace.
		// Depending on your helmClient configuration the release might carry the namespace information.
		if release.Namespace == namespace {
			if err := helmClient.UninstallReleaseByName(release.Name); err != nil {
				return fmt.Errorf("failed to uninstall release %q: %w", release.Name, err)
			}
		}
	}
	return nil
}

// DeleteTestNamespace deletes the test namespace
func DeleteTestNamespace() {
	defer func() {
		err := clientSet.CoreV1().Namespaces().Delete(ctx, testNamespace.Name, metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			Expect(err).NotTo(HaveOccurred())
		}
		err = WaitForNamespacesDeleted([]string{testNamespace.Name}, DefaultNamespaceDeletionTimeout)
		Expect(err).NotTo(HaveOccurred())
	}()
}

// WaitForNamespacesDeleted waits for the namespaces to be deleted.
func WaitForNamespacesDeleted(namespaces []string, timeout time.Duration) error {
	By(fmt.Sprintf("Waiting for namespace %+v deletion", namespaces))
	nsMap := map[string]bool{}
	for _, ns := range namespaces {
		nsMap[ns] = true
	}
	// Now POLL until all namespaces have been eradicated.
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true,
		func(ctx context.Context) (bool, error) {
			nsList, err := clientSet.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
			if err != nil {
				return false, err
			}
			for _, item := range nsList.Items {
				if _, ok := nsMap[item.Name]; ok {
					return false, nil
				}
			}
			return true, nil
		})
}
