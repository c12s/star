package vm

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// VMNodeID is the node id of a VM: UUIDv5 with the host's node id as the
// namespace and the VM name as the name. cockpit derives the same id from a
// fleet spec, so a VM's id is known before it exists, and a re-created VM
// registers with its old id.
func VMNodeID(hostID, vmName string) (string, error) {
	ns, err := uuid.Parse(hostID)
	if err != nil {
		return "", fmt.Errorf("host id %q: %w", hostID, err)
	}
	return uuid.NewSHA1(ns, []byte(vmName)).String(), nil
}

// diskURL turns a VM disk into a download URL. A full URL is used as is;
// a bare artifact name is fetched from the artifact server named by
// DISK_REGISTRY_URL (shipped in the image's node.env).
func diskURL(disk, diskRegistryURL string) (string, error) {
	if strings.Contains(disk, "://") {
		return disk, nil
	}
	if diskRegistryURL == "" {
		return "", fmt.Errorf("disk %q is an artifact name but DISK_REGISTRY_URL is not set", disk)
	}
	return strings.TrimRight(diskRegistryURL, "/") + "/artifacts/" + disk, nil
}
