package startup

import (
	"context"
	"log"
	"os"
	"os/exec"
	"time"

	magnetarapi "github.com/c12s/magnetar/pkg/api"
	"github.com/c12s/star/internal/bootc"
)

// libvirtSocketPaths covers both daemon layouts: the monolithic libvirtd
// socket and the modular virtqemud socket (default on modern Fedora). The VM reconciler shells out to virsh/virt-install, which
// reach either daemon transparently.
var libvirtSocketPaths = []string{
	"/var/run/libvirt/libvirt-sock",
	"/run/libvirt/virtqemud-sock",
}

func discoverCapabilities() []string {
	capabilities := []string{magnetarapi.CapabilityConfig, magnetarapi.CapabilityGossip}
	if commandExists("bootc") {
		capabilities = append(capabilities, magnetarapi.CapabilityOsUpdate)
	}
	if canRunVMs(libvirtSocketPaths, kvmDevice) {
		capabilities = append(capabilities, magnetarapi.CapabilityRunVms)
	}
	return capabilities
}

// kvmDevice exists only where hardware virtualization reaches this OS: on
// bare metal, or in a VM whose CPU passes the virtualization flags through.
const kvmDevice = "/dev/kvm"

// canRunVMs needs a libvirt daemon and /dev/kvm. libvirt is in every image,
// so the socket alone is not enough: a VM without nested KVM has the socket
// but cannot start guests.
func canRunVMs(sockets []string, kvm string) bool {
	if _, err := os.Stat(kvm); err != nil {
		return false
	}
	for _, path := range sockets {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// syncNodeState reports capabilities and OS status, and tells whether
// Magnetar still knows the node. Only an explicit notFound answer counts as
// unknown: no answer (CP down, storage error) proves nothing, and
// re-registering then would orphan the node's org, policies and VMs.
func syncNodeState(client *magnetarapi.NodeUpdateAsyncClient, nodeId string, capabilities []string, osStatus *magnetarapi.OsStatus) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := client.Update(ctx, &magnetarapi.NodeUpdateReq{
		NodeId:       nodeId,
		Capabilities: capabilities,
		OsStatus:     osStatus,
	})
	if err != nil {
		log.Printf("node state sync failed (%v), keeping node id", err)
		return true
	}
	return !resp.GetNotFound()
}

func buildOsStatus() *magnetarapi.OsStatus {
	bootcStatus := bootc.ProbeStatus()
	if bootcStatus == nil {
		return nil
	}
	return &magnetarapi.OsStatus{
		BootedImage:   bootcStatus.Image,
		BootedDigest:  bootcStatus.Digest,
		BootedVersion: bootcStatus.Version,
		Phase:         magnetarapi.OsPhaseHealthy,
	}
}
