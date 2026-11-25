package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/config"
	"github.com/antresol-app/antresol/internal/db"
)

// serviceProfileCommand is `vault service-profile`: the only way to make,
// re-key or remove a service profile, a machine's profile that is in no list
// of people and keeps no history (auth.Store.CreateService). It opens the
// server's database (VAULT_DATA_DIR, which must already hold one) and starts
// nothing else: run it only with a build that has it, as any other command
// of this binary starts a whole server.
//
//	vault service-profile create NAME          make it; prints id= and password=
//	vault service-profile reset-password NAME  a new password (ends its sessions)
//	vault service-profile delete NAME          remove it
//
// The password is printed once, to stdout, as `id=N` and `password=…` lines;
// it is a long random string, only stored hashed.
const serviceProfileCommand = "service-profile"

const serviceProfileUsage = "usage: vault " + serviceProfileCommand + " create|reset-password|delete NAME"

func runServiceProfile(args []string, out io.Writer) error {
	if len(args) != 2 {
		return errors.New(serviceProfileUsage)
	}
	action, name := args[0], args[1]
	if action != "create" && action != "reset-password" && action != "delete" {
		return errors.New(serviceProfileUsage)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	file := filepath.Join(cfg.DataDir, "vault.db")
	if _, err := os.Stat(file); err != nil {
		return fmt.Errorf("no database at %s (VAULT_DATA_DIR): %w", file, err)
	}
	database, err := db.Open(file)
	if err != nil {
		return err
	}
	defer database.Close()
	store, ctx := auth.NewStore(database), context.Background()
	switch action {
	case "create", "reset-password":
		var u auth.User
		var pw string
		if action == "create" {
			u, pw, err = store.CreateService(ctx, name)
		} else {
			u, pw, err = store.ResetServicePassword(ctx, name)
		}
		if err != nil {
			return serviceProfileErr(name, err)
		}
		fmt.Fprintf(out, "id=%d\npassword=%s\n", u.ID, pw)
	case "delete":
		if err := store.DeleteService(ctx, name); err != nil {
			return serviceProfileErr(name, err)
		}
		fmt.Fprintf(out, "service profile %q removed\n", name)
	}
	return nil
}

func serviceProfileErr(name string, err error) error {
	switch {
	case errors.Is(err, auth.ErrExists):
		return fmt.Errorf("the name %q is taken (by a profile of a person or a service profile): reset-password gives a service profile a new password", name)
	case errors.Is(err, auth.ErrNotFound):
		return fmt.Errorf("no service profile called %q", name)
	case errors.Is(err, auth.ErrInvalid):
		return fmt.Errorf("%q is not a name (1 to 64 characters)", name)
	}
	return err
}
