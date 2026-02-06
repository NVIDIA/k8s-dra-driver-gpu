/*
 * Copyright (c) 2025 NVIDIA CORPORATION.  All rights reserved.
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

package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

const (
	pollInterval         time.Duration = 5 * time.Second
	monitorStatusFailure               = "failure"
	monitorStatusTimeout               = "timeout"
	monitorStatusSuccess               = "success"

	// Container waiting reasons indicating failure
	reasonCrashLoopBackOff = "CrashLoopBackOff"
	reasonImagePullBackOff = "ImagePullBackOff"
	reasonErrImagePull     = "ErrImagePull"
)

var ErrBindingFailure = errors.New("binding failure")

type OpaqueDeviceConfig struct {
	Requests []string
	Config   runtime.Object
}

type monitorInfo struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type ResourceClaimManager struct {
	config              *ManagerConfig
	waitGroup           sync.WaitGroup
	cancelContext       context.CancelFunc
	informer            cache.SharedIndexInformer
	nodeManager         *NodeManager
	daemonSetPodManager *DaemonSetPodManager
	activeMonitors      sync.Map
	getComputeDomain    GetComputeDomainFunc
}

type podRef struct {
	namespace string
	name      string
	uid       types.UID
}

func NewResourceClaimManager(config *ManagerConfig, getComputeDomain GetComputeDomainFunc) *ResourceClaimManager {
	informer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
				return config.clientsets.Resource.ResourceClaims("").List(ctx, options)
			},
			WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
				return config.clientsets.Resource.ResourceClaims("").Watch(ctx, options)
			},
		},
		&resourcev1.ResourceClaim{},
		informerResyncPeriod,
		cache.Indexers{},
	)

	m := &ResourceClaimManager{
		config:           config,
		informer:         informer,
		getComputeDomain: getComputeDomain,
	}

	m.nodeManager = NewNodeManager(config, getComputeDomain)
	m.daemonSetPodManager = NewDaemonSetPodManager(config)
	return m
}

func (m *ResourceClaimManager) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	_, err := m.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			m.config.workQueue.EnqueueWithKey(obj, "resourceclaim", m.addOrUpdate)
		},
		UpdateFunc: func(oldObj, newObj any) {
			m.config.workQueue.EnqueueWithKey(newObj, "resourceclaim", m.addOrUpdate)
		},
	})
	if err != nil {
		return fmt.Errorf("error adding event handlers for ResourceClaim informer: %w", err)
	}

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.informer.Run(ctx.Done())
	}()

	if !cache.WaitForCacheSync(ctx.Done(), m.informer.HasSynced) {
		return fmt.Errorf("informer cache sync for ResourceClaim failed")
	}

	if err := m.nodeManager.Start(ctx); err != nil {
		return fmt.Errorf("error starting Node manager: %w", err)
	}

	if err := m.daemonSetPodManager.Start(ctx); err != nil {
		return fmt.Errorf("error starting DaemonSetPod manager: %w", err)
	}
	return nil
}

func (m *ResourceClaimManager) Stop() error {
	if err := m.nodeManager.Stop(); err != nil {
		return fmt.Errorf("error stopping Node manager: %w", err)
	}

	if err := m.daemonSetPodManager.Stop(); err != nil {
		return fmt.Errorf("error stopping DaemonSetPod manager: %w", err)
	}
	if m.cancelContext != nil {
		m.cancelContext()
	}
	m.waitGroup.Wait()
	return nil
}

func (m *ResourceClaimManager) addOrUpdate(ctx context.Context, obj any) error {
	rc, ok := obj.(*resourcev1.ResourceClaim)
	if !ok || rc == nil {
		return fmt.Errorf("failed to cast object to ResourceClaim")
	}

	if rc.Status.Allocation == nil || len(rc.Status.ReservedFor) == 0 {
		return nil
	}

	// Check the allocationResult of ResourceClaim to determine whether it should be monitored.
	isEligible, domainID := m.checkClaimEligibleForComputeDomainLabeling(rc)
	if !isEligible {
		return nil
	}

	if domainID == "" {
		return fmt.Errorf("matching ResourceClaim %s/%s has no domainID in allocation config", rc.Namespace, rc.Name)
	}

	currentAllocationTimestamp := ""
	if rc.Status.Allocation.AllocationTimestamp != nil {
		currentAllocationTimestamp = rc.Status.Allocation.AllocationTimestamp.Format(time.RFC3339Nano)
	} else {
		// If allocationTimestamp is nil, this specific allocation cannot be tracked uniquely
		// and we cannot reliably check timeout based on it.
		// Deciding how to handle this case; for now, consider it not trackable for polling.
		klog.V(4).Infof("ResourceClaim %s/%s has an allocation but no AllocationTimestamp. Skipping monitoring.", rc.Namespace, rc.Name)
		return nil
	}

	// get info for map keys
	rcUID := string(rc.UID)
	rcMonitors, err := m.loadResourceClaimMonitor(rcUID)
	if err != nil {
		return err
	}

	// Cancel a monitor that has already timed out.
	m.cancelTimeoutedMonitor(ctx, rc, rcMonitors, currentAllocationTimestamp)

	//Check namespace
	err = m.AssertComputeDomainNamespace(rc.Namespace, domainID)
	if err != nil {
		return fmt.Errorf("failed to assert Namespace for computeDomain with domainID %s and ResourceClaim %s/%s: %w", domainID, rc.Namespace, rc.Name, err)
	}

	//Check if monitoring already launched and return if yes
	if _, loaded := rcMonitors.Load(currentAllocationTimestamp); loaded {
		return nil
	}

	// Extract the node name from the allocation status
	nodeName, ok := allocatedNodeName(rc)
	if !ok {
		return fmt.Errorf("matching ResourceClaim %s/%s has no allocated nodeName", rc.Namespace, rc.Name)
	}

	if err := m.nodeManager.AddComputeDomainLabels(ctx, nodeName, domainID); err != nil {
		return fmt.Errorf("failed to add node label for node '%s' with domainID '%s': %w", nodeName, domainID, err)
	}

	// Use LoadOrStore to atomically check and create monitor
	monitorCtx, monitorCancel := context.WithCancel(ctx)
	doneChannel := make(chan struct{})
	newMonitorInfo := &monitorInfo{
		cancel: monitorCancel,
		done:   doneChannel,
	}

	if _, loaded := rcMonitors.LoadOrStore(currentAllocationTimestamp, newMonitorInfo); loaded {
		return nil
	}

	// Launch the monitoring goroutine
	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		defer close(doneChannel)
		pollCondition := func(pollCtx context.Context) (bool, error) {
			cd, err := m.getComputeDomain(domainID)
			if err != nil {
				return false, err
			}

			var foundNodesStatus bool
			if cd.Status.Nodes != nil {
				for _, node := range cd.Status.Nodes {
					if node.Name == nodeName {
						foundNodesStatus = true
						// Check ComputeDomain node status
						switch node.Status {
						// Ready
						case nvapi.ComputeDomainStatusReady:
							return true, nil
						// NotReady
						case nvapi.ComputeDomainStatusNotReady:
							return true, fmt.Errorf("%w: binding failed — IMEX daemon failed", ErrBindingFailure)
						}
					}
				}
			}
			// If the ComputeDomain node status has not been written.
			if !foundNodesStatus {
				// Check if IMEX Daemon Pod was started correctly
				err := m.checkDaemonSetPodStatus(pollCtx, domainID, nodeName)
				if err != nil {
					return true, err
				}
			}
			return false, nil
		}

		// Start the polling loop to monitor the ComputeDomain node status.
		err := wait.PollUntilContextCancel(monitorCtx, pollInterval, false, pollCondition)

		switch {
		// Success - BindingConditions is satisfied
		case err == nil:
			m.setBindingConditions(ctx, rc, metav1.ConditionTrue, nvapi.ComputeDomainBindingConditions, "IMEXDaemonSetupSucceeded", "binding succeeded — IMEX daemon run successfully")
			m.cleanup(ctx, rc, monitorStatusSuccess, currentAllocationTimestamp, domainID, nodeName)

		// Failure - BindingFailureConditions is satisfied
		case errors.Is(err, ErrBindingFailure):
			klog.Errorf("ResourceClaim %s/%s binding failed for allocation %s: %v. Node: %s, DomainID: %s",
				rc.Namespace, rc.Name, currentAllocationTimestamp, err, nodeName, domainID)
			m.cleanup(ctx, rc, monitorStatusFailure, currentAllocationTimestamp, domainID, nodeName)
			m.setBindingConditions(ctx, rc, metav1.ConditionTrue, nvapi.ComputeDomainBindingFailureConditions, "IMEXDaemonSetupFailed", err.Error())

		// Timeout - BindingTimeout occurred
		case errors.Is(err, context.Canceled):
			klog.Infof("Monitoring for ResourceClaim %s/%s (Allocation: %s) cancelled.", rc.Namespace, rc.Name, currentAllocationTimestamp)
			m.cleanup(ctx, rc, monitorStatusTimeout, currentAllocationTimestamp, domainID, nodeName)

		default:
			klog.Errorf("Unexpected error during ComputeDomain status polling for ResourceClaim %s/%s (Allocation: %s): %v",
				rc.Namespace, rc.Name, currentAllocationTimestamp, err)
		}
	}()

	return nil
}

// Checks if ResourceClaim is Eligible for Compute Domain Labeling and returns domainID
func (m *ResourceClaimManager) checkClaimEligibleForComputeDomainLabeling(rc *resourcev1.ResourceClaim) (bool, string) {
	var domainID string

	configs, err := GetOpaqueDeviceConfigs(
		nvapi.StrictDecoder,
		DriverName,
		rc.Status.Allocation.Devices.Config,
	)
	if err != nil {
		return false, ""
	}

	var configResults []*resourcev1.DeviceRequestAllocationResult
	for _, result := range rc.Status.Allocation.Devices.Results {
		// Check the driver
		if result.Driver != DriverName {
			continue
		}
		// Check the device is channel device
		for _, c := range slices.Backward(configs) {
			if slices.Contains(c.Requests, result.Request) {
				if _, ok := c.Config.(*nvapi.ComputeDomainChannelConfig); ok {
					configResults = append(configResults, &result)
					if domainID == "" {
						domainID = c.Config.(*nvapi.ComputeDomainChannelConfig).DomainID
					}
					break
				}
			}
		}
	}

	for _, result := range configResults {
		// Check channel device has BindingConditinos
		if slices.Contains(result.BindingConditions, nvapi.ComputeDomainBindingConditions) {
			for _, deviceStatus := range rc.Status.Devices {
				if result.Device == deviceStatus.Device {
					for _, cond := range deviceStatus.Conditions {
						// Check the device is not set BindingConditions
						if cond.Type == nvapi.ComputeDomainBindingConditions && cond.Status == metav1.ConditionTrue {
							return false, domainID
						}
						// Check the device is not set BindingFailureConditions
						if cond.Type == nvapi.ComputeDomainBindingFailureConditions && cond.Status == metav1.ConditionTrue {
							return false, domainID
						}
					}
				}
			}
			return true, domainID
		}
	}
	return false, domainID

}

// Cancel stale monitors that have timed out and clean them up.
func (m *ResourceClaimManager) cancelTimeoutedMonitor(ctx context.Context, rc *resourceapi.ResourceClaim, rcMonitors *sync.Map, timestamp string) {
	ref, hasRef := m.getReservedPodRef(rc)
	if hasRef {
		rcMonitors.Range(func(k, v any) bool {
			oldTS, ok := k.(string)
			if !ok {
				return true
			}
			if oldTS == timestamp {
				return true
			}

			oldT, perr := time.Parse(time.RFC3339Nano, oldTS)
			if perr != nil {
				return true
			}
			// Check whether the allocationResult for the stored old timestamp has timed out.
			seen, err := m.hasBindingTimeoutEventAfter(ctx, ref, oldT)
			if err != nil || !seen {
				return true
			}

			monitorInfo, ok := v.(*monitorInfo)
			if ok && monitorInfo != nil {
				monitorInfo.cancel()
				// Wait for the specific monitoring goroutine to complete
				<-monitorInfo.done
			}

			// After cancellation, remove the old monitor entry.
			rcMonitors.Delete(oldTS)

			return true
		})
	}
}

func (m *ResourceClaimManager) AssertComputeDomainNamespace(claimNamespace, cdUID string) error {
	cd, err := m.getComputeDomain(cdUID)
	if err != nil {
		return fmt.Errorf("error getting ComputeDomain: %w", err)
	}
	if cd == nil {
		return fmt.Errorf("ComputeDomain not found: %s", cdUID)
	}

	if cd.Namespace != claimNamespace {
		return fmt.Errorf("the ResourceClaim's namespace is different than the ComputeDomain's namespace")
	}

	return nil
}

// Update binding conditions on the ResourceClaim's devices.
func (m *ResourceClaimManager) setBindingConditions(ctx context.Context, rc *resourcev1.ResourceClaim, conditionStatus metav1.ConditionStatus, conditionType string, reason string, message string) error {
	rcToUpdate := rc.DeepCopy()
	if len(rcToUpdate.Status.Devices) == 0 {
		for _, allocationDevice := range rcToUpdate.Status.Allocation.Devices.Results {
			device := &resourcev1.AllocatedDeviceStatus{
				Driver: allocationDevice.Driver,
				Device: allocationDevice.Device,
				Pool:   allocationDevice.Pool,
			}
			rcToUpdate.Status.Devices = append(rcToUpdate.Status.Devices, *device)
		}
	}

	if len(rcToUpdate.Status.Devices) == 0 {
		return nil
	}

	for i := range rcToUpdate.Status.Devices {
		device := &rcToUpdate.Status.Devices[i]
		newCondition := metav1.Condition{
			Type:               conditionType,
			Status:             conditionStatus,
			LastTransitionTime: metav1.NewTime(time.Now()),
			Reason:             reason,
			Message:            fmt.Sprintf("Device %s: %s", device.Device, message),
		}
		conditionExists := false
		for j, existingCond := range device.Conditions {
			if existingCond.Type == conditionType {
				if existingCond.Status != newCondition.Status {
					device.Conditions[j] = newCondition
				}
				conditionExists = true
				break
			}
		}
		if !conditionExists {
			device.Conditions = append(device.Conditions, newCondition)
		}
	}

	_, err := m.config.clientsets.Resource.ResourceClaims(rcToUpdate.Namespace).UpdateStatus(ctx, rcToUpdate, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update ResourceClaim %s/%s status with binding conditions: %w", rcToUpdate.Namespace, rcToUpdate.Name, err)
	}

	return nil
}

// cleanup finalizes monitoring for a ResourceClaim based on status.
// - success: remove the active monitor entry.
// - failure: drop the current allocation timestamp entry and remove ComputeDomain labels (waits until removed).
// - timeout: remove ComputeDomain labels (waits until removed).
func (m *ResourceClaimManager) cleanup(ctx context.Context, rc *resourcev1.ResourceClaim, status string, currentAllocationTimestamp string, domainID string, nodeName string) error {
	rcUID := string(rc.UID)
	switch status {
	case monitorStatusSuccess:
		m.activeMonitors.Delete(rcUID)
	case monitorStatusFailure:
		rcMonitors, err := m.loadResourceClaimMonitor(rcUID)
		if err != nil {
			return err
		}
		rcMonitors.Delete(currentAllocationTimestamp)
		if err = m.nodeManager.RemoveComputeDomainLabels(ctx, domainID, nodeName); err != nil {
			return err
		}
		if err = m.waitForLabelRemovalCompletion(ctx, nodeName); err != nil {
			return err
		}
	case monitorStatusTimeout:
		if err := m.nodeManager.RemoveComputeDomainLabels(ctx, domainID, nodeName); err != nil {
			return err
		}
		if err := m.waitForLabelRemovalCompletion(ctx, nodeName); err != nil {
			return err
		}
	}
	return nil
}

func (m *ResourceClaimManager) waitForLabelRemovalCompletion(ctx context.Context, nodeName string) error {
	// Define the expected label key pattern for compute domain
	expectedLabelKey := computeDomainLabelKey

	// Poll until the label is removed or context is cancelled
	pollCondition := func(pollCtx context.Context) (bool, error) {
		// Get the current node state
		node, err := m.config.clientsets.Core.CoreV1().Nodes().Get(pollCtx, nodeName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to get node %s: %w", nodeName, err)
		}

		// Check if the compute domain label still exists
		if node.Labels != nil {
			if _, exists := node.Labels[expectedLabelKey]; exists {
				// Label still exists, continue polling
				return false, nil
			}
		}

		// Label has been successfully removed
		return true, nil
	}

	// Poll with a 1-second interval and 30-second timeout
	pollCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err := wait.PollUntilContextCancel(pollCtx, 1*time.Second, false, pollCondition)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("timeout waiting for compute domain label removal from node %s", nodeName)
		}
		return fmt.Errorf("failed to verify label removal from node %s: %w", nodeName, err)
	}

	klog.V(4).Infof("Successfully verified compute domain label removal from node %s", nodeName)
	return nil
}

// loadResourceClaimMonitor returns (and initializes if absent) the monitor map for the given ResourceClaim UID.
func (m *ResourceClaimManager) loadResourceClaimMonitor(rcUID string) (*sync.Map, error) {
	innerMapVal, _ := m.activeMonitors.LoadOrStore(rcUID, &sync.Map{})
	rcMonitors, ok := innerMapVal.(*sync.Map)
	if !ok {
		klog.Errorf("Internal error: activeMonitors for ResourceClaim %s contained unexpected type.", rcUID)
		m.activeMonitors.Delete(rcUID)
		return nil, fmt.Errorf("internal error with active monitors map for ResourceClaim %s", rcUID)
	} else {
		return rcMonitors, nil
	}
}

func (m *ResourceClaimManager) getReservedPodRef(rc *resourcev1.ResourceClaim) (*podRef, bool) {
	if rc == nil || len(rc.Status.ReservedFor) == 0 {
		return nil, false
	}
	r := rc.Status.ReservedFor[0]
	if r.Resource != "pods" || r.Name == "" || r.UID == "" {
		return nil, false
	}
	return &podRef{
		namespace: rc.Namespace,
		name:      r.Name,
		uid:       r.UID,
	}, true
}

// hasBindingTimeoutEventAfter checks whether a binding timeout event for the given Pod
// occurred after the specified time.
func (m *ResourceClaimManager) hasBindingTimeoutEventAfter(ctx context.Context, ref *podRef, after time.Time) (bool, error) {
	if ref == nil {
		return false, nil
	}

	selector := fields.Set{
		"involvedObject.name": ref.name,
		"involvedObject.kind": "Pod",
	}.AsSelector().String()

	events, err := m.config.clientsets.Core.CoreV1().Events(ref.namespace).List(ctx, metav1.ListOptions{
		FieldSelector: selector,
	})
	if err != nil {
		return false, err
	}

	for _, e := range events.Items {
		t := eventTime(e)
		if !t.After(after) {
			continue
		}
		msg := strings.ToLower(e.Message)
		if strings.Contains(msg, "binding timeout") {
			return true, nil
		}
	}
	return false, nil
}

// checkDaemonSetPodStatus checks the status of the IMEX DaemonSet Pod on the target node.
// Returns nil while the Pod is Pending or not yet Ready, and returns an error if the
// Pod has failed or a container is in a backoff or image pull failure state.
func (m *ResourceClaimManager) checkDaemonSetPodStatus(ctx context.Context, domainID, nodeName string) error {
	dsp, err := m.daemonSetPodManager.Get(ctx, domainID, nodeName)
	if err != nil || dsp == nil {
		return err
	}
	switch dsp.Status.Phase {
	case corev1.PodPending:
		klog.V(3).Infof("IMEX Daemon Pod %s/%s is still Pending", dsp.Namespace, dsp.Name)
		return nil
	case corev1.PodFailed:
		return fmt.Errorf("%w: IMEX Daemon Pod creation failed", ErrBindingFailure)
	case corev1.PodRunning:
		for _, status := range dsp.Status.ContainerStatuses {
			if status.State.Waiting != nil {
				reason := status.State.Waiting.Reason
				if reason == reasonCrashLoopBackOff || reason == reasonImagePullBackOff || reason == reasonErrImagePull {
					return fmt.Errorf("%w: IMEX Daemon Pod container failed: %s", ErrBindingFailure, status.State.Waiting.Message)
				}
			}
		}
		for _, condition := range dsp.Status.Conditions {
			if condition.Type == corev1.PodReady {
				if condition.Status == corev1.ConditionTrue {
					return nil
				}
			}
		}
	}
	return nil
}

// GetOpaqueDeviceConfigs returns an ordered list of the configs contained in possibleConfigs for this driver.
//
// Configs can either come from the resource claim itself or from the device
// class associated with the request. Configs coming directly from the resource
// claim take precedence over configs coming from the device class. Moreover,
// configs found later in the list of configs attached to its source take
// precedence over configs found earlier in the list for that source.
//
// All of the configs relevant to the driver from the list of possibleConfigs
// will be returned in order of precedence (from lowest to highest). If no
// configs are found, nil is returned.
func GetOpaqueDeviceConfigs(
	decoder runtime.Decoder,
	driverName string,
	possibleConfigs []resourcev1.DeviceAllocationConfiguration,
) ([]*OpaqueDeviceConfig, error) {
	// Collect all configs in order of reverse precedence.
	var classConfigs []resourcev1.DeviceAllocationConfiguration
	var claimConfigs []resourcev1.DeviceAllocationConfiguration
	var candidateConfigs []resourcev1.DeviceAllocationConfiguration
	for _, config := range possibleConfigs {
		switch config.Source {
		case resourcev1.AllocationConfigSourceClass:
			classConfigs = append(classConfigs, config)
		case resourcev1.AllocationConfigSourceClaim:
			claimConfigs = append(claimConfigs, config)
		default:
			return nil, fmt.Errorf("invalid config source: %v", config.Source)
		}
	}
	candidateConfigs = append(candidateConfigs, classConfigs...)
	candidateConfigs = append(candidateConfigs, claimConfigs...)

	// Decode all configs that are relevant for the driver.
	var resultConfigs []*OpaqueDeviceConfig
	for _, config := range candidateConfigs {
		// If this is nil, the driver doesn't support some future API extension
		// and needs to be updated.
		if config.Opaque == nil {
			return nil, fmt.Errorf("only opaque parameters are supported by this driver")
		}

		// Configs for different drivers may have been specified because a
		// single request can be satisfied by different drivers. This is not
		// an error -- drivers must skip over other driver's configs in order
		// to support this.
		if config.Opaque.Driver != driverName {
			continue
		}

		decodedConfig, err := runtime.Decode(decoder, config.Opaque.Parameters.Raw)
		if err != nil {
			// Bad opaque config: i) do not retry preparing this resource
			// internally and ii) return notion of permanent error to kubelet,
			// to give it an opportunity to play this error back to the user so
			// that it becomes actionable.
			return nil, fmt.Errorf("error decoding config parameters: %w", err)
		}

		resultConfig := &OpaqueDeviceConfig{
			Requests: config.Requests,
			Config:   decodedConfig,
		}

		resultConfigs = append(resultConfigs, resultConfig)
	}

	return resultConfigs, nil
}

// Extract the allocated node name from the ResourceClaim, if present.
func allocatedNodeName(rc *resourcev1.ResourceClaim) (string, bool) {
	if rc == nil || rc.Status.Allocation == nil || rc.Status.Allocation.NodeSelector == nil {
		return "", false
	}

	for _, term := range rc.Status.Allocation.NodeSelector.NodeSelectorTerms {
		for _, field := range term.MatchFields {
			if field.Key == "metadata.name" &&
				field.Operator == "In" &&
				len(field.Values) > 0 &&
				field.Values[0] != "" {
				return field.Values[0], true
			}
		}
	}
	return "", false
}

// eventTime returns the most relevant timestamp for the Event,
// preferring LastTimestamp, then FirstTimestamp, then CreationTimestamp.
func eventTime(e corev1.Event) time.Time {
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp.Time
	}
	if !e.FirstTimestamp.IsZero() {
		return e.FirstTimestamp.Time
	}
	return e.CreationTimestamp.Time
}
