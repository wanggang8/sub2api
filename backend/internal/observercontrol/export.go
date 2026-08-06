package observercontrol

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const exportSchemaVersion = "fobrain.observer-export/v1"

var (
	errNoExportableData = errors.New("no exportable observer data")
	exportIDPattern     = regexp.MustCompile(`^exp_[0-9]{8}T[0-9]{6}Z_[0-9a-f]{16}$`)
)

type exportFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type exportManifest struct {
	SchemaVersion string       `json:"schema_version"`
	ExportID      string       `json:"export_id"`
	CreatedAt     time.Time    `json:"created_at"`
	Files         []exportFile `json:"files"`
}

type exportSnapshotFile struct {
	memberPath   string
	activePath   string
	snapshotPath string
	observation  bool
}

type exportResult struct {
	id     string
	path   string
	size   int64
	sha256 string
}

type exportReceipt struct {
	SchemaVersion string    `json:"schema_version"`
	UploadID      string    `json:"upload_id"`
	ExportID      string    `json:"export_id"`
	ExportedAt    time.Time `json:"exported_at"`
}

func (s *Server) createExport(writer http.ResponseWriter, request *http.Request) {
	if !s.authenticateExport(writer, request) {
		return
	}
	if !s.exportMu.TryLock() {
		s.writeError(writer, http.StatusConflict, "export_in_progress", "an observer export is already in progress")
		return
	}
	defer s.exportMu.Unlock()

	result, err := s.buildExport()
	if errors.Is(err, errNoExportableData) {
		s.writeError(writer, http.StatusConflict, "no_exportable_data", "there is no observer data to export")
		return
	}
	if err != nil {
		s.writeInternalError(writer, err)
		return
	}
	s.serveExport(writer, result, http.StatusCreated)
}

func (s *Server) downloadExport(writer http.ResponseWriter, request *http.Request) {
	if !s.authenticateExport(writer, request) {
		return
	}
	exportID := request.PathValue("export_id")
	if !exportIDPattern.MatchString(exportID) {
		s.writeError(writer, http.StatusNotFound, "export_not_found", "observer export was not found")
		return
	}
	path := filepath.Join(s.dataDir, "exports", exportID+".tar.gz")
	info, err := regularPrivateFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.writeError(writer, http.StatusNotFound, "export_not_found", "observer export was not found")
		return
	}
	if err != nil {
		s.writeInternalError(writer, err)
		return
	}
	digest, err := hashFile(path)
	if err != nil {
		s.writeInternalError(writer, err)
		return
	}
	s.serveExport(writer, exportResult{id: exportID, path: path, size: info.Size(), sha256: digest}, http.StatusOK)
}

func (s *Server) buildExport() (exportResult, error) {
	if err := s.recoverExportSnapshots(); err != nil {
		return exportResult{}, fmt.Errorf("recover observer exports: %w", err)
	}
	createdAt := s.now().UTC()
	exportID, err := newExportID(createdAt)
	if err != nil {
		return exportResult{}, err
	}
	stagingPath := filepath.Join(s.dataDir, ".export-"+exportID)
	files, err := s.snapshotExport(stagingPath)
	if err != nil {
		_ = os.RemoveAll(stagingPath)
		return exportResult{}, err
	}
	if len(files) == 0 {
		_ = os.RemoveAll(stagingPath)
		return exportResult{}, errNoExportableData
	}
	manifest, err := buildExportManifest(exportID, createdAt, files)
	if err != nil {
		return exportResult{}, err
	}
	result, err := s.persistExport(manifest, files)
	if err != nil {
		return exportResult{}, err
	}
	if err := verifyExportPackage(result.path, exportID, files); err != nil {
		if removeErr := os.Remove(result.path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return exportResult{}, errors.Join(err, removeErr)
		}
		return exportResult{}, err
	}
	if err := s.cleanupExportSnapshot(exportID, createdAt, stagingPath, files); err != nil {
		slog.Error("observer export persisted but source cleanup is incomplete", "export_id", exportID, "error", err)
	}
	return result, nil
}

func buildExportManifest(exportID string, createdAt time.Time, files []exportSnapshotFile) (exportManifest, error) {
	manifest := exportManifest{SchemaVersion: exportSchemaVersion, ExportID: exportID, CreatedAt: createdAt}
	for _, file := range files {
		info, err := regularPrivateFile(file.snapshotPath)
		if err != nil {
			return exportManifest{}, err
		}
		digest, err := hashFile(file.snapshotPath)
		if err != nil {
			return exportManifest{}, err
		}
		manifest.Files = append(manifest.Files, exportFile{Name: file.memberPath, Size: info.Size(), SHA256: digest})
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Name < manifest.Files[j].Name })
	return manifest, nil
}

