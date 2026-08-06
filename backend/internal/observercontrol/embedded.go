package observercontrol

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	observerDataDirEnv           = "OBSERVER_CONTROL_DATA_DIR"
	observerExportTokenSHA256Env = "OBSERVER_EXPORT_TOKEN_SHA256"
)

var (
	//go:embed assets/release-manifest.json
	embeddedReleaseManifest []byte

	//go:embed assets/fobrain-net-observer-linux-amd64
	embeddedReleaseArtifact []byte

	//go:embed assets/observer-release.ed25519.pub
	embeddedReleasePublicKey string

	//go:embed assets/observer-agent-token.sha256
	embeddedAgentTokenSHA256 string
)

// NewEmbedded initializes the observer control endpoints from release material
// compiled into the sub2api binary. Only the public verification key and token
// digest are embedded; the release private key and raw agent token stay offline.
func NewEmbedded() (*Server, error) {
	var manifest ReleaseManifest
	if err := json.Unmarshal(embeddedReleaseManifest, &manifest); err != nil {
		return nil, fmt.Errorf("decode embedded observer release manifest: %w", err)
	}
	publicKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(embeddedReleasePublicKey))
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("decode embedded observer release public key")
	}
	return New(Config{
		DataDir:           embeddedDataDir(),
		AgentTokenSHA256:  strings.TrimSpace(embeddedAgentTokenSHA256),
		ExportTokenSHA256: strings.TrimSpace(os.Getenv(observerExportTokenSHA256Env)),
		ReleaseManifest:   manifest,
		ReleaseArtifact:   embeddedReleaseArtifact,
		ReleasePublicKey:  ed25519.PublicKey(publicKey),
	})
}

func embeddedDataDir() string {
	if dataDir := strings.TrimSpace(os.Getenv(observerDataDirEnv)); dataDir != "" {
		return dataDir
	}
	if dataDir := strings.TrimSpace(os.Getenv("DATA_DIR")); dataDir != "" {
		return filepath.Join(dataDir, "observer-control")
	}
	if info, err := os.Stat("/app/data"); err == nil && info.IsDir() {
		return "/app/data/observer-control"
	}
	return "observer-control"
}
