package server

import (
	"database/sql"
	"errors"
	"github.com/rs/zerolog/log"
	"time"
)

func (a *TenantAuth) initDeviceAuthMode() error {
	cfg := &a.srv.cfg
	if cfg.DeviceAuthMode == "" {
		cfg.DeviceAuthMode = "token"
	}
	if cfg.DeviceAuthMode != "token" && cfg.DeviceAuthMode != "compat" {
		return errors.New("device-auth-mode must be token or compat")
	}
	if cfg.DeviceAuthMode == "token" {
		if cfg.DeviceCompatPlaintext {
			return errors.New("device-compat-plaintext requires compat mode")
		}
		return nil
	}
	var enabled int
	if err := a.db.QueryRow("SELECT count(*) FROM tenants WHERE id=? AND disabled=0", cfg.DeviceCompatTenant).Scan(&enabled); err != nil {
		return err
	}
	if enabled != 1 {
		return errors.New("compat mode requires device-compat-tenant to name an existing enabled tenant")
	}
	log.Warn().Str("tenant", cfg.DeviceCompatTenant).Bool("auto_enroll", cfg.DeviceCompatAutoEnroll).Msg("Device registration compatibility mode: missing and invalid tokens are accepted; platform login still requires authentication")
	return nil
}

// Compatibility is scoped to one server-configured tenant. Device-supplied
// group/token values cannot choose a tenant. Existing tombstones stay reserved.
func (a *TenantAuth) compatDeviceLocked(dev *Device) (id, tenant string, err error) {
	tenant = a.srv.cfg.DeviceCompatTenant
	var enabled int
	if err = a.db.QueryRow("SELECT count(*) FROM tenants WHERE id=? AND disabled=0", tenant).Scan(&enabled); err != nil || enabled != 1 {
		return "", "", errDenied
	}
	var disabled bool
	var deleted int64
	err = a.db.QueryRow("SELECT id,disabled,deleted_at FROM devices WHERE tenant=? AND wire_id=?", tenant, dev.id).Scan(&id, &disabled, &deleted)
	if err == nil {
		if disabled || deleted != 0 {
			return "", "", errDenied
		}
		return id, tenant, nil
	}
	if err != sql.ErrNoRows || !a.srv.cfg.DeviceCompatAutoEnroll {
		return "", "", errDenied
	}
	var count int
	if err = a.db.QueryRow("SELECT count(*) FROM devices WHERE tenant=?", tenant).Scan(&count); err != nil || count >= 1000 {
		return "", "", errDenied
	}
	id = randomID()
	tx, err := a.db.Begin()
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	// Never adopt an unverified caller-supplied token. Returning to token mode
	// requires an administrator to issue a token for an auto-enrolled device.
	_, err = tx.Exec("INSERT INTO devices(id,tenant,wire_id,grp,name,key_hash,web_targets) VALUES(?,?,?,'',?,?,'[]')", id, tenant, dev.id, dev.id, digest(randomID()))
	if err == nil {
		_, err = tx.Exec("INSERT INTO audit(at,user,tenant,action,resource,result) VALUES(?,'system',?,'device-compat-auto-enroll',?,'ok')", time.Now().Unix(), tenant, id)
	}
	if err == nil {
		err = tx.Commit()
	}
	return id, tenant, err
}
