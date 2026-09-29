package main

import (
	"context"
	"math/rand"
	"time"
)

// demoProber simule une connexion avec des problèmes, pour montrer l'interface
// sans attendre une vraie coupure (option -demo).
type demoProber struct{ start time.Time }

func newDemoProber() *demoProber { return &demoProber{start: time.Now()} }

func (p *demoProber) Probe(_ context.Context, devices []Device) Round {
	now := time.Now()
	sec := int(now.Sub(p.start).Seconds()) % 150
	ok := Check{OK: true, Ms: 12 + rand.Float64()*8}
	r := Round{
		Time: now, Gateway: "192.168.1.1",
		Box: Check{OK: true, Ms: 1 + rand.Float64()*2},
		Net: ok, DNS: ok, Web: ok,
		Wifi: &WifiInfo{SSID: "Maison", Signal: 78},
	}
	for _, a := range netTargets {
		r.Targets = append(r.Targets, Target{Addr: a, Check: ok})
	}
	r.Devices = map[string]Check{}
	for i, d := range devices {
		// Le deuxième appareil perd la communication entre 60 et 75 s.
		r.Devices[d.ID] = Check{OK: !(i == 1 && sec >= 60 && sec < 75), Ms: 2 + rand.Float64()*3}
	}
	down := func() {
		r.Net = Check{Ms: -1}
		r.DNS, r.Web = Check{}, Check{}
		for i := range r.Targets {
			r.Targets[i].Check = Check{}
		}
	}
	switch {
	case sec >= 20 && sec < 32: // panne chez l'opérateur
		down()
	case sec >= 50 && sec < 70: // connexion lente
		r.Net.Ms = 240 + rand.Float64()*80
		for i := range r.Targets {
			r.Targets[i].Ms = r.Net.Ms
		}
	case sec >= 90 && sec < 98: // Wi-Fi trop faible
		down()
		for id := range r.Devices {
			r.Devices[id] = Check{}
		}
		r.Box = Check{}
		r.Wifi.Signal = 22
	case sec >= 120 && sec < 128: // DNS en panne
		r.DNS, r.Web = Check{}, Check{}
	}
	return r
}
