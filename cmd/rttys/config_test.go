package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/urfave/cli/v3"
	"github.com/zhaojh329/rttys/v5/internal/server"
)

func TestIdentityConfigAndCLIOverride(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, "identity.db")
	secret := filepath.Join(root, "password")
	config := filepath.Join(root, "rttys.conf")
	if err := os.WriteFile(config, []byte(fmt.Sprintf("tenant-db: %s\nbootstrap-password-file: %s\npprof-token-file: %s\n", db, secret, secret)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, override := range []bool{false, true} {
		cfg := new(server.Config)
		cmd := &cli.Command{
			Flags:  []cli.Flag{&cli.StringFlag{Name: "conf"}, &cli.StringFlag{Name: "tenant-db"}, &cli.StringFlag{Name: "bootstrap-password-file"}, &cli.StringFlag{Name: "pprof-token-file"}},
			Action: func(_ context.Context, cmd *cli.Command) error { return parseConfig(cmd, cfg) },
		}
		args := []string{"rttys", "--conf", config}
		want := db
		if override {
			want = filepath.Join(root, "override.db")
			args = append(args, "--tenant-db", want)
		}
		if err := cmd.Run(context.Background(), args); err != nil {
			t.Fatal(err)
		}
		if cfg.TenantDB != want || cfg.BootstrapPasswordFile != secret || cfg.PprofTokenFile != secret {
			t.Fatalf("config not loaded or precedence lost: %+v", cfg)
		}
	}
}
