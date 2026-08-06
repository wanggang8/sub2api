package observercontrol

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testAgentToken = "observer-test-token"

func TestHeartbeatAuthenticatesAndPersists(t *testing.T) {
	server, dataDir := newTestServer(t, 0)
	recorder := performRequest(t, server.Handler(), http.MethodPost, "/api/v1/observer/agents/heartbeat", "", nil, []byte(`{}`))
	require.Equal(t, http.StatusUnauthorized, recorder.Code)

	metadata := validTestAgentMetadata()
	body, err := json.Marshal(metadata)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/observer/agents/heartbeat", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testAgentToken)
	request.Header.Set(ResolvedClientIPHeader, "203.0.113.18")
	request.RemoteAddr = "10.0.0.2:4321"
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), metadata.InstallationID)
	require.Contains(t, recorder.Body.String(), "203.0.113.18")

	path := filepath.Join(dataDir, "agents", metadata.InstallationID+".json")
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	stored, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(stored), `"observed_egress_ip": "203.0.113.18"`)
	require.Contains(t, string(stored), `"transport_peer_hint": "10.0.0.2"`)
}

func TestReleaseManifestAndArtifact(t *testing.T) {
	server, _ := newTestServer(t, 0)

	recorder := performRequest(t, server.Handler(), http.MethodGet, "/api/v1/observer/releases/latest?goos=linux&goarch=amd64", testAgentToken, nil, nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	var manifest ReleaseManifest
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &manifest))
	require.Equal(t, "0.3.0", manifest.Version)

	recorder = performRequest(t, server.Handler(), http.MethodGet, "/api/v1/observer/releases/latest?goos=darwin&goarch=arm64", testAgentToken, nil, nil)
	require.Equal(t, http.StatusNotFound, recorder.Code)

	recorder = performRequest(t, server.Handler(), http.MethodGet, "/api/v1/observer/releases/artifact?version=0.3.0&goos=linux&goarch=amd64", testAgentToken, nil, nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "test observer artifact", recorder.Body.String())
	require.Equal(t, "application/octet-stream", recorder.Header().Get("Content-Type"))

	recorder = performRequest(t, server.Handler(), http.MethodGet, "/api/v1/observer/releases/artifact?version=0.2.0&goos=linux&goarch=amd64", testAgentToken, nil, nil)
	require.Equal(t, http.StatusNotFound, recorder.Code)

	recorder = performRequest(t, server.Handler(), http.MethodGet, "/api/v1/observer/releases/artifact?goos=linux&goarch=amd64", testAgentToken, nil, nil)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestUploadValidatesAndIsIdempotent(t *testing.T) {
	server, dataDir := newTestServer(t, 0)
	archive := validTestArchive(t)
	header := http.Header{"Content-Type": []string{"application/gzip"}}

	first := performRequest(t, server.Handler(), http.MethodPost, "/api/v1/observer/observations", testAgentToken, header, archive)
	require.Equal(t, http.StatusCreated, first.Code)
	var firstResponse map[string]any
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstResponse))
	uploadID, ok := firstResponse["upload_id"].(string)
	require.True(t, ok)
	require.NotEmpty(t, uploadID)

	second := performRequest(t, server.Handler(), http.MethodPost, "/api/v1/observer/observations", testAgentToken, header, archive)
	require.Equal(t, http.StatusOK, second.Code)
	require.Contains(t, second.Body.String(), uploadID)

	path := filepath.Join(dataDir, "observations", uploadID+".tar.gz")
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	stored, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, archive, stored)

	wrongType := performRequest(t, server.Handler(), http.MethodPost, "/api/v1/observer/observations", testAgentToken, nil, archive)
	require.Equal(t, http.StatusUnsupportedMediaType, wrongType.Code)
}

func TestUploadRejectsInvalidAndOversizedArchives(t *testing.T) {
	server, _ := newTestServer(t, 64)
	header := http.Header{"Content-Type": []string{"application/gzip"}}

	oversized := performRequest(t, server.Handler(), http.MethodPost, "/api/v1/observer/observations", testAgentToken, header, bytes.Repeat([]byte("x"), 65))
	require.Equal(t, http.StatusRequestEntityTooLarge, oversized.Code)

	invalid := performRequest(t, server.Handler(), http.MethodPost, "/api/v1/observer/observations", testAgentToken, header, []byte("not gzip"))
	require.Equal(t, http.StatusUnprocessableEntity, invalid.Code)
}

