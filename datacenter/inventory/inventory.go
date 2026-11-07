// Package inventory reads an explicitly selected, bounded NetBox device cohort.
package inventory

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/alikatgh/le-cli/internal/readapi"
)

const MaxDevices = 512

type Access struct{ URL, Token string }
type Filters struct{ Site, Rack, Host string }
type Device struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Site   string `json:"site"`
	Rack   string `json:"rack"`
	Owner  string `json:"owner"`
	Role   string `json:"role"`
	Status string `json:"status"`
}
type Snapshot struct {
	DataMode      string         `json:"data_mode"`
	CollectedAt   time.Time      `json:"collected_at"`
	SourceDevices int            `json:"source_devices"`
	Devices       int            `json:"devices"`
	Sites         int            `json:"sites"`
	Racks         int            `json:"racks"`
	Owners        int            `json:"owners"`
	Unassigned    map[string]int `json:"unassigned"`
	Lifecycle     map[string]int `json:"lifecycle"`
	Rows          []Device       `json:"rows,omitempty"`
	Coverage      string         `json:"coverage"`
}
type association struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}
type record struct {
	ID     int64        `json:"id"`
	Name   string       `json:"name"`
	Site   *association `json:"site"`
	Rack   *association `json:"rack"`
	Tenant *association `json:"tenant"`
	Role   *association `json:"role"`
	Status struct {
		Value string `json:"value"`
	} `json:"status"`
}
type page struct {
	Count   *int     `json:"count"`
	Results []record `json:"results"`
}

// Fetch performs only root-origin GETs. It never follows a server's next URL.
func Fetch(ctx context.Context, access Access, filters Filters, details bool) (Snapshot, error) {
	if access.Token == "" {
		return Snapshot{}, readapi.ErrConfig
	}
	for _, s := range []string{filters.Site, filters.Rack, filters.Host} {
		if !validText(s) {
			return Snapshot{}, readapi.ErrConfig
		}
	}
	scheme := "Token "
	if strings.HasPrefix(access.Token, "nbt_") {
		scheme = "Bearer "
	}
	client, err := readapi.New(access.URL, scheme+access.Token, true)
	if err != nil {
		return Snapshot{}, err
	}
	defer client.Close()
	var records []record
	seen := make(map[int64]bool)
	total := -1
	for offset := 0; offset < MaxDevices; offset += 64 {
		var response page
		if err := client.GetJSON(ctx, "/api/dcim/devices/", url.Values{"limit": {"64"}, "offset": {strconv.Itoa(offset)}}, &response); err != nil {
			return Snapshot{}, err
		}
		if response.Count == nil || *response.Count < 0 {
			return Snapshot{}, readapi.ErrResponse
		}
		if *response.Count > MaxDevices {
			return Snapshot{}, readapi.ErrLimit
		}
		if total < 0 {
			total = *response.Count
		}
		if total != *response.Count || len(response.Results) > 64 || len(records)+len(response.Results) > total {
			return Snapshot{}, readapi.ErrResponse
		}
		if len(response.Results) == 0 && len(records) != total {
			return Snapshot{}, readapi.ErrResponse
		}
		for _, row := range response.Results {
			if row.ID <= 0 || seen[row.ID] || !validRecord(row) {
				return Snapshot{}, readapi.ErrResponse
			}
			seen[row.ID] = true
			records = append(records, row)
		}
		if len(records) == total {
			return summarize(records, filters, details, "live", time.Now().UTC()), nil
		}
		if len(response.Results) != 64 {
			return Snapshot{}, readapi.ErrResponse
		}
	}
	return Snapshot{}, readapi.ErrLimit
}

func validText(s string) bool {
	if len(s) > 96 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return false
		}
	}
	return true
}
func validRecord(r record) bool {
	if !validText(r.Name) || !validText(r.Status.Value) {
		return false
	}
	for _, a := range []*association{r.Site, r.Rack, r.Tenant, r.Role} {
		if a != nil && (a.ID <= 0 || !validText(a.Name)) {
			return false
		}
	}
	return true
}
func text(a *association) string {
	if a == nil {
		return ""
	}
	return a.Name
}
func matches(want, got string) bool { return want == "" || want == got }
func summarize(records []record, f Filters, details bool, mode string, now time.Time) Snapshot {
	s := Snapshot{DataMode: mode, CollectedAt: now, SourceDevices: len(records), Unassigned: map[string]int{"site": 0, "rack": 0, "owner": 0}, Lifecycle: map[string]int{}, Coverage: "NetBox device records only; lifecycle and ownership are recorded context, not physical verification. Collection time is the read time, not the age of each source record."}
	sites, racks, owners := map[int64]bool{}, map[int64]bool{}, map[int64]bool{}
	for _, r := range records {
		if !matches(f.Site, text(r.Site)) || !matches(f.Rack, text(r.Rack)) || !matches(f.Host, r.Name) {
			continue
		}
		s.Devices++
		if r.Site != nil {
			sites[r.Site.ID] = true
		} else {
			s.Unassigned["site"]++
		}
		if r.Rack != nil {
			racks[r.Rack.ID] = true
		} else {
			s.Unassigned["rack"]++
		}
		if r.Tenant != nil {
			owners[r.Tenant.ID] = true
		} else {
			s.Unassigned["owner"]++
		}
		status := r.Status.Value
		switch status {
		case "offline", "active", "planned", "staged", "failed", "inventory", "decommissioning":
		default:
			status = "unknown"
		}
		s.Lifecycle[status]++
		if details {
			s.Rows = append(s.Rows, Device{ID: r.ID, Name: r.Name, Site: text(r.Site), Rack: text(r.Rack), Owner: text(r.Tenant), Role: text(r.Role), Status: status})
		}
	}
	s.Sites, s.Racks, s.Owners = len(sites), len(racks), len(owners)
	sort.Slice(s.Rows, func(i, j int) bool { return s.Rows[i].ID < s.Rows[j].ID })
	return s
}

// Demo is a labelled offline fixture and never reads credentials or the network.
func Demo(filters Filters, details bool) (Snapshot, error) {
	for _, s := range []string{filters.Site, filters.Rack, filters.Host} {
		if !validText(s) {
			return Snapshot{}, errors.New("invalid inventory filter")
		}
	}
	rows := []record{
		{ID: 1, Name: "demo-cpu-01", Site: &association{ID: 1, Name: "demo-east"}, Rack: &association{ID: 1, Name: "demo-a"}, Tenant: &association{ID: 1, Name: "demo-owner"}, Role: &association{ID: 1, Name: "compute"}},
		{ID: 2, Name: "demo-gpu-01", Site: &association{ID: 1, Name: "demo-east"}, Rack: &association{ID: 1, Name: "demo-a"}, Tenant: &association{ID: 1, Name: "demo-owner"}, Role: &association{ID: 2, Name: "gpu"}},
		{ID: 3, Name: "demo-gpu-02", Site: &association{ID: 2, Name: "demo-west"}, Rack: &association{ID: 2, Name: "demo-b"}, Role: &association{ID: 2, Name: "gpu"}},
	}
	for i := range rows {
		rows[i].Status.Value = "active"
	}
	s := summarize(rows, filters, details, "demo", time.Now().UTC())
	s.Coverage = fmt.Sprintf("STATIC DEMO: %d invented device records; not live hardware or customer inventory.", len(rows))
	return s, nil
}
