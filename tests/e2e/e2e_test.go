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
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	v1beta1 "github.com/NVIDIA/k8s-dra-driver-gpu/pkg/nvidia.com/clientset/versioned"
	"github.com/NVIDIA/k8s-test-infra/pkg/diagnostics"
	"helm.sh/helm/v3/pkg/repo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	helm "github.com/mittwald/go-helm-client"
	helmValues "github.com/mittwald/go-helm-client/values"
	corev1 "k8s.io/api/core/v1"
	extclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	// DefaultNamespaceDeletionTimeout is timeout duration for waiting for a namespace deletion.
	DefaultNamespaceDeletionTimeout = 10 * time.Minute

	// PollInterval is how often to Poll pods, nodes and claims.
	PollInterval = 2 * time.Second
)

// Test context
var (
	Kubeconfig         string
	HelmChart          string
	HelmValuesFile     string
	HelmValues         string
	HostManagedDrivers bool

	// Image
	ImageRepo       string
	ImageTag        string
	ImagePullPolicy string

	// k8s clients
	clientConfig   *rest.Config
	clientSet      clientset.Interface
	extClient      *extclient.Clientset
	resourceClient *v1beta1.Clientset

	testNamespace *corev1.Namespace // Every test has at least one namespace unless creation is skipped
	EnableGFD     bool

	// Helm
	helmClient      helm.Client
	helmLogFile     *os.File
	helmArtifactDir string
	helmLogger      *log.Logger
	helmReleaseName string
	helmOptions     helmValues.Options

	ctx     context.Context
	Timeout time.Duration
	cwd     string

	diagnosticsCollector *diagnostics.Diagnostic
	CollectLogsFrom      string
	LogArtifactDir       string
	packagePath          string
)

func TestMain(t *testing.T) {
	suiteName := "E2E K8s DRA GPU Driver"

	RegisterFailHandler(Fail)

	ctx = context.Background()
	getTestEnv()

	RunSpecs(t,
		suiteName,
	)
}

// BeforeSuite runs before the test suite
var _ = BeforeSuite(func() {
	var err error

	cwd, err = os.Getwd()
	Expect(err).NotTo(HaveOccurred())

	// Get k8s clients
	getK8sClients()

	// Create clients for apiextensions and our CRD api
	extClient = extclient.NewForConfigOrDie(clientConfig)

	// Create a nvidia.com resource client
	resourceClient, err = v1beta1.NewForConfig(clientConfig)
	Expect(err).NotTo(HaveOccurred())

	// Create a namespace for the test
	testNamespace, err = CreateTestingNS("nvidia-dra-driver-gpu-e2e-test", clientSet, nil)
	Expect(err).NotTo(HaveOccurred())

	// Get Helm client
	helmReleaseName = "nvidia-dra-driver-gpu-e2e-test" + rand.String(5)
	getHelmClient()

	// Deploy GFD + NFD
	deployDependencies(ctx)

	// Set Helm values options
	setHelmValuesOptions()
})

var _ = AfterSuite(func() {
	By("Cleaning up namespace resources")
	// Remove finalizers and force delete resourceclaims, resourceclaimtemplates, daemonsets, and pods.
	cleanupNamespaceResources(testNamespace.Name)

	By("Deleting the test namespace")
	// Delete the test namespace to remove any remaining objects.
	DeleteTestNamespace()
})

// getK8sClients creates the k8s clients
func getK8sClients() {
	var err error

	// get config from kubeconfig
	c, err := clientcmd.LoadFromFile(Kubeconfig)
	Expect(err).NotTo(HaveOccurred())

	// get client config
	clientConfig, err = clientcmd.NewDefaultClientConfig(*c, &clientcmd.ConfigOverrides{}).ClientConfig()
	Expect(err).NotTo(HaveOccurred())

	clientSet, err = clientset.NewForConfig(clientConfig)
	Expect(err).NotTo(HaveOccurred())
}

