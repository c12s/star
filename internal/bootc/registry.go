package bootc

import (
	"fmt"
	"strings"
)

// ResolveImageRef completes a short ref ("os/edge-base:v4.1") with the
// node's registry (IMAGE_REGISTRY, shipped in the image's node.env). The
// control plane stores refs without a registry, so moving the registry is an
// image rollout and sites can pull the same digest from their own registry.
// A ref that already names a host is an explicit override and is kept.
// Without a registry the ref is refused rather than resolved to docker.io.
func ResolveImageRef(ref, registry string) (string, error) {
	if hasRegistryHost(ref) {
		return ref, nil
	}
	if registry == "" {
		return "", fmt.Errorf("image ref %q names no registry and IMAGE_REGISTRY is not set", ref)
	}
	return strings.TrimRight(registry, "/") + "/" + ref, nil
}

// hasRegistryHost decides it the way container tools do: a first path
// component with a dot, a port, or "localhost" is a registry host.
func hasRegistryHost(ref string) bool {
	host, _, ok := strings.Cut(ref, "/")
	if !ok {
		return false
	}
	return strings.ContainsAny(host, ".:") || host == "localhost"
}
