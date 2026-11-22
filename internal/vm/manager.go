package vm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Manager wraps the libvirt CLI tools (virt-install, virsh, qemu-img) as a
// library for the VM reconciler. It is deliberately CLI-based: the node image
// already ships the tools for local libvirt management, and shelling out
// keeps the agent free of cgo/bindings and of libvirt version churn.
//
// Naming: every VM the reconciler manages becomes the libvirt domain
// "c12s-vm-<name>", so it can never collide with VMs provisioned by other
// tools on the same host (e.g. wormhole's "c12s-<name>" domains).

const (
	defaultConnectURI = "qemu:///system"
	defaultNetwork    = "default"
	defaultOSVariant  = "fedora-unknown"
	// host CPU without AMD-V/VT-x; QEMU accepts disabling the flag the CPU
	// vendor does not have
	plainCPU         = "host-passthrough,disable=svm,disable=vmx"
	domainNamePrefix = "c12s-vm-"
	diskSizeGB       = 20
)

type ManagerConfig struct {
	ConnectURI string
	Network    string
	OSVariant  string
	UEFI       bool
}

type Manager struct {
	cfg      ManagerConfig
	stateDir string
}

func NewManager(cfg ManagerConfig, stateDir string) (*Manager, error) {
	if cfg.ConnectURI == "" {
		cfg.ConnectURI = defaultConnectURI
	}
	if cfg.Network == "" {
		cfg.Network = defaultNetwork
	}
	if cfg.OSVariant == "" {
		cfg.OSVariant = defaultOSVariant
	}
	for _, bin := range []string{"virt-install", "virsh", "xorriso", "qemu-img"} {
		if _, err := exec.LookPath(bin); err != nil {
			return nil, fmt.Errorf("vm manager: %s not found in PATH", bin)
		}
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create vm state dir: %w", err)
	}
	// the qemu user must traverse the state dir to reach VM disks; MkdirAll
	// does not fix the mode of a pre-existing directory
	if err := os.Chmod(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("chmod vm state dir: %w", err)
	}
	return &Manager{cfg: cfg, stateDir: stateDir}, nil
}

// VMDir is the per-VM state directory (disk overlay, seed, writer files).
func (m *Manager) VMDir(name string) string {
	return filepath.Join(m.stateDir, name)
}

func (m *Manager) domain(name string) string {
	return domainNamePrefix + name
}

// CreateSpec describes a VM to instantiate from a cached qcow2 artifact.
type CreateSpec struct {
	Name     string
	DiskPath string // cached base qcow2 (stays pristine; per-VM copy is used)
	SeedPath string // NoCloud seed.iso built by BuildSeed
	MAC      string
	VCPUs    uint32
	MemoryMB uint32
}

