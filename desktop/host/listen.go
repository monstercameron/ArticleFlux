package host

import (
	"fmt"
	"net"
	"strconv"
)

// devServerPort is the port `./scripts/make.ps1 dev` serves on and the tab Cam
// keeps open. The desktop app must never take it: it would either fail to
// start while the dev server runs or, worse, start first and be the thing that
// tab is showing.
const devServerPort = 9000

// Listen opens the reader's socket on IPv4 loopback.
//
// # Why the same port every launch, when it can be had
//
// The page's ORIGIN includes the port, and the browser keys everything
// per-device by origin: the client's localStorage (the interface language and
// every other per-device choice is stored there, deliberately, because the
// server never sees it), the Service Worker and its cache of the wasm module,
// the cookie jar. A fresh ephemeral port on every launch would make each one a
// stranger's first visit — a language picker that forgets, and a new 24 MB
// cache entry in the WebView profile per launch that nothing ever evicts.
//
// So the first launch takes an ephemeral port from the OS and the shell
// remembers it; later launches ask for that one back and fall back to a fresh
// ephemeral port only when something else holds it.
//
// # Why loopback, and IPv4 specifically
//
// The server runs with no login (DevMode, the one-user mode), so the socket
// must not be reachable from the network. 127.0.0.1 rather than "localhost"
// because the name can resolve to ::1 first, and the gate compares the Host
// header to one exact string.
func Listen(preferred int) (net.Listener, error) {
	if preferred > 0 && preferred < 65536 && preferred != devServerPort {
		if ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(preferred))); err == nil {
			return ln, nil
		}
	}
	// The OS's ephemeral range on Windows starts at 49152, so 9000 cannot come
	// back from port 0 there; the loop is for a machine whose range was
	// reconfigured, where a refusal costs one more syscall.
	for range 4 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("opening a loopback port: %w", err)
		}
		if Port(ln) != devServerPort {
			return ln, nil
		}
		_ = ln.Close()
	}
	return nil, fmt.Errorf("the OS kept handing out port %d, which belongs to the dev server", devServerPort)
}

// Port is the TCP port a listener is bound to.
func Port(ln net.Listener) int {
	if a, ok := ln.Addr().(*net.TCPAddr); ok {
		return a.Port
	}
	return 0
}

// OriginFor is the page origin the window loads, and therefore the one
// string the gate accepts in Host and Origin.
func OriginFor(port int) string {
	return "http://" + HostFor(port)
}

// HostFor is the Host header that origin produces.
func HostFor(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}
