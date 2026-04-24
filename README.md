# Go Container Runtime from Scratch

A low-level Linux container runtime implemented in Go. This project explores the kernel primitives that make modern containerization (like Docker and Kubernetes) possible.

## 🚀 Features

- **Namespaces (Isolation):** Uses `UTS`, `PID`, and `NS` flags to isolate the container's hostname and process tree.
- **Filesystem Jailing:** Implements `chroot` and `mount` to create a dedicated root filesystem using Alpine Linux.
- **Resource Limiting (Cgroups v2):** Manages hardware resources by interfacing with the Linux Control Groups virtual filesystem to set memory hard limits.
- **Device Management:** Uses bind mounts to provide access to system devices (like `/dev/zero`) within the isolated environment.
- **Virtual Networking:** Implements a `veth` pair strategy to connect the container to a host-side bridge (`br0`).
- **Internet Access & DNS:** Configures IP masquerading (NAT) for outbound traffic and provisions `/etc/resolv.conf` for domain name resolution.

## 🛠️ Technical Deep Dive

### Cgroups
The runtime creates a custom cgroup hierarchy at `/sys/fs/cgroup/google-cloud-lab`. It enforces a **10MB memory limit**. If a process inside the container attempts to exceed this, the Linux **OOM (Out Of Memory) Killer** will immediately terminate the process to protect host stability.

### Handshake Sequence
1. **Parent Process:** Fork/Execs a child process with specific Namespace flags.
2. **Child Process:**
   - Moves the child-end of the `veth` pair into the new Network Namespace.
   - Sets up the loopback interface and assigns a private IP to the `veth` interface.
   - Sets a custom Hostname.
   - Applies Cgroup limits to its own PID.
   - Mounts the host's `/dev` into the target rootfs.
   - Calls `chroot` and `chdir` to lock the filesystem.
   - Mounts `/proc` for process visibility.
   - Writes DNS settings to `/etc/resolv.conf` before executing the command.
   - Replaces itself with the target command (e.g., `/bin/sh`).

## 💻 How to Run

### Prerequisites
- A Linux environment (Ubuntu 22.04+ recommended).
- Go 1.18+ installed.
- An Alpine Linux rootfs extracted to `~/container-fs/alpine`.

### Steps
1. **Build the binary:**
   ```bash
   go build -o my-container main.go

2. **Run the container:**
   ```bash
   sudo ./my-container run /bin/sh

3. **Test Resource Limits:**
     ```bash
   dd if=/dev/zero of=/dev/shm/testfile bs=1M count=15

## 🛤️ Roadmap
- [x] Namespaces and Hostname isolation
- [x] Filesystem isolation (Chroot)
- [x] Resource metering (Cgroups)
- [x] Next Up: Virtual Networking (Veth Pairs & Bridges)
