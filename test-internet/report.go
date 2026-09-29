package main

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// reporter enregistre régulièrement le rapport sur le disque, pour ne rien
// perdre si l'ordinateur s'éteint ou si la fenêtre est fermée.
type reporter struct {
	mon  *Monitor
	mu   sync.Mutex
	path string
}

func newReporter(m *Monitor, dir string) *reporter {
	name := "rapport-internet_" + time.Now().Format("2006-01-02_15h04") + ".html"
	var dirs []string
	if dir != "" {
		dirs = append(dirs, dir)
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), "Rapports Test Internet"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Documents", "Rapports Test Internet"))
	}
	dirs = append(dirs, filepath.Join(os.TempDir(), "Rapports Test Internet"))
	for _, d := range dirs {
		if os.MkdirAll(d, 0o755) != nil {
			continue
		}
		p := filepath.Join(d, name)
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			f.Close()
			return &reporter{mon: m, path: p}
		}
	}
	return &reporter{mon: m}
}

func (r *reporter) Path() string { return r.path }

func (r *reporter) Save() (string, error) {
	if r.path == "" {
		return "", fmt.Errorf("aucun dossier accessible en écriture")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.path, os.WriteFile(r.path, buildReport(r.mon.Snapshot()), 0o644)
}

func (r *reporter) AutoSave(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.Save()
		}
	}
}

type reportIncident struct {
	Start, End, Duration, Title, Link, Detail, Class string
}

type reportLink struct {
	Name, State, Class, CutTime, LastCut string
	Cuts, Warnings                       int
}

type reportData struct {
	Computer, Period, Elapsed, Generated string
	Verdict, VerdictClass, MainCause     string
	Availability, Latency                string
	Outages, Warnings                    int
	OutageTime                           string
	Links                                []reportLink
	Incidents                            []reportIncident
	Chart                                template.HTML
	Gateway, Targets                     string
}

var linkStateText = map[string]string{
	"ok": "Fonctionne", "warn": "Lente / instable", "down": "Coupée", "unknown": "Coupée (incertain)", "na": "Non testable",
}

func buildReport(s Snapshot) []byte {
	st := s.Stats()
	d := reportData{
		Period:    s.Started.Format("02.01.2006 15:04") + " → " + s.Now.Format("02.01.2006 15:04"),
		Elapsed:   fmtDuration(st.Elapsed),
		Generated: s.Now.Format("02.01.2006 à 15:04"),
		Outages:   st.Outages, Warnings: st.Warnings, OutageTime: fmtDuration(st.OutageTime),
		Availability: "—", Latency: "—",
		Gateway: orDash(s.Last.Gateway), Targets: strings.Join(netTargets, ", "),
		Chart: template.HTML(chartSVG(s)),
	}
	d.Computer, _ = os.Hostname()
	if st.Availability >= 0 {
		d.Availability = strings.Replace(fmt.Sprintf("%.2f %%", st.Availability), ".", ",", 1)
	}
	if st.MedianMs >= 0 {
		d.Latency = fmt.Sprintf("%.0f ms (%s)", st.MedianMs, strings.ToLower(speedWord(st.MedianMs)))
	}
	switch {
	case st.Outages > 0:
		d.Verdict = fmt.Sprintf("%d coupure(s) constatée(s), %s sans Internet au total.", st.Outages, fmtDuration(st.OutageTime))
		d.VerdictClass = "down"
		d.MainCause = causeText(st.MainCause).Where
	case st.Warnings > 0:
		d.Verdict = "Aucune coupure, mais des lenteurs ou de l'instabilité ont été constatées."
		d.VerdictClass = "warn"
	default:
		d.Verdict = "Aucun problème constaté pendant la surveillance."
		d.VerdictClass = "ok"
	}
	for _, l := range buildLinks(s) {
		rl := reportLink{Name: l.Name, State: linkStateText[l.State], Class: l.State, Cuts: l.Cuts, CutTime: l.CutTime, Warnings: l.Warnings, LastCut: orDash(l.LastCut)}
		if l.Cuts == 0 {
			rl.CutTime = "—"
		}
		d.Links = append(d.Links, rl)
	}
	for _, in := range s.Incidents {
		end := in.End
		endTxt := in.End.Format("02.01 15:04:05")
		if in.Ongoing {
			end, endTxt = s.Now, "en cours"
		}
		var names []string
		lv := linkLevels(in.Cause)
		for _, l := range links {
			if _, ok := lv[l.ID]; ok {
				names = append(names, l.Name)
			}
		}
		class := "down"
		if in.Level == LevelWarn {
			class = "warn"
		}
		d.Incidents = append(d.Incidents, reportIncident{
			Start: in.Start.Format("02.01 15:04:05"), End: endTxt, Duration: fmtDuration(end.Sub(in.Start)),
			Title: causeText(in.Cause).Title, Link: strings.Join(names, " ou "), Detail: in.Detail, Class: class,
		})
	}
	var b bytes.Buffer
	if err := reportTmpl.Execute(&b, d); err != nil {
		return []byte("erreur de génération du rapport : " + err.Error())
	}
	return b.Bytes()
}