func TestUploadRejectsChecksumMismatch(t *testing.T) {
	server, _ := newTestServer(t, 0)
	observationData := []byte(`{"schema_version":"fobrain.network-observation/v1","collector":{"name":"fobrain-net-observer"},"sensor":{"sensor_id":"sensor-test"}}`)
	manifest := uploadManifest{
		APIVersion: APIVersion, AgentVersion: "0.3.0", GOOS: "linux", GOARCH: "amd64",
		CreatedAt: time.Now().UTC(), Agent: pointerTo(validTestAgentMetadata()),
		Files: []archiveFile{{Name: "observation.json", Size: int64(len(observationData)), SHA256: "sha256:" + strings.Repeat("0", 64)}},
	}
	manifestData, err := json.Marshal(manifest)
	require.NoError(t, err)
	archive := makeTestArchive(t, map[string][]byte{"manifest.json": manifestData, "observation.json": observationData})
	header := http.Header{"Content-Type": []string{"application/gzip"}}

	recorder := performRequest(t, server.Handler(), http.MethodPost, "/api/v1/observer/observations", testAgentToken, header, archive)
	require.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
}

func TestNewRejectsTamperedRelease(t *testing.T) {
	artifact, manifest, publicKey := signedTestRelease(t)
	manifest.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	tokenHash := sha256.Sum256([]byte(testAgentToken))
	_, err := New(Config{
		DataDir:          t.TempDir(),
		AgentTokenSHA256: hex.EncodeToString(tokenHash[:]),
		ReleaseManifest:  manifest,
		ReleaseArtifact:  artifact,
		ReleasePublicKey: publicKey,
	})
	require.ErrorContains(t, err, "signature is invalid")
}

func TestEmbeddedReleaseIsValid(t *testing.T) {
	t.Setenv(observerDataDirEnv, t.TempDir())
	server, err := NewEmbedded()
	require.NoError(t, err)
	require.Equal(t, "0.3.1", server.release.Version)
	require.Equal(t, "linux", server.release.GOOS)
	require.Equal(t, "amd64", server.release.GOARCH)
	require.NotEmpty(t, server.artifact)
}

func newTestServer(t *testing.T, maxArchiveBytes int64) (*Server, string) {
	t.Helper()
	artifact, manifest, publicKey := signedTestRelease(t)
	tokenHash := sha256.Sum256([]byte(testAgentToken))
	dataDir := filepath.Join(t.TempDir(), "observer-control")
	server, err := New(Config{
		DataDir:          dataDir,
		AgentTokenSHA256: hex.EncodeToString(tokenHash[:]),
		ReleaseManifest:  manifest,
		ReleaseArtifact:  artifact,
		ReleasePublicKey: publicKey,
		MaxArchiveBytes:  maxArchiveBytes,
		Now: func() time.Time {
			return time.Date(2026, time.August, 6, 1, 2, 3, 0, time.UTC)
		},
	})
	require.NoError(t, err)
	return server, dataDir
}

func signedTestRelease(t *testing.T) ([]byte, ReleaseManifest, ed25519.PublicKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	artifact := []byte("test observer artifact")
	digest := sha256.Sum256(artifact)
	manifest := ReleaseManifest{
		APIVersion: APIVersion, Version: "0.3.0", GOOS: "linux", GOARCH: "amd64",
		Size: int64(len(artifact)), SHA256: "sha256:" + hex.EncodeToString(digest[:]),
		ContentType: "application/octet-stream", DownloadPath: "/api/v1/observer/releases/artifact",
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(hex.EncodeToString(digest[:])))),
	}
	return artifact, manifest, publicKey
}

func validTestAgentMetadata() agentMetadata {
	binaryHash := sha256.Sum256([]byte("agent binary"))
	return agentMetadata{
		InstallationID: "foai_" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32)),
		SensorID:       "sensor-test", AgentVersion: "0.3.0", BinarySHA256: "sha256:" + hex.EncodeToString(binaryHash[:]),
		BinarySize: 1234, GOOS: "linux", GOARCH: "amd64", ReportedAt: time.Now().UTC(),
	}
}

func validTestArchive(t *testing.T) []byte {
	t.Helper()
	observationData := []byte(`{"schema_version":"fobrain.network-observation/v1","collector":{"name":"fobrain-net-observer"},"sensor":{"sensor_id":"sensor-test"}}`)
	observationHash := sha256.Sum256(observationData)
	manifest := uploadManifest{
		APIVersion: APIVersion, AgentVersion: "0.3.0", GOOS: "linux", GOARCH: "amd64",
		CreatedAt: time.Now().UTC(), Agent: pointerTo(validTestAgentMetadata()),
		Files: []archiveFile{{Name: "observation.json", Size: int64(len(observationData)), SHA256: "sha256:" + hex.EncodeToString(observationHash[:])}},
	}
	manifestData, err := json.Marshal(manifest)
	require.NoError(t, err)
	return makeTestArchive(t, map[string][]byte{"manifest.json": manifestData, "observation.json": observationData})
}

func makeTestArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range []string{"manifest.json", "observation.json", "collect.log"} {
		body, exists := files[name]
		if !exists {
			continue
		}
		require.NoError(t, tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body))}))
		_, err := tarWriter.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzipWriter.Close())
	return buffer.Bytes()
}

func performRequest(t *testing.T, handler http.Handler, method, target, token string, headers http.Header, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for name, values := range headers {
		request.Header[name] = append([]string(nil), values...)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func pointerTo[T any](value T) *T { return &value }
