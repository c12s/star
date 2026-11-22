package bootc

import (
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

// Verification happens before every switch:
//
//   - digest: the desired state is digest-pinned and handed to bootc as
//     ref@sha256:... — bootc itself refuses to fetch anything whose manifest
//     digest differs, so integrity is enforced by construction. We still
//     validate the digest form here to catch malformed commands early.
//
//   - signature: when cosign and a public key are present on the node, the
//     image must verify against the key before we stage it. Missing tooling
//     or key degrades verification to digest-only (logged once per run).

const (
	cosignKeyEnv = "OS_COSIGN_KEY"
	cosignKeyDef = "/var/lib/c12s/keys/cosign.pub"
)

var verifySkipLogged bool

// Verify checks that imageRef@digest is acceptable to stage.
func Verify(imageRef, digest string) error {
	const prefix = "sha256:"
	if !strings.HasPrefix(digest, prefix) || len(digest) != len(prefix)+64 {
		return fmt.Errorf("malformed digest %q (want sha256:<64 hex>)", digest)
	}
	if _, err := hex.DecodeString(digest[len(prefix):]); err != nil {
		return fmt.Errorf("malformed digest %q (not valid hex)", digest)
	}
	return verifySignature(pinnedRef(imageRef, digest))
}

// pinnedRef turns "host:port/repo:tag" + digest into "host:port/repo@digest",
// so the signature checked is the one of the exact image that gets staged —
// a tag can be repointed between verification and switch.
func pinnedRef(imageRef, digest string) string {
	repo := imageRef
	if at := strings.Index(repo, "@"); at >= 0 {
		repo = repo[:at]
	}
	if colon := strings.LastIndex(repo, ":"); colon > strings.LastIndex(repo, "/") {
		repo = repo[:colon]
	}
	return repo + "@" + digest
}

func verifySignature(ref string) error {
	cosign, err := exec.LookPath("cosign")
	if err != nil {
		return skipVerification("cosign binary not installed")
	}
	keyPath := os.Getenv(cosignKeyEnv)
	if keyPath == "" {
		keyPath = cosignKeyDef
	}
	if _, err := os.Stat(keyPath); err != nil {
		return skipVerification("cosign key " + keyPath + " not present")
	}

	out, err := exec.Command(cosign, "verify",
		"--key", keyPath,
		"--allow-insecure-registry",
		ref,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("signature verification failed for %s: %s", ref, string(out))
	}
	return nil
}

func skipVerification(reason string) error {
	if !verifySkipLogged {
		verifySkipLogged = true
		log.Printf("os verify: signature check skipped (%s)\n", reason)
	}
	return nil
}
