package configs

import (
	"log"
	"os"
	"strconv"
	"time"
)

type Config struct {
	natsAddress                        string
	registrationReqTimeoutMilliseconds int64
	maxRegistrationRetries             int8
	nodeIdDirPath                      string
	nodeIdFileName                     string
	grpcServerAddress                  string
	serfBindAddress                    string
	serfBindPort                       int
	osPollInterval                     time.Duration
	heartbeatInterval                  time.Duration
	vmCacheDir                         string
	vmStateDir                         string
	vmPollInterval                     time.Duration
	vmIpBase                           string
	vmIpOffset                         int
	vmNetwork                          string
	vmOsVariant                        string
	diskRegistryUrl                    string
	imageRegistry                      string
}

// DiskRegistryUrl is the artifact server bare VM disk names are fetched from.
func (c *Config) DiskRegistryUrl() string {
	return c.diskRegistryUrl
}

// ImageRegistry completes short OS image refs (host[:port], no scheme).
func (c *Config) ImageRegistry() string {
	return c.imageRegistry
}

func (c *Config) NatsAddress() string {
	return c.natsAddress
}

func (c *Config) RegistrationReqTimeoutMilliseconds() int64 {
	return c.registrationReqTimeoutMilliseconds
}

func (c *Config) MaxRegistrationRetries() int8 {
	return c.maxRegistrationRetries
}

func (c *Config) NodeIdDirPath() string {
	return c.nodeIdDirPath
}

func (c *Config) NodeIdFileName() string {
	return c.nodeIdFileName
}

func (c *Config) GrpcServerAddress() string {
	return c.grpcServerAddress
}

func (c *Config) SerfBindAddress() string {
	return c.serfBindAddress
}

func (c *Config) SerfBindPort() int {
	return c.serfBindPort
}

func (c *Config) OsPollInterval() time.Duration {
	return c.osPollInterval
}

func (c *Config) HeartbeatInterval() time.Duration {
	return c.heartbeatInterval
}

func (c *Config) VmCacheDir() string {
	return c.vmCacheDir
}

func (c *Config) VmStateDir() string {
	return c.vmStateDir
}

func (c *Config) VmPollInterval() time.Duration {
	return c.vmPollInterval
}

func (c *Config) VmIpBase() string {
	return c.vmIpBase
}

func (c *Config) VmIpOffset() int {
	return c.vmIpOffset
}

func (c *Config) VmNetwork() string {
	return c.vmNetwork
}

func (c *Config) VmOsVariant() string {
	return c.vmOsVariant
}

const (
	defaultNodeIdDirPath = "/var/lib/c12s"
	defaultNodeIdFile    = "nodeid"
	defaultVmCacheDir    = "/var/lib/star/qcow2"
	defaultVmStateDir    = "/var/lib/star/vms"
	defaultVmIpBase      = "192.168.122"
	defaultVmIpOffset    = 100
	defaultVmNetwork     = "default"
	defaultVmOsVariant   = "fedora-unknown"
	// Every node heartbeats so Magnetar can distinguish live records from
	// stale ones; 30s keeps it cheap and well inside the stale threshold.
	defaultHeartbeatInterval = 30 * time.Second
)

func NewFromEnv() (*Config, error) {
	registrationReqTimeoutMilliseconds, err := strconv.Atoi(os.Getenv("REGISTRATION_REQ_TIMEOUT_MILLISECONDS"))
	if err != nil {
		log.Println(err)
		registrationReqTimeoutMilliseconds = 5000
	}
	maxRegistrationRetries, err := strconv.Atoi(os.Getenv("MAX_REGISTRATION_RETRIES"))
	if err != nil {
		log.Println(err)
		maxRegistrationRetries = 5
	}
	serfBindPort, err := strconv.Atoi(os.Getenv("BIND_PORT"))
	if err != nil {
		log.Println(err)
		serfBindPort = 7946
	}
	nodeIdDirPath := os.Getenv("NODE_ID_DIR_PATH")
	if nodeIdDirPath == "" {
		nodeIdDirPath = defaultNodeIdDirPath
	}
	nodeIdFileName := os.Getenv("NODE_ID_FILE_NAME")
	if nodeIdFileName == "" {
		nodeIdFileName = defaultNodeIdFile
	}
	osPollInterval, err := time.ParseDuration(os.Getenv("OS_POLL_INTERVAL"))
	if err != nil {
		log.Println(err)
		osPollInterval = 30 * time.Second
	}
	heartbeatInterval, err := time.ParseDuration(os.Getenv("HEARTBEAT_INTERVAL"))
	if err != nil || heartbeatInterval <= 0 {
		heartbeatInterval = defaultHeartbeatInterval
	}
	vmCacheDir := os.Getenv("VM_CACHE_DIR")
	if vmCacheDir == "" {
		vmCacheDir = defaultVmCacheDir
	}
	vmStateDir := os.Getenv("VM_STATE_DIR")
	if vmStateDir == "" {
		vmStateDir = defaultVmStateDir
	}
	vmPollInterval, err := time.ParseDuration(os.Getenv("VM_POLL_INTERVAL"))
	if err != nil {
		vmPollInterval = 30 * time.Second
	}
	vmIpBase := os.Getenv("VM_IP_BASE")
	if vmIpBase == "" {
		vmIpBase = defaultVmIpBase
	}
	vmIpOffset, err := strconv.Atoi(os.Getenv("VM_IP_OFFSET"))
	if err != nil {
		vmIpOffset = defaultVmIpOffset
	}
	vmNetwork := os.Getenv("VM_NETWORK")
	if vmNetwork == "" {
		vmNetwork = defaultVmNetwork
	}
	vmOsVariant := os.Getenv("VM_OS_VARIANT")
	if vmOsVariant == "" {
		vmOsVariant = defaultVmOsVariant
	}
	return &Config{
		natsAddress:                        os.Getenv("NATS_ADDRESS"),
		registrationReqTimeoutMilliseconds: int64(registrationReqTimeoutMilliseconds),
		maxRegistrationRetries:             int8(maxRegistrationRetries),
		nodeIdDirPath:                      nodeIdDirPath,
		nodeIdFileName:                     nodeIdFileName,
		grpcServerAddress:                  os.Getenv("STAR_ADDRESS"),
		serfBindAddress:                    os.Getenv("BIND_ADDRESS"),
		serfBindPort:                       serfBindPort,
		osPollInterval:                     osPollInterval,
		heartbeatInterval:                  heartbeatInterval,
		vmCacheDir:                         vmCacheDir,
		vmStateDir:                         vmStateDir,
		vmPollInterval:                     vmPollInterval,
		vmIpBase:                           vmIpBase,
		vmIpOffset:                         vmIpOffset,
		vmNetwork:                          vmNetwork,
		vmOsVariant:                        vmOsVariant,
		diskRegistryUrl:                    os.Getenv("DISK_REGISTRY_URL"),
		imageRegistry:                      os.Getenv("IMAGE_REGISTRY"),
	}, nil
}
