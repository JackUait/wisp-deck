package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/jackuait/wisp-deck/internal/proxy"
)

var (
	proxyAccountsDir string
	proxyListFile    string
	proxyThreshold   float64
	proxyPort        int
	proxyUpstream    string
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
	rootCmd.AddCommand(proxyCmd)
}

// proxyStartupJSON renders the startup line bash reads to learn the port + key.
func proxyStartupJSON(port int, key string) string {
	b, _ := json.Marshal(struct {
		Port int    `json:"port"`
		Key  string `json:"key"`
	}{port, key})
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
	srv := proxy.NewServer(mgr, key, proxyUpstream, proxy.WithAccountsDir(proxyAccountsDir))

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	// Announce the port + key so the launcher can point claude at us, then serve.
	fmt.Fprint(os.Stdout, proxyStartupJSON(port, key))
	os.Stdout.Sync()

	httpSrv := &http.Server{Handler: srv}
	return httpSrv.Serve(ln)
}
