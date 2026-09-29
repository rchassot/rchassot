//go:build windows

package main

import (
	"encoding/binary"
	"errors"
	"net"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

var (
	iphlpapi            = syscall.NewLazyDLL("iphlpapi.dll")
	procIcmpCreateFile  = iphlpapi.NewProc("IcmpCreateFile")
	procIcmpCloseHandle = iphlpapi.NewProc("IcmpCloseHandle")
	procIcmpSendEcho    = iphlpapi.NewProc("IcmpSendEcho")
	procGetBestRoute    = iphlpapi.NewProc("GetBestRoute")
)

// ipAddr convertit une IPv4 dans le format IPAddr de Windows (ordre réseau).
func ipAddr(ip string) (uint32, bool) {
	v4 := net.ParseIP(ip).To4()
	if v4 == nil {
		return 0, false
	}
	return binary.LittleEndian.Uint32(v4), true
}

// ping utilise l'API ICMP de Windows, qui ne demande pas de droits administrateur.
func ping(ip string, timeout time.Duration) (bool, float64) {
	addr, ok := ipAddr(ip)
	if !ok {
		return false, 0
	}
	h, _, _ := procIcmpCreateFile.Call()
	if h == 0 || h == uintptr(syscall.InvalidHandle) {
		return false, 0
	}
	defer procIcmpCloseHandle.Call(h)

	data := []byte("TestInternet")
	reply := make([]byte, 256) // ICMP_ECHO_REPLY + données + marge
	n, _, _ := procIcmpSendEcho.Call(h, uintptr(addr),
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 0,
		uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)),
		uintptr(timeout.Milliseconds()))
	if n == 0 {
		return false, 0
	}
	// ICMP_ECHO_REPLY : Address (4 octets), Status (4), RoundTripTime (4), …
	if status := binary.LittleEndian.Uint32(reply[4:8]); status != 0 {
		return false, 0
	}
	return true, float64(binary.LittleEndian.Uint32(reply[8:12]))
}

// defaultGateway demande à Windows par où il passerait pour joindre Internet :
// l'adresse obtenue est celle de la box.
func defaultGateway() string {
	dest, _ := ipAddr("8.8.8.8")
	var row [14]uint32 // MIB_IPFORWARDROW
	ret, _, _ := procGetBestRoute.Call(uintptr(dest), 0, uintptr(unsafe.Pointer(&row[0])))
	if ret != 0 || row[3] == 0 {
		return ""
	}
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], row[3]) // dwForwardNextHop
	return net.IP(b[:]).String()
}

// wifiInfo lit le nom et la force du Wi-Fi via netsh (nil si pas de Wi-Fi).
func wifiInfo() *WifiInfo {
	out, err := hiddenCmd("netsh", "wlan", "show", "interfaces").Output()
	if err != nil {
		return nil
	}
	return parseNetshWlan(string(out))
}

func isConnRefused(err error) bool {
	return errors.Is(err, syscall.Errno(10061)) // WSAECONNREFUSED
}

func openBrowser(url string) {
	_ = hiddenCmd("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// hiddenCmd lance une commande sans faire clignoter de fenêtre noire.
func hiddenCmd(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return c
}

// openFolder ouvre l'Explorateur Windows avec le fichier sélectionné.
func openFolder(path string) {
	_ = exec.Command("explorer", "/select,", path).Start()
}