// Create imports a new libvirt domain backed by a per-VM reflink copy of the
// cached qcow2 plus a qcow2 overlay (writes land in the overlay; libvirt's
// dynamic ownership chowns the copy, never the cache — same lesson as
// wormhole's libvirt backend).
func (m *Manager) Create(spec CreateSpec) error {
	if spec.Name == "" || spec.DiskPath == "" || spec.SeedPath == "" {
		return fmt.Errorf("vm create: name, disk and seed are required")
	}
	if m.Exists(spec.Name) {
		return fmt.Errorf("vm create: domain %s already exists", m.domain(spec.Name))
	}
	if spec.VCPUs == 0 {
		spec.VCPUs = 2
	}
	if spec.MemoryMB == 0 {
		spec.MemoryMB = 2048
	}

	dir := m.VMDir(spec.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create vm dir: %w", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return fmt.Errorf("chmod vm dir: %w", err)
	}
	base := filepath.Join(dir, "base.qcow2")
	if err := copyIfNeeded(spec.DiskPath, base); err != nil {
		return fmt.Errorf("copy base image: %w", err)
	}
	disk := filepath.Join(dir, "disk.qcow2")
	if err := createOverlay(base, disk); err != nil {
		return err
	}

	args := []string{
		"--connect", m.cfg.ConnectURI,
		"--import", "--noautoconsole",
		"--name", m.domain(spec.Name),
		"--memory", fmt.Sprintf("%d", spec.MemoryMB),
		"--vcpus", fmt.Sprintf("%d", spec.VCPUs),
		"--disk", "path=" + disk + ",format=qcow2",
		"--disk", "path=" + spec.SeedPath + ",device=cdrom",
		"--network", "network=" + m.cfg.Network + ",model=virtio,mac=" + spec.MAC,
		"--os-variant", m.cfg.OSVariant,
		"--graphics", "none",
		// guests are plain nodes: no virtualization flags, so they have no
		// /dev/kvm and do not report run_vms
		"--cpu", plainCPU,
	}
	if m.cfg.UEFI {
		// bootc-image-builder images are UEFI-only
		args = append(args, "--boot", "uefi")
	}
	cmd := exec.Command("virt-install", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("virt-install %s: %s: %w", spec.Name, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (m *Manager) Start(name string) error {
	return m.virsh("start", m.domain(name))
}

func (m *Manager) Shutdown(name string) error {
	return m.virsh("shutdown", m.domain(name))
}

// Destroy removes the libvirt domain and every file the reconciler created
// for it (overlay, per-VM copy, seed). The cached qcow2 is untouched.
func (m *Manager) Destroy(name string) error {
	domain := m.domain(name)
	if m.Exists(name) {
		_ = m.virsh("destroy", domain)
		if err := m.virsh("undefine", domain, "--nvram", "--remove-all-storage"); err != nil {
			if err2 := m.virsh("undefine", domain, "--nvram"); err2 != nil {
				return fmt.Errorf("undefine %s: %w", domain, err2)
			}
		}
	}
	if err := os.RemoveAll(m.VMDir(name)); err != nil {
		return fmt.Errorf("remove vm state for %s: %w", name, err)
	}
	return nil
}

// Exists reports whether a domain for this VM is defined (running or not).
func (m *Manager) Exists(name string) bool {
	return m.virsh("domid", m.domain(name)) == nil
}

// State returns the libvirt domain state: "running", "shut off", "paused", ...
func (m *Manager) State(name string) (string, error) {
	out, err := m.virshOutput("domstate", m.domain(name))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// List returns the names of all domains created by the reconciler
// ("c12s-vm-" prefix stripped), including stopped ones.
func (m *Manager) List() ([]string, error) {
	out, err := m.virshOutput("list", "--all", "--name")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		domain := strings.TrimSpace(line)
		if strings.HasPrefix(domain, domainNamePrefix) {
			names = append(names, strings.TrimPrefix(domain, domainNamePrefix))
		}
	}
	return names, nil
}

func (m *Manager) virsh(args ...string) error {
	_, err := m.virshOutput(args...)
	return err
}

func (m *Manager) virshOutput(args ...string) (string, error) {
	cmd := exec.Command("virsh", append([]string{"-c", m.cfg.ConnectURI}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("virsh %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}

// copyReflink copies a file using CoW reflink when the filesystem supports
// it (instant for multi-GB images), falling back to a plain copy.
func copyReflink(src, dst string) error {
	if _, err := exec.LookPath("cp"); err == nil {
		if err := exec.Command("cp", "--reflink=auto", src, dst).Run(); err == nil {
			return nil
		}
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// copyIfNeeded copies src to dst unless dst is a completed copy of the same
// size — Create is retried every reconcile tick, and re-copying a multi-GB
// image on a filesystem without reflink would be wasteful.
func copyIfNeeded(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if dstInfo, err := os.Stat(dst); err == nil && dstInfo.Size() == srcInfo.Size() {
		return nil
	}
	return copyReflink(src, dst)
}

// createOverlay makes a qcow2 overlay backed by the per-VM copy of the
// cached image.
func createOverlay(source, target string) error {
	absSource, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	cmd := exec.Command("qemu-img", "create", "-f", "qcow2", "-F", "qcow2",
		"-b", absSource, target, fmt.Sprintf("%dG", diskSizeGB))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("qemu-img create: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
