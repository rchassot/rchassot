package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
)

//go:embed web/index.html
var indexHTML []byte

type incidentDTO struct {
	Start       string `json:"start"`
	End         string `json:"end"`
	Duration    string `json:"duration"`
	Ongoing     bool   `json:"ongoing"`
	Level       int    `json:"level"`
	Title       string `json:"title"`
	Explanation string `json:"explanation"`
	Advice      string `json:"advice"`
	Detail      string `json:"detail"`
}

type linkDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	State    string `json:"state"` // ok, warn, down, unknown, na
	Cuts     int    `json:"cuts"`
	CutTime  string `json:"cutTime"`
	Warnings int    `json:"warnings"`
	LastCut  string `json:"lastCut"`
	Timeline []int  `json:"timeline"`
}

type statusDTO struct {
	Level        int               `json:"level"`
	Cause        string            `json:"cause"`
	Title        string            `json:"title"`
	Explanation  string            `json:"explanation"`
	Advice       string            `json:"advice"`
	Detail       string            `json:"detail"`
	Nodes        map[string]string `json:"nodes"`
	Links        []linkDTO         `json:"links"`
	Elapsed      string            `json:"elapsed"`
	Outages      int               `json:"outages"`
	OutageTime   string            `json:"outageTime"`
	Warnings     int               `json:"warnings"`
	Availability float64           `json:"availability"`
	LatencyMs    float64           `json:"latencyMs"`
	Speed        string            `json:"speed"`
	LossPct      float64           `json:"lossPct"`
	Timeline     []int             `json:"timeline"`
	Incidents    []incidentDTO     `json:"incidents"`
	Tech         map[string]string `json:"tech"`
	ReportPath   string            `json:"reportPath"`
	Demo         bool              `json:"demo"`
}

// nodeStates donne l'état de chaque maillon : ok, down, warn ou unknown (grisé).
func nodeStates(s Snapshot) map[string]string {
	n := map[string]string{"pc": "unknown", "box": "unknown", "internet": "unknown", "web": "unknown"}
	if !s.HaveLast {
		return n
	}
	r := s.Last
	switch {
	case r.Gateway != "" || r.Net.OK:
		n["pc"] = "ok"
	default:
		n["pc"] = "down"
		return n
	}
	switch {
	case r.Box.OK || r.Net.OK:
		n["box"] = "ok" // si Internet passe, la box fonctionne forcément
	case s.BoxEverOK:
		n["box"] = "down"
		return n
	}
	if !r.Net.OK {
		n["internet"] = "down"
		return n
	}
	n["internet"] = "ok"
	switch {
	case !r.DNS.OK:
		n["web"] = "down"
	case s.Level == LevelWarn && s.Cause == "web":
		n["web"] = "warn"
	default:
		n["web"] = "ok"
	}
	if s.Level == LevelWarn && (s.Cause == "slow" || s.Cause == "unstable") {
		n["internet"] = "warn"
	}
	return n
}

func buildStatus(s Snapshot, reportPath string, demo bool) statusDTO {
	st := s.Stats()
	ct := causeText(s.Cause)
	d := statusDTO{
		Level: int(s.Level), Cause: s.Cause,
		Title: ct.Title, Explanation: ct.Explanation, Advice: ct.Advice, Detail: s.Detail,
		Nodes:   nodeStates(s),
		Elapsed: fmtDuration(st.Elapsed), Outages: st.Outages, OutageTime: fmtDuration(st.OutageTime),
		Warnings: st.Warnings, Availability: st.Availability,
		LatencyMs: s.MedianMs, Speed: speedWord(s.MedianMs), LossPct: s.LossPct,
		Timeline:   s.Timeline(60),
		Incidents:  []incidentDTO{},
		Tech:       map[string]string{},
		ReportPath: reportPath, Demo: demo,
	}
	d.Links = buildLinks(s)
	for i := len(s.Incidents) - 1; i >= 0; i-- {
		in := s.Incidents[i]
		t := causeText(in.Cause)
		end := s.Now
		if !in.Ongoing {
			end = in.End
		}
		d.Incidents = append(d.Incidents, incidentDTO{
			Start: fmtWhen(in.Start, s.Now), End: fmtWhen(in.End, s.Now),
			Duration: fmtDuration(end.Sub(in.Start)), Ongoing: in.Ongoing, Level: int(in.Level),
			Title: t.Title, Explanation: t.Explanation, Advice: t.Advice, Detail: in.Detail,
		})
	}
	if s.HaveLast {
		r := s.Last
		d.Tech["Adresse de la box"] = orDash(r.Gateway)
		if r.Wifi != nil {
			d.Tech["Connexion"] = fmt.Sprintf("Wi-Fi « %s », signal %d %%", r.Wifi.SSID, r.Wifi.Signal)
		} else {
			d.Tech["Connexion"] = "Câble (ou Wi-Fi non détecté)"
		}
		d.Tech["Réponse de la box"] = fmtCheck(r.Box)
		d.Tech["Réponse d'Internet"] = fmtCheck(r.Net)
		d.Tech["Annuaire (DNS)"] = fmtCheck(r.DNS)
		d.Tech["Page web de test"] = fmtCheck(r.Web)
		d.Tech["Pertes (2 dernières min)"] = fmt.Sprintf("%.0f %%", s.LossPct)
	}
	if h, err := os.Hostname(); err == nil {
		d.Tech["Ordinateur"] = h
	}
	return d
}

var linkStateNames = map[int]string{int(LevelOK): "ok", int(LevelWarn): "warn", int(LevelDown): "down", LinkUnknown: "unknown"}

func buildLinks(s Snapshot) []linkDTO {
	var out []linkDTO
	for _, l := range links {
		st := s.LinkStats(l.ID)
		d := linkDTO{
			ID: l.ID, Name: l.Name, State: "na",
			Cuts: st.Cuts, CutTime: fmtDuration(st.CutTime), Warnings: st.Warnings,
			Timeline: s.LinkTimeline(l.ID, 60),
		}
		if v, measured := linkState(s.Cause, l.ID); measured && s.HaveLast {
			d.State = linkStateNames[v]
		}
		if st.LastCut != nil {
			d.LastCut = fmtWhen(st.LastCut.Start, s.Now)
		}
		out = append(out, d)
	}
	return out
}

func fmtCheck(c Check) string {
	if !c.OK {
		return "pas de réponse"
	}
	if c.Ms < 1 {
		return "OK (< 1 ms)"
	}
	return fmt.Sprintf("OK (%.0f ms)", c.Ms)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func newHandler(m *Monitor, rep *reporter, demo bool, quit func()) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, appID)
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(buildStatus(m.Snapshot(), rep.Path(), demo))
	})
	mux.HandleFunc("GET /rapport", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(buildReport(m.Snapshot()))
	})
	mux.HandleFunc("POST /api/quit", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
		go quit()
	})
	return localOnly(mux)
}

// localOnly refuse les requêtes qui ne visent pas explicitement la machine
// locale (protection contre les sites malveillants qui tenteraient d'y accéder).
func localOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" {
			http.Error(w, "accès refusé", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			if o := r.Header.Get("Origin"); o != "" && !strings.HasPrefix(o, "http://127.0.0.1:") && !strings.HasPrefix(o, "http://localhost:") {
				http.Error(w, "accès refusé", http.StatusForbidden)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}
