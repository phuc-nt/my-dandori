package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/auth"
)

// tokenCmd manages per-operator ingest tokens (v10). Canonical binding is
// operators.id = <username> (H3) — the same console account created by
// `dandori operator add`, distinct from the machine-principal rows
// ResolveOperator auto-creates for legacy/local capture.
var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage per-operator ingest tokens (create, list, revoke)",
}

var flagTokenName string

var tokenCreateCmd = &cobra.Command{
	Use:   "create <username>",
	Short: "Issue a new ingest token for an operator (prints the token once)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		username := args[0]
		if flagTokenName == "" {
			return fmt.Errorf("--name is required (a label for this token, e.g. the machine name)")
		}
		_, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()

		role, err := st.OperatorRole(username)
		if err != nil {
			return fmt.Errorf("look up operator %q: %w", username, err)
		}
		if role == "" {
			return fmt.Errorf("operator %q does not exist — create it first with `dandori operator add %s --role <admin|viewer>`", username, username)
		}
		found, enabled, err := st.OperatorEnabled(username)
		if err != nil {
			return fmt.Errorf("check operator %q status: %w", username, err)
		}
		if !found || !enabled {
			return fmt.Errorf("operator %q is disabled — re-enable the account before issuing new tokens", username)
		}

		plain, hash, err := auth.GenerateToken()
		if err != nil {
			return fmt.Errorf("generate token: %w", err)
		}
		if err := st.CreateToken(hash, username, flagTokenName); err != nil {
			return fmt.Errorf("save token: %w", err)
		}

		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "token created for %q (%s):\n\n", username, flagTokenName)
		fmt.Fprintf(out, "  %s\n\n", plain)
		fmt.Fprintln(out, "SAVE THIS NOW — it will not be shown again. Store it in the machine's")
		fmt.Fprintln(out, "connect.yaml (see `dandori connect --token`) or a secret manager.")
		return nil
	},
}

var tokenListCmd = &cobra.Command{
	Use:   "list <username>",
	Short: "List an operator's ingest tokens (never prints the plaintext)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		username := args[0]
		_, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()

		tokens, err := st.ListTokens(username)
		if err != nil {
			return fmt.Errorf("list tokens for %q: %w", username, err)
		}
		out := cmd.OutOrStdout()
		if len(tokens) == 0 {
			fmt.Fprintf(out, "no tokens for %q — create one with `dandori token create %s --name <label>`\n", username, username)
			return nil
		}
		for _, t := range tokens {
			status := "active"
			if t.RevokedAt != nil && *t.RevokedAt != "" {
				status = "revoked"
			}
			lastUsed := "never"
			if t.LastUsedAt != nil && *t.LastUsedAt != "" {
				lastUsed = *t.LastUsedAt
			}
			fmt.Fprintf(out, "%s  %-16s created=%s last_used=%s %s\n",
				shortID(t.ID), t.DisplayName, t.CreatedAt, lastUsed, status)
		}
		return nil
	},
}

var tokenRevokeCmd = &cobra.Command{
	Use:   "revoke <token-id>",
	Short: "Revoke a token by id (or unambiguous id prefix, as shown by `token list`)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		idOrPrefix := args[0]
		_, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()

		id := idOrPrefix
		if len(idOrPrefix) < 64 { // full hash is a 64-char hex sha256
			full, err := st.TokenByPrefix(idOrPrefix)
			if err != nil {
				return fmt.Errorf("resolve token id %q: no unambiguous match", idOrPrefix)
			}
			id = full
		}
		if err := st.RevokeToken(id); err != nil {
			return fmt.Errorf("revoke token %q: %w", idOrPrefix, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "token %s revoked\n", shortID(id))
		return nil
	},
}

// shortID prints a short, grep-friendly prefix of a token hash — full hashes
// are 64 hex chars and unwieldy in a terminal listing.
func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func init() {
	tokenCreateCmd.Flags().StringVar(&flagTokenName, "name", "", "label for this token (e.g. machine name)")
	tokenCmd.AddCommand(tokenCreateCmd, tokenListCmd, tokenRevokeCmd)
	rootCmd.AddCommand(tokenCmd)
}
