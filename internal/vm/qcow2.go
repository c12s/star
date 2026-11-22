package vm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Qcow2Cache downloads bootc qcow2 artifacts from Singularity and caches them
// locally, keyed by their sha256 digest (phase-next.md §2.3 Q3). The digest is
// authoritative: virt-install only ever sees a file whose bytes hash to it.
//
// The cache keeps every version (no eviction); images are ~1.3 GB, so old
// versions have to be removed from the cache directory by hand.
type Qcow2Cache struct {
	cacheDir string
	client   *http.Client
}

func NewQcow2Cache(cacheDir string) *Qcow2Cache {
	return &Qcow2Cache{
		cacheDir: cacheDir,
		// large downloads: no overall timeout (the body streams for minutes),
		// but a server that stops answering must not block the reconciler
		client: &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       60 * time.Second,
		}},
	}
}

// Ensure returns the local path of the artifact behind url, downloading it on
// first use. When digest is empty the sha256 sidecar (<url>/sha256, provided
// by the Singularity file server) is used to pin the download.
func (c *Qcow2Cache) Ensure(url, digest string) (string, error) {
	if url == "" {
		return "", fmt.Errorf("qcow2 url is empty")
	}
	var err error
	if digest == "" {
		digest, err = c.fetchChecksum(url)
		if err != nil {
			return "", err
		}
	}
	if err := validateDigest(digest); err != nil {
		return "", err
	}

	path := c.cachedPath(digest)
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		log.Printf("[vm] qcow2 %s already cached at %s", digest, path)
		return path, nil
	}
	return c.download(url, digest, path)
}

func (c *Qcow2Cache) cachedPath(digest string) string {
	return filepath.Join(c.cacheDir, strings.TrimPrefix(digest, "sha256:"), "disk.qcow2")
}

func (c *Qcow2Cache) fetchChecksum(url string) (string, error) {
	resp, err := c.client.Get(strings.TrimRight(url, "/") + "/sha256")
	if err != nil {
		return "", fmt.Errorf("fetch qcow2 checksum: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch qcow2 checksum %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", fmt.Errorf("read qcow2 checksum: %w", err)
	}
	return strings.TrimSpace(string(body)), nil
}

func (c *Qcow2Cache) download(url, digest, path string) (string, error) {
	start := time.Now()
	log.Printf("[vm] downloading qcow2 %s from %s", digest, url)

	resp, err := c.client.Get(url)
	if err != nil {
		return "", fmt.Errorf("download qcow2: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download qcow2 %s: HTTP %d", url, resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create qcow2 cache dir: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("create qcow2 temp file: %w", err)
	}

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(f, hash), resp.Body)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("write qcow2: %w", err)
	}

	actual := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if actual != digest {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("qcow2 digest mismatch: want %s, got %s", digest, actual)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("commit qcow2 to cache: %w", err)
	}

	log.Printf("[vm] cached qcow2 %s (%d bytes, %s)", digest, written, time.Since(start).Round(time.Second))
	return path, nil
}

func validateDigest(digest string) error {
	const prefix = "sha256:"
	if !strings.HasPrefix(digest, prefix) || len(digest) != len(prefix)+64 {
		return fmt.Errorf("malformed qcow2 digest %q (want sha256:<64 hex>)", digest)
	}
	if _, err := hex.DecodeString(digest[len(prefix):]); err != nil {
		return fmt.Errorf("malformed qcow2 digest %q (not valid hex)", digest)
	}
	return nil
}
