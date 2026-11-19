package agent

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// ParseStat reads the `show stat` CSV of one HAProxy instance rendered by
// HAProxyConfig. It returns the health of every server row (target key and
// the listen port of its backend "be<port>"; first status token UP is
// healthy, DOWN unhealthy, anything else or a check status INI or UNK
// unknown) and the counters summed over the frontends.
func ParseStat(data []byte) ([]agentapi.TargetHealth, agentapi.LBCounters, error) {
	var counters agentapi.LBCounters
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	header, err := r.Read()
	if err != nil {
		return nil, counters, fmt.Errorf("show stat: header: %w", err)
	}
	if len(header) == 0 || !strings.HasPrefix(header[0], "# ") {
		return nil, counters, errors.New("show stat: no header line")
	}
	header[0] = strings.TrimPrefix(header[0], "# ")
	col := map[string]int{}
	for i, name := range header {
		col[name] = i
	}
	for _, name := range []string{"pxname", "svname", "scur", "stot", "bin", "bout", "status", "type", "check_status", "req_tot"} {
		if _, ok := col[name]; !ok {
			return nil, counters, fmt.Errorf("show stat: no column %s", name)
		}
	}

	var health []agentapi.TargetHealth
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, counters, fmt.Errorf("show stat: %w", err)
		}
		field := func(name string) string {
			if i := col[name]; i < len(rec) {
				return rec[i]
			}
			return ""
		}
		num := func(name string) uint64 {
			n, _ := strconv.ParseUint(field(name), 10, 64)
			return n
		}
		switch field("type") {
		case "0":
			counters.OpenConnections += num("scur")
			counters.ConnectionsTotal += num("stot")
			counters.RequestsTotal += num("req_tot")
			counters.BytesIn += num("bin")
			counters.BytesOut += num("bout")
		case "2":
			port, err := strconv.Atoi(strings.TrimPrefix(field("pxname"), "be"))
			if err != nil || !strings.HasPrefix(field("pxname"), "be") {
				continue
			}
			health = append(health, agentapi.TargetHealth{
				Key: field("svname"), ListenPort: port, Status: serverHealth(field("status"), field("check_status")),
			})
		}
	}
	return health, counters, nil
}

// serverHealth maps a server's status and check status to the Hetzner
// health status.
func serverHealth(status, check string) string {
	if check == "INI" || check == "UNK" {
		return "unknown"
	}
	first, _, _ := strings.Cut(strings.TrimSpace(status), " ")
	switch first {
	case "UP":
		return "healthy"
	case "DOWN":
		return "unhealthy"
	}
	return "unknown"
}
