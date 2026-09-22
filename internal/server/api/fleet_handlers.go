package api

// The fleet: the machines this project talks to and nobody measures. One agent sees its
// neighbours through the live connections of its own machine, so installing the first one is
// enough to know the others exist. The list is plain text on purpose: what reads it is a shell
// script on somebody's laptop, with their ssh keys, and no JSON reader.

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/topology"
)

func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request, p project.Project) {
	g, err := topology.Build(r.Context(), s.DB, p.ID, time.Now())
	if err != nil {
		serverError(w, err)
		return
	}
	type machine struct{ address, name, services string }
	byAddress := map[string]*machine{}
	for _, n := range g.Nodes {
		if n.Monitored || n.Kind == "internet" || n.Kind == "cdn" || n.LocalOnly {
			continue
		}
		for _, addr := range n.Addresses {
			if addr == "" {
				continue
			}
			m := byAddress[addr]
			if m == nil {
				m = &machine{address: addr, name: n.Name}
				byAddress[addr] = m
			}
			if n.Service != "" && !strings.Contains(m.services, n.Service) {
				if m.services != "" {
					m.services += ","
				}
				m.services += n.Service
			}
		}
	}
	list := make([]*machine, 0, len(byAddress))
	for _, m := range byAddress {
		list = append(list, m)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].address < list[j].address })

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, m := range list {
		services := m.services
		if services == "" {
			services = "-"
		}
		// address <tab> name <tab> what it answers: one machine per line, nothing to parse.
		_, _ = w.Write([]byte(m.address + "\t" + m.name + "\t" + services + "\n"))
	}
}
