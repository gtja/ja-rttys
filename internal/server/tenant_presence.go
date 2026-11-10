package server

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

func (a *TenantAuth) initPresence() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS presence_meta(id INTEGER PRIMARY KEY CHECK(id=1),started INTEGER NOT NULL);
 INSERT OR IGNORE INTO presence_meta(id,started) VALUES(1,unixepoch());
 CREATE TABLE IF NOT EXISTS device_presence(id INTEGER PRIMARY KEY AUTOINCREMENT,device TEXT NOT NULL REFERENCES devices(id),tenant TEXT NOT NULL REFERENCES tenants(id),online_at INTEGER NOT NULL,last_seen INTEGER NOT NULL,offline_at INTEGER,reason TEXT NOT NULL DEFAULT '',peer TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS presence_device_time ON device_presence(device,online_at DESC);
 CREATE INDEX IF NOT EXISTS presence_tenant_time ON device_presence(tenant,online_at DESC);
 UPDATE device_presence SET offline_at=last_seen,reason='server_restart' WHERE offline_at IS NULL;`)
	if err != nil {
		return err
	}
	rows, err := a.db.Query("PRAGMA table_info(device_presence)")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var def any
		if err = rows.Scan(&cid, &name, &kind, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "link" {
			found = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !found {
		_, err = a.db.Exec("ALTER TABLE device_presence ADD COLUMN link TEXT NOT NULL DEFAULT 'default'")
	}
	return err
}

// Called while holding the identity lock. Rejected registrations never enter history.
func (a *TenantAuth) presenceStartLocked(dev *Device) error {
	now := time.Now().Unix()
	res, err := a.db.Exec("INSERT INTO device_presence(device,tenant,online_at,last_seen,peer,link) VALUES(?,?,?,?,?,?)", dev.id, dev.group, now, now, dev.conn.RemoteAddr().String(), dev.link)
	if err != nil {
		return err
	}
	dev.presenceID, err = res.LastInsertId()
	dev.presenceSeen = now
	return err
}

func (a *TenantAuth) presenceSeen(dev *Device) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now().Unix()
	if dev.presenceID == 0 || now-dev.presenceSeen < 10 {
		return
	}
	if _, err := a.db.Exec("UPDATE device_presence SET last_seen=max(last_seen,?) WHERE id=? AND offline_at IS NULL", now, dev.presenceID); err != nil {
		log.Error().Err(err).Msg("persist device observation failed")
	} else {
		dev.presenceSeen = now
	}
}

func (a *TenantAuth) presenceEnd(dev *Device) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if dev.presenceID == 0 {
		return
	}
	reason := "connection_closed"
	if dev.presenceTimeout.Load() {
		reason = "heartbeat_timeout"
	}
	if _, err := a.db.Exec("UPDATE device_presence SET offline_at=max(online_at,?),reason=? WHERE id=? AND offline_at IS NULL", time.Now().Unix(), reason, dev.presenceID); err != nil {
		log.Error().Err(err).Msg("persist device disconnection failed")
	}
}

// Scope is evaluated on current membership and grants, including historical reads.
func presenceScope(p *Actor) (string, []any) {
	where := "d.deleted_at=0"
	args := []any{}
	if p.Admin && p.Tenant == "" {
		return where, args
	}
	where += " AND d.tenant=? AND t.disabled=0"
	args = append(args, p.Tenant)
	if p.Role == "admin" {
		return where, args
	}
	where += " AND d.disabled=0"
	if p.Role == "operator" {
		where += " AND EXISTS (SELECT 1 FROM grants g WHERE g.tenant=d.tenant AND g.user=? AND (g.device=d.id OR (g.device='' AND EXISTS (SELECT 1 FROM device_groups dg WHERE dg.device=d.id AND dg.tenant=d.tenant AND dg.grp=g.grp))))"
		args = append(args, p.ID)
	} else if p.Role != "viewer" {
		where += " AND 0"
	}
	return where, args
}

func (a *TenantAuth) presence(c *gin.Context) {
	days, err := strconv.Atoi(c.DefaultQuery("days", "7"))
	if err != nil || (days != 1 && days != 7 && days != 30 && days != 90) {
		c.JSON(400, gin.H{"error": "days must be 1, 7, 30 or 90"})
		return
	}
	before, err := strconv.ParseInt(c.DefaultQuery("before", "0"), 10, 64)
	if err != nil || before < 0 {
		c.Status(400)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err := a.actorLocked(tenantSession(c))
	if err != nil {
		c.Status(401)
		return
	}
	where, args := presenceScope(p)
	if id := c.Query("device_id"); id != "" {
		where += " AND d.id=?"
		args = append(args, id)
		var n int
		err = a.db.QueryRow("SELECT count(*) FROM devices d JOIN tenants t ON t.id=d.tenant WHERE "+where, args...).Scan(&n)
		if err != nil {
			c.Status(503)
			return
		}
		if n == 0 {
			c.Status(404)
			return
		}
	}
	now := time.Now().Unix()
	var started int64
	if err = a.db.QueryRow("SELECT started FROM presence_meta WHERE id=1").Scan(&started); err != nil {
		c.Status(503)
		return
	}
	since := max(now-int64(days)*86400, started)
	// Every following query uses this same authorization CTE; all values remain bound.
	cte := "WITH scoped AS (SELECT d.id,d.wire_id,d.name,d.tenant,t.name AS tenant_name,(d.disabled OR t.disabled) AS disabled FROM devices d JOIN tenants t ON t.id=d.tenant WHERE " + where + ") "
	query := func(q string, extra ...any) ([]gin.H, error) {
		values := append(append([]any{}, args...), extra...)
		rows, e := a.db.Query(cte+q, values...)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		cols, e := rows.Columns()
		if e != nil {
			return nil, e
		}
		out := []gin.H{}
		for rows.Next() {
			v := make([]any, len(cols))
			ptr := make([]any, len(cols))
			for i := range v {
				ptr[i] = &v[i]
			}
			if e = rows.Scan(ptr...); e != nil {
				return nil, e
			}
			row := gin.H{}
			for i, col := range cols {
				row[col] = v[i]
			}
			out = append(out, row)
		}
		return out, rows.Err()
	}
	fail := func(err error) { log.Error().Err(err).Msg("query presence failed"); c.Status(503) }
	population, err := query(`SELECT count(*) AS devices,coalesce(sum(disabled),0) AS disabled,
 (SELECT count(DISTINCT p.device) FROM device_presence p JOIN scoped s ON s.id=p.device WHERE p.offline_at IS NULL) AS online FROM scoped`)
	if err != nil {
		fail(err)
		return
	}
	stats, err := query(`SELECT
 coalesce(sum(p.online_at>=?),0) AS connections,
 coalesce(sum(p.offline_at>=? AND p.reason<>'server_restart'),0) AS disconnections,
 coalesce(sum(p.reason='server_restart'),0) AS uncertain,
 count(*) AS observed_sessions,
 coalesce(sum(max(0,min(coalesce(p.offline_at,?),?)-max(p.online_at,?))),0) AS online_seconds,
 coalesce(max(max(0,min(coalesce(p.offline_at,?),?)-max(p.online_at,?))),0) AS longest_seconds
 FROM device_presence p JOIN scoped s ON s.id=p.device WHERE p.online_at<=? AND coalesce(p.offline_at,?)>=?`, since, since, now, now, since, now, now, since, now, now, since)
	if err != nil {
		fail(err)
		return
	}
	summary := population[0]
	for k, v := range stats[0] {
		summary[k] = v
	}
	recent, err := query(`SELECT s.*,
 (SELECT max(online_at) FROM device_presence WHERE device=s.id) AS last_online,
 (SELECT max(offline_at) FROM device_presence WHERE device=s.id AND reason<>'server_restart') AS last_offline,
 EXISTS(SELECT 1 FROM device_presence WHERE device=s.id AND offline_at IS NULL) AS online
 FROM scoped s WHERE EXISTS(SELECT 1 FROM device_presence WHERE device=s.id AND online_at>=?)
 ORDER BY last_online DESC,s.id LIMIT 100`, since)
	if err != nil {
		fail(err)
		return
	}
	eventQuery := `SELECT p.id,p.link,p.device AS device_id,s.wire_id,s.name,s.tenant_name,p.online_at,p.offline_at,p.last_seen,p.reason,
 max(0,coalesce(p.offline_at,?)-p.online_at) AS duration_seconds
 FROM device_presence p JOIN scoped s ON s.id=p.device WHERE p.online_at<=? AND coalesce(p.offline_at,?)>=?`
	eventArgs := []any{now, now, now, since}
	if before > 0 {
		eventQuery += " AND p.id<?"
		eventArgs = append(eventArgs, before)
	}
	events, err := query(eventQuery+" ORDER BY p.id DESC LIMIT 51", eventArgs...)
	if err != nil {
		fail(err)
		return
	}
	var next any
	if len(events) > 50 {
		events = events[:50]
		next = events[49]["id"]
	}
	daily, err := query(`SELECT day,sum(up) AS connections,sum(down) AS disconnections,sum(uncertain) AS uncertain FROM (
 SELECT date(p.online_at,'unixepoch','+8 hours') AS day,1 AS up,0 AS down,0 AS uncertain FROM device_presence p JOIN scoped s ON s.id=p.device WHERE p.online_at BETWEEN ? AND ?
 UNION ALL SELECT date(p.offline_at,'unixepoch','+8 hours'),0,CASE WHEN reason='server_restart' THEN 0 ELSE 1 END,CASE WHEN reason='server_restart' THEN 1 ELSE 0 END FROM device_presence p JOIN scoped s ON s.id=p.device WHERE p.offline_at BETWEEN ? AND ?
 ) GROUP BY day ORDER BY day DESC`, since, now, since, now)
	if err != nil {
		fail(err)
		return
	}
	c.JSON(200, gin.H{"summary": summary, "recent": recent, "events": events, "next_before": next, "daily": daily, "since": since, "until": now, "recording_since": started, "timezone": "Asia/Shanghai"})
}
