package main

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Level est l'état global de la connexion, du meilleur au pire.
type Level int

const (
	LevelOK   Level = 0
	LevelWarn Level = 1
	LevelDown Level = 2
)

const (
	weakWifiSignal  = 40               // en %, en dessous le Wi-Fi est considéré comme faible
	slowMs          = 150              // temps de réponse médian au-delà duquel c'est « lent »
	unstableLossPct = 10               // pertes au-delà desquelles c'est « instable »
	mergeGap        = 30 * time.Second // deux problèmes identiques rapprochés = un seul
	maxSamples      = 200000           // ~11 jours à une mesure toutes les 5 s
)

// Check est le résultat d'un test unitaire.
type Check struct {
	OK bool
	Ms float64
}

// Target est le résultat d'un ping vers un serveur Internet.
type Target struct {
	Addr string
	Check
}

// WifiInfo décrit la connexion Wi-Fi en cours, si l'ordinateur en utilise une.
type WifiInfo struct {
	SSID   string
	Signal int
}

// Round regroupe tous les tests effectués à un instant donné.
type Round struct {
	Time    time.Time
	Gateway string   // adresse de la box ("" = aucun réseau)
	Box     Check    // ping de la box
	Targets []Target // pings des serveurs Internet
	Net     Check    // Internet joignable (ping ou connexion TCP)
	DNS     Check    // l'annuaire d'Internet répond
	Web     Check    // une page web se charge
	Wifi    *WifiInfo
	Devices map[string]Check // résultat par appareil surveillé (clé : Device.ID)
}

// Sample est ce qu'on garde de chaque tour pour l'historique et le graphique.
type Sample struct {
	T     time.Time
	Level Level
	Cause string
	NetMs float64 // -1 si Internet n'a pas répondu
}

// Incident est une période continue pendant laquelle un même problème a été observé.
type Incident struct {
	Start, End time.Time
	Ongoing    bool
	Level      Level
	Cause      string
	Detail     string
}

func (i Incident) Duration() time.Duration { return i.End.Sub(i.Start) }

// Prober effectue un tour de tests.
type Prober interface {
	Probe(ctx context.Context, devices []Device) Round
}

// Monitor lance les tests à intervalle régulier et en tire un diagnostic.
type Monitor struct {
	prober   Prober
	interval time.Duration

	mu        sync.Mutex
	started   time.Time
	last      Round
	haveLast  bool
	level     Level
	cause     string
	detail    string
	lossPct   float64
	medianMs  float64
	samples   []Sample
	incidents []*Incident
	boxEverOK bool
	seen      map[string]bool
	lossHist  []float64
	msHist    []float64
	webFails  int
	devices   []*deviceTrack
}

func NewMonitor(p Prober, interval time.Duration) *Monitor {
	return &Monitor{
		prober:   p,
		interval: interval,
		started:  time.Now(),
		cause:    "starting",
		medianMs: -1,
		seen:     map[string]bool{},
	}
}

func (m *Monitor) Run(ctx context.Context) {
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		m.Step(m.prober.Probe(ctx, m.Devices()))
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Step intègre un tour de tests : statistiques, diagnostic, journal des incidents.
func (m *Monitor) Step(r Round) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if r.Box.OK {
		m.boxEverOK = true
	}
	if r.Net.OK {
		// Les pertes ne sont comptées que sur les serveurs qui ont déjà répondu au
		// moins une fois : certains réseaux bloquent un serveur en permanence.
		tried, lost := 0, 0
		for _, t := range r.Targets {
			if m.seen[t.Addr] {
				tried++
				if !t.OK {
					lost++
				}
			}
		}
		for _, t := range r.Targets {
			if t.OK {
				m.seen[t.Addr] = true
			}
		}
		if tried > 0 {
			m.lossHist = pushCap(m.lossHist, float64(lost)/float64(tried), 24)
		}
		m.msHist = pushCap(m.msHist, r.Net.Ms, 12)
	}
	if r.Web.OK || !r.Net.OK || !r.DNS.OK {
		m.webFails = 0
	} else {
		m.webFails++
	}

	m.lossPct = mean(m.lossHist) * 100
	m.medianMs = median(m.msHist)
	level, cause, detail := diagnose(diagInput{
		Round:     r,
		BoxEverOK: m.boxEverOK,
		LossPct:   m.lossPct,
		MedianMs:  m.medianMs,
		WebFails:  m.webFails,
	})

	m.last, m.haveLast = r, true
	m.level, m.cause, m.detail = level, cause, detail

	netMs := -1.0
	if r.Net.OK {
		netMs = r.Net.Ms
	}
	m.samples = append(m.samples, Sample{T: r.Time, Level: level, Cause: cause, NetMs: netMs})
	if len(m.samples) > maxSamples {
		m.samples = append(m.samples[:0:0], m.samples[len(m.samples)-maxSamples:]...)
	}
	m.record(r.Time, level, cause, detail)
	m.stepDevices(r)
}

