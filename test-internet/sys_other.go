//go:build !windows

// Version pour Linux/macOS, utile pour développer et tester. Le programme est
// prévu pour Windows (voir sys_windows.go).

package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// ping sans droits root : on tente une connexion TCP ; une réponse, même un
// refus, prouve que la machine est joignable.
func ping(ip string, timeout time.Duration) (bool, float64) {
	return tcpPing(context.Background(), net.JoinHostPort(ip, "80"), timeout)
}

func defaultGateway() string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 3 || fs[1] != "00000000" {
			continue
		}
		b, err := hex.DecodeString(fs[2])
		if err != nil || len(b) != 4 {
			continue
		}
		var ip [4]byte
		binary.LittleEndian.PutUint32(ip[:], binary.BigEndian.Uint32(b))
		return net.IP(ip[:]).String()
	}
	return ""
}

func wifiInfo() *WifiInfo { return nil }

func isConnRefused(err error) bool { return errors.Is(err, syscall.ECONNREFUSED) }

func openBrowser(url string) {
	cmd := "xdg-open"
	if runtime.GOOS == "darwin" {
		cmd = "open"
	}
	_ = exec.Command(cmd, url).Start()
}

func openFolder(path string) {
	cmd := "xdg-open"
	if runtime.GOOS == "darwin" {
		cmd = "open"
	}
	_ = exec.Command(cmd, filepath.Dir(path)).Start()
}
