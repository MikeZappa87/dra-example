package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"k8s.io/klog/v2"
	drapb "k8s.io/kubelet/pkg/apis/dra/v1"
)

const (
	resourceFileName = "file1"
	cdiDir           = "/var/run/cdi"
	cdiVersion       = "0.5.0"
)

// Driver implements the DRA plugin interface
type Driver struct {
	drapb.UnimplementedDRAPluginServer

	driverName   string
	nodeName     string
	pluginSocket string
	resourceDir  string

	server *grpc.Server
	mu     sync.Mutex

	// Track which pods are using resources
	podResources map[string]string // claimUID -> podName
}

// New creates a new DRA driver instance
func New(driverName, nodeName, pluginSocket, resourceDir string) (*Driver, error) {
	return &Driver{
		driverName:   driverName,
		nodeName:     nodeName,
		pluginSocket: pluginSocket,
		resourceDir:  resourceDir,
		podResources: make(map[string]string),
	}, nil
}

// Start starts the gRPC server
func (d *Driver) Start(ctx context.Context) error {
	// Remove existing socket file if it exists
	socketDir := filepath.Dir(d.pluginSocket)
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}

	if err := os.Remove(d.pluginSocket); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing socket: %w", err)
	}

	listener, err := net.Listen("unix", d.pluginSocket)
	if err != nil {
		return fmt.Errorf("failed to listen on socket: %w", err)
	}

	d.server = grpc.NewServer()
	drapb.RegisterDRAPluginServer(d.server, d)

	klog.Infof("DRA driver listening on %s", d.pluginSocket)

	// Run server in goroutine
	errCh := make(chan error, 1)
	go func() {
		if err := d.server.Serve(listener); err != nil {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		klog.Info("Shutting down gRPC server")
		d.server.GracefulStop()
		return nil
	case err := <-errCh:
		return fmt.Errorf("gRPC server failed: %w", err)
	}
}

// NodePrepareResources prepares resources for a pod
func (d *Driver) NodePrepareResources(ctx context.Context, req *drapb.NodePrepareResourcesRequest) (*drapb.NodePrepareResourcesResponse, error) {
	klog.Infof("NodePrepareResources called with %d claims", len(req.Claims))

	resp := &drapb.NodePrepareResourcesResponse{
		Claims: make(map[string]*drapb.NodePrepareResourceResponse),
	}

	for _, claim := range req.Claims {
		klog.Infof("Preparing resource for claim: %s, namespace: %s, pod: %s/%s",
			claim.Uid, claim.Namespace, claim.Namespace, claim.Name)

		// Get pod name from the claim - in DRA, the pod info comes from structured parameters
		// For this demo, we'll extract it from the claim name or use a placeholder
		podName := d.extractPodName(claim)

		if err := d.prepareResource(claim.Uid, podName); err != nil {
			klog.Errorf("Failed to prepare resource for claim %s: %v", claim.Uid, err)
			resp.Claims[claim.Uid] = &drapb.NodePrepareResourceResponse{
				Error: err.Error(),
			}
			continue
		}

		// Create CDI spec for this claim
		cdiDeviceID := fmt.Sprintf("%s/file=%s", d.driverName, resourceFileName)
		if err := d.createCDISpec(claim.Uid); err != nil {
			klog.Errorf("Failed to create CDI spec for claim %s: %v", claim.Uid, err)
			resp.Claims[claim.Uid] = &drapb.NodePrepareResourceResponse{
				Error: err.Error(),
			}
			continue
		}

		// Return CDI device ID - containerd will use this to inject the file mount
		resp.Claims[claim.Uid] = &drapb.NodePrepareResourceResponse{
			Devices: []*drapb.Device{
				{
					PoolName:     "default",
					DeviceName:   resourceFileName,
					CdiDeviceIds: []string{cdiDeviceID},
				},
			},
		}

		klog.Infof("Successfully prepared resource for claim %s, pod %s, CDI device: %s", claim.Uid, podName, cdiDeviceID)
	}

	return resp, nil
}

// NodeUnprepareResources unprepares resources when a pod is done
func (d *Driver) NodeUnprepareResources(ctx context.Context, req *drapb.NodeUnprepareResourcesRequest) (*drapb.NodeUnprepareResourcesResponse, error) {
	klog.Infof("NodeUnprepareResources called with %d claims", len(req.Claims))

	resp := &drapb.NodeUnprepareResourcesResponse{
		Claims: make(map[string]*drapb.NodeUnprepareResourceResponse),
	}

	for _, claim := range req.Claims {
		klog.Infof("Unpreparing resource for claim: %s", claim.Uid)

		if err := d.unprepareResource(claim.Uid); err != nil {
			klog.Errorf("Failed to unprepare resource for claim %s: %v", claim.Uid, err)
			resp.Claims[claim.Uid] = &drapb.NodeUnprepareResourceResponse{
				Error: err.Error(),
			}
			continue
		}

		// Delete CDI spec
		if err := d.deleteCDISpec(claim.Uid); err != nil {
			klog.Warningf("Failed to delete CDI spec for claim %s: %v", claim.Uid, err)
		}

		resp.Claims[claim.Uid] = &drapb.NodeUnprepareResourceResponse{}
		klog.Infof("Successfully unprepared resource for claim %s", claim.Uid)
	}

	return resp, nil
}

