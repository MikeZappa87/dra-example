package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
	"k8s.io/klog/v2"

	"github.com/example/dra-poc/pkg/driver"
	"github.com/example/dra-poc/pkg/plugin"
)

var (
	driverName         string
	nodeName           string
	pluginSocket       string
	registrationSocket string
	resourceDir        string
)

func main() {
	cmd := &cobra.Command{
		Use:   "dra-file-driver",
		Short: "DRA driver that manages file resources",
		Run:   run,
	}

	cmd.Flags().StringVar(&driverName, "driver-name", "file.dra.example.com", "Name of the DRA driver")
	cmd.Flags().StringVar(&nodeName, "node-name", "", "Name of the node (from downward API)")
	cmd.Flags().StringVar(&pluginSocket, "plugin-socket", "/var/lib/kubelet/plugins/file.dra.example.com/plugin.sock", "Path to the plugin socket")
	cmd.Flags().StringVar(&registrationSocket, "registration-socket", "", "Path to the registration socket (defaults to kubelet plugin registration dir)")
	cmd.Flags().StringVar(&resourceDir, "resource-dir", "/etc/dra", "Directory for resource files")

	if err := cmd.Execute(); err != nil {
		klog.Fatal(err)
	}
}

func run(cmd *cobra.Command, args []string) {
	if nodeName == "" {
		nodeName = os.Getenv("NODE_NAME")
		if nodeName == "" {
			klog.Fatal("node-name is required (use --node-name or NODE_NAME env var)")
		}
	}

	// Default registration socket path
	if registrationSocket == "" {
		registrationSocket = filepath.Join("/var/lib/kubelet/plugins_registry", driverName+"-reg.sock")
	}

	klog.Infof("Starting DRA file driver: %s on node %s", driverName, nodeName)
	klog.Infof("Plugin socket: %s", pluginSocket)
	klog.Infof("Registration socket: %s", registrationSocket)
	klog.Infof("Resource directory: %s", resourceDir)

	// Create the resource directory if it doesn't exist
	if err := os.MkdirAll(resourceDir, 0755); err != nil {
		klog.Fatalf("Failed to create resource directory: %v", err)
	}

	// Create and start the driver
	d, err := driver.New(driverName, nodeName, pluginSocket, resourceDir)
	if err != nil {
		klog.Fatalf("Failed to create driver: %v", err)
	}

	// Create the registration server
	// The kubelet expects version strings in the format "v1.DRAPlugin" or "v1beta1.DRAPlugin"
	regServer := plugin.NewRegistrationServer(
		driverName,
		pluginSocket,
		[]string{"v1.DRAPlugin"},
	)

	// Create the resource publisher (to publish ResourceSlices)
	publisher, err := driver.NewResourcePublisher(driverName, nodeName, "file1")
	if err != nil {
		klog.Warningf("Failed to create resource publisher (will continue without it): %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		klog.Infof("Received signal %v, shutting down", sig)
		cancel()
	}()

	// Start resource publisher in background
	if publisher != nil {
		go publisher.StartPublishing(ctx)
	}

	// Start registration server in background
	go func() {
		if err := regServer.Start(ctx, registrationSocket); err != nil {
			klog.Errorf("Registration server failed: %v", err)
		}
	}()

	// Start the DRA driver (blocking)
	if err := d.Start(ctx); err != nil {
		klog.Fatalf("Driver failed: %v", err)
	}

	fmt.Println("Driver stopped")
}
