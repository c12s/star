# star — c12s node agent

Single Go binary that runs on every c12s node (bare-metal machine, bootc VM,
or nested VM). It enrolls the node with the control plane, discovers the
node's capabilities from its environment, and runs exactly the modules those
capabilities imply — OS updates via bootc, VM reconciliation via libvirt —
while serving the existing configuration chain.

## Role in c12s

Star is the node end of every pipeline:

| Pipeline | Flow |
|---|---|
| Configuration | Kuiper (config) / Meridian (app config) → NATS → **star** → Serf gossip → local gRPC store |
| Liveness | **star** → `magnetar.node.heartbeat` every `HEARTBEAT_INTERVAL` → Magnetar's liveness view + stale-record GC |
| OS updates | `cockpit image update` → gateway → gravity → Magnetar (recorded intent) + NATS push → **star** bootc module → `bootc switch` → reboot → greenboot verdict |
| VM fleet | `cockpit vms set` → gateway → Magnetar (desired VMs) → **star** VM reconciler → libvirt VMs that themselves enroll as c12s nodes |

Everything is capability-driven: there are no node "modes". The same binary
on a plain machine runs only the config chain; on a bootc machine it adds the
OS module; on a libvirt host it adds the VM reconciler.

## Capabilities

Discovered at startup (`internal/startup/capabilities.go`), reported at
registration and on every status sync:

| Capability | Condition | What it starts |
|---|---|---|
| `config` | always | Kuiper/Meridian async clients, Serf, gRPC config server |
| `gossip` | always | Serf agent + cluster-join listener |
| `os_update` | `bootc` in PATH | bootc module (poller + `os_update` handler + reporter) |
| `run_vms` | libvirt socket exists (`libvirtd` or `virtqemud`) **and** `/dev/kvm` exists | VM reconciler |

libvirt ships in every image, so `/dev/kvm` is what tells a VM host from a
plain node (a nested VM without KVM passthrough reports no `run_vms`).

## Lifecycle

1. Connect to NATS (retries forever; reconnect is unlimited).
2. Read node id from `$NODE_ID_DIR_PATH/$NODE_ID_FILE_NAME`
   (default `/var/lib/c12s/nodeid` — in `/var` so it survives every bootc
   update and rollback).
3. If absent, register over NATS (`magnetar.registration`); store the UUID.
   Re-registration **asserts the id star already has**, so Magnetar updates
   (or recreates) the same record instead of minting a duplicate UUID — the
   id file is never deleted to "start over".
