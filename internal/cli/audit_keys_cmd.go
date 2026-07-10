package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/govern"
)

var auditKeygenCmd = &cobra.Command{
	Use:   "keygen",
	Short: "Generate an Ed25519 signing keypair for audit co-signing",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		privB64, pubB64, err := govern.GenerateSigningKeypair()
		if err != nil {
			return err
		}
		fp, err := fingerprint(pubB64)
		if err != nil {
			return fmt.Errorf("generated public key failed to decode (unexpected): %w", err)
		}
		out := cmd.OutOrStdout()
		fmt.Fprintln(out, "Ed25519 keypair generated. Set the private key as an env var to enable audit signing:")
		fmt.Fprintln(out)
		fmt.Fprintf(out, "  export DANDORI_AUDIT_SIGNING_KEY=%s\n", privB64)
		fmt.Fprintln(out)
		fmt.Fprintln(out, "SAVE THIS NOW — it will not be shown again. Store it in .env (gitignored) or a secret manager, never in config.yaml.")
		fmt.Fprintln(out)
		fmt.Fprintf(out, "Public key (share out-of-band for verification, e.g. pin it in an auditor's runbook):\n  %s\n", pubB64)
		fmt.Fprintf(out, "Public key fingerprint (sha256, hex): %s\n", fp)
		return nil
	},
}

var auditPubkeyCmd = &cobra.Command{
	Use:   "pubkey",
	Short: "Print the configured audit signing public key and its fingerprint",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := loadConfig(); err != nil { // populates env from .env/config for PublicKeyFromSigningKey
			return err
		}
		pubB64, ok := govern.PublicKeyFromSigningKey()
		if !ok {
			return fmt.Errorf("no audit signing key configured (DANDORI_AUDIT_SIGNING_KEY unset) — run `dandori audit keygen` first")
		}
		fp, err := fingerprint(pubB64)
		if err != nil {
			return fmt.Errorf("configured public key is not valid base64: %w", err)
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "public key: %s\n", pubB64)
		fmt.Fprintf(out, "fingerprint (sha256, hex): %s\n", fp)
		return nil
	},
}

// fingerprint is a thin wrapper over govern.PubkeyFingerprint — kept as a
// package-local name since call sites in this file predate the export
// package needing the same hash, and CLI output strings reference
// "fingerprint" directly.
func fingerprint(pubB64 string) (string, error) {
	return govern.PubkeyFingerprint(pubB64)
}

func init() {
	auditCmd.AddCommand(auditKeygenCmd, auditPubkeyCmd)
}
