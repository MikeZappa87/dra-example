package driver

import (
	"context"
	"fmt"
	"time"

	resourceapi "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	resourceclient "k8s.io/client-go/kubernetes/typed/resource/v1"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

// ResourcePublisher publishes available resources as ResourceSlices
type ResourcePublisher struct {
	client       resourceclient.ResourceV1Interface
	driverName   string
	nodeName     string
	resourceName string
}

// NewResourcePublisher creates a new resource publisher
func NewResourcePublisher(driverName, nodeName, resourceName string) (*ResourcePublisher, error) {
	// Get in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	return &ResourcePublisher{
		client:       clientset.ResourceV1(),
		driverName:   driverName,
		nodeName:     nodeName,
		resourceName: resourceName,
	}, nil
}

// PublishResources creates or updates the ResourceSlice for this node
func (p *ResourcePublisher) PublishResources(ctx context.Context) error {
	sliceName := fmt.Sprintf("%s-%s", p.nodeName, p.driverName)

	// Each node has its own pool (pool name = node name)
	poolName := p.nodeName

	// Create the ResourceSlice
	slice := &resourceapi.ResourceSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name: sliceName,
		},
		Spec: resourceapi.ResourceSliceSpec{
			Driver:   p.driverName,
			NodeName: &p.nodeName,
			Pool: resourceapi.ResourcePool{
				Name:               poolName,
				Generation:         1,
				ResourceSliceCount: 1,
			},
			Devices: []resourceapi.Device{
				{
					Name: p.resourceName,
					Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
						"file.dra.example.com/type": {
							StringValue: stringPtr("file"),
						},
						"file.dra.example.com/filename": {
							StringValue: stringPtr(p.resourceName),
						},
						"file.dra.example.com/path": {
							StringValue: stringPtr("/etc/dra/" + p.resourceName),
						},
					},
				},
			},
		},
	}

	// Try to create or update
	_, err := p.client.ResourceSlices().Create(ctx, slice, metav1.CreateOptions{})
	if err != nil {
		// Try update if create fails
		existing, getErr := p.client.ResourceSlices().Get(ctx, sliceName, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("failed to create or get ResourceSlice: create=%v, get=%v", err, getErr)
		}
		slice.ResourceVersion = existing.ResourceVersion
		_, err = p.client.ResourceSlices().Update(ctx, slice, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update ResourceSlice: %w", err)
		}
	}

	klog.Infof("Published ResourceSlice %s", sliceName)
	return nil
}

// UnpublishResources removes the ResourceSlice
func (p *ResourcePublisher) UnpublishResources(ctx context.Context) error {
	sliceName := fmt.Sprintf("%s-%s", p.nodeName, p.driverName)
	err := p.client.ResourceSlices().Delete(ctx, sliceName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete ResourceSlice: %w", err)
	}
	klog.Infof("Unpublished ResourceSlice %s", sliceName)
	return nil
}

// StartPublishing starts a goroutine that keeps the ResourceSlice updated
func (p *ResourcePublisher) StartPublishing(ctx context.Context) {
	// Initial publish
	if err := p.PublishResources(ctx); err != nil {
		klog.Errorf("Failed to publish resources: %v", err)
	}

	// Periodic refresh
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Cleanup on shutdown
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := p.UnpublishResources(cleanupCtx); err != nil {
				klog.Errorf("Failed to unpublish resources: %v", err)
			}
			cancel()
			return
		case <-ticker.C:
			if err := p.PublishResources(ctx); err != nil {
				klog.Errorf("Failed to refresh resources: %v", err)
			}
		}
	}
}

func stringPtr(s string) *string {
	return &s
}
