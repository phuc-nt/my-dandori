package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/ingest"
)

// connectCmd points this machine's hooks at a central Dandori server. The
// token lands in a mode-0600 file — it is a secret and must never be pasted
// into config.yaml or committed anywhere.
var connectCmd = &cobra.Command{
	Use:   "connect <server-url>",
	Short: "Connect this machine's hooks to a central Dandori server",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagConnectToken == "" {
			flagConnectToken = os.Getenv("DANDORI_INGEST_TOKEN")
		}
		if flagConnectToken == "" {
			return fmt.Errorf("--token (or DANDORI_INGEST_TOKEN) is required")
		}
		url := strings.TrimRight(args[0], "/")
		b, err := yaml.Marshal(map[string]string{"server_url": url, "token": flagConnectToken})
		if err != nil {
			return err
		}
		path := config.ConnectFile()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return err
		}
		fmt.Printf("connected: hooks on this machine now report to %s\n", url)
		fmt.Println("(remove", path, "to return to local mode)")
		return nil
	},
}

// relayCmd flushes the offline spool manually.
var relayCmd = &cobra.Command{
	Use:   "relay",
	Short: "Flush spooled events to the central server now",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		if !ingest.Enabled(cfg) {
			return fmt.Errorf("not connected — run `dandori connect <url> --token X` first")
		}
		if err := ingest.NewClient(cfg).Flush(); err != nil {
			return fmt.Errorf("relay failed (events stay spooled): %w", err)
		}
		fmt.Println("spool flushed")
		return nil
	},
}

var flagConnectToken string

func init() {
	connectCmd.Flags().StringVar(&flagConnectToken, "token", "", "ingest token issued by the server operator")
	rootCmd.AddCommand(connectCmd, relayCmd)
}
