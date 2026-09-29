package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"
)

// Device est un appareil du réseau local que l'utilisateur veut surveiller
// (imprimante, NAS, autre ordinateur, caméra…).
type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Addr string `json:"addr"` // adresse IP ou nom sur le réseau
}

// Ports essayés quand l'appareil ne répond pas au ping : il suffit qu'un seul
// réponde (même par un refus) pour prouver que l'appareil est joignable.
var devicePorts = []string{
	responderPort, "80", "443", "445", "139", "22", "3389", "9100", "631", "515", "554", "5000", "8080", "53",
}

const deviceTimeout = 1500 * time.Millisecond

// États d'un appareil dans la frise (en plus de LevelOK / LevelDown).
const devNotMeasured = -1

type devPoint struct {
	T     time.Time
	State int // 0 joignable, 2 injoignable, -1 non testé
}

type deviceTrack struct {
	Device
	last      Check
	haveLast  bool
	measured  bool // faux si l'ordinateur n'était relié à aucun réseau
	everOK    bool
	hist      []devPoint
	incidents []*Incident
}

// DeviceSnapshot est la copie de l'état d'un appareil pour l'affichage.
type DeviceSnapshot struct {
	Device
	Last      Check
	HaveLast  bool
	Measured  bool
	EverOK    bool
	Hist      []devPoint
	Incidents []Incident
}

func newDeviceID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// cleanDevice vérifie et nettoie ce que l'utilisateur a saisi.
func cleanDevice(name, addr string) (Device, error) {
	name, addr = strings.TrimSpace(name), strings.TrimSpace(addr)
	addr = strings.TrimPrefix(strings.TrimPrefix(addr, "http://"), "https://")
	addr = strings.TrimRight(addr, "/")
	if addr == "" {
		return Device{}, fmt.Errorf("indiquez l'adresse de l'appareil (par exemple 192.168.1.20)")
	}
	if len(addr) > 253 || len(name) > 60 {
		return Device{}, fmt.Errorf("nom ou adresse trop long")
	}
	for _, c := range addr {
		if !(c == '.' || c == '-' || c == ':' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return Device{}, fmt.Errorf("l'adresse contient un caractère non valable : %q", c)
		}
	}
	if name == "" {
		name = addr
	}
	return Device{ID: newDeviceID(), Name: name, Addr: addr}, nil
}

// Devices renvoie la liste des appareils surveillés.
func (m *Monitor) Devices() []Device {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Device, len(m.devices))
	for i, d := range m.devices {
		out[i] = d.Device
	}
	return out
}

func (m *Monitor) AddDevice(d Device) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.devices = append(m.devices, &deviceTrack{Device: d})
}

func (m *Monitor) RemoveDevice(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, d := range m.devices {
		if d.ID == id {
			m.devices = append(m.devices[:i], m.devices[i+1:]...)
			return true
		}
	}
	return false
}

// stepDevices intègre les résultats des appareils (appelé sous verrou).
func (m *Monitor) stepDevices(r Round) {
	offline := r.Gateway == "" && !r.Net.OK
	for _, d := range m.devices {
		c, ok := r.Devices[d.ID]
		if !ok {
			continue // ajouté pendant le tour : sera testé au prochain
		}
		d.last, d.haveLast, d.measured = c, true, !offline
		state := devNotMeasured
		switch {
		case offline:
			// L'ordinateur n'est relié à rien : ce n'est pas la liaison avec l'appareil qui est en cause.
		case c.OK:
			d.everOK = true
			state = int(LevelOK)
		case d.everOK:
			state = int(LevelDown)
		}
		d.hist = append(d.hist, devPoint{T: r.Time, State: state})
		if len(d.hist) > maxSamples {
			d.hist = append(d.hist[:0:0], d.hist[len(d.hist)-maxSamples:]...)
		}
		switch state {
		case int(LevelOK):
			recordIncident(&d.incidents, r.Time, LevelOK, "", "")
		case int(LevelDown):
			recordIncident(&d.incidents, r.Time, LevelDown, "device", "")
		}
	}
}

func (m *Monitor) snapshotDevices() []DeviceSnapshot {
	var out []DeviceSnapshot
	for _, d := range m.devices {
		ds := DeviceSnapshot{
			Device: d.Device, Last: d.last, HaveLast: d.haveLast, Measured: d.measured, EverOK: d.everOK,
			Hist: append([]devPoint(nil), d.hist...),
		}
		for _, i := range d.incidents {
			ds.Incidents = append(ds.Incidents, *i)
		}
		out = append(out, ds)
	}
	return out
}

// State résume l'état actuel : ok, down, never (n'a jamais répondu), na (non testé), wait.
func (d DeviceSnapshot) State() string {
	switch {
	case !d.HaveLast:
		return "wait"
	case !d.Measured:
		return "na"
	case d.Last.OK:
		return "ok"
	case !d.EverOK:
		return "never"
	default:
		return "down"
	}
}

func (d DeviceSnapshot) Stats(now time.Time) LinkStats {
	var st LinkStats
	for i := range d.Incidents {
		in := d.Incidents[i]
		dur := in.Duration()
		if in.Ongoing {
			dur = now.Sub(in.Start)
		}
		st.Cuts++
		st.CutTime += dur
		st.LastCut = &in
	}
	return st
}

func (d DeviceSnapshot) Timeline(now time.Time, minutes int) []int {
	out := make([]int, minutes)
	for i := range out {
		out[i] = -1
	}
	end := now.Truncate(time.Minute).Add(time.Minute)
	for i := len(d.Hist) - 1; i >= 0; i-- {
		p := d.Hist[i]
		idx := minutes - 1 - int(end.Sub(p.T)/time.Minute)
		if idx < 0 {
			break
		}
		if idx < minutes && p.State > out[idx] {
			out[idx] = p.State
		}
	}
	return out
}

// probeDevice teste un appareil : ping, et en parallèle quelques ports TCP
// courants (les ordinateurs Windows bloquent souvent le ping).
func probeDevice(ctx context.Context, addr string) Check {
	ctx, cancel := context.WithTimeout(ctx, 2*deviceTimeout+time.Second)
	defer cancel()
	host := addr
	if net.ParseIP(addr) == nil {
		c, cancelRes := context.WithTimeout(ctx, deviceTimeout)
		ips, err := net.DefaultResolver.LookupHost(c, addr)
		cancelRes()
		if err != nil || len(ips) == 0 {
			return Check{}
		}
		host = ips[0]
		for _, ip := range ips {
			if net.ParseIP(ip).To4() != nil {
				host = ip
				break
			}
		}
	}

	res := make(chan Check, len(devicePorts)+1)
	go func() {
		ok, ms := ping(host, deviceTimeout)
		res <- Check{OK: ok, Ms: ms}
	}()
	for _, port := range devicePorts {
		go func() {
			ok, ms := tcpPing(ctx, net.JoinHostPort(host, port), deviceTimeout)
			res <- Check{OK: ok, Ms: ms}
		}()
	}
	for range len(devicePorts) + 1 {
		if c := <-res; c.OK {
			return c
		}
	}
	return Check{}
}

// localIP renvoie l'adresse de cet ordinateur sur le réseau local (aucun
// paquet n'est envoyé : on demande juste à Windows quelle interface il utiliserait).
func localIP() string {
	c, err := net.Dial("udp", "8.8.8.8:53")
	if err != nil {
		return ""
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return ""
}