4. Sync capabilities and OS status (`magnetar.node.update`). Only an explicit
   `notFound` reply means Magnetar forgot the node — star then re-registers
   with the same id. A timeout/outage keeps the id (re-registering would
   orphan the node's org, policies and VMs).
5. Start the heartbeat loop and capability modules, config servers, Serf
   listener.
6. `SIGTERM`/`SIGINT` → graceful stop.

At startup the OS status sync is deliberately *skipped* when the bootc module
runs: the booted digest alone says nothing about greenboot's verdict, so the
poller reports the real phase instead of a premature `healthy`.

## Modules

### Heartbeat (always)

Every node — regardless of capabilities — sends a heartbeat
(`magnetar.node.heartbeat`) every `HEARTBEAT_INTERVAL` (default 30 s). It is
the liveness signal for Magnetar's `online`/`stale`/`offline` view and the
input to its stale-record GC, and it is how a node *without* the bootc module
learns that the control plane lost its record: on a `found=false` reply star
restarts once and re-registers with the same id. The heartbeat carries no
state, so it can never race capability/OS-status reports.

### bootc (`internal/bootc`, capability `os_update`)

- **Desired state (pull).** Every `OS_POLL_INTERVAL` (default 30 s) the
  poller asks Magnetar for the desired image (`magnetar.os.desired.get`).
  The control plane's answer wins; no answer → keep converging on the local
  copy. The NATS push is only the fast path → offline nodes converge when
  they return.
- **Switch flow.** `bootc switch <ref>@<digest>` (verify first when cosign
  material is present), record the attempt `applied` **before** rebooting,
  reboot, wait for greenboot's health verdict, then report
  `switching` → `healthy` or `rolled_back`.
- **Park rule.** A digest greenboot rejected is parked (`rolled_back`) and
  never retried automatically; a new declaration (or pulled change) lifts it.
- **Drift.** Out-of-band OS changes are detected on the next tick and
  reconciled back to the desired image.
- **Short refs.** A ref without a registry host (`os/edge-base:v4.1`) is
  completed with `IMAGE_REGISTRY` from the image's node defaults; a ref that
  names a host is used as is.
- **Handler.** NATS subject `<node-id>.os_update` (fast path, from gravity)
  accepts an image update. It also still accepts the older `rollback`
  command (clears the intent, runs `bootc rollback`); `cockpit image
  rollback` no longer sends it: it declares the node's previous deployment as
  the desired image, so the node rolls back through the normal update path
  and stays managed.
- **State files** (mode 0600): `/var/lib/c12s/os_desired.json`,
  `/var/lib/c12s/os_attempt.json`.
- **Reports** to Magnetar via `magnetar.node.update`:
  `{healthy, switching, degraded, rolled_back, unmanaged}` plus the booted
  image and bootc's rollback deployment (image + digest). `unmanaged`: no
  desired image is declared; the node reports what it booted and nothing
  reconciles it.

### VM reconciler (`internal/vm`, capability `run_vms`)

- Polls `magnetar.vms.desired.get` every `VM_POLL_INTERVAL`; diffs against
  the libvirt domain list.
- Missing VM: download the qcow2 into `VM_CACHE_DIR` (sha256-verified,
  atomic rename of temp download). A disk given by artifact name
  (`edge-base-v4.0.qcow2`) is fetched from `DISK_REGISTRY_URL`; a full
  `http(s)` URL is used as is. Then generate a NoCloud seed (`cloud-init`
  user-data + network-config, deterministic MAC/IP) and `virt-install
  --import` (UEFI — bootc-image-builder images are UEFI-only; plain CPU
  model, no nested virtualization).
- **VM identity.** A VM's node id is UUIDv5(host node id, VM name), known
  before the VM exists. The seed writes it to the VM's
  `/var/lib/c12s/nodeid` together with `NODE_LABELS` (`vm-host=<host id>`,
  `vm-name=<name>`), so a re-created VM keeps its id, org and OS intent.
- Crashed VM: `virsh start`. No longer desired: `virsh destroy` + `undefine`.
- Reports observed state with `magnetar.vms.status.set`.
- VMs boot into their own Star, enroll as first-class nodes, and get their
  own bootc module.
- **State:** `$VM_CACHE_DIR/<digest>/disk.qcow2` (base images, shared),
  `$VM_STATE_DIR/<name>/` (per-VM overlay + seed).

### Configuration chain

- `ConfigAsyncServer`: receives Kuiper standalone/group configs, stores them,
  and gossips when the delivery strategy is `gossip`.
- `AppConfigAsyncServer`: Meridian app-config listener (currently logs; no
  apply path).
- Local gRPC `StarConfig` (`pkg/api/proto/star.proto`, reflection enabled):
  `GetStandaloneConfig`, `GetConfigGroup`.

## API surface

NATS subjects star uses (request/reply unless noted):

| Subject | Direction | Purpose |
|---|---|---|
| `magnetar.registration` | star → Magnetar | enroll a node (re-asserts the stored id) |
| `magnetar.node.update` | star → Magnetar | capabilities + OS status sync |
| `magnetar.node.heartbeat` | star → Magnetar | liveness (all nodes); `found=false` ⇒ re-register |
| `magnetar.os.desired.get` | star → Magnetar | pull desired OS image |
| `magnetar.vms.desired.get` | host → Magnetar | pull desired VM set |
| `magnetar.vms.status.set` | host → Magnetar | report VM status |
| `<node-id>.os_update` | gravity → star (push) | fast-path OS update / rollback |
| `<node-id>.join` | CP → star | Serf cluster join (`address|clusterId`) |
| `<node-id>.configs`, `<node-id>.app_config` | Kuiper/Meridian → star | config delivery |

gRPC: `StarConfig` served on `STAR_ADDRESS` (e.g. `:11000`).

## Configuration

Read in `internal/configs/config.go`. On bootc nodes `star.service` loads two
files: `/usr/lib/c12s/node.env` (defaults shipped in the OS image, so every
update delivers them: control-plane addresses, agent settings) and then
`/var/lib/c12s/node.env` (per-node values from the cloud-init seed; a key set
there wins).

| Variable | Default | Purpose |
|---|---|---|
| `NATS_ADDRESS` | — (required) | NATS server (host:port) |
| `STAR_ADDRESS` | — | gRPC listen address (e.g. `:11000`) |
| `NODE_ID_DIR_PATH` | `/var/lib/c12s` | node id directory |
| `NODE_ID_FILE_NAME` | `nodeid` | node id file |
| `BIND_ADDRESS` / `BIND_PORT` | — / `7946` | Serf bind |
| `REGISTRATION_REQ_TIMEOUT_MILLISECONDS` | `5000` | per registration attempt |
| `MAX_REGISTRATION_RETRIES` | `5` | registration attempts |
| `HEARTBEAT_INTERVAL` | `30s` | liveness heartbeat period |
| `OS_POLL_INTERVAL` | `30s` | bootc poller tick |
| `IMAGE_REGISTRY` | — | registry host:port that completes short image refs (e.g. `registry.example:5001`) |
| `DISK_REGISTRY_URL` | — | disk registry base URL; VM disks given by name are fetched from it (e.g. `http://registry.example:8081`) |
| `NODE_LABELS` | — | `key=value,…` labels sent at registration (set by the VM seed) |
| `VM_CACHE_DIR` | `/var/lib/star/qcow2` | qcow2 cache |
| `VM_STATE_DIR` | `/var/lib/star/vms` | per-VM disks/seed |
| `VM_POLL_INTERVAL` | `30s` | reconciler tick |
| `VM_IP_BASE` | `192.168.122` | VM network base |
| `VM_IP_OFFSET` | `100` | first static VM address |
| `VM_NETWORK` | `default` | libvirt network name |
| `VM_OS_VARIANT` | `fedora-unknown` | `virt-install --os-variant` |

## Build and run

```bash
go build ./...
go test ./...        # unit tests (bootc, vm, magnetar API clients)
C12S_NATS_TESTS=1 go test ./internal/services/   # live-NATS tests (need a
                     # running control plane; they register a test node)
go run ./cmd         # needs NATS_ADDRESS and a reachable Magnetar
```

The live-NATS tests create a node with the fixed id
`11111111-1111-4111-8111-111111111111`; remove it afterwards with
`cockpit delete node --node-id 11111111-1111-4111-8111-111111111111`.

On bootc nodes the binary is baked into the OS image
(`tools/bootc/Containerfile.bootc`) and started by `star.service` with both
`EnvironmentFile`s above. `starometry.service` runs next to it and serves the
node's metrics. For local development, dockerized
"simulated" nodes are started by `tools/start.sh` (`NODES_NUM`).