func (m *Monitor) record(t time.Time, level Level, cause, detail string) {
	recordIncident(&m.incidents, t, level, cause, detail)
}

// recordIncident met à jour un journal : prolonge l'incident en cours, le
// clôt, ou en ouvre un nouveau (en fusionnant avec un incident identique récent).
func recordIncident(list *[]*Incident, t time.Time, level Level, cause, detail string) {
	incidents := *list
	var cur *Incident
	if n := len(incidents); n > 0 && incidents[n-1].Ongoing {
		cur = incidents[n-1]
	}
	if level == LevelOK {
		if cur != nil {
			cur.End, cur.Ongoing = t, false
		}
		return
	}
	if cur != nil && cur.Cause == cause {
		cur.End, cur.Detail = t, detail
		return
	}
	if cur != nil {
		cur.End, cur.Ongoing = t, false
	}
	if n := len(incidents); n > 0 {
		prev := incidents[n-1]
		if prev.Cause == cause && t.Sub(prev.End) <= mergeGap {
			prev.End, prev.Ongoing, prev.Detail = t, true, detail
			return
		}
	}
	*list = append(incidents, &Incident{Start: t, End: t, Ongoing: true, Level: level, Cause: cause, Detail: detail})
}

type diagInput struct {
	Round     Round
	BoxEverOK bool
	LossPct   float64
	MedianMs  float64
	WebFails  int
}

// diagnose traduit les résultats bruts en un état et une cause compréhensibles.
// On remonte la chaîne ordinateur → box → Internet → sites web : le premier
// maillon qui ne répond pas indique où se trouve le problème.
func diagnose(in diagInput) (Level, string, string) {
	r := in.Round
	if !r.Net.OK {
		switch {
		case r.Gateway == "":
			return LevelDown, "no_network", ""
		case r.Box.OK:
			return LevelDown, "isp", ""
		case in.BoxEverOK:
			if r.Wifi != nil && r.Wifi.Signal < weakWifiSignal {
				return LevelDown, "box_wifi", fmt.Sprintf("signal Wi-Fi %d %%", r.Wifi.Signal)
			}
			return LevelDown, "box", ""
		default:
			// Certaines box ne répondent jamais au ping : impossible de trancher.
			return LevelDown, "net_unknown", ""
		}
	}
	if !r.DNS.OK {
		return LevelDown, "dns", ""
	}
	if in.WebFails >= 2 {
		return LevelWarn, "web", ""
	}
	if in.LossPct >= unstableLossPct {
		return LevelWarn, "unstable", fmt.Sprintf("%.0f %% de pertes", in.LossPct)
	}
	if in.MedianMs >= slowMs {
		return LevelWarn, "slow", fmt.Sprintf("temps de réponse %.0f ms", in.MedianMs)
	}
	return LevelOK, "ok", ""
}

// Snapshot est une copie cohérente de l'état, utilisable sans verrou.
type Snapshot struct {
	Now       time.Time
	Started   time.Time
	Interval  time.Duration
	HaveLast  bool
	Last      Round
	Level     Level
	Cause     string
	Detail    string
	LossPct   float64
	MedianMs  float64
	BoxEverOK bool
	Samples   []Sample
	Incidents []Incident
	Devices   []DeviceSnapshot
}

func (m *Monitor) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Snapshot{
		Now: time.Now(), Started: m.started, Interval: m.interval,
		HaveLast: m.haveLast, Last: m.last,
		Level: m.level, Cause: m.cause, Detail: m.detail,
		LossPct: m.lossPct, MedianMs: m.medianMs, BoxEverOK: m.boxEverOK,
		Samples: append([]Sample(nil), m.samples...),
	}
	for _, i := range m.incidents {
		s.Incidents = append(s.Incidents, *i)
	}
	s.Devices = m.snapshotDevices()
	return s
}

// Stats résume la session.
type Stats struct {
	Elapsed      time.Duration
	Outages      int
	OutageTime   time.Duration
	Warnings     int
	Availability float64 // en %, -1 si pas encore de mesure
	MedianMs     float64 // -1 si inconnu
	MainCause    string  // cause ayant provoqué le plus de temps de coupure
}

func (s Snapshot) Stats() Stats {
	st := Stats{Elapsed: s.Now.Sub(s.Started), Availability: -1, MedianMs: -1}
	byCause := map[string]time.Duration{}
	for _, i := range s.Incidents {
		if i.Level == LevelDown {
			st.Outages++
			st.OutageTime += i.Duration()
			byCause[i.Cause] += i.Duration()
		} else {
			st.Warnings++
		}
	}
	var best time.Duration
	for c, d := range byCause {
		if d > best || (d == best && c < st.MainCause) {
			best, st.MainCause = d, c
		}
	}
	if n := len(s.Samples); n > 0 {
		up := 0
		var ms []float64
		for _, x := range s.Samples {
			if x.Level != LevelDown {
				up++
			}
			if x.NetMs >= 0 {
				ms = append(ms, x.NetMs)
			}
		}
		st.Availability = 100 * float64(up) / float64(n)
		st.MedianMs = median(ms)
	}
	return st
}

