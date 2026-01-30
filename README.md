# DRA (Dynamic Resource Allocation) Demo

This demo showcases Kubernetes Dynamic Resource Allocation (DRA) by implementing a simple driver that:
- Publishes a file as a resource (`/etc/dra/file1`)
- Writes the pod name to the file during `NodePrepareResources`
- Removes the pod name during `NodeUnprepareResources`

## Understanding DRA Architecture

DRA follows a **publish-subscribe pattern** where resource drivers advertise their capabilities and Kubernetes components consume that information to make scheduling and allocation decisions.

### Key Patterns

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           PUBLISH-SUBSCRIBE FLOW                            │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   ┌──────────────┐         ResourceSlice            ┌──────────────────┐    │
│   │  DRA Driver  │ ──────────────────────────────▶  │   API Server     │    │
│   │  (Publisher) │   "I have these devices          │     (Broker)     │    │
│   └──────────────┘    on this node"                 └────────┬─────────┘    │
│                                                              │              │
│                              ┌───────────────────────────────┤              │
│                              │                               │              │
│                              ▼                               ▼              │
│                    ┌──────────────────┐           ┌─────────────────┐       │
│                    │   kube-scheduler │           │     kubelet     │       │
│                    │   (Subscriber)   │           │   (Subscriber)  │       │
│                    └──────────────────┘           └─────────────────┘       │
│                              │                               │              │
│                              │ "Find node with               │ "Prepare     │
│                              │  matching devices"            │  device"     │
│                              ▼                               ▼              │
│                    ┌──────────────────┐           ┌─────────────────┐       │
│                    │  ResourceClaim   │           │   DRA Driver    │       │
│                    │  (allocated)     │◀──────────│   (gRPC call)   │       │
│                    └──────────────────┘           └─────────────────┘       │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Components and Their Roles

| Component | Role | Pub-Sub Analogy |
|-----------|------|-----------------|
| **DRA Driver** | Publishes available devices via ResourceSlices | **Publisher** |
| **API Server** | Stores ResourceSlices, ResourceClaims, DeviceClasses | **Broker** |
| **kube-scheduler** | Watches ResourceSlices to find nodes with available devices | **Subscriber** |
| **kubelet** | Calls driver to prepare/unprepare devices for pods | **Subscriber** |
| **ResourceClaim** | User's request for a device (flows through the system) | **Request** |
| **DeviceClass** | Selector/filter for device types | **Topic Filter** |

*Note: These are analogies to help understand DRA's architecture, not formal pattern names.*

### The Publish-Subscribe Flow

1. **Publishing (Driver → API Server)**
   - DRA driver discovers local devices (GPUs, FPGAs, files, etc.)
   - Creates/updates `ResourceSlice` objects describing available devices
   - Each ResourceSlice contains: driver name, node name, pool info, device list

2. **Subscribing (Scheduler watches ResourceSlices)**
   - kube-scheduler watches all ResourceSlices across the cluster
   - Builds an in-memory view of which nodes have which devices
   - When a pod needs a device, scheduler filters nodes that can satisfy the claim

3. **Allocation (Scheduler updates ResourceClaim)**
   - Scheduler picks a node and writes allocation result to ResourceClaim
   - Specifies which exact device(s) from which pool are allocated

4. **Preparation (Kubelet → Driver via gRPC)**
   - Kubelet sees pod scheduled to its node with an allocated claim
   - Calls driver's `NodePrepareResources` gRPC method
   - Driver sets up the device (mounts, permissions, config files, etc.)

5. **Cleanup (Kubelet → Driver via gRPC)**
   - When pod terminates, kubelet calls `NodeUnprepareResources`
   - Driver cleans up device state

### Why This Pattern?

- **Decoupling**: Drivers don't need to know about scheduler internals
- **Scalability**: Scheduler aggregates info from many drivers efficiently  
- **Extensibility**: New device types just need a new driver
- **Consistency**: Standard API for all resource types (GPUs, FPGAs, network devices, etc.)

