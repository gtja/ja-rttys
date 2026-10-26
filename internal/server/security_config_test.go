package server

import (
	"database/sql"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecurityPprofLoopbackOnly(t *testing.T) {
	for _, addr := range []string{":6060", "0.0.0.0:6060", "[::]:6060", "192.0.2.1:6060", "example.com:6060", "127.0.0.1:http", "127.0.0.1:65536"} {
		if _, err := pprofListenAddress(addr); err == nil {
			t.Errorf("accepted pprof address %q", addr)
		}
		if err := New(Config{PprofAddr: addr}).Run(); err == nil {
			t.Errorf("startup accepted pprof address %q", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:6060", "[::1]:6060", "localhost:6060"} {
		if _, err := pprofListenAddress(addr); err != nil {
			t.Errorf("rejected local pprof address %q: %v", addr, err)
		}
	}
}

func TestSecurityBootstrapFile(t *testing.T) {
	root := t.TempDir()
	password := filepath.Join(root, "password")
	if err := os.WriteFile(password, []byte("Bootstrap-test-password!"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateSecretFile(password); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(password, link); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{root, link} {
		if _, err := readPrivateSecretFile(bad); err == nil {
			t.Errorf("accepted non-regular bootstrap file: %s", bad)
		}
	}
	if err := os.WriteFile(password, []byte(strings.Repeat("x", 4097)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateSecretFile(password); err == nil {
		t.Fatal("accepted oversized bootstrap file")
	}
}

func TestSecurityPprofAuthentication(t *testing.T) {
	token := strings.Repeat("test", 10)
	handler := newPprofHandler(token)
	for _, tc := range []struct {
		auth string
		want int
	}{{"", 401}, {"Bearer wrong", 401}, {"Bearer " + token, 200}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "http://localhost/debug/pprof/", nil)
		r.Header.Set("Authorization", tc.auth)
		handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("pprof status=%d, want=%d", w.Code, tc.want)
		}
	}
	if _, err := readPprofToken(""); err == nil {
		t.Fatal("accepted missing pprof token file")
	}
	if err := New(Config{PprofAddr: "127.0.0.1:0"}).Run(); err == nil {
		t.Fatal("started unauthenticated pprof")
	}
}

func TestSecuritySQLiteReconnect(t *testing.T) {
	db, err := sql.Open("sqlite", tenantDBDSN(filepath.Join(t.TempDir(), "identity # ?.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`CREATE TABLE parent(id INTEGER PRIMARY KEY); CREATE TABLE child(parent INTEGER REFERENCES parent(id) ON DELETE CASCADE); INSERT INTO parent VALUES(1); INSERT INTO child VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	// Retire the original connection and force the driver to open a replacement.
	db.SetMaxIdleConns(0)
	db.SetMaxIdleConns(1)
	var foreignKeys, timeout int
	var locking string
	if err = db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("PRAGMA locking_mode").Scan(&locking); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 || timeout != 5000 || locking != "exclusive" {
		t.Fatalf("lost connection settings: fk=%d timeout=%d locking=%s", foreignKeys, timeout, locking)
	}
	if _, err = db.Exec("DELETE FROM parent WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = db.QueryRow("SELECT count(*) FROM child").Scan(&n); err != nil || n != 0 {
		t.Fatalf("cascade after reconnect: count=%d err=%v", n, err)
	}
}

func TestSecurityBootstrapPermissions(t *testing.T) {
	for _, mode := range []os.FileMode{0644, 0640, 0660} {
		t.Run(mode.String(), func(t *testing.T) {
			root := t.TempDir()
			password := filepath.Join(root, "password")
			if err := os.WriteFile(password, []byte("Bootstrap-test-password!"), mode); err != nil {
				t.Fatal(err)
			}
			srv := New(Config{TenantDB: filepath.Join(root, "identity.db"), BootstrapPasswordFile: password})
			a, err := openTenantAuth(srv)
			if err == nil {
				a.db.Close()
				t.Fatal("accepted group/world-readable bootstrap password")
			}
		})
	}
}

func TestSecurityRelativeIdentityDB(t *testing.T) {
	t.Chdir(t.TempDir())
	a, err := openTenantAuth(New(Config{TenantDB: "identity.db"}))
	if a != nil {
		a.db.Close()
	}
	if err == nil {
		t.Fatal("accepted implicit relative identity store")
	}
	if _, err := os.Stat("identity.db"); !os.IsNotExist(err) {
		t.Fatal("created database at relative path")
	}
}
