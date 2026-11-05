package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sort"
	"time"
)

// Разрешённые команды wdtt-server admin. restart/import-* и т.п. намеренно исключены.
var allowedCommands = map[string]bool{
	"list": true, "details": true, "create": true, "delete": true, "unbind": true,
	"deactivate": true, "activate": true, "set-label": true, "set-hash": true,
	"set-expiry": true, "set-ports": true, "set-password": true, "update-client": true,
	"set-dns": true, "set-limit": true, "set-default-ports": true, "set-public-ip": true,
	"refresh-public-ip": true, "cleanup-expired": true, "cleanup-orphans": true,
	"reset-traffic": true, "backup-status": true, "backup-list": true,
	"backup-configure": true, "backup-create": true, "backup-verify": true,
	"backup-export": true, "backup-delete": true,
}

const maxSocketResponse = 128 << 20

func socketPath(configDir string) string {
	if filepath.Clean(configDir) == "/etc/wdtt" {
		return "/run/wdtt/admin.sock"
	}
	return filepath.Join(configDir, "admin.sock")
}

// buildArgs превращает команду и флаги в аргументы вида `cmd --k=v`.
// Форма --k=v исключает интерпретацию значения, начинающегося с «-», как флага.
func buildArgs(cmd string, flags map[string]string) []string {
	keys := make([]string, 0, len(flags))
	for k := range flags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := []string{cmd}
	for _, k := range keys {
		args = append(args, "--"+k+"="+flags[k])
	}
	return args
}

// callAdmin отправляет запрос в admin-сокет wdtt-server и возвращает сырой JSON-ответ.
func callAdmin(cfg *Config, args []string) (json.RawMessage, error) {
	conn, err := net.DialTimeout("unix", socketPath(cfg.WDTTConfigDir), 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("admin-сокет wdtt-server недоступен (запущен ли wdtt.service?): %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(90 * time.Second))
	req := map[string]any{"main_password": cfg.MainPassword, "args": args}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(conn, maxSocketResponse))
	if err != nil {
		return nil, err
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("некорректный ответ wdtt-server")
	}
	return data, nil
}