## Prerequisites

- Docker
- Kind (Kubernetes in Docker)
- kubectl
- Go 1.25+

## Quick Start

```bash
# Create kind cluster with DRA enabled
make cluster

# Build and load the DRA driver image
make build load

# Deploy the DRA driver and resources
make deploy

# Test with a sample pod
make test

# Check the file content
make check

# Cleanup
make clean
```

## Project Structure

```
.
├── cmd/
│   └── dra-driver/
│       └── main.go           # DRA driver entrypoint
├── pkg/
│   ├── driver/
│   │   ├── driver.go         # DRA driver implementation (gRPC server)
│   │   └── publisher.go      # ResourceSlice publisher (pub/sub producer)
│   └── plugin/
│       └── registration.go   # Kubelet plugin registration
├── deploy/
│   ├── namespace.yaml        # dra-system namespace
│   ├── driver.yaml           # DRA driver DaemonSet
│   ├── resourceclass.yaml    # DeviceClass definition (topic filter)
│   └── deployment.yaml       # Deployment + ResourceClaimTemplate (consumer)
├── Dockerfile
├── Makefile
├── go.mod
└── README.md
```

## How It Works

### Components in This Demo

1. **DRA Driver** (`pkg/driver/driver.go`): Implements the DRA v1 gRPC interface
   - `NodePrepareResources`: Called by kubelet when pod needs the device
   - `NodeUnprepareResources`: Called by kubelet when pod releases the device
   - Generates CDI (Container Device Interface) specs for device injection

2. **ResourceSlice Publisher** (`pkg/driver/publisher.go`): Publishes device inventory
   - Creates ResourceSlice objects advertising available "file" devices
   - Periodically refreshes to maintain presence in the cluster

3. **Plugin Registration** (`pkg/plugin/registration.go`): Registers with kubelet
   - Uses kubelet's plugin registration mechanism
   - Tells kubelet where to find the driver's gRPC socket

4. **DeviceClass** (`deploy/resourceclass.yaml`): Defines which devices to select
   - Uses CEL expressions to filter devices by driver name

5. **ResourceClaimTemplate** (`deploy/deployment.yaml`): Template for per-pod claims
   - References the DeviceClass
   - Each pod replica gets its own ResourceClaim

6. **Deployment** (`deploy/deployment.yaml`): Consumes allocated devices
   - References the ResourceClaimTemplate
   - Each replica gets its own claim, enabling multi-node scaling

### End-to-End Flow

```
1. Driver starts → Registers with kubelet → Publishes ResourceSlice
                                            "node-x has device 'file1'"

2. User creates ResourceClaim → "I need a device from DeviceClass 'file-resource'"

3. User creates Pod → References the ResourceClaim

4. kube-scheduler:
   - Watches ResourceSlices (knows node-x has 'file1')
   - Filters nodes that can satisfy the claim
   - Allocates 'file1' on node-x to the claim
   - Binds pod to node-x

5. kubelet on node-x:
   - Sees pod needs prepared claim
   - Calls driver.NodePrepareResources(claim)
   - Driver writes pod name to /etc/dra/file1

6. Pod runs with access to the file

7. Pod terminates:
   - kubelet calls driver.NodeUnprepareResources(claim)
   - Driver removes pod name from the file
```

## Manual Testing

```bash
# Watch the DRA driver logs
kubectl logs -n dra-system -l app=dra-file-driver -f

# Check ResourceSlices (see what the driver published)
kubectl get resourceslices

# Check ResourceClaims (see allocation status)
kubectl get resourceclaims

# Check the file on a node (using a debug pod)
kubectl debug node/<node-name> -it --image=busybox -- cat /host/etc/dra/file1

# Or exec into a deployment pod
kubectl exec -it deploy/dra-test-deployment -- cat /etc/dra/file1
```

## Cleanup

```bash
# Remove all resources
make undeploy

# Delete the kind cluster
make cluster-delete
```

