package main

import ( 
	"fmt"
	"time"
	"os"
	"net"
	"os/exec"
	"path/filepath"
	"syscall"
	"github.com/vishvananda/netlink"
)


// usage: go run main.go run <command>

func main() { 
	switch os.Args[1] { 
	case "run":
		run()
	case "child":
		child()
	default:
		panic("invalid command")
	}
}



func setupNetwork() { 
	// 1. Create Bridge named "br0"
	la := netlink.NewLinkAttrs()
	la.Name = "br0"
	br := &netlink.Bridge{LinkAttrs: la}
	if err := netlink.LinkAdd(br); err != nil { 
		fmt.Printf("Bridge exists or error: %v\n", err)
	}


	// 2. Give the bridge an IP address aka the container's gateway 
	addr, _ := netlink.ParseAddr("10.0.0.1/24")
	netlink.AddrAdd(br, addr)


	// 3. Bring the bridge UP
	netlink.LinkSetUp(br)


	// 4. create the Veth Pair 
	veth := &netlink.Veth{ 
		LinkAttrs: netlink.LinkAttrs{
			Name: "veth_host",
		},
		PeerName: "veth_child",
	}
	
	if err := netlink.LinkAdd(veth); err != nil { 
		fmt.Printf("Failed to create veth pair: %v\n", err)
		return
	}

	// 5. Connect host-end to the bridge 
	vethHost, _ := netlink.LinkByName("veth_host")
	netlink.LinkSetMaster(vethHost, br)
	netlink.LinkSetUp(vethHost)
}


func setupContainerNetwork() { 
	
	// waits for the parent to push the veth thru the namespace
	time.Sleep(1 * time.Second)

	// turn on the loopback interface (127.0.0.1) -> req. by linux
	lo, _ := netlink.LinkByName("lo")
	netlink.LinkSetUp(lo)

	// catch the cable
	eth, err := netlink.LinkByName("veth_child")
	if err != nil {
		fmt.Printf("Container network error: %v\n", err)
		return
	}
		
	// rename the veth to "eth0" 
	netlink.LinkSetDown(eth)
	netlink.LinkSetName(eth, "eth0")
	
	// give it to the 10.0.0.2 address
	addr, _ := netlink.ParseAddr("10.0.0.2/24")
	netlink.AddrAdd(eth, addr)

	netlink.LinkSetUp(eth)

	// add the default gateway
	gw := net.ParseIP("10.0.0.1")
	route := &netlink.Route { 
		Scope: netlink.SCOPE_UNIVERSE,
		Gw: gw,
	}

	netlink.RouteAdd(route)

}


func run() { 
	fmt.Printf("Running %v as PID %d\n", os.Args[2:], os.Getpid())
	
	setupNetwork()
		
	// We call our own binary again, but with the "child" argument
	cmd := exec.Command("/proc/self/exe", append([]string{"child"}, os.Args[2:]...)...)

	// We tell the kernal to create new namespaces
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{ 
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWNET | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
	}


	if err := cmd.Start(); err != nil { 
		fmt.Printf("Error starting: %v\n", err)
		os.Exit(1)
	}

		
	pid := cmd.Process.Pid
	
	//trying to find the end of the link
	vethChild, err := netlink.LinkByName("veth_child")
	if err != nil { 
		fmt.Printf("Error: Could not find veth_child: %v\n", err)
		return //STOP HERE SO WE DONT CRASH 
	}
	
	// checking if veth_child is nil just in case
	if vethChild == nil { 
		fmt.Println("Error: veth_child is nil")
		return 
	}

	if err := netlink.LinkSetNsPid(vethChild, pid); err != nil { 
		fmt.Printf("Error teleporting cable: %v\n", err)
		return
	}
	
	if err := cmd.Wait(); err != nil { 
		fmt.Printf("Error running child: %v\n", err)
		os.Exit(1)
	}
}


func child() { 
	fmt.Printf("Running %v as PID %d inside the container\n", os.Args[2:], os.Getpid())

	limit()
	containerPath := "/home/ubuntu/container-fs/alpine"
	syscall.Sethostname([]byte("google-cloud-lab"))

	// unshare the mount namespace from the host
	syscall.Unshare(syscall.CLONE_NEWNS)


	// make all future mounts private -> prevents the PTY/Host-crash error
	syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, "")


	// Phase 2: Mount /proc so 'ps' works
	// MS_NOEXEC (no programs run from here), MS_NOSUID (ignore set-user-ID bits), MS_NODEV (no device files) 
	// defaultMountFlags := syscall.MS_NOEXEC | syscall.MS_NOSUID | syscall.MS_NODEV
	// syscall.Mount("proc", "/proc", "proc", uintptr(defaultMountFlags), "")



	containerDevPath := filepath.Join(containerPath, "dev")
	syscall.Mount("/dev", containerDevPath, "", syscall.MS_BIND, "")


	// Phase 3: The Jail 
	// 3.1 Change the root to our Apline folder
	if err := syscall.Chroot(containerPath); err != nil { 
		fmt.Printf("Chroot error: %v\n", err)
		os.Exit(1)
	}

	// 3.2 Move the working directory to the new root
	if err := os.Chdir("/"); err != nil { 
		fmt.Printf("Chdir error: %v\n", err)
		os.Exit(1)
	}


	// 3.3 Mount /proc INSIDE the new root
	syscall.Mount("proc", "/proc", "proc", 0, "")

	
	setupContainerNetwork()	
	
	err := os.WriteFile("/etc/resolv.conf", []byte("nameserver 8.8.8.8\n"), 0644)
	if err != nil { 
		fmt.Printf("Warning: Could not create resolv.conf: %v\n", err)
	}

	cmd := exec.Command(os.Args[2], os.Args[3:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil { 
		fmt.Printf("Error: %v\n", err)
	}

	
	// Cleanup: Unmount after the command finishes
	syscall.Unmount("/proc", 0)

}


func limit() { 
	cgroups := "/sys/fs/cgroup/"
	pnet := filepath.Join(cgroups, "google-cloud-lab")

	// 1. Create a new cgroup folder
	os.Mkdir(pnet, 0755)

	// 2. Set Memory Limit to 10MB (10 * 1024 * 1024 bytes)
	// for Cgroup v2 (modern linux)
	err := os.WriteFile(filepath.Join(pnet, "memory.max"), []byte("10485760"), 0700)
	if err != nil { 
		fmt.Printf("Error setting memory linit: %v\n", err)
	}


	// 3. Add our current process to this cgroup 
	// Writing '0' to cgroup.procs tells the kernel to add the current PID
	err = os.WriteFile(filepath.Join(pnet, "cgroup.procs"), []byte("0"), 0700)
	if err != nil { 
		fmt.Printf("Error adding process to cgroup: %v\n", err)
	}


	// 4. Cleanup the cgroup after the process exits
	// In a real runtime, we would delete this folder once the container stops 
}




