package metrics

import (
	"strconv"
	"strings"
)

const groupLabels = "data_mode,site,rack,host,Hostname,instance,job"
const gpuGroupLabels = groupLabels + ",gpu,UUID,modelName"

func selector(name string, f Filters, extra string) string {
	s := name + `{data_mode=~"live|"`
	if f.Site != "" {
		s += ",site=" + strconv.Quote(f.Site)
	}
	if f.Rack != "" {
		s += ",rack=" + strconv.Quote(f.Rack)
	}
	return s + extra + "}"
}

func tagged(expr, tag, field string) string {
	return `label_replace((` + expr + `),"` + tag + `","` + field + `","__name__",".*")`
}

type queryPair struct{ values, timestamps string }

func queries(f Filters, gpu bool) queryPair {
	var values, stamps []string
	add := func(field, expr string) { values = append(values, tagged(expr, "__le_field", field)) }
	raw := func(field, name, extra string) {
		group := groupLabels
		if gpu && name != "up" {
			group = gpuGroupLabels
		}
		expr := "min by (" + group + ") (timestamp(" + selector(name, f, extra) + "))"
		stamps = append(stamps, tagged(expr, "__le_raw", field))
	}
	if gpu {
		for _, it := range []struct{ field, name string }{{"util", "DCGM_FI_DEV_GPU_UTIL"}, {"fb_used", "DCGM_FI_DEV_FB_USED"}, {"fb_free", "DCGM_FI_DEV_FB_FREE"}, {"temperature", "DCGM_FI_DEV_GPU_TEMP"}, {"power", "DCGM_FI_DEV_POWER_USAGE"}} {
			add(it.field, selector(it.name, f, ""))
			raw(it.field, it.name, "")
		}
	} else {
		add("cpu", "100 * (1 - avg by ("+groupLabels+") (rate("+selector("node_cpu_seconds_total", f, `,mode="idle"`)+"[5m])))")
		add("ram", "100 * (1 - "+selector("node_memory_MemAvailable_bytes", f, "")+" / "+selector("node_memory_MemTotal_bytes", f, "")+")")
		add("filesystem", "100 * (1 - "+selector("node_filesystem_avail_bytes", f, `,mountpoint="/"`)+" / "+selector("node_filesystem_size_bytes", f, `,mountpoint="/"`)+")")
		add("rx", "sum by ("+groupLabels+") (rate("+selector("node_network_receive_bytes_total", f, `,device!="lo"`)+"[5m]))")
		add("tx", "sum by ("+groupLabels+") (rate("+selector("node_network_transmit_bytes_total", f, `,device!="lo"`)+"[5m]))")
		for _, it := range []struct{ field, name, extra string }{{"cpu", "node_cpu_seconds_total", `,mode="idle"`}, {"ram_available", "node_memory_MemAvailable_bytes", ""}, {"ram_total", "node_memory_MemTotal_bytes", ""}, {"filesystem_available", "node_filesystem_avail_bytes", `,mountpoint="/"`}, {"filesystem_size", "node_filesystem_size_bytes", `,mountpoint="/"`}, {"rx", "node_network_receive_bytes_total", `,device!="lo"`}, {"tx", "node_network_transmit_bytes_total", `,device!="lo"`}} {
			raw(it.field, it.name, it.extra)
		}
	}
	add("up", selector("up", f, ""))
	raw("up", "up", "")
	return queryPair{strings.Join(values, " or "), strings.Join(stamps, " or ")}
}
