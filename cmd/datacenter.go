package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/alikatgh/le-cli/datacenter/inventory"
	"github.com/alikatgh/le-cli/datacenter/metrics"
	"github.com/alikatgh/le-cli/internal/readapi"
	"github.com/alikatgh/le-cli/tools"
)

type datacenterOptions struct {
	prometheus, netbox, site, rack, host string
	demo, json, details                  bool
	timeout                              time.Duration
}
type dataSection[T any] struct {
	Configured bool   `json:"configured"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
	Data       *T     `json:"data,omitempty"`
}
type datacenterReport struct {
	DataMode    string                          `json:"data_mode"`
	GeneratedAt time.Time                       `json:"generated_at"`
	Notice      string                          `json:"notice"`
	Fleet       dataSection[metrics.Fleet]      `json:"fleet"`
	GPU         dataSection[metrics.GPUFleet]   `json:"gpu"`
	Inventory   dataSection[inventory.Snapshot] `json:"inventory"`
}

func datacenterCmd() *cobra.Command {
	o := &datacenterOptions{}
	root := &cobra.Command{Use: "datacenter", Short: "Read fleet, GPU and inventory evidence from approved sources", Long: "One-shot read-only Prometheus/DCGM and NetBox views. No discovery, agents or remote controls.\nUse --demo for invented offline data. Missing data is unknown, never assumed zero.\nTokens are environment-only; use LOCALHOST_PROMETHEUS_TOKEN and LOCALHOST_NETBOX_TOKEN through Varlock.\nLive URLs require HTTPS except explicit literal loopback. Requests, pagination and output are bounded.", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error { return runDatacenter(cmd, "summary", o) }}
	f := root.PersistentFlags()
	f.StringVar(&o.prometheus, "prometheus", "", "Approved Prometheus base URL (or LOCALHOST_PROMETHEUS_URL)")
	f.StringVar(&o.netbox, "netbox", "", "Approved NetBox root URL (or LOCALHOST_NETBOX_URL)")
	f.StringVar(&o.site, "site", "", "Exact site label/name")
	f.StringVar(&o.rack, "rack", "", "Exact rack label/name")
	f.StringVar(&o.host, "host", "", "Exact mapped host/name; host labels may fall back to exporter instance")
	f.BoolVar(&o.demo, "demo", false, "Invented offline fixture; ignores credentials and makes no requests")
	f.BoolVar(&o.json, "json", false, "Structured JSON, with missing values represented explicitly")
	f.BoolVar(&o.details, "details", false, "Include NetBox device/site/rack/owner names in output")
	f.DurationVar(&o.timeout, "timeout", 30*time.Second, "Whole collection deadline (positive, at most 30s)")
	for _, name := range []string{"summary", "fleet", "gpu", "inventory"} {
		name := name
		short := map[string]string{"summary": "Read all configured sources with explicit per-source status", "fleet": "CPU, RAM, root filesystem, network and source freshness", "gpu": "DCGM utilization, framebuffer, temperature, power and freshness", "inventory": "NetBox counts, ownership coverage and recorded lifecycle"}[name]
		root.AddCommand(&cobra.Command{Use: name, Short: short, Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error { return runDatacenter(cmd, name, o) }})
	}
	return root
}

func runDatacenter(cmd *cobra.Command, view string, o *datacenterOptions) error {
	if o.timeout <= 0 || o.timeout > 30*time.Second {
		return usage(errors.New("datacenter timeout must be positive and at most 30s"))
	}
	for _, s := range []string{o.site, o.rack, o.host} {
		if !utf8.ValidString(s) || len(s) > 96 {
			return usage(errors.New("invalid datacenter filter"))
		}
		for _, r := range s {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				return usage(errors.New("invalid datacenter filter"))
			}
		}
	}
	if o.demo && (o.prometheus != "" || o.netbox != "") {
		return usage(errors.New("--demo cannot be combined with live endpoints"))
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), o.timeout)
	defer cancel()
	prom, netbox := o.prometheus, o.netbox
	if !o.demo {
		if prom == "" {
			prom = os.Getenv("LOCALHOST_PROMETHEUS_URL")
		}
		if netbox == "" {
			netbox = os.Getenv("LOCALHOST_NETBOX_URL")
		}
		if view == "inventory" && netbox == "" || (view == "fleet" || view == "gpu") && prom == "" || view == "summary" && prom == "" && netbox == "" {
			return usage(errors.New("select an approved source URL or use --demo"))
		}
	}
	filters := metrics.Filters{Site: o.site, Rack: o.rack, Host: o.host}
	invFilters := inventory.Filters{Site: o.site, Rack: o.rack, Host: o.host}
	fleet := func() (metrics.Fleet, error) {
		if o.demo {
			return demoFleet(filters), nil
		}
		return metrics.FetchFleet(ctx, metrics.Access{URL: prom, Token: os.Getenv("LOCALHOST_PROMETHEUS_TOKEN")}, filters)
	}
	gpu := func() (metrics.GPUFleet, error) {
		if o.demo {
			return demoGPU(filters), nil
		}
		return metrics.FetchGPU(ctx, metrics.Access{URL: prom, Token: os.Getenv("LOCALHOST_PROMETHEUS_TOKEN")}, filters)
	}
	inv := func() (inventory.Snapshot, error) {
		if o.demo {
			return inventory.Demo(invFilters, o.details)
		}
		return inventory.Fetch(ctx, inventory.Access{URL: netbox, Token: os.Getenv("LOCALHOST_NETBOX_TOKEN")}, invFilters, o.details)
	}
	switch view {
	case "fleet":
		data, err := fleet()
		if err != nil {
			return dataError(err)
		}
		return writeData(cmd, data, o.json, func(w io.Writer) { writeFleet(w, data) })
	case "gpu":
		data, err := gpu()
		if err != nil {
			return dataError(err)
		}
		return writeData(cmd, data, o.json, func(w io.Writer) { writeGPU(w, data) })
	case "inventory":
		data, err := inv()
		if err != nil {
			return dataError(err)
		}
		return writeData(cmd, data, o.json, func(w io.Writer) { writeInventory(w, data) })
	}
	report := datacenterReport{DataMode: "live", GeneratedAt: time.Now().UTC(), Notice: "Read-only source evidence. Missing data is unknown. Hardware labels and NetBox names do not establish an automatic identity join."}
	if o.demo {
		report.DataMode = "demo"
		report.Notice = "STATIC DEMO: invented values; no source requests or credential reads."
	}
	var failed bool
	if prom != "" || o.demo {
		data, err := fleet()
		report.Fleet = makeSection(data, err)
		failed = failed || err != nil
		g, err := gpu()
		report.GPU = makeSection(g, err)
		failed = failed || err != nil
	}
	if netbox != "" || o.demo {
		data, err := inv()
		report.Inventory = makeSection(data, err)
		failed = failed || err != nil
	}
	if err := writeData(cmd, report, o.json, func(w io.Writer) {
		_, _ = fmt.Fprintf(w, "data_mode=%s  %s\n", report.DataMode, report.Notice)
		if report.Fleet.Data != nil {
			writeFleet(w, *report.Fleet.Data)
		} else {
			writeSectionState(w, "fleet", report.Fleet.Configured, report.Fleet.Error)
		}
		if report.GPU.Data != nil {
			writeGPU(w, *report.GPU.Data)
		} else {
			writeSectionState(w, "gpu", report.GPU.Configured, report.GPU.Error)
		}
		if report.Inventory.Data != nil {
			writeInventory(w, *report.Inventory.Data)
		} else {
			writeSectionState(w, "inventory", report.Inventory.Configured, report.Inventory.Error)
		}
	}); err != nil {
		return err
	}
	if failed {
		if ctx.Err() != nil {
			return dataError(ctx.Err())
		}
		return errors.New("one or more configured datacenter sources failed; report contains explicit failure state")
	}
	return nil
}
func makeSection[T any](data T, err error) dataSection[T] {
	s := dataSection[T]{Configured: true, Success: err == nil}
	if err != nil {
		s.Error = err.Error()
	} else {
		s.Data = &data
	}
	return s
}
func dataError(err error) error {
	if errors.Is(err, readapi.ErrConfig) {
		return usage(err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("collection deadline elapsed: %w", tools.ErrTimeout)
	}
	return err
}
func writeSectionState(w io.Writer, name string, configured bool, message string) {
	if !configured {
		message = "unconfigured (unknown)"
	}
	_, _ = fmt.Fprintf(w, "%s: %s\n", name, message)
}
func number(v *float64) string {
	if v == nil {
		return "unknown"
	}
	return fmt.Sprintf("%.2f", *v)
}
func truth(v *bool) string {
	if v == nil {
		return "unknown"
	}
	if *v {
		return "up"
	}
	return "down"
}
func textOrUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
func writeFleet(w io.Writer, data metrics.Fleet) {
	_, _ = fmt.Fprintf(w, "Fleet [%s], collected %s; missing values are unknown\n", data.DataMode, data.CollectedAt.Format(time.RFC3339))
	t := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(t, "SITE\tRACK\tHOST\tCPU%\tRAM%\tROOTFS%\tRX B/s\tTX B/s\tSOURCE\tAGE s\tFRESHNESS")
	for _, r := range data.Rows {
		_, _ = fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", textOrUnknown(r.Site), textOrUnknown(r.Rack), textOrUnknown(r.Host), number(r.CPUPercent), number(r.RAMUsedPercent), number(r.RootFilesystemUsedPercent), number(r.NetworkRXBytesPerSecond), number(r.NetworkTXBytesPerSecond), truth(r.Up), number(r.SampleAgeSeconds), r.Freshness)
	}
	_ = t.Flush()
	if len(data.Rows) == 0 {
		_, _ = fmt.Fprintln(w, "No matching host observations (unavailable; not zero utilization).")
	}
}
func writeGPU(w io.Writer, data metrics.GPUFleet) {
	_, _ = fmt.Fprintf(w, "GPU [%s], collected %s; classification is not measured workload ownership\n", data.DataMode, data.CollectedAt.Format(time.RFC3339))
	t := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(t, "SITE\tRACK\tHOST\tGPU\tUTIL%\tUSED B\tFREE B\tTEMP C\tPOWER W\tSOURCE\tAGE s\tFRESHNESS")
	for _, r := range data.Rows {
		_, _ = fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", textOrUnknown(r.Site), textOrUnknown(r.Rack), textOrUnknown(r.Host), textOrUnknown(r.GPU), number(r.UtilizationPercent), number(r.FramebufferUsedBytes), number(r.FramebufferFreeBytes), number(r.TemperatureCelsius), number(r.PowerWatts), truth(r.Up), number(r.SampleAgeSeconds), r.Freshness)
	}
	_ = t.Flush()
	if len(data.Rows) == 0 {
		_, _ = fmt.Fprintln(w, "No matching GPU observations (unavailable; not idle capacity).")
	}
}
func writeInventory(w io.Writer, data inventory.Snapshot) {
	_, _ = fmt.Fprintf(w, "Inventory [%s], collected %s: %d selected / %d source records; %d sites, %d racks, %d owners\n%s\n", data.DataMode, data.CollectedAt.Format(time.RFC3339), data.Devices, data.SourceDevices, data.Sites, data.Racks, data.Owners, data.Coverage)
	for _, key := range []string{"site", "rack", "owner"} {
		_, _ = fmt.Fprintf(w, "unassigned_%s=%d ", key, data.Unassigned[key])
	}
	_, _ = fmt.Fprintln(w)
	statuses := make([]string, 0, len(data.Lifecycle))
	for key := range data.Lifecycle {
		statuses = append(statuses, key)
	}
	sort.Strings(statuses)
	for _, key := range statuses {
		_, _ = fmt.Fprintf(w, "%s=%d ", key, data.Lifecycle[key])
	}
	_, _ = fmt.Fprintln(w)
	if len(data.Rows) == 0 {
		return
	}
	t := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(t, "ID\tDEVICE\tSITE\tRACK\tOWNER\tROLE\tLIFECYCLE")
	for _, r := range data.Rows {
		_, _ = fmt.Fprintf(t, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, textOrUnknown(r.Name), textOrUnknown(r.Site), textOrUnknown(r.Rack), textOrUnknown(r.Owner), textOrUnknown(r.Role), r.Status)
	}
	_ = t.Flush()
}
func f64(v float64) *float64 { return &v }
func demoFleet(f metrics.Filters) metrics.Fleet {
	out := metrics.Fleet{DataMode: "demo", CollectedAt: time.Now().UTC(), Rows: []metrics.Host{}, SourceHealth: []metrics.SourceHealth{}}
	for _, r := range []metrics.Host{{Site: "demo-east", Rack: "demo-a", Host: "demo-cpu-01", CPUPercent: f64(38), RAMUsedPercent: f64(61), RootFilesystemUsedPercent: f64(50), NetworkRXBytesPerSecond: f64(18874368), NetworkTXBytesPerSecond: f64(9437184), Freshness: "static-demo"}, {Site: "demo-east", Rack: "demo-a", Host: "demo-gpu-01", CPUPercent: f64(64), RAMUsedPercent: f64(63), RootFilesystemUsedPercent: f64(62), Freshness: "static-demo"}, {Site: "demo-west", Rack: "demo-b", Host: "demo-gpu-02", CPUPercent: f64(22), RAMUsedPercent: f64(25), RootFilesystemUsedPercent: f64(25), Freshness: "static-demo"}} {
		if (f.Site == "" || f.Site == r.Site) && (f.Rack == "" || f.Rack == r.Rack) && (f.Host == "" || f.Host == r.Host) {
			out.Rows = append(out.Rows, r)
		}
	}
	return out
}
func demoGPU(f metrics.Filters) metrics.GPUFleet {
	out := metrics.GPUFleet{DataMode: "demo", CollectedAt: time.Now().UTC(), Rows: []metrics.GPU{}, SourceHealth: []metrics.SourceHealth{}}
	for _, r := range []metrics.GPU{{Site: "demo-east", Rack: "demo-a", Host: "demo-gpu-01", GPU: "0", UtilizationPercent: f64(78), FramebufferUsedBytes: f64(49152 * 1048576), FramebufferFreeBytes: f64(32768 * 1048576), TemperatureCelsius: f64(69), PowerWatts: f64(345), Freshness: "static-demo"}, {Site: "demo-west", Rack: "demo-b", Host: "demo-gpu-02", GPU: "0", UtilizationPercent: f64(0), FramebufferUsedBytes: f64(0), FramebufferFreeBytes: f64(81920 * 1048576), TemperatureCelsius: f64(31), PowerWatts: f64(52), Freshness: "static-demo"}} {
		if (f.Site == "" || f.Site == r.Site) && (f.Rack == "" || f.Rack == r.Rack) && (f.Host == "" || f.Host == r.Host) {
			out.Rows = append(out.Rows, r)
		}
	}
	return out
}

// Serialize before the first write so validation/size failures emit no partial
// data. Format controls are escaped without changing JSON string semantics.
func writeData(cmd *cobra.Command, data any, asJSON bool, table func(io.Writer)) error {
	var output []byte
	if asJSON {
		encoded, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return errors.New("cannot encode bounded result")
		}
		var b strings.Builder
		for _, r := range string(encoded) {
			if unicode.In(r, unicode.Cf) {
				if r > 0xffff {
					a, z := utf16.EncodeRune(r)
					_, _ = fmt.Fprintf(&b, "\\u%04x\\u%04x", a, z)
				} else {
					_, _ = fmt.Fprintf(&b, "\\u%04x", r)
				}
			} else {
				b.WriteRune(r)
			}
		}
		output = append([]byte(b.String()), '\n')
	} else {
		var b bytes.Buffer
		table(&b)
		output = b.Bytes()
	}
	if len(output) > 1<<20 {
		return errors.New("result exceeds 1 MiB; narrow the selected cohort")
	}
	n, err := cmd.OutOrStdout().Write(output)
	if err == nil && n != len(output) {
		return io.ErrShortWrite
	}
	return err
}
