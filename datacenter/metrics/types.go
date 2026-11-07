// Package metrics reads bounded, one-shot Prometheus/DCGM observations. It does
// not discover hardware, infer inventory ownership, or control exporters.
package metrics

import "time"

type Access struct{ URL, Token string }
type Filters struct{ Site, Rack, Host string }

type SourceHealth struct {
	Site             string   `json:"site"`
	Rack             string   `json:"rack"`
	Host             string   `json:"host"`
	Job              string   `json:"job"`
	Instance         string   `json:"instance"`
	Up               *bool    `json:"up"`
	SampleAgeSeconds *float64 `json:"sample_age_seconds"`
	Freshness        string   `json:"freshness"`
	Healthy          bool     `json:"healthy"`
}

type Fleet struct {
	CollectedAt  time.Time      `json:"collected_at"`
	DataMode     string         `json:"data_mode"`
	Rows         []Host         `json:"rows"`
	SourceHealth []SourceHealth `json:"source_health"`
}

type Host struct {
	Site                      string         `json:"site"`
	Rack                      string         `json:"rack"`
	Host                      string         `json:"host"`
	MappingMissing            []string       `json:"mapping_missing"`
	CPUPercent                *float64       `json:"cpu_percent"`
	RAMUsedPercent            *float64       `json:"ram_used_percent"`
	RootFilesystemUsedPercent *float64       `json:"root_filesystem_used_percent"`
	NetworkRXBytesPerSecond   *float64       `json:"network_rx_bytes_per_second"`
	NetworkTXBytesPerSecond   *float64       `json:"network_tx_bytes_per_second"`
	Up                        *bool          `json:"up"`
	SampleAgeSeconds          *float64       `json:"sample_age_seconds"`
	Freshness                 string         `json:"freshness"`
	Sources                   []SourceHealth `json:"sources"`
}

type GPUFleet struct {
	CollectedAt  time.Time      `json:"collected_at"`
	DataMode     string         `json:"data_mode"`
	Rows         []GPU          `json:"rows"`
	SourceHealth []SourceHealth `json:"source_health"`
}

type GPU struct {
	Site                 string         `json:"site"`
	Rack                 string         `json:"rack"`
	Host                 string         `json:"host"`
	GPU                  string         `json:"gpu"`
	UUID                 string         `json:"uuid"`
	Model                string         `json:"model"`
	MappingMissing       []string       `json:"mapping_missing"`
	UtilizationPercent   *float64       `json:"utilization_percent"`
	FramebufferUsedBytes *float64       `json:"framebuffer_used_bytes"`
	FramebufferFreeBytes *float64       `json:"framebuffer_free_bytes"`
	TemperatureCelsius   *float64       `json:"temperature_celsius"`
	PowerWatts           *float64       `json:"power_watts"`
	Up                   *bool          `json:"up"`
	SampleAgeSeconds     *float64       `json:"sample_age_seconds"`
	Freshness            string         `json:"freshness"`
	Sources              []SourceHealth `json:"sources"`
}
