package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/phuc-nt/dandori/internal/auth"
)

// operatorCmd manages console login accounts (username+password+role),
// layered onto the same operators table used for machine-principal
// attribution. Canonical id = username (H3) — distinct from the
// alice@dev-laptop rows ResolveOperator auto-creates for capture.
var operatorCmd = &cobra.Command{
	Use:   "operator",
	Short: "Manage console login accounts (bootstrap, password, disable)",
}

// validRoles is the in-app allowlist (M2: no CHECK constraint on the role
// column — validated here instead).
var validRoles = map[string]bool{"admin": true, "viewer": true}

var flagOperatorRole string
var flagOperatorPassword string // escape hatch for scripting/tests; empty = interactive prompt

var operatorAddCmd = &cobra.Command{
	Use:   "add <username>",
	Short: "Create a console login account",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		username := args[0]
		if !validRoles[flagOperatorRole] {
			return fmt.Errorf("--role must be admin or viewer, got %q", flagOperatorRole)
		}
		password, err := resolvePassword(cmd, flagOperatorPassword, true)
		if err != nil {
			return err
		}
		hash, err := auth.HashPassword(password)
		if err != nil {
			return fmt.Errorf("hash password: %w", err)
		}
		_, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.CreateOperatorAccount(username, username, hash, flagOperatorRole); err != nil {
			return fmt.Errorf("create operator %q: %w", username, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "operator %q created (role=%s)\n", username, flagOperatorRole)
		return nil
	},
}

var operatorSetPasswordCmd = &cobra.Command{
	Use:   "set-password <username>",
	Short: "Change an operator's password (invalidates their existing sessions)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		username := args[0]
		password, err := resolvePassword(cmd, flagOperatorPassword, true)
		if err != nil {
			return err
		}
		hash, err := auth.HashPassword(password)
		if err != nil {
			return fmt.Errorf("hash password: %w", err)
		}
		_, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.SetPassword(username, hash); err != nil {
			return fmt.Errorf("set password for %q: %w", username, err)
		}
		// [H4] Kill every existing session for this operator immediately —
		// a leaked old password/session must not survive a reset.
		if err := auth.NewSessionStore(st).DeleteForOperator(username); err != nil {
			return fmt.Errorf("invalidate sessions for %q: %w", username, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "password updated for %q; existing sessions invalidated\n", username)
		return nil
	},
}

var operatorDisableCmd = &cobra.Command{
	Use:   "disable <username>",
	Short: "Disable an operator's login account (off-board)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		username := args[0]
		_, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.DisableOperator(username); err != nil {
			return fmt.Errorf("disable %q: %w", username, err)
		}
		// [H4] Off-boarding must end active sessions immediately.
		if err := auth.NewSessionStore(st).DeleteForOperator(username); err != nil {
			return fmt.Errorf("invalidate sessions for %q: %w", username, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "operator %q disabled; sessions invalidated, ingest tokens no longer accepted\n", username)
		return nil
	},
}

var operatorListCmd = &cobra.Command{
	Use:   "list",
	Short: "List console login accounts",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		rows, err := st.DB.Query(`SELECT id, role, disabled_at FROM operators
			WHERE username IS NOT NULL ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		printed := false
		for rows.Next() {
			var id, role string
			var disabledAt *string
			if err := rows.Scan(&id, &role, &disabledAt); err != nil {
				return err
			}
			status := "active"
			if disabledAt != nil && *disabledAt != "" {
				status = "disabled"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-20s role=%-8s %s\n", id, role, status)
			printed = true
		}
		if !printed {
			fmt.Fprintln(cmd.OutOrStdout(), "no operator accounts — create one with `dandori operator add <username> --role admin`")
		}
		return rows.Err()
	},
}

// resolvePassword returns explicit (the --password flag) if set, otherwise
// prompts twice on the terminal with no echo, requiring both entries to
// match. Never logs or echoes the password.
func resolvePassword(cmd *cobra.Command, explicit string, confirm bool) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("stdin is not a terminal; pass --password explicitly for non-interactive use")
	}
	fmt.Fprint(cmd.OutOrStdout(), "Password: ")
	pw1, err := readPasswordNoEcho()
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if pw1 == "" {
		return "", fmt.Errorf("password must not be empty")
	}
	if !confirm {
		return pw1, nil
	}
	fmt.Fprint(cmd.OutOrStdout(), "\nConfirm password: ")
	pw2, err := readPasswordNoEcho()
	if err != nil {
		return "", fmt.Errorf("read password confirmation: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout())
	if pw1 != pw2 {
		return "", fmt.Errorf("passwords do not match")
	}
	return pw1, nil
}

// readPasswordNoEcho is a var so tests can stub it; production uses
// term.ReadPassword (no terminal echo).
var readPasswordNoEcho = func() (string, error) {
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func init() {
	operatorAddCmd.Flags().StringVar(&flagOperatorRole, "role", "viewer", "admin or viewer")
	operatorAddCmd.Flags().StringVar(&flagOperatorPassword, "password", "", "password (non-interactive; omit to be prompted)")
	operatorSetPasswordCmd.Flags().StringVar(&flagOperatorPassword, "password", "", "password (non-interactive; omit to be prompted)")
	operatorCmd.AddCommand(operatorAddCmd, operatorSetPasswordCmd, operatorDisableCmd, operatorListCmd)
	rootCmd.AddCommand(operatorCmd)
}
