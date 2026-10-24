package server

import (
	"time"

	"golang.org/x/crypto/bcrypt"
)

const maxLoginAttempts = 20000

// Missing and disabled users perform the same cost-12 work as real users.
// The comparison result for this hash is never accepted as authentication.
var dummyLoginHash = func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte(randomID()), 12)
	if err != nil {
		panic(err)
	}
	return hash
}()

// Called under a.mu. Only the requesting source/account can be throttled;
// reaching the memory bound evicts old entries rather than closing all login.
func (a *TenantAuth) limitLoginLocked(ipKey, accountKey string, now time.Time) time.Duration {
	limits := []struct {
		key   string
		count int
	}{{ipKey, 100}, {accountKey, 10}}
	var retry time.Duration
	for _, limit := range limits {
		v := a.attempts[limit.key]
		if v.Until.After(now) && v.Count >= limit.count && v.Until.Sub(now) > retry {
			retry = v.Until.Sub(now)
		}
	}
	if retry > 0 {
		return retry
	}
	needed := 0
	for _, limit := range limits {
		if _, ok := a.attempts[limit.key]; !ok {
			needed++
		}
	}
	if len(a.attempts)+needed > maxLoginAttempts {
		for key, v := range a.attempts {
			if key != ipKey && key != accountKey && !v.Until.After(now) {
				delete(a.attempts, key)
			}
		}
		for len(a.attempts)+needed > maxLoginAttempts {
			var oldest string
			var until time.Time
			for key, v := range a.attempts {
				if key == ipKey || key == accountKey {
					continue
				}
				if oldest == "" || v.Until.Before(until) {
					oldest, until = key, v.Until
				}
			}
			delete(a.attempts, oldest)
		}
	}
	for _, limit := range limits {
		v := a.attempts[limit.key]
		if !v.Until.After(now) {
			v = loginAttempt{Until: now.Add(time.Minute)}
		}
		v.Count++
		a.attempts[limit.key] = v
	}
	return 0
}
