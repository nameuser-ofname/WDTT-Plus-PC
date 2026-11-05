package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const pbkdf2Iterations = 210000

// Config хранится в /etc/wdtt-admin/config.json с правами 0600.
type Config struct {
	Listen        string `json:"listen"`
	WDTTConfigDir string `json:"wdtt_config_dir"`
	MainPassword  string `json:"main_password"`
	PanelSalt     string `json:"panel_salt"`
	PanelHash     string `json:"panel_hash"`
	SessionSecret string `json:"session_secret"`
	CertFile      string `json:"cert_file"`
	KeyFile       string `json:"key_file"`
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Listen == "" {
		c.Listen = "0.0.0.0:8443"
	}
	if c.WDTTConfigDir == "" {
		c.WDTTConfigDir = "/etc/wdtt"
	}
	if c.MainPassword == "" || c.PanelHash == "" || c.SessionSecret == "" {
		return nil, errors.New("конфигурация неполная: выполните wdtt-admin init")
	}
	return &c, nil
}

func (c *Config) save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cfg-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (c *Config) setPanelPassword(password string) error {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, 32)
	if err != nil {
		return err
	}
	c.PanelSalt = base64.StdEncoding.EncodeToString(salt)
	c.PanelHash = base64.StdEncoding.EncodeToString(key)
	return nil
}

func (c *Config) checkPanelPassword(password string) bool {
	salt, err1 := base64.StdEncoding.DecodeString(c.PanelSalt)
	want, err2 := base64.StdEncoding.DecodeString(c.PanelHash)
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, 32)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}