func (s *Server) snapshotExport(stagingPath string) ([]exportSnapshotFile, error) {
	s.dataMu.Lock()
	defer s.dataMu.Unlock()
	if err := os.Mkdir(stagingPath, 0o700); err != nil {
		return nil, err
	}
	var files []exportSnapshotFile
	for _, source := range []struct {
		directory   string
		extension   string
		memberRoot  string
		validate    func(string) bool
		observation bool
	}{
		{directory: "observations", extension: ".tar.gz", memberRoot: "observations", validate: validUploadID, observation: true},
		{directory: "agents", extension: ".json", memberRoot: "agents", validate: validInstallationID},
	} {
		stageDirectory := filepath.Join(stagingPath, source.directory)
		if err := os.Mkdir(stageDirectory, 0o700); err != nil {
			return nil, err
		}
		activeDirectory := filepath.Join(s.dataDir, source.directory)
		entries, err := os.ReadDir(activeDirectory)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			name := entry.Name()
			identifier := strings.TrimSuffix(name, source.extension)
			if identifier == name || !source.validate(identifier) {
				return nil, fmt.Errorf("invalid observer export source name: %s", name)
			}
			activePath := filepath.Join(activeDirectory, name)
			if _, err := regularPrivateFile(activePath); err != nil {
				return nil, err
			}
			snapshotPath := filepath.Join(stageDirectory, name)
			if err := os.Link(activePath, snapshotPath); err != nil {
				return nil, fmt.Errorf("snapshot observer export source %s: %w", name, err)
			}
			files = append(files, exportSnapshotFile{
				memberPath: filepath.ToSlash(filepath.Join(source.memberRoot, name)), activePath: activePath,
				snapshotPath: snapshotPath, observation: source.observation,
			})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].memberPath < files[j].memberPath })
	return files, nil
}

func (s *Server) persistExport(manifest exportManifest, files []exportSnapshotFile) (exportResult, error) {
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return exportResult{}, err
	}
	manifestData = append(manifestData, '\n')
	exportDirectory := filepath.Join(s.dataDir, "exports")
	temporary, err := os.CreateTemp(exportDirectory, ".observer-export-")
	if err != nil {
		return exportResult{}, err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return exportResult{}, err
	}
	gzipWriter := gzip.NewWriter(temporary)
	gzipWriter.Header.ModTime = manifest.CreatedAt
	tarWriter := tar.NewWriter(gzipWriter)
	if err := writeTarMember(tarWriter, "manifest.json", manifestData, manifest.CreatedAt); err != nil {
		return exportResult{}, err
	}
	for _, file := range files {
		body, readErr := os.ReadFile(file.snapshotPath)
		if readErr != nil {
			return exportResult{}, readErr
		}
		if err := writeTarMember(tarWriter, file.memberPath, body, manifest.CreatedAt); err != nil {
			return exportResult{}, err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return exportResult{}, err
	}
	if err := gzipWriter.Close(); err != nil {
		return exportResult{}, err
	}
	if err := temporary.Sync(); err != nil {
		return exportResult{}, err
	}
	if err := temporary.Close(); err != nil {
		return exportResult{}, err
	}
	digest, err := hashFile(temporaryPath)
	if err != nil {
		return exportResult{}, err
	}
	info, err := regularPrivateFile(temporaryPath)
	if err != nil {
		return exportResult{}, err
	}
	targetPath := filepath.Join(exportDirectory, manifest.ExportID+".tar.gz")
	if err := os.Rename(temporaryPath, targetPath); err != nil {
		return exportResult{}, err
	}
	committed = true
	syncDirectory(exportDirectory)
	return exportResult{id: manifest.ExportID, path: targetPath, size: info.Size(), sha256: digest}, nil
}

