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
	"strings"
	"time"

	helm "github.com/mittwald/go-helm-client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1Core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/NVIDIA/k8s-test-infra/pkg/diagnostics"
)

// Actual test suite
var _ = Describe("K8S DRA GPU Driver", Ordered, func() {
	defaultCollectorObjects := []string{
		"pods",
		"nodes",
		"namespaces",
		"deployments",
		"daemonsets",
		"nodeFeature",
	}

	// check Collector objects
	collectLogsFrom := defaultCollectorObjects
	if CollectLogsFrom != "" && CollectLogsFrom != "default" {
		collectLogsFrom = strings.Split(CollectLogsFrom, ",")
	}

	AfterEach(func(ctx context.Context) {
		// Run diagnostic collector if test failed
		if CurrentSpecReport().Failed() {
			var err error
			diagnosticsCollector, err = diagnostics.New(
				diagnostics.WithNamespace(testNamespace.Name),
				diagnostics.WithArtifactDir(LogArtifactDir),
				diagnostics.WithKubernetesClient(clientSet),
				diagnostics.WithObjects(collectLogsFrom...),
			)
			Expect(err).NotTo(HaveOccurred())

			err = diagnosticsCollector.Collect(ctx)
			Expect(err).NotTo(HaveOccurred())
		}
	})

	When("deploying the K8S DRA Driver GPU via Helm", func() {
		It("should be successful", func(ctx context.Context) {
			// Chart spec
			chartSpec := &helm.ChartSpec{
				ReleaseName:     helmReleaseName,
				ChartName:       HelmChart,
				ValuesOptions:   helmOptions,
				ValuesYaml:      HelmValues,
				Namespace:       testNamespace.Name,
				CreateNamespace: true,
				Wait:            true,
				Timeout:         5 * time.Minute,
				CleanupOnFail:   true,
			}

			err := helmClient.UpdateChartRepos()
			Expect(err).NotTo(HaveOccurred())

			By("Installing nvidia-dra-driver-gpu Helm chart")
			_, err = helmClient.InstallOrUpgradeChart(ctx, chartSpec, nil)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	When("checking the K8S DRA Driver GPU deployment", func() {
		It("should be successful", func() {
			By("Checking the nvidia-dra-driver-gpu-controller deployment")
			deployment, err := clientSet.AppsV1().Deployments(testNamespace.Name).Get(ctx, "nvidia-dra-driver-gpu-controller", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(deployment).NotTo(BeNil())

			By("Checking the nvidia-dra-driver-gpu-kubelet-plugin daemonset")
			daemonset, err := clientSet.AppsV1().DaemonSets(testNamespace.Name).Get(ctx, "nvidia-dra-driver-gpu-kubelet-plugin", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(daemonset).NotTo(BeNil())

			By("Waiting for nvidia-dra-driver-gpu-kubelet-plugin daemonset to be fully scheduled")
			Eventually(func() bool {
				dsList, err := clientSet.AppsV1().DaemonSets(testNamespace.Name).List(ctx, metav1.ListOptions{})
				Expect(err).NotTo(HaveOccurred())
				for _, ds := range dsList.Items {
					if ds.Status.CurrentNumberScheduled != ds.Status.DesiredNumberScheduled {
						return false
					}
				}
				return true
			}, 5*time.Minute, 10*time.Second).Should(BeTrue())

			By("Checking the ComputeDomains CRD creation")
			crds, err := extClient.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, "computedomains.resource.nvidia.com", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(crds).NotTo(BeNil())
		})
	})

	When("running a pod that consumes a computeDomain", func() {
		It("should be successful", func(ctx context.Context) {
			By("Creating the resource claim template")
			rct, err := CreateOrUpdateResourceClaimTemplatesFromFile(ctx, clientSet, "resourceclaimtemplate-1.yaml", testNamespace.Name)
			Expect(err).NotTo(HaveOccurred())

			By("Creating the computeDomain")
			cd, err := CreateOrUpdateComputeDomainsFromFile(ctx, resourceClient, "computedomain-1.yaml", testNamespace.Name)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying that expected resource claim templates exist")
			expectedTemplates := []string{
				rct[0],
				cd[0],
			}
			for _, tmplName := range expectedTemplates {
				Eventually(func() error {
					_, err := clientSet.ResourceV1beta1().ResourceClaimTemplates(testNamespace.Name).
						Get(ctx, tmplName, metav1.GetOptions{})
					return err
				}, 5*time.Minute, 5*time.Second).Should(Succeed())
			}

			By("Waiting for all daemonsets to be fully scheduled")
			Eventually(func() bool {
				dsList, err := clientSet.AppsV1().DaemonSets(testNamespace.Name).List(ctx, metav1.ListOptions{})
				Expect(err).NotTo(HaveOccurred())
				for _, ds := range dsList.Items {
					if ds.Status.CurrentNumberScheduled != ds.Status.DesiredNumberScheduled {
						return false
					}
				}
				return true
			}, 5*time.Minute, 10*time.Second).Should(BeTrue())

			By("Creating the test pod")
			pod, err := CreateOrUpdatePodsFromFile(ctx, clientSet, "pod-1.yaml", testNamespace.Name)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for the test pod to be running")
			Eventually(func() (v1Core.PodPhase, error) {
				p, err := clientSet.CoreV1().Pods(testNamespace.Name).Get(ctx, pod[0], metav1.GetOptions{})
				if err != nil {
					return "", err
				}
				return p.Status.Phase, nil
			}, 5*time.Minute, 10*time.Second).Should(Equal(v1Core.PodRunning))

			By("Checking the test pod logs")
			Eventually(func() bool {
				podLogs, err := clientSet.CoreV1().Pods(testNamespace.Name).GetLogs(pod[0], &v1Core.PodLogOptions{}).DoRaw(ctx)
				Expect(err).NotTo(HaveOccurred())
				logs := string(podLogs)
				return validatePodLogs(logs)
			}, 5*time.Minute, 10*time.Second).Should(BeTrue())

			By("Deleting the test1 resources")
			err = cleanupTestResources(testNamespace.Name, pod[0], cd[0], rct[0])
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
