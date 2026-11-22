package vm

import (
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// NoCloud seed generation for VMs created by the reconciler (phase-next.md
// §2.6.6). Modeled on wormhole's provisioning seed. The control-plane
// addresses are not in the seed: the VMs boot the same OS image as every
// node, which ships them in /usr/lib/c12s/node.env. The seed carries only
// per-VM values (identity, network) and an optional NATS override.
//
// The seed is produced out of band of the control plane: it writes
// /var/lib/c12s/node.env inside the guest; the guest's own Star enrolls on
// first boot exactly like a bare-metal node.

// SeedSpec describes one VM's first-boot configuration.
type SeedSpec struct {
	Dir         string            // per-VM directory for meta-data/user-data/network-config/seed.iso
	Domain      string            // libvirt domain name (also cloud-init instance-id)
	MAC         string            // deterministic MAC; the NIC is matched by this address
	Address     string            // static IPv4 for the guest
	Gateway     string            // gateway of the VM network
	NATSAddress string            // optional override; the default ships in the image
	NodeID      string            // written to /var/lib/c12s/nodeid: the VM registers with this id
	Labels      map[string]string // NODE_LABELS: string labels the VM's star registers with
	Extras      map[string]string
}

// BuildSeed renders the NoCloud seed files and packs them into seed.iso
// (volume label `cidata`, picked up by cloud-init on first boot).
func BuildSeed(spec SeedSpec) (string, error) {
	if spec.Dir == "" || spec.Domain == "" {
		return "", fmt.Errorf("seed dir and domain are required")
	}
	if spec.MAC == "" || spec.Address == "" || spec.Gateway == "" {
		return "", fmt.Errorf("seed for %s: mac, address and gateway are required", spec.Domain)
	}
	if err := os.MkdirAll(spec.Dir, 0o755); err != nil {
		return "", fmt.Errorf("create seed dir: %w", err)
	}
	// MkdirAll/WriteFile do not change the mode of pre-existing paths (e.g.
	// from an earlier failed attempt), and qemu must be able to read the seed
	if err := os.Chmod(spec.Dir, 0o755); err != nil {
		return "", fmt.Errorf("chmod seed dir: %w", err)
	}

	metaData := fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", spec.Domain, spec.Domain)
	userData := fmt.Sprintf(`#cloud-config
users:
  - name: c12s
    gecos: c12s debug user
    groups: [wheel]
    shell: /bin/bash
    lock_passwd: false
    sudo: ALL=(ALL) NOPASSWD:ALL
chpasswd:
  expire: false
  users:
    - name: c12s
      password: c12s
      type: text
ssh_pwauth: true
write_files:
  - path: /var/lib/c12s/node.env
    permissions: '0644'
    content: |
%s
%sruncmd:
  - systemctl enable --now serial-getty@ttyS0.service
`, indent(strings.Join(nodeEnvLines(spec.NATSAddress, seedEnv(spec)), "\n"), "      "), nodeIDFile(spec.NodeID))

	networkConfig := fmt.Sprintf(`version: 1
config:
  - type: physical
    name: nic0
    mac_address: "%s"
    subnets:
      - type: static
        address: %s
        netmask: 255.255.255.0
        gateway: %s
`, spec.MAC, spec.Address, spec.Gateway)

	metaPath := filepath.Join(spec.Dir, "meta-data")
	userPath := filepath.Join(spec.Dir, "user-data")
	netPath := filepath.Join(spec.Dir, "network-config")
	if err := os.WriteFile(metaPath, []byte(metaData), 0o644); err != nil {
		return "", fmt.Errorf("write meta-data: %w", err)
	}
	if err := os.WriteFile(userPath, []byte(userData), 0o644); err != nil {
		return "", fmt.Errorf("write user-data: %w", err)
	}
	if err := os.WriteFile(netPath, []byte(networkConfig), 0o644); err != nil {
		return "", fmt.Errorf("write network-config: %w", err)
	}
	for _, path := range []string{metaPath, userPath, netPath} {
		if err := os.Chmod(path, 0o644); err != nil {
			return "", fmt.Errorf("chmod %s: %w", filepath.Base(path), err)
		}
	}

	seedPath := filepath.Join(spec.Dir, "seed.iso")
	cmd := exec.Command(
		"xorriso", "-as", "mkisofs",
		"-o", seedPath,
		"-volid", "cidata",
		"-joliet", "-rock",
		metaPath, userPath, netPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build seed iso (xorriso): %s: %w", strings.TrimSpace(string(out)), err)
	}
	return seedPath, nil
}

// seedEnv is the per-VM env: the caller's extras plus NODE_LABELS.
func seedEnv(spec SeedSpec) map[string]string {
	env := map[string]string{}
	for k, v := range spec.Extras {
		env[k] = v
	}
	if len(spec.Labels) > 0 {
		keys := make([]string, 0, len(spec.Labels))
		for k := range spec.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		pairs := make([]string, 0, len(keys))
		for _, k := range keys {
			pairs = append(pairs, k+"="+spec.Labels[k])
		}
		env["NODE_LABELS"] = strings.Join(pairs, ",")
	}
	return env
}

// nodeIDFile is the write_files entry that fixes the VM's node id; empty
// when no id is set (the VM's star then generates one).
func nodeIDFile(id string) string {
	if id == "" {
		return ""
	}
	return fmt.Sprintf("  - path: /var/lib/c12s/nodeid\n    permissions: '0644'\n    content: %q\n", id)
}

// nodeEnvLines is the per-VM part of node.env; shared settings come from the
// image's /usr/lib/c12s/node.env.
func nodeEnvLines(natsAddress string, extras map[string]string) []string {
	lines := []string{"# per-node values; defaults: /usr/lib/c12s/node.env (image)"}
	if natsAddress != "" {
		lines = append(lines, "NATS_ADDRESS="+natsAddress)
	}
	keys := make([]string, 0, len(extras))
	for k := range extras {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lines = append(lines, k+"="+extras[k])
	}
	return lines
}

// deterministicMAC maps a VM name to a stable locally-administered MAC in the
// QEMU/KVM OUI (52:54:00). Same scheme as the provisioning helper (wormhole) so
// a VM's address is reproducible from its name alone.
func deterministicMAC(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	b := h.Sum32()
	return fmt.Sprintf("52:54:00:c1:2e:%02x", b&0xFF)
}

// staticAddress derives a stable IPv4 from the VM name: trailing digits
// offset ipBase+offset (vm-1 with base 192.168.122 and offset 100 →
// 192.168.122.101). Names without digits hash into the pool.
func staticAddress(name, ipBase string, offset int) (string, error) {
	digits := ""
	for _, r := range name {
		if r >= '0' && r <= '9' {
			digits += string(r)
		}
	}
	if digits == "" {
		h := fnv.New32a()
		h.Write([]byte(name))
		digits = strconv.Itoa(int(h.Sum32()%50) + 1)
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 0 || n+offset > 254 {
		return "", fmt.Errorf("vm name %q: bad node number %q for ip base %s", name, digits, ipBase)
	}
	return ipBase + "." + strconv.Itoa(offset+n), nil
}

func gatewayFromBase(ipBase string) string {
	if !strings.HasSuffix(ipBase, ".") {
		ipBase += "."
	}
	return ipBase + "1"
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}