// chartSVG dessine le temps de réponse d'Internet sur toute la session, avec
// les coupures en rouge et les lenteurs en orange.
func chartSVG(s Snapshot) string {
	const W, H, L, R, T, B = 900.0, 230.0, 48.0, 12.0, 12.0, 28.0
	if len(s.Samples) < 2 {
		return `<p class="muted">Pas encore assez de mesures pour tracer un graphique.</p>`
	}
	t0, t1 := s.Samples[0].T, s.Samples[len(s.Samples)-1].T
	span := t1.Sub(t0).Seconds()
	if span <= 0 {
		span = 1
	}
	n := int(W - L - R)
	type bucket struct {
		sum   float64
		cnt   int
		level Level
		any   bool
	}
	bs := make([]bucket, n)
	var all []float64
	for _, x := range s.Samples {
		i := int(x.T.Sub(t0).Seconds() / span * float64(n-1))
		b := &bs[i]
		b.any = true
		if x.Level > b.level {
			b.level = x.Level
		}
		if x.NetMs >= 0 {
			b.sum += x.NetMs
			b.cnt++
			all = append(all, x.NetMs)
		}
	}
	yMax := 100.0
	if len(all) > 0 {
		sort.Float64s(all)
		p := all[int(float64(len(all)-1)*0.98)] * 1.2
		yMax = math.Min(math.Max(yMax, niceCeil(p)), 2000)
	}
	x := func(i int) float64 { return L + float64(i) }
	y := func(ms float64) float64 { return T + (H-T-B)*(1-math.Min(ms, yMax)/yMax) }

	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg viewBox="0 0 %.0f %.0f" class="chart" role="img" aria-label="Temps de réponse d'Internet">`, W, H)
	for _, v := range []float64{0, yMax / 2, yMax} {
		fmt.Fprintf(&sb, `<line x1="%.0f" x2="%.0f" y1="%.1f" y2="%.1f" class="grid"/><text x="%.0f" y="%.1f" class="axis" text-anchor="end">%.0f ms</text>`,
			L, W-R, y(v), y(v), L-6, y(v)+4, v)
	}
	for i := 0; i < n; i++ {
		b := bs[i]
		if !b.any || b.level == LevelOK {
			continue
		}
		// La bande s'étend jusqu'à la mesure suivante d'un autre état.
		j := i + 1
		for j < n && (!bs[j].any || bs[j].level == b.level) {
			j++
		}
		cls := "band-down"
		if b.level == LevelWarn {
			cls = "band-warn"
		}
		fmt.Fprintf(&sb, `<rect x="%.1f" y="%.0f" width="%.1f" height="%.0f" class="%s"/>`, x(i), T, float64(j-i), H-T-B, cls)
		i = j - 1
	}
	var pts []string
	flush := func() {
		if len(pts) > 1 {
			fmt.Fprintf(&sb, `<polyline points="%s" class="line"/>`, strings.Join(pts, " "))
		}
		pts = pts[:0]
	}
	for i, b := range bs {
		switch {
		case b.cnt > 0:
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", x(i), y(b.sum/float64(b.cnt))))
		case b.any:
			flush() // coupure : on interrompt la courbe
		}
	}
	flush()
	for k := 0; k <= 4; k++ {
		t := t0.Add(time.Duration(float64(k) / 4 * span * float64(time.Second)))
		anchor := "middle"
		if k == 0 {
			anchor = "start"
		} else if k == 4 {
			anchor = "end"
		}
		fmt.Fprintf(&sb, `<text x="%.1f" y="%.0f" class="axis" text-anchor="%s">%s</text>`, L+float64(k)/4*float64(n-1), H-8, anchor, shortWhen(t, s.Now))
	}
	sb.WriteString(`</svg>`)
	return sb.String()
}

func shortWhen(t, now time.Time) string {
	if w := fmtWhen(t, now); len(w) > 8 {
		return t.Format("02.01 15:04")
	}
	return t.Format("15:04")
}

func niceCeil(v float64) float64 {
	for _, s := range []float64{100, 150, 200, 300, 500, 750, 1000, 1500, 2000} {
		if v <= s {
			return s
		}
	}
	return 2000
}

var reportTmpl = template.Must(template.New("r").Parse(`<!doctype html>
<html lang="fr"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Rapport de connexion Internet</title>
<style>
:root{--ok:#15803d;--ok-bg:#dcfce7;--warn:#b45309;--warn-bg:#fef3c7;--down:#b91c1c;--down-bg:#fee2e2;--muted:#64748b;--line:#e2e8f0}
*{box-sizing:border-box}body{font:15px/1.5 "Segoe UI",system-ui,sans-serif;color:#0f172a;background:#fff;margin:0;padding:32px 16px}
main{max-width:920px;margin:0 auto}h1{font-size:26px;margin:0 0 4px}h2{font-size:18px;margin:32px 0 10px}
.muted{color:var(--muted)}.verdict{padding:16px 20px;border-radius:12px;font-size:18px;font-weight:600;margin-top:20px}
.verdict.ok{background:var(--ok-bg);color:var(--ok)}.verdict.warn{background:var(--warn-bg);color:var(--warn)}.verdict.down{background:var(--down-bg);color:var(--down)}
.verdict small{display:block;font-weight:400;color:#0f172a;margin-top:4px;font-size:15px}
.kpis{display:grid;grid-template-columns:repeat(auto-fit,minmax(160px,1fr));gap:12px;margin-top:16px}
.kpi{border:1px solid var(--line);border-radius:10px;padding:12px}.kpi b{display:block;font-size:20px}
table{width:100%;border-collapse:collapse;font-size:14px}th,td{text-align:left;padding:8px;border-bottom:1px solid var(--line);vertical-align:top}th{color:var(--muted);font-weight:600}
.pill{display:inline-block;padding:1px 8px;border-radius:999px;font-size:13px;font-weight:600}
.pill.ok{background:var(--ok-bg);color:var(--ok)}.pill.warn{background:var(--warn-bg);color:var(--warn)}.pill.down,.pill.unknown{background:var(--down-bg);color:var(--down)}.pill.na{background:#f1f5f9;color:var(--muted)}
.chart{width:100%;height:auto}.chart .grid{stroke:var(--line)}.chart .axis{font-size:11px;fill:var(--muted)}
.chart .line{fill:none;stroke:#2563eb;stroke-width:1.5}.band-down{fill:var(--down);opacity:.35}.band-warn{fill:#f59e0b;opacity:.3}
.legend span{margin-right:16px;font-size:13px}.sw{display:inline-block;width:12px;height:12px;border-radius:3px;vertical-align:-1px;margin-right:4px}
.hint{background:#f1f5f9;border-radius:8px;padding:10px 14px;font-size:14px}@media print{.hint{display:none}body{padding:0}}
</style></head><body><main>
<p class="hint">💡 Pour enregistrer ce rapport en PDF : appuyez sur <b>Ctrl + P</b> puis choisissez « Enregistrer au format PDF ».</p>
<h1>Rapport de connexion Internet</h1>
<div class="muted">Ordinateur : {{.Computer}} · Période : {{.Period}} ({{.Elapsed}}) · Généré le {{.Generated}}</div>

<div class="verdict {{.VerdictClass}}">{{.Verdict}}{{if .MainCause}}<small>Origine principale probable : <b>{{.MainCause}}</b></small>{{end}}</div>

<div class="kpis">
<div class="kpi"><span class="muted">Coupures</span><b>{{.Outages}}</b></div>
<div class="kpi"><span class="muted">Temps sans Internet</span><b>{{.OutageTime}}</b></div>
<div class="kpi"><span class="muted">Disponibilité</span><b>{{.Availability}}</b></div>
<div class="kpi"><span class="muted">Temps de réponse habituel</span><b>{{.Latency}}</b></div>
</div>

<h2>Coupures par liaison</h2>
<p class="muted">La connexion passe par trois liaisons successives. Ce tableau montre sur laquelle les coupures ont eu lieu.</p>
<table><tr><th>Liaison</th><th>État à la fin</th><th>Coupures</th><th>Temps coupé</th><th>Lenteurs</th><th>Dernière coupure</th></tr>
{{range .Links}}<tr><td><b>{{.Name}}</b></td><td><span class="pill {{.Class}}">{{.State}}</span></td><td>{{.Cuts}}</td><td>{{.CutTime}}</td><td>{{.Warnings}}</td><td>{{.LastCut}}</td></tr>{{end}}
</table>

<h2>Temps de réponse d'Internet</h2>
{{.Chart}}
<div class="legend"><span><i class="sw" style="background:#2563eb"></i>Temps de réponse</span><span><i class="sw" style="background:#fca5a5"></i>Coupure</span><span><i class="sw" style="background:#fcd34d"></i>Lenteur / instabilité</span></div>

<h2>Journal détaillé</h2>
{{if .Incidents}}<table><tr><th>Début</th><th>Fin</th><th>Durée</th><th>Problème</th><th>Liaison concernée</th></tr>
{{range .Incidents}}<tr><td>{{.Start}}</td><td>{{.End}}</td><td>{{.Duration}}</td><td><span class="pill {{.Class}}">{{if eq .Class "down"}}Coupure{{else}}Lenteur{{end}}</span> {{.Title}}{{if .Detail}} <span class="muted">({{.Detail}})</span>{{end}}</td><td>{{.Link}}</td></tr>{{end}}
</table>{{else}}<p>Aucun problème enregistré.</p>{{end}}

<h2>Informations techniques</h2>
<table>
<tr><td>Adresse de la box</td><td>{{.Gateway}}</td></tr>
<tr><td>Serveurs Internet testés</td><td>{{.Targets}} (ping, et connexion TCP si le ping est bloqué)</td></tr>
<tr><td>Méthode</td><td>Tests toutes les 5 secondes : ping de la box, ping de 3 serveurs Internet, résolution DNS de 3 sites, chargement de pages de test Microsoft et Google.</td></tr>
</table>
</main></body></html>`))
