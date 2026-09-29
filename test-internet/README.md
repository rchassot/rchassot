# 🌐 Test Internet

Petit utilitaire Windows qui surveille la connexion Internet en continu et **explique en français simple** d'où viennent les coupures : de chez vous (Wi‑Fi, câble, box) ou de l'opérateur.

- **Un seul fichier** `TestInternet.exe` (~9 Mo), rien à installer, pas besoin de droits administrateur. Il marche aussi depuis une clé USB.
- Windows 10 et 11.
- Les résultats s'affichent dans le navigateur (page locale, rien n'est envoyé sur Internet).

## Pour l'utilisateur

**Télécharger :** https://github.com/rchassot/rchassot/releases/latest/download/TestInternet.exe

1. Double-cliquez sur **TestInternet.exe**.
   - Si Windows affiche « Windows a protégé votre ordinateur », cliquez sur **Informations complémentaires**, puis sur **Exécuter quand même**. Cet avertissement apparaît parce que le programme n'est pas signé.
2. Une fenêtre noire s'ouvre, puis la page de résultats s'affiche dans le navigateur. **Ne fermez pas la fenêtre noire** : vous pouvez la réduire.
3. Laissez tourner quelques heures (idéalement une journée) en utilisant l'ordinateur normalement.
4. Cliquez sur **Voir le rapport** pour obtenir un rapport à envoyer à votre opérateur. Pour en faire un PDF : Ctrl + P, puis « Enregistrer au format PDF ».

### Où est enregistré le rapport ?

Le rapport est enregistré automatiquement chaque minute, puis une dernière fois à l'arrêt, dans un dossier **`Rapports Test Internet` créé à côté de l'exe**. Par exemple, si l'exe est dans Téléchargements : `Téléchargements\Rapports Test Internet\rapport-internet_2026-09-29_14h02.html`.

- Si ce dossier n'est pas accessible en écriture, le rapport va dans `Documents\Rapports Test Internet`.
- Le chemin exact s'affiche en bas de la page, et le bouton **📁 Ouvrir le dossier du rapport** l'ouvre directement dans l'Explorateur.
- Chaque lancement crée un nouveau fichier : l'heure de démarrage est dans le nom.
- Le fichier `parametres.json`, dans le même dossier, garde la liste des appareils surveillés.

## Tester la communication avec d'autres appareils du réseau

Dans la section **Vos appareils sur le réseau**, ajoutez une imprimante, un NAS, une caméra ou un autre ordinateur (nom + adresse IP ou nom réseau). Le programme vérifie toutes les 5 secondes que la communication entre **cet ordinateur** et chaque appareil passe. Pour chaque appareil, il affiche son état, ses coupures et une frise de la dernière heure. Les coupures apparaissent aussi dans le journal et dans le rapport.

- Le test se fait par ping, et en parallèle sur des ports courants (partage Windows, web, imprimante, SSH, bureau à distance…). Un appareil qui bloque le ping est donc quand même détecté.
- Un appareil qui n'a **jamais** répondu n'est pas compté comme coupé : l'adresse est peut-être fausse. La page le signale.
- Si cet ordinateur perd lui-même le réseau, les appareils sont marqués « non testable » plutôt que coupés.

**Entre deux ordinateurs Windows** : lancez Test Internet sur les deux, et ajoutez sur chacun l'adresse de l'autre (elle s'affiche dans l'encadré « Tester entre deux ordinateurs »). Windows bloque par défaut les tests venant d'un autre PC. Cochez donc **« Permettre aux autres ordinateurs de tester celui-ci »** sur les deux, et acceptez le message du pare-feu Windows. Le programme répond alors sur le port TCP 47801 : il accepte la connexion et la ferme aussitôt, sans échanger aucune donnée.

## Ce qu'affiche la page

