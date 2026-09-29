package main

import (
	"strconv"
	"strings"
)

// parseNetshWlan lit la sortie de « netsh wlan show interfaces ». Les clés
// « SSID » et « Signal » sont les mêmes en français et en anglais.
func parseNetshWlan(out string) *WifiInfo {
	var w WifiInfo
	found := false
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "ssid":
			if w.SSID == "" {
				w.SSID = v
			}
		case "signal":
			if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(v, "%"))); err == nil && !found {
				w.Signal, found = n, true
			}
		}
	}
	if !found {
		return nil
	}
	return &w
}
