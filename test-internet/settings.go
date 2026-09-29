package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
)

// Port sur lequel Test Internet répond quand « Permettre aux autres
// ordinateurs de tester celui-ci » est activé.
const responderPort = "47801"

// settings est enregistré dans parametres.json, à côté des rapports, pour
// retrouver ses appareils au prochain lancement.
type settings struct {
	Devices   []Device `json:"devices"`
	Responder bool     `json:"responder"`
}

type settingsStore struct {
	mu   sync.Mutex
	path string
}

func newSettingsStore(dir string) *settingsStore {
	if dir == "" {
		return &settingsStore{}
	}
	return &settingsStore{path: filepath.Join(dir, "parametres.json")}
}

func (s *settingsStore) Load() settings {
	var st settings
	if s.path == "" {
		return st
	}
	if b, err := os.ReadFile(s.path); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

func (s *settingsStore) Save(st settings) {
	if s.path == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, err := json.MarshalIndent(st, "", "  "); err == nil {
		os.WriteFile(s.path, b, 0o644)
	}
}

// responder accepte les connexions des autres Test Internet du réseau et les
// referme aussitôt : cela suffit pour mesurer que la communication passe.
type responder struct {
	mu  sync.Mutex
	ln  net.Listener
	err string
}

func (r *responder) Set(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !on {
		if r.ln != nil {
			r.ln.Close()
			r.ln = nil
		}
		r.err = ""
		return
	}
	if r.ln != nil {
		return
	}
	ln, err := net.Listen("tcp", ":"+responderPort)
	if err != nil {
		r.err = "impossible d'activer : " + err.Error()
		return
	}
	r.ln, r.err = ln, ""
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
}

func (r *responder) State() (on bool, errMsg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ln != nil, r.err
}