// Timeline renvoie le pire état de chacune des `minutes` dernières minutes
// (-1 = pas de mesure).
func (s Snapshot) Timeline(minutes int) []int {
	out := make([]int, minutes)
	for i := range out {
		out[i] = -1
	}
	end := s.Now.Truncate(time.Minute).Add(time.Minute)
	for i := len(s.Samples) - 1; i >= 0; i-- {
		x := s.Samples[i]
		idx := minutes - 1 - int(end.Sub(x.T)/time.Minute)
		if idx < 0 {
			break
		}
		if idx < minutes && int(x.Level) > out[idx] {
			out[idx] = int(x.Level)
		}
	}
	return out
}

func pushCap(xs []float64, v float64, n int) []float64 {
	xs = append(xs, v)
	if len(xs) > n {
		xs = xs[len(xs)-n:]
	}
	return xs
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	t := 0.0
	for _, x := range xs {
		t += x
	}
	return t / float64(len(xs))
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return -1
	}
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	if len(c)%2 == 1 {
		return c[len(c)/2]
	}
	return (c[len(c)/2-1] + c[len(c)/2]) / 2
}

// Liaisons de la chaîne ordinateur → box → Internet → sites web.
var links = []struct{ ID, Name string }{
	{"pc_box", "Ordinateur ↔ Box"},
	{"box_net", "Box ↔ Internet"},
	{"net_web", "Internet ↔ Sites web"},
}

// LinkUnknown : la communication ne passe pas, mais on ne sait pas sur
// laquelle des deux liaisons (box qui ne répond pas aux tests).
const LinkUnknown = 3

// linkLevels indique quelle(s) liaison(s) une cause met en défaut.
func linkLevels(cause string) map[string]int {
	switch cause {
	case "no_network", "box", "box_wifi":
		return map[string]int{"pc_box": int(LevelDown)}
	case "isp":
		return map[string]int{"box_net": int(LevelDown)}
	case "slow", "unstable":
		return map[string]int{"box_net": int(LevelWarn)}
	case "net_unknown":
		return map[string]int{"pc_box": LinkUnknown, "box_net": LinkUnknown}
	case "dns":
		return map[string]int{"net_web": int(LevelDown)}
	case "web":
		return map[string]int{"net_web": int(LevelWarn)}
	}
	return nil
}

// linkState donne l'état d'une liaison pour une cause. measured est faux quand
// une liaison située avant est coupée : on ne peut alors rien tester au-delà.
func linkState(cause, link string) (v int, measured bool) {
	lv := linkLevels(cause)
	if v, ok := lv[link]; ok {
		return v, true
	}
	for _, l := range links {
		if l.ID == link {
			return lv[link], true
		}
		if x := lv[l.ID]; x == int(LevelDown) || x == LinkUnknown {
			return 0, false
		}
	}
	return 0, false
}

// LinkStats résume l'historique d'une liaison.
type LinkStats struct {
	Cuts     int           // coupures (ou coupures possibles) sur cette liaison
	CutTime  time.Duration // durée totale coupée
	Warnings int           // périodes de lenteur / instabilité
	LastCut  *Incident
}

func (s Snapshot) LinkStats(link string) LinkStats {
	var st LinkStats
	for i := range s.Incidents {
		in := s.Incidents[i]
		lv, ok := linkLevels(in.Cause)[link]
		if !ok {
			continue
		}
		d := in.Duration()
		if in.Ongoing {
			d = s.Now.Sub(in.Start)
		}
		if lv == int(LevelWarn) {
			st.Warnings++
			continue
		}
		st.Cuts++
		st.CutTime += d
		st.LastCut = &in
	}
	return st
}

// LinkTimeline : pire état de la liaison pour chacune des dernières minutes
// (-1 pas de mesure, 0 ok, 1 lent/instable, 2 coupé, 3 coupé mais incertain).
func (s Snapshot) LinkTimeline(link string, minutes int) []int {
	out := make([]int, minutes)
	for i := range out {
		out[i] = -1
	}
	rank := func(v int) int { // ordre de gravité : coupé > incertain > lent > ok
		switch v {
		case int(LevelDown):
			return 3
		case LinkUnknown:
			return 2
		}
		return v
	}
	end := s.Now.Truncate(time.Minute).Add(time.Minute)
	for i := len(s.Samples) - 1; i >= 0; i-- {
		x := s.Samples[i]
		idx := minutes - 1 - int(end.Sub(x.T)/time.Minute)
		if idx < 0 {
			break
		}
		if idx >= minutes {
			continue
		}
		v, measured := linkState(x.Cause, link)
		if !measured || x.Cause == "starting" {
			continue
		}
		if out[idx] < 0 || rank(v) > rank(out[idx]) {
			out[idx] = v
		}
	}
	return out
}
