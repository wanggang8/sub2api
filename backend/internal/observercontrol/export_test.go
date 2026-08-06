package observercontrol

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExportPackagesDataCleansSourcesAndSupportsRepeatDownload(t *testing.T) {
	server, dataDir := newTestServer(t, 0)
	metadata := validTestAgentMetadata()
	heartbeatData, err := json.Marshal(metadata)
	require.NoError(t, err)
	heartbeat := performRequest(t, server.Handler(), http.MethodPost, "/api/v1/observer/agents/heartbeat", testAgentToken, nil, heartbeatData)
	require.Equal(t, http.StatusOK, heartbeat.Code)

	archive := validTestArchive(t)
	upload := performRequest(t, server.Handler(), http.MethodPost, "/api/v1/observer/observations", testAgentToken, http.Header{"Content-Type": []string{"application/gzip"}}, archive)
	require.Equal(t, http.StatusCreated, upload.Code)
	var uploadResponse struct {
		UploadID string `json:"upload_id"`
	}
	require.NoError(t, json.Unmarshal(upload.Body.Bytes(), &uploadResponse))
	require.NotEmpty(t, uploadResponse.UploadID)

	exported := performRequest(t, server.Handler(), http.MethodPost, "/api/v1/observer/exports", testExportToken, nil, nil)
	require.Equal(t, http.StatusCreated, exported.Code)
	require.Equal(t, "application/gzip", exported.Header().Get("Content-Type"))
	exportID := exported.Header().Get("X-Observer-Export-ID")
	require.NotEmpty(t, exportID)
	require.Equal(t, `attachment; filename="observer-export-`+exportID+`.tar.gz"`, exported.Header().Get("Content-Disposition"))
	digest := sha256.Sum256(exported.Body.Bytes())
	require.Equal(t, "sha256:"+hex.EncodeToString(digest[:]), exported.Header().Get("X-Checksum-SHA256"))

	members := readExportMembers(t, exported.Body.Bytes())
	wantNames := []string{
		"agents/" + metadata.InstallationID + ".json",
		"manifest.json",
		"observations/" + uploadResponse.UploadID + ".tar.gz",
	}
	gotNames := make([]string, 0, len(members))
	for name := range members {
		gotNames = append(gotNames, name)
	}
	sort.Strings(gotNames)
	require.Equal(t, wantNames, gotNames)
	require.Equal(t, archive, members["observations/"+uploadResponse.UploadID+".tar.gz"])

	var manifest struct {
		SchemaVersion string    `json:"schema_version"`
		ExportID      string    `json:"export_id"`
		CreatedAt     time.Time `json:"created_at"`
		Files         []struct {
			Name   string `json:"name"`
			Size   int64  `json:"size"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	require.NoError(t, json.Unmarshal(members["manifest.json"], &manifest))
	require.Equal(t, "fobrain.observer-export/v1", manifest.SchemaVersion)
	require.Equal(t, exportID, manifest.ExportID)
	require.Equal(t, time.Date(2026, time.August, 6, 1, 2, 3, 0, time.UTC), manifest.CreatedAt)
	require.Len(t, manifest.Files, 2)
	for _, item := range manifest.Files {
		body, exists := members[item.Name]
		require.True(t, exists)
		fileDigest := sha256.Sum256(body)
		require.Equal(t, int64(len(body)), item.Size)
		require.Equal(t, "sha256:"+hex.EncodeToString(fileDigest[:]), item.SHA256)
	}

	_, err = os.Stat(filepath.Join(dataDir, "observations", uploadResponse.UploadID+".tar.gz"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(dataDir, "agents", metadata.InstallationID+".json"))
	require.ErrorIs(t, err, os.ErrNotExist)

	repeated := performRequest(t, server.Handler(), http.MethodGet, "/api/v1/observer/exports/"+exportID, testExportToken, nil, nil)
	require.Equal(t, http.StatusOK, repeated.Code)
	require.Equal(t, exported.Body.Bytes(), repeated.Body.Bytes())
	require.Equal(t, exported.Header().Get("X-Checksum-SHA256"), repeated.Header().Get("X-Checksum-SHA256"))

	duplicate := performRequest(t, server.Handler(), http.MethodPost, "/api/v1/observer/observations", testAgentToken, http.Header{"Content-Type": []string{"application/gzip"}}, archive)
	require.Equal(t, http.StatusOK, duplicate.Code)
	require.Contains(t, duplicate.Body.String(), uploadResponse.UploadID)
	_, err = os.Stat(filepath.Join(dataDir, "observations", uploadResponse.UploadID+".tar.gz"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestExportRejectsEmptyBatchAndUnknownID(t *testing.T) {
	server, _ := newTestServer(t, 0)
	empty := performRequest(t, server.Handler(), http.MethodPost, "/api/v1/observer/exports", testExportToken, nil, nil)
	require.Equal(t, http.StatusConflict, empty.Code)
	require.Contains(t, empty.Body.String(), `"code":"no_exportable_data"`)

	unknown := performRequest(t, server.Handler(), http.MethodGet, "/api/v1/observer/exports/exp_20260806T010203Z_0000000000000000", testExportToken, nil, nil)
	require.Equal(t, http.StatusNotFound, unknown.Code)
	require.Contains(t, unknown.Body.String(), `"code":"export_not_found"`)

	invalid := performRequest(t, server.Handler(), http.MethodGet, "/api/v1/observer/exports/not-an-export", testExportToken, nil, nil)
	require.Equal(t, http.StatusNotFound, invalid.Code)
	require.Contains(t, invalid.Body.String(), `"code":"export_not_found"`)
}

func TestExportPreservesHeartbeatUpdatedAfterSnapshot(t *testing.T) {
	server, dataDir := newTestServer(t, 0)
	metadata := validTestAgentMetadata()
	metadata.Hostname = "before-export"
	writeHeartbeatForExportTest(t, server, metadata)

	exportID := "exp_20260806T010203Z_0000000000000003"
	stagingPath := filepath.Join(dataDir, ".export-"+exportID)
	files, err := server.snapshotExport(stagingPath)
	require.NoError(t, err)
	var heartbeatSnapshot []byte
	for _, file := range files {
		if file.memberPath == "agents/"+metadata.InstallationID+".json" {
			heartbeatSnapshot, err = os.ReadFile(file.snapshotPath)
			require.NoError(t, err)
		}
	}
	require.Contains(t, string(heartbeatSnapshot), `"hostname": "before-export"`)

	metadata.Hostname = "after-snapshot"
	writeHeartbeatForExportTest(t, server, metadata)
	require.NoError(t, server.cleanupExportSnapshot(exportID, time.Date(2026, time.August, 6, 1, 2, 3, 0, time.UTC), stagingPath, files))

	activePath := filepath.Join(dataDir, "agents", metadata.InstallationID+".json")
	active, err := os.ReadFile(activePath)
	require.NoError(t, err)
	require.Contains(t, string(active), `"hostname": "after-snapshot"`)

	next := performRequest(t, server.Handler(), http.MethodPost, "/api/v1/observer/exports", testExportToken, nil, nil)
	require.Equal(t, http.StatusCreated, next.Code)
	nextMembers := readExportMembers(t, next.Body.Bytes())
	require.Contains(t, string(nextMembers["agents/"+metadata.InstallationID+".json"]), `"hostname": "after-snapshot"`)
}

func TestExportRejectsConcurrentCreation(t *testing.T) {
	server, _ := newTestServer(t, 0)
	writeHeartbeatForExportTest(t, server, validTestAgentMetadata())
	server.exportMu.Lock()
	concurrent := performRequest(t, server.Handler(), http.MethodPost, "/api/v1/observer/exports", testExportToken, nil, nil)
	require.Equal(t, http.StatusConflict, concurrent.Code)
	require.Contains(t, concurrent.Body.String(), `"code":"export_in_progress"`)
	server.exportMu.Unlock()
	created := performRequest(t, server.Handler(), http.MethodPost, "/api/v1/observer/exports", testExportToken, nil, nil)
	require.Equal(t, http.StatusCreated, created.Code)
}

func TestRecoverExportSnapshots(t *testing.T) {
	t.Run("incomplete package preserves active sources", func(t *testing.T) {
		server, dataDir := newTestServer(t, 0)
		archive := validTestArchive(t)
		uploadID := uploadArchiveForExportTest(t, server, archive)
		exportID := "exp_20260806T010203Z_0000000000000001"
		stagingPath := filepath.Join(dataDir, ".export-"+exportID)
		files, err := server.snapshotExport(stagingPath)
		require.NoError(t, err)
		require.NotEmpty(t, files)

		require.NoError(t, server.recoverExportSnapshots())
		_, err = os.Stat(stagingPath)
		require.ErrorIs(t, err, os.ErrNotExist)
		_, err = os.Stat(filepath.Join(dataDir, "observations", uploadID+".tar.gz"))
		require.NoError(t, err)
	})

	t.Run("corrupt completed package never cleans active sources", func(t *testing.T) {
		server, dataDir := newTestServer(t, 0)
		archive := validTestArchive(t)
		uploadID := uploadArchiveForExportTest(t, server, archive)
		exportID := "exp_20260806T010203Z_0000000000000004"
		stagingPath := filepath.Join(dataDir, ".export-"+exportID)
		files, err := server.snapshotExport(stagingPath)
		require.NoError(t, err)
		require.NotEmpty(t, files)
		require.NoError(t, os.WriteFile(filepath.Join(dataDir, "exports", exportID+".tar.gz"), []byte("corrupt export"), 0o600))

		require.Error(t, server.recoverExportSnapshots())
		_, err = os.Stat(filepath.Join(dataDir, "observations", uploadID+".tar.gz"))
		require.NoError(t, err)
		receipted, err := server.hasExportReceipt(uploadID)
		require.NoError(t, err)
		require.False(t, receipted)
	})

	t.Run("truncated gzip footer never cleans active sources", func(t *testing.T) {
		server, dataDir := newTestServer(t, 0)
		archive := validTestArchive(t)
		uploadID := uploadArchiveForExportTest(t, server, archive)
		exportID := "exp_20260806T010203Z_0000000000000005"
		stagingPath := filepath.Join(dataDir, ".export-"+exportID)
		files, err := server.snapshotExport(stagingPath)
		require.NoError(t, err)
		manifest, err := buildExportManifest(exportID, time.Date(2026, time.August, 6, 1, 2, 3, 0, time.UTC), files)
		require.NoError(t, err)
		result, err := server.persistExport(manifest, files)
		require.NoError(t, err)
		packageData, err := os.ReadFile(result.path)
		require.NoError(t, err)
		require.Greater(t, len(packageData), 8)
		require.NoError(t, os.WriteFile(result.path, packageData[:len(packageData)-8], 0o600))

		require.Error(t, server.recoverExportSnapshots())
		_, err = os.Stat(filepath.Join(dataDir, "observations", uploadID+".tar.gz"))
		require.NoError(t, err)
	})

	t.Run("completed package resumes cleanup and receipt", func(t *testing.T) {
		server, dataDir := newTestServer(t, 0)
		archive := validTestArchive(t)
		uploadID := uploadArchiveForExportTest(t, server, archive)
		exportID := "exp_20260806T010203Z_0000000000000002"
		stagingPath := filepath.Join(dataDir, ".export-"+exportID)
		files, err := server.snapshotExport(stagingPath)
		require.NoError(t, err)
		manifest, err := buildExportManifest(exportID, time.Date(2026, time.August, 6, 1, 2, 3, 0, time.UTC), files)
		require.NoError(t, err)
		_, err = server.persistExport(manifest, files)
		require.NoError(t, err)

		require.NoError(t, server.recoverExportSnapshots())
		_, err = os.Stat(stagingPath)
		require.ErrorIs(t, err, os.ErrNotExist)
		_, err = os.Stat(filepath.Join(dataDir, "observations", uploadID+".tar.gz"))
		require.ErrorIs(t, err, os.ErrNotExist)
		receipted, err := server.hasExportReceipt(uploadID)
		require.NoError(t, err)
		require.True(t, receipted)

		duplicate := performRequest(t, server.Handler(), http.MethodPost, "/api/v1/observer/observations", testAgentToken, http.Header{"Content-Type": []string{"application/gzip"}}, archive)
		require.Equal(t, http.StatusOK, duplicate.Code)
	})
}

func writeHeartbeatForExportTest(t *testing.T, server *Server, metadata agentMetadata) {
	t.Helper()
	data, err := json.Marshal(metadata)
	require.NoError(t, err)
	response := performRequest(t, server.Handler(), http.MethodPost, "/api/v1/observer/agents/heartbeat", testAgentToken, nil, data)
	require.Equal(t, http.StatusOK, response.Code)
}

func uploadArchiveForExportTest(t *testing.T, server *Server, archive []byte) string {
	t.Helper()
	response := performRequest(t, server.Handler(), http.MethodPost, "/api/v1/observer/observations", testAgentToken, http.Header{"Content-Type": []string{"application/gzip"}}, archive)
	require.Equal(t, http.StatusCreated, response.Code)
	var result struct {
		UploadID string `json:"upload_id"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.NotEmpty(t, result.UploadID)
	return result.UploadID
}

func readExportMembers(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	gzipReader, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer gzipReader.Close()

	result := map[string][]byte{}
	tarReader := tar.NewReader(gzipReader)
	for {
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			break
		}
		require.NoError(t, nextErr)
		require.Equal(t, byte(tar.TypeReg), header.Typeflag)
		require.Equal(t, int64(0o600), header.Mode)
		body, readErr := io.ReadAll(tarReader)
		require.NoError(t, readErr)
		_, exists := result[header.Name]
		require.False(t, exists)
		result[header.Name] = body
	}
	return result
}