// extractPodName extracts the pod name from claim information
func (d *Driver) extractPodName(claim *drapb.Claim) string {
	// In a real implementation, this would come from structured parameters
	// or the ResourceClaimParameters. For this demo, we'll construct a meaningful name.
	if claim.Name != "" {
		return fmt.Sprintf("pod-using-%s", claim.Name)
	}
	return fmt.Sprintf("pod-%s", claim.Uid[:8])
}

// prepareResource writes the pod name to the resource file
func (d *Driver) prepareResource(claimUID, podName string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	filePath := filepath.Join(d.resourceDir, resourceFileName)

	// Write pod name to file (overwrite)
	content := fmt.Sprintf("%s (claim: %s)\n", podName, claimUID)

	// Write to file
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write resource file: %w", err)
	}

	// Track the pod
	d.podResources[claimUID] = podName

	klog.Infof("Wrote pod name '%s' to %s", podName, filePath)
	return nil
}

// unprepareResource removes the pod name from the resource file
func (d *Driver) unprepareResource(claimUID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	podName, ok := d.podResources[claimUID]
	if !ok {
		klog.Warningf("No tracked pod for claim %s", claimUID)
		return nil
	}

	filePath := filepath.Join(d.resourceDir, resourceFileName)

	// Read existing content
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read resource file: %w", err)
	}

	// Remove the pod entry
	lines := strings.Split(string(data), "\n")
	var newLines []string
	searchPattern := fmt.Sprintf("%s (claim: %s)", podName, claimUID)

	for _, line := range lines {
		if line != "" && line != searchPattern {
			newLines = append(newLines, line)
		}
	}

	// Write back
	newContent := strings.Join(newLines, "\n")
	if newContent != "" && !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}

	if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to write resource file: %w", err)
	}

	// Remove tracking
	delete(d.podResources, claimUID)

	klog.Infof("Removed pod name '%s' from %s", podName, filePath)
	return nil
}

// CDI spec structures
type cdiSpec struct {
	CDIVersion string      `json:"cdiVersion"`
	Kind       string      `json:"kind"`
	Devices    []cdiDevice `json:"devices"`
}

type cdiDevice struct {
	Name           string            `json:"name"`
	ContainerEdits cdiContainerEdits `json:"containerEdits"`
}

type cdiContainerEdits struct {
	Mounts []cdiMount `json:"mounts,omitempty"`
}

type cdiMount struct {
	HostPath      string   `json:"hostPath"`
	ContainerPath string   `json:"containerPath"`
	Options       []string `json:"options,omitempty"`
}

// createCDISpec creates a CDI spec file for the claim
func (d *Driver) createCDISpec(claimUID string) error {
	// Ensure CDI directory exists
	if err := os.MkdirAll(cdiDir, 0755); err != nil {
		return fmt.Errorf("failed to create CDI directory: %w", err)
	}

	filePath := filepath.Join(d.resourceDir, resourceFileName)

	spec := cdiSpec{
		CDIVersion: cdiVersion,
		Kind:       fmt.Sprintf("%s/file", d.driverName),
		Devices: []cdiDevice{
			{
				Name: resourceFileName,
				ContainerEdits: cdiContainerEdits{
					Mounts: []cdiMount{
						{
							HostPath:      filePath,
							ContainerPath: filePath,
							Options:       []string{"ro", "bind"},
						},
					},
				},
			},
		},
	}

	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal CDI spec: %w", err)
	}

	// CDI spec filename: <driver>-<device>.json
	cdiFileName := fmt.Sprintf("%s-file-%s.json", strings.ReplaceAll(d.driverName, "/", "-"), resourceFileName)
	cdiFilePath := filepath.Join(cdiDir, cdiFileName)

	if err := os.WriteFile(cdiFilePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write CDI spec: %w", err)
	}

	klog.Infof("Created CDI spec at %s", cdiFilePath)
	return nil
}

// deleteCDISpec removes the CDI spec file for the claim
func (d *Driver) deleteCDISpec(claimUID string) error {
	cdiFileName := fmt.Sprintf("%s-file-%s.json", strings.ReplaceAll(d.driverName, "/", "-"), resourceFileName)
	cdiFilePath := filepath.Join(cdiDir, cdiFileName)

	if err := os.Remove(cdiFilePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete CDI spec: %w", err)
	}

	klog.Infof("Deleted CDI spec at %s", cdiFilePath)
	return nil
}
