package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/jackuait/wisp-deck/internal/proxy"
)

var (
	proxyAccountsDir    string
	proxyListFile       string
	proxyThreshold      float64
	proxyPort           int
	proxyUpstream       string
	proxyMITM           bool
	proxyCertDir        string
	proxyActiveAcctFile string
)

var proxyCmd = &cobra.Command{
	Use:    "proxy",
	Short:  "Run the account-rotation proxy",
	Long:   "Runs a local reverse proxy that injects a rotating Claude account's OAuth token, switching accounts as quota is exhausted. Prints {\"port\":N,\"key\":\"...\"} on startup.",
	Hidden: true,
	RunE:   runProxy,
}

func init() {
	proxyCmd.Flags().StringVar(&proxyAccountsDir, "accounts-dir", "", "directory holding per-account credential dirs")
	proxyCmd.Flags().StringVar(&proxyListFile, "list", "", "path to the claude-accounts.list file (label:dir lines)")
	proxyCmd.Flags().Float64Var(&proxyThreshold, "threshold", 0.98, "utilization (0-1) at which to switch accounts")
	proxyCmd.Flags().IntVar(&proxyPort, "port", 0, "listen port (0 picks a free port)")
	proxyCmd.Flags().StringVar(&proxyUpstream, "upstream", "https://api.anthropic.com", "upstream Anthropic base URL")
	proxyCmd.Flags().BoolVar(&proxyMITM, "mitm", true, "enable the CONNECT/MITM forward proxy (like teamclaude's default); --mitm=false uses base-URL mode only")
	proxyCmd.Flags().StringVar(&proxyCertDir, "cert-dir", "", "directory for the MITM CA/leaf certs (defaults to the accounts dir's parent)")
	proxyCmd.Flags().StringVar(&proxyActiveAcctFile, "active-account-file", "", "path to persist the currently-serving account's dir name (for the status line)")
	rootCmd.AddCommand(proxyCmd)
}

// proxyStartupJSON renders the startup line bash reads to learn the port, key,
// and (in MITM mode) the CA cert path clients trust via NODE_EXTRA_CA_CERTS.
func proxyStartupJSON(port int, key, ca string) string {
	b, _ := json.Marshal(struct {
		Port int    `json:"port"`
		Key  string `json:"key"`
		CA   string `json:"ca,omitempty"`
	}{port, key, ca})
	return string(b) + "\n"
}

// generateProxyKey returns a random local proxy key claude authenticates with.
func generateProxyKey() string {
	buf := make([]byte, 18)
	_, _ = rand.Read(buf)
	return "wd-" + base64.RawURLEncoding.EncodeToString(buf)
}

func runProxy(cmd *cobra.Command, args []string) error {
	accounts, err := proxy.LoadAccounts(proxyAccountsDir, proxyListFile)
	if err != nil {
		return fmt.Errorf("load accounts: %w", err)
	}
	if len(accounts) < 2 {
		return fmt.Errorf("account rotation needs at least 2 accounts with credentials, found %d", len(accounts))
	}

	mgr := proxy.NewManager(accounts, proxyThreshold)
	mgr.SelectBest(time.Now())
	key := generateProxyKey()
	srv := proxy.NewServer(mgr, key, proxyUpstream,
		proxy.WithAccountsDir(proxyAccountsDir),
		proxy.WithActiveAccountFile(proxyActiveAcctFile))

	// Seed the active-account file with the initial pick so the status line shows
	// the starting account before the first request triggers any rotation.
	if proxyActiveAcctFile != "" {
		active := mgr.Active()
		if active.Dir != "" {
			_ = os.WriteFile(proxyActiveAcctFile, []byte(active.Dir+"\n"), 0o644)
		}
	}

	// Enable the CONNECT/MITM forward proxy by default (teamclaude's default
	// mode), so even hardcoded api.anthropic.com endpoints get the injected
	// token. The CA is trusted per-process via NODE_EXTRA_CA_CERTS.
	caPath := ""
	if proxyMITM {
		certDir := proxyCertDir
		if certDir == "" {
			certDir = filepath.Dir(proxyAccountsDir)
		}
		var err error
		caPath, err = srv.EnableMITM(certDir)
		if err != nil {
			return fmt.Errorf("enable mitm: %w", err)
		}
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	// Announce the port + key (+ CA path) so the launcher can point claude at us.
	fmt.Fprint(os.Stdout, proxyStartupJSON(port, key, caPath))
	os.Stdout.Sync()

	httpSrv := &http.Server{Handler: srv}
	return httpSrv.Serve(ln)
}
