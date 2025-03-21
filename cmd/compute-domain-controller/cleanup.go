package main

import (
	"context"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

const (
	cleanupInterval = 10 * time.Minute
)

type CleanupCallback[T metav1.Object] func(ctx context.Context, cdUID string) error

type CleanupManager[T metav1.Object] struct {
	waitGroup     sync.WaitGroup
	cancelContext context.CancelFunc

	informer         cache.SharedIndexInformer
	getComputeDomain GetComputeDomainFunc
	callback         CleanupCallback[T]
}

func NewCleanupManager[T metav1.Object](informer cache.SharedIndexInformer, getComputeDomain GetComputeDomainFunc, callback CleanupCallback[T]) *CleanupManager[T] {
	klog.Infof("Creating new Cleanup Manager for %T", *new(T))
	return &CleanupManager[T]{
		informer:         informer,
		getComputeDomain: getComputeDomain,
		callback:         callback,
	}
}

func (m *CleanupManager[T]) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.periodicCleanup(ctx)
	}()

	return nil
}

func (m *CleanupManager[T]) Stop() error {
	m.cancelContext()
	m.waitGroup.Wait()
	return nil
}

func (m *CleanupManager[T]) periodicCleanup(ctx context.Context) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			klog.V(6).Infof("Running periodic sync to remove %T objects owned by stale ComputeDomain", *new(T))
			store := m.informer.GetStore()
			items := store.List()
			klog.V(6).Infof("Found %d items to check for cleanup", len(items))

			for _, item := range items {
				obj, ok := item.(T)
				if !ok {
					klog.V(6).Infof("Expected object %T but got %T, skipping..", *new(T), obj)
					continue
				}

				labels := obj.GetLabels()
				if labels == nil {
					klog.V(6).Infof("Object %T has no labels, skipping..", *new(T))
					continue
				}

				uid, exists := labels[computeDomainLabelKey]
				if !exists {
					klog.V(6).Infof("Object %T does not have ComputeDomain label, skipping..", *new(T))
					continue
				}

				computeDomain, err := m.getComputeDomain(uid)
				if err != nil {
					klog.Errorf("error getting ComputeDomain: %v", err)
					continue
				}

				if computeDomain != nil {
					klog.V(6).Infof("ComputeDomain with UID %s still exists, skipping cleanup", uid)
					continue
				}

				klog.Infof("Stale %T object found for ComputeDomain '%s', running cleanup callback", *new(T), uid)
				if err := m.callback(ctx, uid); err != nil {
					klog.Errorf("error running CleanupManager callback: %v", err)
					continue
				}
			}

		case <-ctx.Done():
			return
		}
	}
}
