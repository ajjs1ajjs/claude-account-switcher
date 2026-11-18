package main

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Only one switcher may run. It holds instancePort; a second start says hello
// with its version and a per-user secret stored in the data directory. The
// running one quits when the newcomer is newer and carries the matching secret
// (when one is set), so starting a new version replaces the old one.
const instancePort = "127.0.0.1:47823"

const (
	helloPrefix        = "claude-account-switcher "
	instanceSecretFile = "instance.secret"
)

func writeInstanceSecret(dir string) string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		log.Printf("instance secret: %v", err)
		return ""
	}
	secret := hex.EncodeToString(raw[:])
	if err := writeFileAtomic(filepath.Join(dir, instanceSecretFile), []byte(secret)); err != nil {
		log.Printf("write instance secret: %v", err)
		return ""
	}
	return secret
}

func readInstanceSecret(dir string) string {
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, instanceSecretFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func removeInstanceSecret(dir string) {
	if dir != "" {
		_ = os.Remove(filepath.Join(dir, instanceSecretFile))
	}
}

// lockInstance takes the single-instance port, taking it over from an older
// switcher that is running. It fails when a switcher of this version or a
// newer one is running.
func lockInstance(addr, dir string) (net.Listener, bool) {
	if l, err := net.Listen("tcp", addr); err == nil {
		return l, true
	}
	switch hello(addr, version, readInstanceSecret(dir)) {
	case "quit":
		log.Print("an older switcher is running, it is quitting")
	case "stay":
		log.Print("already running")
		return nil, false
	default:
		// Versions before 0.3.3 hold the port without answering. Ending another
		// process looks like malware to Windows Defender, so the user quits it.
		log.Print("a switcher from before 0.3.3 is running")
		askToQuitOld()
		return nil, false
	}
	// The old switcher finishes a running switch before it quits.
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if l, err := net.Listen("tcp", addr); err == nil {
			return l, true
		}
	}
	log.Print("the running switcher did not quit")
	return nil, false
}

// hello tells the switcher holding addr this version and optional secret, and
// returns its answer, "quit" or "stay", or "" when it does not answer.
func hello(addr, v, secret string) string {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	msg := helloPrefix + v
	if secret != "" {
		msg += " " + secret
	}
	if _, err := conn.Write([]byte(msg + "\n")); err != nil {
		return ""
	}
	answer, _ := bufio.NewReader(conn).ReadString('\n')
	return strings.TrimSpace(answer)
}

func secretAuthorized(expected, got string) bool {
	if expected == "" {
		return true
	}
	return len(got) == len(expected) && subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

// serveInstance answers hellos on the lock and calls quit, once, when an
// authorized newer version says hello.
func serveInstance(l net.Listener, secret string, quit func()) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		line, _ := bufio.NewReader(conn).ReadString('\n')
		rest, ok := strings.CutPrefix(line, helloPrefix)
		v, gotSecret, _ := strings.Cut(strings.TrimSpace(rest), " ")
		if ok && secretAuthorized(secret, gotSecret) && newerVersion(v, version) {
			conn.Write([]byte("quit\n"))
			conn.Close()
			log.Printf("version %s started, quitting", v)
			quit()
			return
		}
		conn.Write([]byte("stay\n"))
		conn.Close()
	}
}
