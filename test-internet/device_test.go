package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDeviceTracking(t *testing.T) {
	m := NewMonitor(nil, 5*time.Second)
	nas := Device{ID: "nas", Name: "NAS", Addr: "192.168.1.10"}
	wrong := Device{ID: "wrong", Name: "Faute de frappe", Addr: "192.168.1.250"}
	m.AddDevice(nas)
	m.AddDevice(wrong)
	s := &stepper{m: m, t: time.Now()}
	with := func(nasOK bool) func(Round) Round {
		return func(r Round) Round {
			r.Devices = map[string]Check{"nas": {OK: nasOK, Ms: 2}, "wrong": {}}
			return r
		}
	}
	s.step(with(true))
	s.step(with(false)) // coupure de 10 s avec le NAS
	s.step(with(false))
	s.step(with(true))
	s.step(func(r Round) Round { // ordinateur sans réseau : non compté
		r = with(false)(netDown(r))
		r.Gateway = ""
		return r
	})

	snap := m.Snapshot()
	snap.Now = s.t
	byID := map[string]DeviceSnapshot{}
	for _, d := range snap.Devices {
		byID[d.ID] = d
	}
	n := byID["nas"]
	if st := n.Stats(snap.Now); st.Cuts != 1 || st.CutTime != 10*time.Second {
		t.Fatalf("NAS: %+v", st)
	}
	if n.State() != "na" {
		t.Fatalf("NAS hors réseau: état %q, want na", n.State())
	}
	w := byID["wrong"]
	if w.State() != "na" || len(w.Incidents) != 0 {
		t.Fatalf("appareil jamais joignable: état %q, %d incidents", w.State(), len(w.Incidents))
	}
	m.Step(with(false)(okRound(s.t.Add(5 * time.Second))))
	for _, d := range m.Snapshot().Devices {
		if d.ID == "wrong" && d.State() != "never" {
			t.Fatalf("état %q, want never", d.State())
		}
	}
	if !m.RemoveDevice("nas") || len(m.Devices()) != 1 {
		t.Fatal("suppression ratée")
	}
}

func TestCleanDevice(t *testing.T) {
	d, err := cleanDevice("  ", " http://192.168.1.20/ ")
	if err != nil || d.Addr != "192.168.1.20" || d.Name != "192.168.1.20" || d.ID == "" {
		t.Fatalf("got %+v, %v", d, err)
	}
	for _, bad := range []string{"", "192.168.1.1; rm", "a b"} {
		if _, err := cleanDevice("x", bad); err == nil {
			t.Errorf("adresse %q acceptée", bad)
		}
	}
}

func TestDeviceAPI(t *testing.T) {
	dir := t.TempDir()
	m := NewMonitor(nil, 5*time.Second)
	a := &app{mon: m, rep: newReporter(m, dir), store: newSettingsStore(dir), resp: &responder{}, quit: func() {}}
	h := newHandler(a)
	do := func(method, url, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://127.0.0.1:47800"+url, strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rec := do("POST", "/api/devices", `{"name":"Imprimante","addr":"192.168.1.20"}`)
	if rec.Code != 200 {
		t.Fatalf("ajout: %d %s", rec.Code, rec.Body)
	}
	var d Device
	json.Unmarshal(rec.Body.Bytes(), &d)
	if got := newSettingsStore(dir).Load().Devices; len(got) != 1 || got[0].Name != "Imprimante" {
		t.Fatalf("paramètres enregistrés: %+v", got)
	}
	if rec := do("POST", "/api/devices", `{"addr":""}`); rec.Code != 400 {
		t.Fatalf("adresse vide acceptée: %d", rec.Code)
	}
	if rec := do("GET", "/api/status", ""); !strings.Contains(rec.Body.String(), `"Imprimante"`) {
		t.Fatalf("status sans l'appareil: %s", rec.Body)
	}
	if rec := do("DELETE", "/api/devices/"+d.ID, ""); rec.Code != 200 || len(m.Devices()) != 0 {
		t.Fatalf("suppression: %d", rec.Code)
	}
	// Une page web étrangère ne doit pas pouvoir piloter l'application.
	req := httptest.NewRequest("POST", "http://127.0.0.1:47800/api/devices", strings.NewReader(`{"addr":"1.2.3.4"}`))
	req.Header.Set("Origin", "http://evil.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("origine étrangère: %d", rec.Code)
	}
}

func TestResponder(t *testing.T) {
	var r responder
	r.Set(true)
	defer r.Set(false)
	if on, errMsg := r.State(); !on {
		t.Skipf("port %s indisponible: %s", responderPort, errMsg)
	}
	if ok, _ := tcpPing(context.Background(), "127.0.0.1:"+responderPort, time.Second); !ok {
		t.Fatal("le répondeur ne répond pas")
	}
	if c := probeDevice(context.Background(), "127.0.0.1"); !c.OK {
		t.Fatal("probeDevice n'a pas joint le répondeur")
	}
}
