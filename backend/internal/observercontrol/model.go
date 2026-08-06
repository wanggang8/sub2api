package observercontrol

import "time"

const APIVersion = "fobrain-observer-remote/v1"

type ReleaseManifest struct {
	APIVersion   string `json:"api_version"`
	Version      string `json:"version"`
	GOOS         string `json:"goos"`
	GOARCH       string `json:"goarch"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	ContentType  string `json:"content_type"`
	DownloadPath string `json:"download_path,omitempty"`
	Signature    string `json:"signature"`
	SigningKeyID string `json:"signing_key_id,omitempty"`
}

type archiveFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type uploadManifest struct {
	APIVersion   string         `json:"api_version"`
	AgentVersion string         `json:"agent_version"`
	GOOS         string         `json:"goos"`
	GOARCH       string         `json:"goarch"`
	CreatedAt    time.Time      `json:"created_at"`
	Agent        *agentMetadata `json:"agent,omitempty"`
	Files        []archiveFile  `json:"files"`
}

type agentAddress struct {
	IP        string `json:"ip"`
	PrefixLen int    `json:"prefix_length"`
	Scope     string `json:"scope"`
}

type agentInterface struct {
	Name      string         `json:"name"`
	Index     int            `json:"index"`
	Addresses []agentAddress `json:"addresses"`
}

type agentMetadata struct {
	InstallationID string           `json:"agent_installation_id"`
	SensorID       string           `json:"sensor_id,omitempty"`
	MachineIDHash  string           `json:"machine_id_hash,omitempty"`
	Hostname       string           `json:"hostname,omitempty"`
	AgentVersion   string           `json:"agent_version"`
	BinarySHA256   string           `json:"binary_sha256"`
	BinarySize     int64            `json:"binary_size"`
	GOOS           string           `json:"goos"`
	GOARCH         string           `json:"goarch"`
	Interfaces     []agentInterface `json:"interfaces"`
	ReportedAt     time.Time        `json:"reported_at"`
}

type heartbeatRecord struct {
	Agent             agentMetadata `json:"agent"`
	ReceivedAt        time.Time     `json:"received_at"`
	ObservedEgressIP  string        `json:"observed_egress_ip,omitempty"`
	TransportPeerHint string        `json:"transport_peer_hint,omitempty"`
}

type observation struct {
	SchemaVersion string `json:"schema_version"`
	Collector     struct {
		Name string `json:"name"`
	} `json:"collector"`
	Sensor struct {
		SensorID string `json:"sensor_id"`
	} `json:"sensor"`
}
