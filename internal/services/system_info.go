package services

import (
	"errors"
	"math"
	"os"

	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/host"
	"github.com/shirou/gopsutil/mem"
)

func cpuCores() (float64, error) {
	cpuCores, err := cpu.Counts(true)
	return float64(cpuCores), err
}

func coreMhz(coreId string) (float64, error) {
	core, err := findCoreById(coreId)
	if err != nil {
		return 0, err
	}
	return core.Mhz, nil
}

func coreVendorId(coreId string) (string, error) {
	core, err := findCoreById(coreId)
	if err != nil {
		return "", err
	}
	return core.VendorID, nil
}

func coreModel(coreId string) (string, error) {
	core, err := findCoreById(coreId)
	if err != nil {
		return "", err
	}
	return core.ModelName, nil
}

func coreCacheKB(coreId string) (float64, error) {
	core, err := findCoreById(coreId)
	if err != nil {
		return 0, err
	}
	return float64(core.CacheSize / 1000), nil
}

func findCoreById(coreId string) (cpu.InfoStat, error) {
	cores, err := cpu.Info()
	if err != nil {
		return cpu.InfoStat{}, err
	}
	for _, core := range cores {
		if core.CoreID == coreId {
			return core, nil
		}
	}
	return cpu.InfoStat{}, errors.New("core not found")
}

func fsType() (string, error) {
	diskInfo, err := disk.Usage("/")
	if err != nil {
		return "", err
	} else if diskInfo.Fstype == "" {
		return "", errors.New("fs-type undefined")
	}
	return diskInfo.Fstype, nil
}

// dataDisk is the filesystem disk labels describe. On a bootc node "/" is the
// read-only composefs image mount (reported as ~0 bytes); node data and VM
// disks live on the writable disk behind /var. Plain hosts fall back to "/".
func dataDisk() string {
	if _, err := os.Stat("/var"); err == nil {
		return "/var"
	}
	return "/"
}

func diskTotalGB() (float64, error) {
	diskInfo, err := disk.Usage(dataDisk())
	if err != nil {
		return 0, err
	}
	return bytesToGB(diskInfo.Total), nil
}

func diskFreeGB() (float64, error) {
	diskInfo, err := disk.Usage(dataDisk())
	if err != nil {
		return 0, err
	}
	return bytesToGB(diskInfo.Free), nil
}

// bytesToGB converts to decimal GB, rounded to two places (the label shows
// two). Dividing before converting to float used to drop the fraction.
func bytesToGB(b uint64) float64 {
	return math.Round(float64(b)/1e7) / 100
}

func kernelArch() (string, error) {
	return host.KernelArch()
}

func kernelVersion() (string, error) {
	return host.KernelVersion()
}

func platform() (string, error) {
	platform, _, _, err := host.PlatformInformation()
	return platform, err
}

func platformFamily() (string, error) {
	_, platformFamily, _, err := host.PlatformInformation()
	return platformFamily, err
}

func platformVersion() (string, error) {
	_, _, platformVersion, err := host.PlatformInformation()
	return platformVersion, err
}

func memoryTotalGB() (float64, error) {
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		return 0, err
	}
	return bytesToGB(memInfo.Total), nil
}
