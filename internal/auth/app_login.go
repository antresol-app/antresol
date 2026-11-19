package auth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"
)

// LoginResult is the outcome of AppLogin.
type LoginResult int

const (
	LoginOK                 LoginResult = iota
	LoginBad                            // wrong password or unknown profile (a strike)
	LoginNoAppPassword                  // the profile has no app password (a strike)
	LoginThrottled                      // locked: AppLoginOutcome.Wait
	LoginRemotePasswordless             // a profile without a password, not from home
)

// AppLoginOutcome: User is set for LoginOK; Wait for LoginThrottled.
// History is the managed account whose app password matched (ID 0: the
// person's own): the app is the person signed in, and its history goes to
// that account (WithHistory).
type AppLoginOutcome struct {
	User    User
	History History
	Result  LoginResult
	Wait    time.Duration
}

// AppLogin is the login of third-party protocols (Subsonic, OPDS): the
// profile name and its app password, which check compares (in constant
// time; it never gets an empty app password). The login password is never
// accepted there. A profile without a password logs in from the home
// network only, as on the web. Failures count in the shared throttle under
// the web login's keys (ip:<client>, user:<id>:home|remote) plus app:<id>,
// so guessing here also slows the web login. A success clears only app:<id>:
// client apps log in on every request, and clearing the shared keys would
// wipe the strikes of someone guessing the profile's web password. proto
// prefixes the log lines, which carry the profile id and the client key only.
func AppLogin(ctx context.Context, users *Store, network *Network, th *Throttle, r *http.Request, proto, name string, check func(app string) bool) (AppLoginOutcome, error) {
	trusted := network.IsTrusted(r)
	client := network.ClientKey(r)
	where := "remote"
	if trusted {
		where = "home"
	}
	keys := []string{"ip:" + client}
	u, err := users.ByName(ctx, name)
	found := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return AppLoginOutcome{}, err
	}
	if found {
		keys = append(keys, fmt.Sprintf("user:%d:%s", u.ID, where), appThrottleKey(u.ID))
	}
	// Wait and Fail are not one atomic step: parallel guesses all pass Wait
	// before the first Fail. With 82-bit app passwords that buys nothing.
	if wait := th.Wait(keys...); wait > 0 {
		return AppLoginOutcome{Result: LoginThrottled, Wait: wait}, nil
	}
	if found && !trusted && !u.HasPassword {
		log.Printf("%s: passwordless login for profile %d refused from %s (not the home network)", proto, u.ID, client)
		return AppLoginOutcome{Result: LoginRemotePasswordless}, nil
	}
	// the person's own app password, then their managed accounts' (a TV's
	// player signs in with the person's name and the TV account's password)
	var creds []appCredential
	if found {
		if creds, err = users.appCredentials(ctx, u.ID); err != nil {
			return AppLoginOutcome{}, err
		}
	}
	matched := -1
	for i, c := range creds {
		if check(c.password) {
			matched = i
			break
		}
	}
	if matched < 0 {
		th.Fail(keys...)
		switch {
		case found && len(creds) == 0:
			log.Printf("%s: login for profile %d from %s: no app password set", proto, u.ID, client)
			return AppLoginOutcome{Result: LoginNoAppPassword}, nil
		case found:
			log.Printf("%s: failed login for profile %d from %s", proto, u.ID, client)
		default:
			log.Printf("%s: failed login for an unknown profile from %s", proto, client)
		}
		return AppLoginOutcome{Result: LoginBad}, nil
	}
	th.Reset(appThrottleKey(u.ID))
	return AppLoginOutcome{User: u, History: creds[matched].history, Result: LoginOK}, nil
}

func appThrottleKey(id int64) string { return fmt.Sprintf("app:%d", id) }
