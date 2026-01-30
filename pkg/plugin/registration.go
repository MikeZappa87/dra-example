package plugin

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"google.golang.org/grpc"
	"k8s.io/klog/v2"
	registerapi "k8s.io/kubelet/pkg/apis/pluginregistration/v1"
)

// RegistrationServer implements the kubelet plugin registration service
type RegistrationServer struct {
	registerapi.UnimplementedRegistrationServer
	driverName        string
	endpoint          string
	supportedVersions []string
}

// NewRegistrationServer creates a new registration server
func NewRegistrationServer(driverName, endpoint string, supportedVersions []string) *RegistrationServer {
	return &RegistrationServer{
		driverName:        driverName,
		endpoint:          endpoint,
		supportedVersions: supportedVersions,
	}
}

// GetInfo returns plugin info for registration
func (r *RegistrationServer) GetInfo(ctx context.Context, req *registerapi.InfoRequest) (*registerapi.PluginInfo, error) {
	klog.Infof("GetInfo called - returning plugin info for %s", r.driverName)
	return &registerapi.PluginInfo{
		Type:              registerapi.DRAPlugin,
		Name:              r.driverName,
		Endpoint:          r.endpoint,
		SupportedVersions: r.supportedVersions,
	}, nil
}

// NotifyRegistrationStatus handles registration status notifications
func (r *RegistrationServer) NotifyRegistrationStatus(ctx context.Context, status *registerapi.RegistrationStatus) (*registerapi.RegistrationStatusResponse, error) {
	if status.PluginRegistered {
		klog.Infof("Plugin %s registered successfully", r.driverName)
	} else {
		klog.Errorf("Plugin %s registration failed: %s", r.driverName, status.Error)
	}
	return &registerapi.RegistrationStatusResponse{}, nil
}

// Start starts the registration server on the given socket path
func (r *RegistrationServer) Start(ctx context.Context, socketPath string) error {
	// Ensure socket directory exists
	socketDir := filepath.Dir(socketPath)
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}

	// Remove existing socket
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing socket: %w", err)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket: %w", err)
	}

	server := grpc.NewServer()
	registerapi.RegisterRegistrationServer(server, r)

	klog.Infof("Registration server listening on %s", socketPath)

	// Run server
	errCh := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		klog.Info("Shutting down registration server")
		server.GracefulStop()
		return nil
	case err := <-errCh:
		return fmt.Errorf("registration server failed: %w", err)
	}
}
