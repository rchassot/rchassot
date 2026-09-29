package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Serveurs publics très fiables : s'ils ne répondent pas tous, c'est Internet qui est coupé.
var netTargets = []string{"1.1.1.1", "8.8.8.8", "9.9.9.9"}

// Noms de sites à résoudre pour tester l'annuaire (DNS).
var dnsNames = []string{"www.google.com", "www.wikipedia.org", "www.microsoft.com"}

// Pages de test utilisées par Windows et Android pour savoir si Internet fonctionne.
var webChecks = []struct {
	URL    string
	Status int
	Body   string
}{
	{"http://www.msftconnecttest.com/connecttest.txt", 200, "Microsoft Connect Test"},
	{"http://connectivitycheck.gstatic.com/generate_204", 204, ""},
}

const (
	pingTimeout = 1500 * time.Millisecond
	tcpTimeout  = 2 * time.Second
	dnsTimeout  = 3 * time.Second
	webTimeout  = 4 * time.Second
)

type realProber struct {
	client   *http.Client
	resolver *net.Resolver
	n        int
	wifi     *WifiInfo
}

func newRealProber() *realProber {
	return &realProber{
		client: &http.Client{
			Timeout: webTimeout,
			// Une nouvelle connexion à chaque test, sinon une connexion déjà ouverte
			// masquerait les problèmes.
			Transport: &http.Transport{DisableKeepAlives: true, Proxy: http.ProxyFromEnvironment},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse // une redirection = portail captif, pas la vraie page
			},
		},
		// Résolveur interne à Go : il interroge directement les serveurs DNS de la
		// machine, sans le cache de Windows qui masquerait les pannes.
		resolver: &net.Resolver{PreferGo: true},
	}
}

func (p *realProber) Probe(ctx context.Context, devices []Device) Round {
	r := Round{Time: time.Now(), Gateway: defaultGateway()}
	if p.n%3 == 0 {
		p.wifi = wifiInfo()
	}
	p.n++
	r.Wifi = p.wifi

	var wg sync.WaitGroup
	run := func(f func()) {
		wg.Add(1)
		go func() { defer wg.Done(); f() }()
	}

	if r.Gateway != "" {
		run(func() { r.Box.OK, r.Box.Ms = ping(r.Gateway, pingTimeout) })
	}
	r.Targets = make([]Target, len(netTargets))
	tcp := make([]Check, len(netTargets))
	for i, a := range netTargets {
		run(func() {
			ok, ms := ping(a, pingTimeout)
			r.Targets[i] = Target{Addr: a, Check: Check{ok, ms}}
		})
		// En parallèle, une connexion TCP : utile si un pare-feu bloque le ping.
		run(func() { tcp[i].OK, tcp[i].Ms = tcpPing(ctx, net.JoinHostPort(a, "443"), tcpTimeout) })
	}
	run(func() { r.DNS = p.checkDNS(ctx) })
	run(func() { r.Web = p.checkWeb(ctx) })
	devRes := make([]Check, len(devices))
	for i, d := range devices {
		run(func() { devRes[i] = probeDevice(ctx, d.Addr) })
	}
	wg.Wait()
	r.Devices = make(map[string]Check, len(devices))
	for i, d := range devices {
		r.Devices[d.ID] = devRes[i]
	}

	pings := make([]Check, len(r.Targets))
	for i, t := range r.Targets {
		pings[i] = t.Check
	}
	if r.Net = best(pings); !r.Net.OK {
		r.Net = best(tcp)
	}
	return r
}

// best renvoie la réponse la plus rapide parmi celles qui ont réussi.
func best(cs []Check) Check {
	b := Check{Ms: -1}
	for _, c := range cs {
		if c.OK && (!b.OK || c.Ms < b.Ms) {
			b = c
		}
	}
	return b
}

func (p *realProber) checkDNS(ctx context.Context) Check {
	res := make(chan Check, len(dnsNames))
	for _, name := range dnsNames {
		go func() {
			c, cancel := context.WithTimeout(ctx, dnsTimeout)
			defer cancel()
			start := time.Now()
			addrs, err := p.resolver.LookupHost(c, name)
			res <- Check{OK: err == nil && len(addrs) > 0, Ms: msSince(start)}
		}()
	}
	return collect(res, len(dnsNames))
}

func (p *realProber) checkWeb(ctx context.Context) Check {
	res := make(chan Check, len(webChecks))
	for _, w := range webChecks {
		go func() {
			start := time.Now()
			ok := false
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.URL, nil)
			if err == nil {
				if resp, err := p.client.Do(req); err == nil {
					body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
					resp.Body.Close()
					ok = resp.StatusCode == w.Status && strings.Contains(string(body), w.Body)
				}
			}
			res <- Check{OK: ok, Ms: msSince(start)}
		}()
	}
	return collect(res, len(webChecks))
}

// collect attend n résultats et garde le meilleur.
func collect(res <-chan Check, n int) Check {
	cs := make([]Check, n)
	for i := range cs {
		cs[i] = <-res
	}
	return best(cs)
}

// tcpPing mesure le temps d'ouverture d'une connexion. Un refus de connexion
// prouve aussi que la machine est joignable.
func tcpPing(ctx context.Context, addr string, timeout time.Duration) (bool, float64) {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	conn, err := (&net.Dialer{}).DialContext(c, "tcp", addr)
	ms := msSince(start)
	if err == nil {
		conn.Close()
		return true, ms
	}
	if isConnRefused(err) {
		return true, ms
	}
	return false, 0
}

func msSince(t time.Time) float64 { return float64(time.Since(t).Microseconds()) / 1000 }
