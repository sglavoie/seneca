package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var openCmd = &cobra.Command{
	Use:   "open",
	Short: "Open the folder holding the journal in the configured editor",
	Args:  cobra.NoArgs,
	RunE:  runOpen,
}

func init() {
	rootCmd.AddCommand(openCmd)
}

func runOpen(cmd *cobra.Command, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}
	target, err := resolveOpenTarget()
	if err != nil {
		return err
	}
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("target directory not accessible: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("target is not a directory: %s", target)
	}
	parts, err := loadEditorCommand(home)
	if err != nil {
		return err
	}
	fmt.Printf("Opening %s with %s\n", target, strings.Join(parts, " "))
	parts = append(parts, target)
	c := exec.Command(parts[0], parts[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

func resolveOpenTarget() (string, error) {
	reflectionsPath, err := resolveFilePath()
	if err != nil {
		return "", err
	}
	return journalDirForReflectionsPath(reflectionsPath), nil
}

func journalDirForReflectionsPath(reflectionsPath string) string {
	return filepath.Dir(reflectionsPath)
}

func loadEditorCommand(home string) ([]string, error) {
	cfgPath := configFilePath(home)
	if cfg, err := loadConfigFile(home); err == nil && len(cfg.Editor.Command) > 0 {
		return append([]string(nil), cfg.Editor.Command...), nil
	}
	if v := os.Getenv("VISUAL"); v != "" {
		if parts := strings.Fields(v); len(parts) > 0 {
			return parts, nil
		}
	}
	if v := os.Getenv("EDITOR"); v != "" {
		if parts := strings.Fields(v); len(parts) > 0 {
			return parts, nil
		}
	}
	return nil, fmt.Errorf("no editor configured: set [editor].command in %s or $VISUAL/$EDITOR", cfgPath)
}