- **Un voyant** : ✅ tout va bien, ⚠️ lent ou instable, ❌ coupure. Il est accompagné d'une explication et de « 👉 Que faire ? ».
- **Le chemin de la connexion** : 💻 Ordinateur → 📡 Box → 🌍 Internet → 📰 Sites web. Chaque **liaison** entre deux éléments affiche son état en direct (communication OK, lente ou coupée ✂️), avec son nombre de coupures et le temps total coupé.
- **La dernière heure, liaison par liaison** : une frise par liaison, avec une case par minute. On voit tout de suite *entre quels éléments* la communication a été coupée, et quand.
- **Le journal des problèmes** : l'heure, la durée, l'explication et le conseil pour chaque incident.

## Comment le diagnostic est fait

Toutes les 5 secondes, en parallèle :

| Test | Comment | Ce que ça vérifie |
|---|---|---|
| Box | ping de la passerelle par défaut (trouvée via `GetBestRoute`) | liaison Ordinateur ↔ Box |
| Internet | ping de 1.1.1.1, 8.8.8.8 et 9.9.9.9 ; si le ping est bloqué, connexion TCP sur le port 443 | liaison Box ↔ Internet, pertes, temps de réponse |
| DNS | résolution de 3 noms avec le résolveur Go (il n'utilise pas le cache Windows) | l'« annuaire » d'Internet |
| Web | pages de test Microsoft (`connecttest.txt`) et Google (`generate_204`) | liaison Internet ↔ Sites web, portail captif |
| Wi-Fi | `netsh wlan show interfaces` (toutes les 15 s) | force du signal |

Le programme remonte ensuite la chaîne. Le premier maillon qui ne répond plus indique l'endroit de la coupure :

| Observation | Diagnostic | Liaison en cause |
|---|---|---|
| pas de passerelle | aucun réseau (Wi‑Fi coupé, câble débranché) | Ordinateur ↔ Box |
| box muette + Internet muet | problème chez vous (ou Wi‑Fi trop faible si signal < 40 %) | Ordinateur ↔ Box |
| box OK + Internet muet | problème chez l'opérateur | Box ↔ Internet |
| box qui ne répond jamais au ping + Internet muet | indéterminé (certaines box ignorent le ping) | l'une des deux |
| Internet OK + DNS muet | annuaire (DNS) en panne | Internet ↔ Sites web |
| Internet OK + pages web KO (2 fois de suite) | pages bloquées ou portail captif | Internet ↔ Sites web |
| pertes ≥ 10 % sur 2 min | connexion instable | Box ↔ Internet |
| temps de réponse médian ≥ 150 ms | connexion lente | Box ↔ Internet |

## Pour le développeur

```sh
go test ./...          # tests
go run . -demo         # interface avec des coupures simulées (Linux/macOS/Windows)
./build.sh             # compile TestInternet.exe pour Windows
```

Options : `-demo` (simulation), `-no-browser`, `-out <dossier>` (emplacement du rapport).

La page est servie sur `http://127.0.0.1:47800`. Si on relance l'exe alors qu'il tourne déjà, il ouvre simplement la page existante.

À chaque push, GitHub Actions teste et compile l'exe (workflow `Test Internet`), puis le publie dans la release `test-internet` (remplacée à chaque fois).

| Fichier | Rôle |
|---|---|
| `main.go` | démarrage, console, ouverture du navigateur |
| `probe.go` | les tests réseau |
| `monitor.go` | diagnostic, incidents, liaisons, statistiques |
| `device.go` | appareils du réseau local : test et suivi |
| `settings.go` | paramètres enregistrés, mode « répondeur » (port 47801) |
| `texts.go` | tous les textes affichés à l'utilisateur |
| `server.go` | API locale pour la page |
| `report.go` | rapport HTML (avec graphique) |
| `sys_windows.go` | ping ICMP Windows, passerelle, Wi‑Fi |
| `sys_other.go` | équivalents Linux/macOS pour le développement |
| `web/index.html` | l'interface (intégrée dans l'exe) |
