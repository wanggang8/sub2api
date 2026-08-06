package observercontrol

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const maxArchiveMemberBytes = int64(64 << 20)

type parsedArchive struct {
	manifest       uploadManifest
	observationSHA string
}

func (s *Server) upload(writer http.ResponseWriter, request *http.Request) {
	if !s.authenticate(writer, request) {
		return
	}
	if mediaType(request.Header.Get("Content-Type")) != "application/gzip" {
		s.writeError(writer, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/gzip")
		return
	}
	archive, err := io.ReadAll(io.LimitReader(request.Body, s.maxArchiveBytes+1))
	if err != nil || int64(len(archive)) > s.maxArchiveBytes {
		s.writeError(writer, http.StatusRequestEntityTooLarge, "archive_too_large", "observation archive exceeds limit")
		return
	}
	parsed, err := parseArchive(archive, s.maxArchiveBytes)
	if err != nil {
		s.writeError(writer, http.StatusUnprocessableEntity, "invalid_archive", "observation archive validation failed")
		return
	}
	if parsed.manifest.Agent == nil || !validAgentMetadata(*parsed.manifest.Agent) {
		s.writeError(writer, http.StatusUnprocessableEntity, "invalid_agent_metadata", "agent metadata is incomplete or invalid")
		return
	}
	identifierDigest := sha256.Sum256([]byte(parsed.manifest.Agent.InstallationID + "\x00" + parsed.observationSHA))
	uploadID := "obs_" + hex.EncodeToString(identifierDigest[:16])
	path := filepath.Join(s.dataDir, "observations", uploadID+".tar.gz")
	s.dataMu.Lock()
	defer s.dataMu.Unlock()
	receipted, err := s.hasExportReceipt(uploadID)
	if err != nil {
		s.writeInternalError(writer, err)
		return
	}
	if receipted {
		s.writeJSON(writer, http.StatusOK, map[string]any{"api_version": APIVersion, "upload_id": uploadID, "status": "accepted"})
		return
	}
	created := true
	if err := writeAtomic(path, archive, false); err != nil {
		if !errors.Is(err, os.ErrExist) {
			s.writeInternalError(writer, err)
			return
		}
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			s.writeInternalError(writer, errors.New("existing observation archive is invalid"))
			return
		}
		created = false
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	s.writeJSON(writer, status, map[string]any{"api_version": APIVersion, "upload_id": uploadID, "status": "accepted"})
}

func parseArchive(data []byte, maxArchiveBytes int64) (parsedArchive, error) {
	if int64(len(data)) > maxArchiveBytes {
		return parsedArchive{}, errors.New("archive exceeds limit")
	}
	gzipReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return parsedArchive{}, err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(io.LimitReader(gzipReader, 2*maxArchiveBytes))
	files := map[string][]byte{}
	allowed := map[string]bool{"manifest.json": true, "observation.json": true, "collect.log": true}
	for {
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return parsedArchive{}, nextErr
		}
		if !allowed[header.Name] || header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > maxArchiveMemberBytes {
			return parsedArchive{}, errors.New("invalid archive member")
		}
		if _, exists := files[header.Name]; exists {
			return parsedArchive{}, errors.New("duplicate archive member")
		}
		body, readErr := io.ReadAll(io.LimitReader(tarReader, header.Size+1))
		if readErr != nil || int64(len(body)) != header.Size {
			return parsedArchive{}, errors.New("archive member size mismatch")
		}
		files[header.Name] = body
	}
	if files["manifest.json"] == nil || files["observation.json"] == nil {
		return parsedArchive{}, errors.New("required archive member missing")
	}
	var manifest uploadManifest
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		return parsedArchive{}, err
	}
	if manifest.APIVersion != APIVersion || manifest.Agent == nil || manifest.AgentVersion != manifest.Agent.AgentVersion ||
		manifest.GOOS != manifest.Agent.GOOS || manifest.GOARCH != manifest.Agent.GOARCH {
		return parsedArchive{}, errors.New("invalid upload manifest")
	}
	declared := map[string]archiveFile{}
	for _, item := range manifest.Files {
		if item.Name == "manifest.json" || !allowed[item.Name] {
			return parsedArchive{}, errors.New("invalid manifest file")
		}
		if _, exists := declared[item.Name]; exists {
			return parsedArchive{}, errors.New("duplicate manifest file")
		}
		declared[item.Name] = item
	}
	for name, body := range files {
		if name == "manifest.json" {
			continue
		}
		item, ok := declared[name]
		digest := sha256.Sum256(body)
		if !ok || item.Size != int64(len(body)) || item.SHA256 != "sha256:"+hex.EncodeToString(digest[:]) {
			return parsedArchive{}, errors.New("archive file checksum mismatch")
		}
	}
	if len(declared) != len(files)-1 {
		return parsedArchive{}, errors.New("manifest file set mismatch")
	}
	var observationValue observation
	if err := json.Unmarshal(files["observation.json"], &observationValue); err != nil {
		return parsedArchive{}, err
	}
	if observationValue.SchemaVersion != "fobrain.network-observation/v1" || observationValue.Collector.Name != "fobrain-net-observer" {
		return parsedArchive{}, errors.New("invalid observation")
	}
	if manifest.Agent.SensorID != observationValue.Sensor.SensorID {
		return parsedArchive{}, errors.New("agent sensor identity mismatch")
	}
	if logData, exists := files["collect.log"]; exists && !bytes.Contains(logData, []byte("collection started")) && !bytes.Contains(logData, []byte("capture interface opened")) {
		return parsedArchive{}, errors.New("invalid collection log")
	}
	observationDigest := sha256.Sum256(files["observation.json"])
	return parsedArchive{manifest: manifest, observationSHA: fmt.Sprintf("sha256:%x", observationDigest)}, nil
}
