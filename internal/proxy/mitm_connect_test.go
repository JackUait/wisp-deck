package proxy

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// dialMITM performs a CONNECT to the proxy for `host`, then completes a TLS
// handshake against the proxy's MITM leaf (trusting the local CA) and returns
// the decrypted connection — mirroring what claude does under HTTPS_PROXY +
// NODE_EXTRA_CA_CERTS.
func dialMITM(t *testing.T, proxyAddr, caPath, host string) *tls.Conn {
	t.Helper()
	raw, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	// Authenticate the CONNECT the way claude does under HTTPS_PROXY with
	// embedded credentials: Proxy-Authorization: Basic base64("wisp-deck:key").
	auth := base64.StdEncoding.EncodeToString([]byte("wisp-deck:proxy-key"))
	connect := "CONNECT " + host + ":443 HTTP/1.1\r\nHost: " + host + ":443\r\n" +
		"Proxy-Authorization: Basic " + auth + "\r\n\r\n"
	if _, err := raw.Write([]byte(connect)); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	br := bufio.NewReader(raw)
	status, err := br.ReadString('\n')
	if err != nil || !strings.Contains(status, "200") {
		t.Fatalf("CONNECT response = %q err=%v", status, err)
	}
	// Drain the rest of the CONNECT response headers (until blank line).
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read CONNECT headers: %v", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	tlsConn := tls.Client(raw, &tls.Config{RootCAs: pool, ServerName: host})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("MITM TLS handshake: %v", err)
	}
	return tlsConn
}

func TestMITM_injectsTokenOverTunnel(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
		io.WriteString(w, "mitm-ok")
	}))
	defer upstream.Close()

	mgr := NewManager([]Account{{Label: "A", AccessToken: "tok-A"}, {Label: "B", AccessToken: "tok-B"}}, 0.98)
	srv := NewServer(mgr, "proxy-key", upstream.URL, WithSleep(func(time.Duration) {}))
	// The intercepted host (what claude CONNECTs to) is decoupled from where we
	// actually forward (the fake upstream). Intercept the real DNS name; the leaf
	// is minted for it, so the client's TLS verify succeeds.
	host := "api.anthropic.com"
	srv.mitmHost = host
	caPath, err := srv.EnableMITM(t.TempDir())
	if err != nil {
		t.Fatalf("EnableMITM: %v", err)
	}

	proxy := httptest.NewServer(srv)
	defer proxy.Close()
	proxyAddr := strings.TrimPrefix(proxy.URL, "http://")

	conn := dialMITM(t, proxyAddr, caPath, host)
	defer conn.Close()

	req, _ := http.NewRequest(http.MethodPost, "https://"+host+"/v1/messages", strings.NewReader("{}"))
	if err := req.Write(conn); err != nil {
		t.Fatalf("write request: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "mitm-ok" {
		t.Fatalf("got (%d, %q), want (200, mitm-ok)", resp.StatusCode, string(body))
	}
	if gotAuth != "Bearer tok-A" {
		t.Errorf("upstream Authorization = %q, want Bearer tok-A (injected over tunnel)", gotAuth)
	}
}

func TestMITM_testHostAnsweredLocally(t *testing.T) {
	mgr := NewManager([]Account{{AccessToken: "tok-A"}, {AccessToken: "tok-B"}}, 0.98)
	srv := NewServer(mgr, "proxy-key", "https://api.anthropic.com", WithSleep(func(time.Duration) {}))
	caPath, err := srv.EnableMITM(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(srv)
	defer proxy.Close()

	conn := dialMITM(t, strings.TrimPrefix(proxy.URL, "http://"), caPath, TestHost)
	defer conn.Close()

	req, _ := http.NewRequest(http.MethodGet, "https://"+TestHost+"/", nil)
	req.Write(conn)
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "OK") {
		t.Errorf("test host = (%d, %q), want a local 200 OK", resp.StatusCode, string(body))
	}
}
