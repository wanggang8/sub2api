package observercontrol

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ResolvedClientIPHeader = "X-Sub2api-Observer-Client-IP"
	defaultMaxArchiveBytes = int64(70 << 20)
	maxJSONBodyBytes       = int64(1 << 20)
)

type Config struct {
	DataDir           string
	AgentTokenSHA256  string
	ExportTokenSHA256 string
	ReleaseManifest   ReleaseManifest
	ReleaseArtifact   []byte
	ReleasePublicKey  ed25519.PublicKey
	MaxArchiveBytes   int64
	Now               func() time.Time
}

type Server struct {
	dataDir          string
	agentTokenHash   [sha256.Size]byte
	exportTokenHash  [sha256.Size]byte
	exportConfigured bool
	release          ReleaseManifest
	artifact         []byte
	maxArchiveBytes  int64
	now              func() time.Time
	dataMu           sync.Mutex
	exportMu         sync.Mutex
}

func New(config Config) (*Server, error) {
	dataDir := filepath.Clean(strings.TrimSpace(config.DataDir))
	if dataDir == "." || dataDir == "" {
		return nil, errors.New("observer control data directory is required")
	}
	tokenHash, err := decodeSHA256(config.AgentTokenSHA256)
	if err != nil {
		return nil, fmt.Errorf("observer agent token hash: %w", err)
	}
	if err := validateEmbeddedRelease(config.ReleaseManifest, config.ReleaseArtifact, config.ReleasePublicKey); err != nil {
		return nil, err
	}
	var exportTokenHash [sha256.Size]byte
	exportConfigured := false
	if raw := strings.TrimSpace(config.ExportTokenSHA256); raw != "" {
		if decoded, decodeErr := decodeSHA256(raw); decodeErr == nil {
			exportTokenHash = decoded
			exportConfigured = true
		} else {
			slog.Warn("observer export is disabled because its token digest is invalid")
		}
	}
	for _, directory := range []string{
		dataDir,
		filepath.Join(dataDir, "agents"),
		filepath.Join(dataDir, "observations"),
		filepath.Join(dataDir, "exports"),
		filepath.Join(dataDir, "export-receipts"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, fmt.Errorf("create observer control data directory: %w", err)
		}
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("observer control data path is not a directory: %s", directory)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return nil, fmt.Errorf("restrict observer control data directory permissions: %w", err)
		}
	}
	maxArchiveBytes := config.MaxArchiveBytes
	if maxArchiveBytes <= 0 {
		maxArchiveBytes = defaultMaxArchiveBytes
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	server := &Server{
		dataDir: dataDir, agentTokenHash: tokenHash, exportTokenHash: exportTokenHash, exportConfigured: exportConfigured,
		release:  config.ReleaseManifest,
		artifact: config.ReleaseArtifact, maxArchiveBytes: maxArchiveBytes, now: now,
	}
	if err := server.recoverExportSnapshots(); err != nil {
		slog.Warn("observer export recovery is incomplete", "error", err)
	}
	return server, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/observer/agents/heartbeat", s.heartbeat)
	mux.HandleFunc("POST /api/v1/observer/observations", s.upload)
	mux.HandleFunc("POST /api/v1/observer/exports", s.createExport)
	mux.HandleFunc("GET /api/v1/observer/exports/{export_id}", s.downloadExport)
	mux.HandleFunc("GET /api/v1/observer/releases/latest", s.latestRelease)
	mux.HandleFunc("GET /api/v1/observer/releases/artifact", s.releaseArtifact)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(writer, request)
	})
}

func (s *Server) heartbeat(writer http.ResponseWriter, request *http.Request) {
	if !s.authenticate(writer, request) {
		return
	}
	var metadata agentMetadata
	if err := decodeJSONLimited(request.Body, maxJSONBodyBytes, &metadata); err != nil {
		s.writeError(writer, http.StatusBadRequest, "invalid_request", "invalid heartbeat JSON")
		return
	}
	if !validAgentMetadata(metadata) {
		s.writeError(writer, http.StatusUnprocessableEntity, "invalid_agent_metadata", "agent metadata is incomplete or invalid")
		return
	}
	now := s.now().UTC()
	egressIP, transportPeer := observedClientIP(request)
	record := heartbeatRecord{Agent: metadata, ReceivedAt: now, ObservedEgressIP: egressIP, TransportPeerHint: transportPeer}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		s.writeInternalError(writer, err)
		return
	}
	data = append(data, '\n')
	path := filepath.Join(s.dataDir, "agents", metadata.InstallationID+".json")
	s.dataMu.Lock()
	err = writeAtomic(path, data, true)
	s.dataMu.Unlock()
	if err != nil {
		s.writeInternalError(writer, err)
		return
	}
	s.writeJSON(writer, http.StatusOK, map[string]any{
		"api_version": APIVersion, "agent_installation_id": metadata.InstallationID,
		"status": "accepted", "received_at": now, "observed_egress_ip": egressIP,
	})
}

func (s *Server) latestRelease(writer http.ResponseWriter, request *http.Request) {
	if !s.authenticate(writer, request) {
		return
	}
	goos := strings.TrimSpace(request.URL.Query().Get("goos"))
	goarch := strings.TrimSpace(request.URL.Query().Get("goarch"))
	if goos == "" || goarch == "" {
		s.writeError(writer, http.StatusBadRequest, "invalid_request", "goos and goarch are required")
		return
	}
	if goos != s.release.GOOS || goarch != s.release.GOARCH {
		s.writeError(writer, http.StatusNotFound, "release_not_found", "release was not found")
		return
	}
	s.writeJSON(writer, http.StatusOK, s.release)
}