// getHelmClient creates a new Helm client
func getHelmClient() {
	var err error

	// Set Helm log file
	helmArtifactDir = filepath.Join(LogArtifactDir, "helm")

	// Create a Helm client
	err = os.MkdirAll(helmArtifactDir, 0755)
	Expect(err).NotTo(HaveOccurred())

	helmLogFile, err = os.OpenFile(filepath.Join(LogArtifactDir, "helm_logs"), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	Expect(err).NotTo(HaveOccurred())

	helmLogger = log.New(helmLogFile, fmt.Sprintf("%s\t", testNamespace.Name), log.Ldate|log.Ltime)

	helmRestConf := &helm.RestConfClientOptions{
		Options: &helm.Options{
			Namespace:        testNamespace.Name,
			RepositoryCache:  "/tmp/.helmcache",
			RepositoryConfig: "/tmp/.helmrepo",
			Debug:            true,
			DebugLog:         helmLogger.Printf,
		},
		RestConfig: clientConfig,
	}

	helmClient, err = helm.NewClientFromRestConf(helmRestConf)
	Expect(err).NotTo(HaveOccurred())
}

// getTestEnv gets the test environment variables
func getTestEnv() {
	defer GinkgoRecover()
	var err error

	_, thisFile, _, _ := runtime.Caller(0)
	packagePath = filepath.Dir(thisFile)

	Kubeconfig = os.Getenv("KUBECONFIG")
	Expect(Kubeconfig).NotTo(BeEmpty(), "KUBECONFIG must be set")

	Timeout = time.Duration(getIntEnvVar("E2E_TIMEOUT_SECONDS", 1800)) * time.Second

	HelmChart = os.Getenv("HELM_CHART")
	Expect(HelmChart).NotTo(BeEmpty(), "HELM_CHART must be set")

	LogArtifactDir = os.Getenv("LOG_ARTIFACT_DIR")

	ImageRepo = os.Getenv("E2E_IMAGE_REPO")
	Expect(ImageRepo).NotTo(BeEmpty(), "IMAGE_REPO must be set")

	ImageTag = os.Getenv("E2E_IMAGE_TAG")
	Expect(ImageTag).NotTo(BeEmpty(), "IMAGE_TAG must be set")

	ImagePullPolicy = os.Getenv("E2E_IMAGE_PULL_POLICY")
	Expect(ImagePullPolicy).NotTo(BeEmpty(), "IMAGE_PULL_POLICY must be set")

	EnableGFD = getBoolEnvVar("ENABLE_GFD", true)
	Expect(EnableGFD).NotTo(BeNil(), "ENABLE_GFD must be set")

	HostManagedDrivers = getBoolEnvVar("E2E_HOST_MANAGED_DRIVERS", true)
	Expect(HostManagedDrivers).NotTo(BeNil(), "E2E_HOST_MANAGED_DRIVERS must be set")

	CollectLogsFrom = os.Getenv("COLLECT_LOGS_FROM")

	// Get current working directory
	cwd, err = os.Getwd()
	Expect(err).NotTo(HaveOccurred())
}

// getBoolEnvVar returns the boolean value of the environment variable or the default value if not set.
func getBoolEnvVar(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	boolValue, err := strconv.ParseBool(value)
	if err != nil {
		return defaultValue
	}
	return boolValue
}

// getIntEnvVar returns the integer value of the environment variable or the default value if not set.
func getIntEnvVar(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	intValue, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return intValue
}

// CreateTestingNS should be used by every test, note that we append a common prefix to the provided test name.
// Please see NewFramework instead of using this directly.
func CreateTestingNS(baseName string, c clientset.Interface, labels map[string]string) (*corev1.Namespace, error) {
	uid := RandomSuffix()
	if labels == nil {
		labels = map[string]string{}
	}
	labels["e2e-run"] = uid

	// We don't use ObjectMeta.GenerateName feature, as in case of API call
	// failure we don't know whether the namespace was created and what is its
	// name.
	name := fmt.Sprintf("%v-%v", baseName, uid)

	namespaceObj := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "",
			Labels:    labels,
		},
		Status: corev1.NamespaceStatus{},
	}
	// Be robust about making the namespace creation call.
	var got *corev1.Namespace
	if err := wait.PollUntilContextTimeout(ctx, PollInterval, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		var err error
		got, err = c.CoreV1().Namespaces().Create(ctx, namespaceObj, metav1.CreateOptions{})
		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				// regenerate on conflict
				namespaceObj.Name = fmt.Sprintf("%v-%v", baseName, uid)
			}
			return false, nil
		}
		return true, nil
	}); err != nil {
		return nil, err
	}

	return got, nil
}

// deployDependencies installs all the dependent helm charts
func deployDependencies(ctx context.Context) {
	// Install dependencies if not told to skip
	if EnableGFD {
		// Add or Update Helm repo
		helmRepo := repo.Entry{
			Name: "gfd",
			URL:  "https://nvidia.github.io/k8s-device-plugin",
		}
		err := helmClient.AddOrUpdateChartRepo(helmRepo)
		Expect(err).NotTo(HaveOccurred())

		err = helmClient.UpdateChartRepos()
		Expect(err).NotTo(HaveOccurred())

		values := helmValues.Options{
			Values: []string{
				"gfd.enabled=true",
				"nfd.enabled=true",
				"devicePlugin.enabled=false",
			},
		}

		chartSpec := &helm.ChartSpec{
			ReleaseName:     "gfd",
			ChartName:       "gfd/nvidia-device-plugin",
			Namespace:       testNamespace.Name,
			CreateNamespace: false,
			ValuesOptions:   values,
			Wait:            true,
			Timeout:         5 * time.Minute,
			CleanupOnFail:   true,
		}
		_, err = helmClient.InstallOrUpgradeChart(ctx, chartSpec, nil)
		Expect(err).NotTo(HaveOccurred())
	}
}

// setHelmValuesOptions sets the Helm values options
func setHelmValuesOptions() {
	helmOptions = helmValues.Options{
		Values: []string{
			fmt.Sprintf("image.repository=%s", ImageRepo),
			fmt.Sprintf("image.tag=%s", ImageTag),
			fmt.Sprintf("image.pullPolicy=%s", ImagePullPolicy),
			"gpuResourcesEnabledOverride=true",
		},
	}

	// By default, the helm values of the dra-driver-gpu assume the driver is managed by the host.
	// see https://github.com/NVIDIA/k8s-dra-driver-gpu/blob/main/deployments/helm/nvidia-dra-driver-gpu/values.yaml
	if !HostManagedDrivers {
		helmOptions.Values = append(helmOptions.Values,
			"nvidiaDriverRoot=/run/nvidia/driver",
			"nvidiaCtkPath=/usr/local/nvidia/toolkit/nvidia-ctk",
		)
	}
}

// RandomSuffix provides a random sequence to append to pods,services,rcs.
func RandomSuffix() string {
	return strconv.Itoa(rand.Intn(10000))
}
