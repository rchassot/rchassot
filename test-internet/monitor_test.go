package main

import (
	"strings"
	"testing"
	"time"
)

func okRound(t time.Time) Round {
	ok := Check{OK: true, Ms: 15}
	return Round{
		Time: t, Gateway: "192.168.1.1", Box: Check{OK: true, Ms: 1},
		Targets: []Target{{"1.1.1.1", ok}, {"8.8.8.8", ok}, {"9.9.9.9", ok}},
		Net:     ok, DNS: ok, Web: ok,
	}
}

func netDown(r Round) Round {
	r.Net, r.DNS, r.Web = Check{}, Check{}, Check{}
	for i := range r.Targets {
		r.Targets[i].Check = Check{}
	}
	return r
}

func TestDiagnose(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		round     func() Round
		boxEverOK bool
		loss, ms  float64
		webFails  int
		level     Level
		cause     string
	}{
		{"tout va bien", func() Round { return okRound(now) }, true, 0, 15, 0, LevelOK, "ok"},
		{"aucun réseau", func() Round { r := netDown(okRound(now)); r.Gateway, r.Box = "", Check{}; return r }, true, 0, -1, 0, LevelDown, "no_network"},
		{"opérateur", func() Round { return netDown(okRound(now)) }, true, 0, -1, 0, LevelDown, "isp"},
		{"box injoignable", func() Round { r := netDown(okRound(now)); r.Box = Check{}; return r }, true, 0, -1, 0, LevelDown, "box"},
		{"wifi faible", func() Round {
			r := netDown(okRound(now))
			r.Box, r.Wifi = Check{}, &WifiInfo{Signal: 20}
			return r
		}, true, 0, -1, 0, LevelDown, "box_wifi"},
		{"box muette au ping", func() Round { r := netDown(okRound(now)); r.Box = Check{}; return r }, false, 0, -1, 0, LevelDown, "net_unknown"},
		{"dns", func() Round { r := okRound(now); r.DNS, r.Web = Check{}, Check{}; return r }, true, 0, 15, 0, LevelDown, "dns"},
		{"web filtré", func() Round { r := okRound(now); r.Web = Check{}; return r }, true, 0, 15, 2, LevelWarn, "web"},
		{"instable", func() Round { return okRound(now) }, true, 15, 20, 0, LevelWarn, "unstable"},
		{"lent", func() Round { return okRound(now) }, true, 0, 300, 0, LevelWarn, "slow"},
		{"box muette mais Internet OK", func() Round { r := okRound(now); r.Box = Check{}; return r }, false, 0, 15, 0, LevelOK, "ok"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, c, _ := diagnose(diagInput{Round: tt.round(), BoxEverOK: tt.boxEverOK, LossPct: tt.loss, MedianMs: tt.ms, WebFails: tt.webFails})
			if l != tt.level || c != tt.cause {
				t.Fatalf("got (%d, %s), want (%d, %s)", l, c, tt.level, tt.cause)
			}
			if _, ok := causeTexts[c]; !ok {
				t.Fatalf("pas de texte pour la cause %q", c)
			}
		})
	}
}

type stepper struct {
	m *Monitor
	t time.Time
}

func (s *stepper) step(f func(Round) Round) {
	s.t = s.t.Add(5 * time.Second)
	s.m.Step(f(okRound(s.t)))
}

func TestIncidentsAndLinks(t *testing.T) {
	m := NewMonitor(nil, 5*time.Second)
	s := &stepper{m: m, t: time.Now().Add(-10 * time.Minute)}
	same := func(r Round) Round { return r }
	boxDown := func(r Round) Round { r = netDown(r); r.Box = Check{}; return r }

	s.step(same)
	s.step(netDown) // coupure opérateur de 10 s
	s.step(netDown)
	s.step(same)
	for range 10 { // 50 s plus tard : coupure côté box
		s.step(same)
	}
	s.step(boxDown)
	s.step(same)

	snap := m.Snapshot()
	snap.Now = s.t
	if len(snap.Incidents) != 2 {
		t.Fatalf("incidents = %d, want 2: %+v", len(snap.Incidents), snap.Incidents)
	}
	if got := snap.Incidents[0]; got.Cause != "isp" || got.Duration() != 10*time.Second || got.Ongoing {
		t.Fatalf("premier incident inattendu: %+v", got)
	}
	st := snap.Stats()
	if st.Outages != 2 || st.OutageTime != 15*time.Second || st.MainCause != "isp" {
		t.Fatalf("stats inattendues: %+v", st)
	}
	if ls := snap.LinkStats("box_net"); ls.Cuts != 1 || ls.CutTime != 10*time.Second {
		t.Fatalf("box_net: %+v", ls)
	}
	if ls := snap.LinkStats("pc_box"); ls.Cuts != 1 {
		t.Fatalf("pc_box: %+v", ls)
	}
	if ls := snap.LinkStats("net_web"); ls.Cuts != 0 {
		t.Fatalf("net_web: %+v", ls)
	}
}

