package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	magnetarapi "github.com/c12s/magnetar/pkg/api"
	"github.com/c12s/star/internal/domain"
)

type RegistrationService struct {
	client     *magnetarapi.RegistrationAsyncClient
	nodeIdRepo domain.NodeIdStore
}

func NewRegistrationService(client *magnetarapi.RegistrationAsyncClient, nodeIdRepo domain.NodeIdStore) *RegistrationService {
	return &RegistrationService{
		client:     client,
		nodeIdRepo: nodeIdRepo,
	}
}

func (rs *RegistrationService) Register(maxRetries int8, timeout time.Duration, bindAddress string, capabilities []string, osStatus *magnetarapi.OsStatus) error {
	err := rs.nodeIdRepo.PutClusterId("")
	if err != nil {
		log.Fatal(err)
	}
	req := rs.buildReq(bindAddress, capabilities, osStatus)
	// Re-assert the id we already have (re-registration after a control-plane
	// reset, or after the control plane lost our record): Magnetar then
	// updates that record instead of minting a duplicate UUID.
	if id, err := rs.nodeIdRepo.Get(); err == nil && id.Value != "" {
		req.NodeId = id.Value
	}
	for attemptsLeft := maxRetries; attemptsLeft > 0; attemptsLeft-- {
		err := rs.tryRegister(req, timeout)
		if err == nil {
			return nil
		}
		log.Println(err)
	}
	return errors.New("max registration attempts exceeded")
}

func (rs *RegistrationService) tryRegister(req *magnetarapi.RegistrationReq, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	resp, err := rs.client.Register(ctx, req)
	if err != nil {
		return err
	}
	return rs.nodeIdRepo.Put(domain.NodeId{Value: resp.NodeId})
}

func (rs *RegistrationService) buildReq(bindAddress string, capabilities []string, osStatus *magnetarapi.OsStatus) *magnetarapi.RegistrationReq {
	builder := magnetarapi.NewRegistrationReqBuilder()
	builder = builder.AddCapabilities(capabilities...)
	builder = builder.SetOsStatus(osStatus)
	cpuCores, err := cpuCores()
	if err == nil {
		builder = builder.AddFloat64Label("cpu-cores", cpuCores)
	}
	for coreId := 0; coreId < int(cpuCores); coreId++ {
		mhz, err := coreMhz(strconv.Itoa(coreId))
		if err == nil {
			builder = builder.AddFloat64Label(fmt.Sprintf("core%dmhz", coreId), mhz)
		}
		vendorId, err := coreVendorId(strconv.Itoa(coreId))
		if err == nil {
			builder = builder.AddStringLabel(fmt.Sprintf("core%dvendorId", coreId), vendorId)
		}
		model, err := coreModel(strconv.Itoa(coreId))
		if err == nil {
			builder = builder.AddStringLabel(fmt.Sprintf("core%dmodel", coreId), model)
		}
		cacheKB, err := coreCacheKB(strconv.Itoa(coreId))
		if err == nil {
			builder = builder.AddFloat64Label(fmt.Sprintf("core%dcacheKB", coreId), cacheKB)
		}
	}
	fsType, err := fsType()
	if err == nil {
		builder = builder.AddStringLabel("fs-type", fsType)
	}
	diskTotalGB, err := diskTotalGB()
	if err == nil {
		builder = builder.AddFloat64Label("disk-totalGB", diskTotalGB)
	}
	diskFreeGB, err := diskFreeGB()
	if err == nil {
		builder = builder.AddFloat64Label("disk-freeGB", diskFreeGB)
	}
	kernelArch, err := kernelArch()
	if err == nil {
		builder = builder.AddStringLabel("kernel-arch", kernelArch)
	}
	kernelVersion, err := kernelVersion()
	if err == nil {
		builder = builder.AddStringLabel("kernel-version", kernelVersion)
	}
	platform, err := platform()
	if err == nil {
		builder = builder.AddStringLabel("platform", platform)
	}
	platformFamily, err := platformFamily()
	if err == nil {
		builder = builder.AddStringLabel("platform-family", platformFamily)
	}
	platformVersion, err := platformVersion()
	if err == nil {
		builder = builder.AddStringLabel("platform-version", platformVersion)
	}
	memoryTotalGB, err := memoryTotalGB()
	if err == nil {
		builder = builder.AddFloat64Label("memory-totalGB", memoryTotalGB)
	}
	// NODE_LABELS (from the seed) lets a provisioner tag the node, e.g. a VM
	// with its host, so it can be claimed precisely
	for key, value := range parseNodeLabels(os.Getenv("NODE_LABELS")) {
		builder = builder.AddStringLabel(key, value)
	}
	req := builder.Request()
	req.Resources["mem"] = memoryTotalGB
	req.Resources["cpu"] = cpuCores
	req.Resources["disk"] = diskFreeGB
	req.BindAddress = bindAddress
	return req
}

func (rs *RegistrationService) Registered() bool {
	if _, err := rs.nodeIdRepo.Get(); err != nil {
		return false
	}
	return true
}

// parseNodeLabels reads "k1=v1,k2=v2"; malformed entries are skipped.
func parseNodeLabels(raw string) map[string]string {
	labels := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(pair), "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" || value == "" {
			continue
		}
		labels[key] = value
	}
	return labels
}