func (s *Server) cleanupExportSnapshot(exportID string, exportedAt time.Time, stagingPath string, files []exportSnapshotFile) error {
	s.dataMu.Lock()
	defer s.dataMu.Unlock()
	var cleanupErrors []error
	for _, file := range files {
		activeInfo, activeErr := os.Lstat(file.activePath)
		if errors.Is(activeErr, os.ErrNotExist) {
			continue
		}
		snapshotInfo, snapshotErr := os.Lstat(file.snapshotPath)
		if activeErr != nil || snapshotErr != nil {
			cleanupErrors = append(cleanupErrors, errors.Join(activeErr, snapshotErr))
			continue
		}
		if !os.SameFile(activeInfo, snapshotInfo) {
			continue
		}
		if file.observation {
			uploadID := strings.TrimSuffix(filepath.Base(file.activePath), ".tar.gz")
			if err := s.writeExportReceipt(uploadID, exportID, exportedAt); err != nil {
				cleanupErrors = append(cleanupErrors, err)
				continue
			}
		}
		if err := os.Remove(file.activePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	if len(cleanupErrors) == 0 {
		if err := os.RemoveAll(stagingPath); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func (s *Server) hasExportReceipt(uploadID string) (bool, error) {
	path := filepath.Join(s.dataDir, "export-receipts", uploadID+".json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	info, err := regularPrivateFile(path)
	if err != nil || info.Size() != int64(len(data)) {
		return false, fmt.Errorf("invalid observer export receipt for %s", uploadID)
	}
	var receipt exportReceipt
	if err := json.Unmarshal(data, &receipt); err != nil || receipt.SchemaVersion != exportSchemaVersion || receipt.UploadID != uploadID || !exportIDPattern.MatchString(receipt.ExportID) {
		return false, fmt.Errorf("invalid observer export receipt for %s", uploadID)
	}
	return true, nil
}

func (s *Server) writeExportReceipt(uploadID, exportID string, exportedAt time.Time) error {
	data, err := json.Marshal(exportReceipt{SchemaVersion: exportSchemaVersion, UploadID: uploadID, ExportID: exportID, ExportedAt: exportedAt})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(s.dataDir, "export-receipts", uploadID+".json")
	if err := writeAtomic(path, data, false); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		valid, validateErr := s.hasExportReceipt(uploadID)
		if validateErr != nil {
			return validateErr
		}
		if !valid {
			return fmt.Errorf("observer export receipt disappeared for %s", uploadID)
		}
	}
	return nil
}

func (s *Server) recoverExportSnapshots() error {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return err
	}
	var recoveryErrors []error
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".export-") {
			continue
		}
		exportID := strings.TrimPrefix(entry.Name(), ".export-")
		if !exportIDPattern.MatchString(exportID) || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			recoveryErrors = append(recoveryErrors, fmt.Errorf("invalid observer export staging path: %s", entry.Name()))
			continue
		}
		stagingPath := filepath.Join(s.dataDir, entry.Name())
		exportPath := filepath.Join(s.dataDir, "exports", exportID+".tar.gz")
		info, statErr := regularPrivateFile(exportPath)
		if errors.Is(statErr, os.ErrNotExist) {
			if removeErr := os.RemoveAll(stagingPath); removeErr != nil {
				recoveryErrors = append(recoveryErrors, removeErr)
			}
			continue
		}
		if statErr != nil {
			recoveryErrors = append(recoveryErrors, statErr)
			continue
		}
		files, loadErr := s.loadExportSnapshot(stagingPath)
		if loadErr != nil {
			recoveryErrors = append(recoveryErrors, loadErr)
			continue
		}
		if verifyErr := verifyExportPackage(exportPath, exportID, files); verifyErr != nil {
			recoveryErrors = append(recoveryErrors, verifyErr)
			continue
		}
		if cleanupErr := s.cleanupExportSnapshot(exportID, info.ModTime().UTC(), stagingPath, files); cleanupErr != nil {
			recoveryErrors = append(recoveryErrors, cleanupErr)
		}
	}
	return errors.Join(recoveryErrors...)
}