func TestIncidentMerge(t *testing.T) {
	m := NewMonitor(nil, 5*time.Second)
	s := &stepper{m: m, t: time.Now()}
	for range 3 { // coupures rapprochées (< 30 s) : un seul incident
		s.step(netDown)
		s.step(func(r Round) Round { return r })
	}
	if n := len(m.Snapshot().Incidents); n != 1 {
		t.Fatalf("incidents = %d, want 1", n)
	}
}

func TestLinkState(t *testing.T) {
	cases := []struct {
		cause, link string
		v           int
		measured    bool
	}{
		{"ok", "pc_box", 0, true},
		{"ok", "net_web", 0, true},
		{"box", "pc_box", 2, true},
		{"box", "box_net", 0, false}, // au-delà de la coupure : non testable
		{"isp", "pc_box", 0, true},
		{"isp", "box_net", 2, true},
		{"isp", "net_web", 0, false},
		{"net_unknown", "pc_box", LinkUnknown, true},
		{"net_unknown", "box_net", LinkUnknown, true},
		{"dns", "net_web", 2, true},
		{"slow", "box_net", 1, true},
		{"slow", "net_web", 0, true},
	}
	for _, c := range cases {
		v, measured := linkState(c.cause, c.link)
		if v != c.v || measured != c.measured {
			t.Errorf("linkState(%s, %s) = (%d, %v), want (%d, %v)", c.cause, c.link, v, measured, c.v, c.measured)
		}
	}
}

func TestTimeline(t *testing.T) {
	m := NewMonitor(nil, 5*time.Second)
	now := time.Now().Truncate(time.Minute).Add(30 * time.Second)
	m.Step(okRound(now.Add(-2 * time.Minute)))
	m.Step(netDown(okRound(now)))
	snap := m.Snapshot()
	snap.Now = now
	tl := snap.Timeline(60)
	if tl[59] != int(LevelDown) || tl[57] != int(LevelOK) || tl[58] != -1 {
		t.Fatalf("timeline = %v", tl[55:])
	}
	if lt := snap.LinkTimeline("box_net", 60); lt[59] != int(LevelDown) || lt[57] != 0 {
		t.Fatalf("link timeline = %v", lt[55:])
	}
	if lt := snap.LinkTimeline("net_web", 60); lt[59] != -1 {
		t.Fatalf("net_web non testable pendant la coupure, got %v", lt[55:])
	}
}

func TestParseNetshWlan(t *testing.T) {
	out := `
Il existe 1 interface sur le système :

    Nom                    : Wi-Fi
    État                   : connecté
    SSID                   : Maison
    BSSID                  : aa:bb:cc:dd:ee:ff
    Signal                 : 34%
`
	w := parseNetshWlan(strings.ReplaceAll(out, "\n", "\r\n"))
	if w == nil || w.SSID != "Maison" || w.Signal != 34 {
		t.Fatalf("got %+v", w)
	}
	if parseNetshWlan("Le service WLAN n'est pas en cours d'exécution.") != nil {
		t.Fatal("pas de Wi-Fi attendu")
	}
}

func TestFmtDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		45 * time.Second: "45 s", 3*time.Minute + 20*time.Second: "3 min 20 s",
		2 * time.Minute: "2 min", 2*time.Hour + 5*time.Minute: "2 h 05 min",
	} {
		if got := fmtDuration(d); got != want {
			t.Errorf("fmtDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestReportRenders(t *testing.T) {
	m := NewMonitor(nil, 5*time.Second)
	s := &stepper{m: m, t: time.Now()}
	s.step(func(r Round) Round { return r })
	s.step(netDown)
	s.step(func(r Round) Round { return r })
	html := string(buildReport(m.Snapshot()))
	for _, want := range []string{"Rapport de connexion Internet", "Box ↔ Internet", "1 coupure(s)", "<svg"} {
		if !strings.Contains(html, want) {
			t.Errorf("rapport sans %q", want)
		}
	}
}