func (s *Server) releaseArtifact(writer http.ResponseWriter, request *http.Request) {
	if !s.authenticate(writer, request) {
		return
	}
	query := request.URL.Query()
	if query.Get("version") == "" || query.Get("goos") == "" || query.Get("goarch") == "" {
		s.writeError(writer, http.StatusBadRequest, "invalid_request", "version, goos, and goarch are required")
		return
	}
	if query.Get("version") != s.release.Version || query.Get("goos") != s.release.GOOS || query.Get("goarch") != s.release.GOARCH {
		s.writeError(writer, http.StatusNotFound, "release_not_found", "release was not found")
		return
	}
	writer.Header().Set("Content-Type", s.release.ContentType)
	writer.Header().Set("Content-Length", strconv.FormatInt(s.release.Size, 10))
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(s.artifact)
}

func (s *Server) authenticate(writer http.ResponseWriter, request *http.Request) bool {
	token := bearerToken(request.Header.Get("Authorization"))
	provided := sha256.Sum256([]byte(token))
	if token == "" || subtle.ConstantTimeCompare(provided[:], s.agentTokenHash[:]) != 1 {
		s.writeError(writer, http.StatusUnauthorized, "unauthorized", "authentication failed")
		return false
	}
	return true
}

func (s *Server) authenticateExport(writer http.ResponseWriter, request *http.Request) bool {
	if !s.exportConfigured {
		s.writeError(writer, http.StatusServiceUnavailable, "export_not_configured", "observer export is not configured")
		return false
	}
	token := bearerToken(request.Header.Get("Authorization"))
	provided := sha256.Sum256([]byte(token))
	if token == "" || subtle.ConstantTimeCompare(provided[:], s.exportTokenHash[:]) != 1 {
		s.writeError(writer, http.StatusUnauthorized, "unauthorized", "authentication failed")
		return false
	}
	return true
}

func observedClientIP(request *http.Request) (string, string) {
	transportPeer := request.RemoteAddr
	if host, _, err := net.SplitHostPort(request.RemoteAddr); err == nil {
		transportPeer = host
	}
	resolved := strings.TrimSpace(request.Header.Get(ResolvedClientIPHeader))
	if address, err := netip.ParseAddr(resolved); err == nil {
		return address.Unmap().String(), transportPeer
	}
	if address, err := netip.ParseAddr(transportPeer); err == nil {
		return address.Unmap().String(), transportPeer
	}
	return "", transportPeer
}

func validateEmbeddedRelease(manifest ReleaseManifest, artifact []byte, publicKey ed25519.PublicKey) error {
	if manifest.APIVersion != APIVersion || manifest.Version == "" || manifest.GOOS == "" || manifest.GOARCH == "" {
		return errors.New("embedded observer release manifest is incomplete")
	}
	if manifest.ContentType != "application/octet-stream" && manifest.ContentType != "application/x-executable" {
		return errors.New("embedded observer release content type is invalid")
	}
	if manifest.DownloadPath != "/api/v1/observer/releases/artifact" || manifest.Size != int64(len(artifact)) || manifest.Size <= 0 {
		return errors.New("embedded observer release size or download path is invalid")
	}
	digest := sha256.Sum256(artifact)
	expected, err := decodeSHA256(manifest.SHA256)
	if err != nil || subtle.ConstantTimeCompare(digest[:], expected[:]) != 1 {
		return errors.New("embedded observer release SHA-256 mismatch")
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("embedded observer release public key is invalid")
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(manifest.Signature))
	if err != nil || !ed25519.Verify(publicKey, []byte(hex.EncodeToString(digest[:])), signature) {
		return errors.New("embedded observer release signature is invalid")
	}
	return nil
}

func validAgentMetadata(metadata agentMetadata) bool {
	if !validInstallationID(metadata.InstallationID) || strings.TrimSpace(metadata.AgentVersion) == "" || metadata.BinarySize <= 0 {
		return false
	}
	if _, err := decodeSHA256(metadata.BinarySHA256); err != nil {
		return false
	}
	return metadata.GOOS != "" && metadata.GOARCH != ""
}

func validInstallationID(value string) bool {
	if !strings.HasPrefix(value, "foai_") {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "foai_"))
	return err == nil && len(decoded) == 32
}

func decodeSHA256(value string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	normalized := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "sha256:")
	decoded, err := hex.DecodeString(normalized)
	if err != nil || len(decoded) != sha256.Size {
		return result, errors.New("invalid SHA-256")
	}
	copy(result[:], decoded)
	return result, nil
}

func decodeJSONLimited(reader io.Reader, limit int64, output any) error {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || int64(len(data)) > limit {
		return errors.New("JSON body exceeds limit")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON body contains trailing data")
	}
	return nil
}

func bearerToken(value string) string {
	scheme, token, ok := strings.Cut(strings.TrimSpace(value), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

func writeAtomic(path string, data []byte, replace bool) error {
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, ".observer-control-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if replace {
		if err := os.Rename(tempPath, path); err != nil {
			return err
		}
	} else if err := os.Link(tempPath, path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		committed = true
		_ = os.Remove(tempPath)
		return os.ErrExist
	}
	committed = true
	_ = os.Remove(tempPath)
	if handle, err := os.Open(directory); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
	return nil
}

func (s *Server) writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func (s *Server) writeError(writer http.ResponseWriter, status int, code, message string) {
	s.writeJSON(writer, status, map[string]any{"api_version": APIVersion, "error": map[string]string{"code": code, "message": message}})
}

func (s *Server) writeInternalError(writer http.ResponseWriter, err error) {
	slog.Error("observer control request failed", "error", err)
	s.writeError(writer, http.StatusInternalServerError, "internal_error", "request failed")
}

func mediaType(value string) string {
	parsed, _, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed)
}