func verifyExportPackage(path, exportID string, files []exportSnapshotFile) error {
	expected := make(map[string]exportFile, len(files))
	for _, file := range files {
		info, err := regularPrivateFile(file.snapshotPath)
		if err != nil {
			return err
		}
		digest, err := hashFile(file.snapshotPath)
		if err != nil {
			return err
		}
		expected[file.memberPath] = exportFile{Name: file.memberPath, Size: info.Size(), SHA256: digest}
	}

	handle, err := os.Open(path)
	if err != nil {
		return err
	}
	defer handle.Close()
	gzipReader, err := gzip.NewReader(handle)
	if err != nil {
		return fmt.Errorf("open observer export gzip: %w", err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	seen := make(map[string]bool, len(expected))
	manifestSeen := false
	for {
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("read observer export tar: %w", nextErr)
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 {
			return fmt.Errorf("invalid observer export member: %s", header.Name)
		}
		if header.Name == "manifest.json" {
			if manifestSeen || header.Size > maxJSONBodyBytes {
				return errors.New("invalid observer export manifest member")
			}
			manifestData, readErr := io.ReadAll(io.LimitReader(tarReader, header.Size+1))
			if readErr != nil || int64(len(manifestData)) != header.Size {
				return errors.New("invalid observer export manifest size")
			}
			var manifest exportManifest
			if err := json.Unmarshal(manifestData, &manifest); err != nil || manifest.SchemaVersion != exportSchemaVersion || manifest.ExportID != exportID || len(manifest.Files) != len(expected) {
				return errors.New("invalid observer export manifest")
			}
			declared := make(map[string]exportFile, len(manifest.Files))
			for _, item := range manifest.Files {
				want, ok := expected[item.Name]
				if !ok || item != want || declared[item.Name].Name != "" {
					return errors.New("observer export manifest does not match snapshot")
				}
				declared[item.Name] = item
			}
			manifestSeen = true
			continue
		}
		want, ok := expected[header.Name]
		if !ok || seen[header.Name] || header.Size != want.Size {
			return fmt.Errorf("unexpected observer export member: %s", header.Name)
		}
		hash := sha256.New()
		written, copyErr := io.Copy(hash, tarReader)
		if copyErr != nil || written != want.Size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != want.SHA256 {
			return fmt.Errorf("observer export member failed integrity validation: %s", header.Name)
		}
		seen[header.Name] = true
	}
	if _, err := io.Copy(io.Discard, gzipReader); err != nil {
		return fmt.Errorf("observer export gzip failed integrity validation: %w", err)
	}
	if !manifestSeen || len(seen) != len(expected) {
		return errors.New("observer export member set is incomplete")
	}
	return nil
}

func (s *Server) loadExportSnapshot(stagingPath string) ([]exportSnapshotFile, error) {
	var files []exportSnapshotFile
	for _, source := range []struct {
		directory   string
		extension   string
		validate    func(string) bool
		observation bool
	}{
		{directory: "observations", extension: ".tar.gz", validate: validUploadID, observation: true},
		{directory: "agents", extension: ".json", validate: validInstallationID},
	} {
		stageDirectory := filepath.Join(stagingPath, source.directory)
		entries, err := os.ReadDir(stageDirectory)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			name := entry.Name()
			identifier := strings.TrimSuffix(name, source.extension)
			if identifier == name || !source.validate(identifier) {
				return nil, fmt.Errorf("invalid observer export snapshot name: %s", name)
			}
			snapshotPath := filepath.Join(stageDirectory, name)
			if _, err := regularPrivateFile(snapshotPath); err != nil {
				return nil, err
			}
			files = append(files, exportSnapshotFile{
				memberPath: filepath.ToSlash(filepath.Join(source.directory, name)),
				activePath: filepath.Join(s.dataDir, source.directory, name), snapshotPath: snapshotPath,
				observation: source.observation,
			})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].memberPath < files[j].memberPath })
	return files, nil
}

func (s *Server) serveExport(writer http.ResponseWriter, result exportResult, status int) {
	handle, err := os.Open(result.path)
	if err != nil {
		s.writeInternalError(writer, err)
		return
	}
	defer handle.Close()
	writer.Header().Set("Content-Type", "application/gzip")
	writer.Header().Set("Content-Length", strconv.FormatInt(result.size, 10))
	writer.Header().Set("Content-Disposition", `attachment; filename="observer-export-`+result.id+`.tar.gz"`)
	writer.Header().Set("X-Observer-Export-ID", result.id)
	writer.Header().Set("X-Checksum-SHA256", result.sha256)
	writer.WriteHeader(status)
	if _, err := io.Copy(writer, handle); err != nil {
		slog.Warn("observer export response interrupted", "export_id", result.id, "error", err)
	}
}

func newExportID(now time.Time) (string, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("exp_%s_%x", now.UTC().Format("20060102T150405Z"), suffix[:]), nil
}

func validUploadID(value string) bool {
	if !strings.HasPrefix(value, "obs_") || len(value) != len("obs_")+32 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "obs_"))
	return err == nil
}

func regularPrivateFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("observer control path is not a regular file: %s", path)
	}
	return info, nil
}

func hashFile(path string) (string, error) {
	handle, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer handle.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, handle); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func writeTarMember(writer *tar.Writer, name string, data []byte, modifiedAt time.Time) error {
	if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: modifiedAt.UTC(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := writer.Write(data)
	return err
}

func syncDirectory(path string) {
	handle, err := os.Open(path)
	if err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
}
