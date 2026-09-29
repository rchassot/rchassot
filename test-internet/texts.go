package main

import (
	"fmt"
	"time"
)

// CauseText est l'explication d'un état, écrite pour quelqu'un qui n'est pas informaticien.
type CauseText struct {
	Title       string // ce qui se passe, en une phrase
	Explanation string // pourquoi / d'où ça vient
	Advice      string // que faire
	Where       string // origine probable, pour le résumé du rapport
}

var causeTexts = map[string]CauseText{
	"starting": {
		Title:       "Premier test en cours…",
		Explanation: "Quelques secondes de patience, le programme vérifie votre connexion.",
	},
	"ok": {
		Title:       "Votre connexion Internet fonctionne bien",
		Explanation: "Votre ordinateur, votre box et Internet répondent normalement.",
		Advice:      "Laissez le test tourner : il enregistre automatiquement chaque coupure, même courte, avec l'heure exacte.",
	},
	"no_network": {
		Title:       "Votre ordinateur n'est connecté à aucun réseau",
		Explanation: "Il n'est relié ni en Wi-Fi ni par câble à votre box.",
		Advice:      "Vérifiez que le Wi-Fi est activé (icône en bas à droite de l'écran) ou que le câble réseau est bien branché des deux côtés.",
		Where:       "chez vous (Wi-Fi ou câble de l'ordinateur)",
	},
	"box": {
		Title:       "Votre ordinateur n'arrive plus à joindre votre box",
		Explanation: "Le problème se situe chez vous, entre l'ordinateur et la box Internet : Wi-Fi perturbé, câble défectueux ou box qui redémarre.",
		Advice:      "Regardez si les voyants de la box sont normaux. Si cela se répète : rapprochez l'ordinateur de la box, essayez un câble, ou redémarrez la box.",
		Where:       "chez vous (entre l'ordinateur et la box)",
	},
	"box_wifi": {
		Title:       "Le Wi-Fi est trop faible pour joindre votre box",
		Explanation: "Le signal Wi-Fi reçu par l'ordinateur est faible : la connexion avec la box se perd.",
		Advice:      "Rapprochez l'ordinateur de la box, évitez les murs épais entre les deux, ou utilisez un câble réseau. Un répéteur Wi-Fi peut aussi aider.",
		Where:       "chez vous (Wi-Fi trop faible)",
	},
	"isp": {
		Title:       "Votre box fonctionne, mais Internet ne répond plus",
		Explanation: "L'ordinateur joint bien la box, mais la box n'arrive plus à sortir sur Internet. Le problème vient très probablement de votre opérateur (ligne, fibre, panne dans le quartier).",
		Advice:      "Si cela se répète, contactez votre opérateur et envoyez-lui le rapport : il contient l'heure et la durée de chaque coupure.",
		Where:       "chez l'opérateur (ligne Internet)",
	},
	"net_unknown": {
		Title:       "Internet ne répond plus",
		Explanation: "Votre box ne répond pas à ce type de test (c'est normal pour certains modèles), on ne peut donc pas savoir si le problème vient de chez vous ou de votre opérateur.",
		Advice:      "Regardez les voyants de la box pendant la coupure : s'ils indiquent un problème de ligne, c'est l'opérateur. Sinon, vérifiez le Wi-Fi ou les câbles.",
		Where:       "indéterminée (box ou opérateur)",
	},
	"dns": {
		Title:       "Internet fonctionne, mais les sites ne sont plus trouvés",
		Explanation: "L'« annuaire » d'Internet (le DNS), qui traduit les noms des sites en adresses, ne répond plus. Résultat : les sites ne s'ouvrent pas.",
		Advice:      "Redémarrez la box. Si le problème revient souvent, votre opérateur ou un informaticien peut configurer un autre annuaire (DNS).",
		Where:       "annuaire Internet (DNS) de la box ou de l'opérateur",
	},
	"web": {
		Title:       "Internet répond, mais les pages web ne se chargent pas bien",
		Explanation: "Les tests de base passent, mais les pages de test ne s'affichent pas correctement.",
		Advice:      "Sur un Wi-Fi public (hôtel, gare…), une page de connexion doit peut-être être validée dans le navigateur. Sinon, un pare-feu ou un antivirus bloque peut-être l'accès.",
		Where:       "pages web bloquées ou filtrées",
	},
	"unstable": {
		Title:       "La connexion est instable",
		Explanation: "Une partie des messages envoyés sur Internet se perd. Les appels vidéo peuvent couper ou saccader.",
		Advice:      "En Wi-Fi, rapprochez-vous de la box. Si cela arrive aussi avec un câble, parlez-en à votre opérateur.",
		Where:       "connexion instable",
	},
	"slow": {
		Title:       "La connexion est lente",
		Explanation: "Internet met beaucoup de temps à répondre. Les pages s'ouvrent lentement et les appels vidéo peuvent saccader.",
		Advice:      "Quelqu'un utilise peut-être beaucoup la connexion (téléchargements, films, mises à jour). En Wi-Fi, rapprochez-vous de la box.",
		Where:       "connexion lente",
	},
}

func causeText(c string) CauseText {
	if t, ok := causeTexts[c]; ok {
		return t
	}
	return CauseText{Title: c}
}

// fmtDuration écrit une durée « à la française » : 45 s, 3 min 20 s, 2 h 05 min.
func fmtDuration(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	if s < 60 {
		return fmt.Sprintf("%d s", s)
	}
	m := s / 60
	s %= 60
	if m < 60 {
		if s == 0 {
			return fmt.Sprintf("%d min", m)
		}
		return fmt.Sprintf("%d min %d s", m, s)
	}
	return fmt.Sprintf("%d h %02d min", m/60, m%60)
}

// fmtWhen affiche l'heure, avec la date si ce n'est pas aujourd'hui.
func fmtWhen(t, now time.Time) string {
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	if y1 == y2 && m1 == m2 && d1 == d2 {
		return t.Format("15:04:05")
	}
	return t.Format("02.01 15:04:05")
}

// speedWord qualifie un temps de réponse en mots simples.
func speedWord(ms float64) string {
	switch {
	case ms < 0:
		return "—"
	case ms < 40:
		return "Très rapide"
	case ms < 80:
		return "Rapide"
	case ms < slowMs:
		return "Correct"
	default:
		return "Lent"
	}
}
