// Test Internet : surveille la connexion en continu et explique simplement
// d'où viennent les coupures.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"
)

const (
	appID   = "testinternet"
	appPort = 47800
)

func main() {
	demo := flag.Bool("demo", false, "simule des coupures pour découvrir l'interface")
	noBrowser := flag.Bool("no-browser", false, "ne pas ouvrir le navigateur")
	outDir := flag.String("out", "", "dossier où enregistrer le rapport")
	flag.Parse()

	fmt.Println("==============================================")
	fmt.Println("  TEST INTERNET")
	fmt.Println("==============================================")

	url := fmt.Sprintf("http://127.0.0.1:%d/", appPort)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", appPort))
	if err != nil {
		if alreadyRunning(url) {
			fmt.Println("Le test est déjà lancé : ouverture de la page…")
			openBrowser(url)
			time.Sleep(3 * time.Second)
			return
		}
		if ln, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			fail("Impossible de démarrer : %v", err)
		}
		url = fmt.Sprintf("http://%s/", ln.Addr())
	}

	var prober Prober = newRealProber()
	interval := 5 * time.Second
	if *demo {
		prober, interval = newDemoProber(), time.Second
	}
	mon := NewMonitor(prober, interval)
	rep := newReporter(mon, *outDir)
	a := &app{mon: mon, rep: rep, store: newSettingsStore(rep.Dir()), resp: &responder{}, demo: *demo}
	if *demo {
		mon.AddDevice(Device{ID: newDeviceID(), Name: "NAS", Addr: "192.168.1.10"})
		mon.AddDevice(Device{ID: newDeviceID(), Name: "Imprimante du bureau", Addr: "192.168.1.20"})
	} else {
		st := a.store.Load()
		for _, d := range st.Devices {
			mon.AddDevice(d)
		}
		if st.Responder {
			a.resp.Set(true)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	go mon.Run(ctx)
	go rep.AutoSave(ctx, time.Minute)

	var once sync.Once
	done := make(chan struct{})
	a.quit = func() { once.Do(func() { close(done) }) }
	go func() {
		if err := http.Serve(ln, newHandler(a)); err != nil {
			fail("Erreur du serveur : %v", err)
		}
	}()

	fmt.Println()
	fmt.Println("Le test de votre connexion est en cours.")
	fmt.Println("La page de résultats s'ouvre dans votre navigateur.")
	fmt.Println("Si elle ne s'ouvre pas, tapez cette adresse :", url)
	fmt.Println()
	fmt.Println("NE FERMEZ PAS CETTE FENÊTRE tant que le test doit continuer.")
	fmt.Println("Vous pouvez la réduire et utiliser votre ordinateur normalement.")
	fmt.Println()
	if !*noBrowser {
		openBrowser(url)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	select {
	case <-done:
	case <-sig:
	}
	cancel()
	if p, err := rep.Save(); err == nil {
		fmt.Println("Test arrêté. Le rapport est enregistré ici :")
		fmt.Println("  ", p)
	} else {
		fmt.Println("Test arrêté. Le rapport n'a pas pu être enregistré :", err)
	}
	time.Sleep(2 * time.Second)
}

func alreadyRunning(url string) bool {
	c := http.Client{Timeout: time.Second}
	resp, err := c.Get(url + "api/ping")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	return string(b) == appID
}

func fail(format string, a ...any) {
	fmt.Printf(format+"\n", a...)
	fmt.Println("Appuyez sur Entrée pour fermer.")
	bufio.NewReader(os.Stdin).ReadString('\n')
	os.Exit(1)
}
