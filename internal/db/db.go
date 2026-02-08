package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Tunnel 隧道配置
type Tunnel struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	LocalHost    string `json:"localHost"`
	LocalPort    int    `json:"localPort"`
	Protocol     string `json:"protocol"`
	TunnelType   string `json:"tunnelType"`   // "quick" 或 "named"
	Token        string `json:"token"`         // Cloudflare tunnel token
	CustomDomain string `json:"customDomain"`  // 自定义域名
	AutoStart    bool   `json:"autoStart"`     // 开机自启动
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

// DB 数据库实例
type DB struct {
	conn *sql.DB
}

// DataDir 返回应用数据目录
func DataDir() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".trynet")
	os.MkdirAll(dir, 0755)
	return dir
}

// New 创建数据库实例
func New() (*DB, error) {
	dbPath := filepath.Join(DataDir(), "trynet.db")
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	conn.Exec("PRAGMA journal_mode=WAL")
	d := &DB{conn: conn}
	if err := d.migrate(); err != nil {
		return nil, err
	}
	return d, nil
}

// migrate 执行数据库迁移
func (d *DB) migrate() error {
	_, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS tunnels (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			local_host TEXT NOT NULL DEFAULT '127.0.0.1',
			local_port INTEGER NOT NULL,
			protocol TEXT NOT NULL DEFAULT 'http',
			tunnel_type TEXT NOT NULL DEFAULT 'quick',
			token TEXT NOT NULL DEFAULT '',
			custom_domain TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
	`)
	if err != nil {
		return err
	}
	// 兼容旧数据库升级
	d.conn.Exec("ALTER TABLE tunnels ADD COLUMN tunnel_type TEXT NOT NULL DEFAULT 'quick'")
	d.conn.Exec("ALTER TABLE tunnels ADD COLUMN token TEXT NOT NULL DEFAULT ''")
	d.conn.Exec("ALTER TABLE tunnels ADD COLUMN custom_domain TEXT NOT NULL DEFAULT ''")
	d.conn.Exec("ALTER TABLE tunnels ADD COLUMN auto_start INTEGER NOT NULL DEFAULT 0")
	return nil
}

// ListTunnels 获取所有隧道配置
func (d *DB) ListTunnels() ([]Tunnel, error) {
	rows, err := d.conn.Query("SELECT id, name, local_host, local_port, protocol, tunnel_type, token, custom_domain, auto_start, created_at, updated_at FROM tunnels ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Tunnel
	for rows.Next() {
		var t Tunnel
		if err := rows.Scan(&t.ID, &t.Name, &t.LocalHost, &t.LocalPort, &t.Protocol, &t.TunnelType, &t.Token, &t.CustomDomain, &t.AutoStart, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	if list == nil {
		list = []Tunnel{}
	}
	return list, nil
}

// CreateTunnel 创建隧道配置
func (d *DB) CreateTunnel(name, host string, port int, protocol, tunnelType, token, customDomain string, autoStart bool) (*Tunnel, error) {
	now := time.Now().Format("2006-01-02 15:04:05")
	res, err := d.conn.Exec(
		"INSERT INTO tunnels (name, local_host, local_port, protocol, tunnel_type, token, custom_domain, auto_start, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		name, host, port, protocol, tunnelType, token, customDomain, autoStart, now, now,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Tunnel{ID: id, Name: name, LocalHost: host, LocalPort: port, Protocol: protocol, TunnelType: tunnelType, Token: token, CustomDomain: customDomain, AutoStart: autoStart, CreatedAt: now, UpdatedAt: now}, nil
}

// UpdateTunnel 更新隧道配置
func (d *DB) UpdateTunnel(id int64, name, host string, port int, protocol, tunnelType, token, customDomain string, autoStart bool) error {
	now := time.Now().Format("2006-01-02 15:04:05")
	_, err := d.conn.Exec(
		"UPDATE tunnels SET name=?, local_host=?, local_port=?, protocol=?, tunnel_type=?, token=?, custom_domain=?, auto_start=?, updated_at=? WHERE id=?",
		name, host, port, protocol, tunnelType, token, customDomain, autoStart, now, id,
	)
	return err
}

// DeleteTunnel 删除隧道配置
func (d *DB) DeleteTunnel(id int64) error {
	_, err := d.conn.Exec("DELETE FROM tunnels WHERE id=?", id)
	return err
}

// GetSetting 获取设置项
func (d *DB) GetSetting(key string) string {
	var val string
	d.conn.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&val)
	return val
}

// SetSetting 设置配置项
func (d *DB) SetSetting(key, value string) error {
	_, err := d.conn.Exec(
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
		key, value,
	)
	return err
}

// Close 关闭数据库连接
func (d *DB) Close() {
	d.conn.Close()
}
