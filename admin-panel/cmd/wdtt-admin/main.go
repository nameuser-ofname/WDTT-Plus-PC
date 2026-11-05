// wdtt-admin — веб-панель управления сервером WDTT.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const version = "1.0.0"

const defaultConfig = "/etc/wdtt-admin/config.json"

func usage() {
	fmt.Fprintf(os.Stderr, `wdtt-admin %s

  wdtt-admin serve    [--config PATH]
  wdtt-admin init     [--config PATH] --main-password PWD [--panel-password PWD] [--listen ADDR] [--host NAME[,NAME]] [--wdtt-config-dir DIR]
  wdtt-admin passwd   [--config PATH] [--panel-password PWD]
  wdtt-admin check    [--config PATH]
  wdtt-admin version
`, version)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "serve":
		err = cmdServe(args)
	case "init":
		err = cmdInit(args)
	case "passwd":
		err = cmdPasswd(args)
	case "check":
		err = cmdCheck(args)
	case "version", "--version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	path := fs.String("config", defaultConfig, "путь к config.json")
	fs.Parse(args)
	cfg, err := loadConfig(*path)
	if err != nil {
		return err
	}
	return serve(cfg, *path)
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	path := fs.String("config", defaultConfig, "путь к config.json")
	mainPwd := fs.String("main-password", "", "главный пароль WDTT-сервера")
	panelPwd := fs.String("panel-password", "", "пароль входа в панель (если пусто — сгенерируется)")
	listen := fs.String("listen", "0.0.0.0:8443", "адрес прослушивания")
	hosts := fs.String("host", "localhost", "имена/IP для сертификата, через запятую")
	wdttDir := fs.String("wdtt-config-dir", "/etc/wdtt", "каталог конфигурации wdtt-server")
	fs.Parse(args)
	if strings.TrimSpace(*mainPwd) == "" {
		return fmt.Errorf("укажите --main-password")
	}
	cfg := &Config{}
	if existing, err := loadConfig(*path); err == nil {
		cfg = existing // повторный запуск: сохраняем сессионный секрет и сертификат
	}
	cfg.Listen, cfg.WDTTConfigDir, cfg.MainPassword = *listen, *wdttDir, *mainPwd
	dir := filepath.Dir(*path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if cfg.SessionSecret == "" {
		cfg.SessionSecret = randomString(32)
	}
	if cfg.CertFile == "" || cfg.KeyFile == "" {
		cfg.CertFile, cfg.KeyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
		if err := generateSelfSigned(cfg.CertFile, cfg.KeyFile, append(strings.Split(*hosts, ","), "localhost", "127.0.0.1")); err != nil {
			return err
		}
	}
	generated := ""
	if *panelPwd == "" && cfg.PanelHash == "" {
		generated = randomString(12)
		*panelPwd = generated
	}
	if *panelPwd != "" {
		if len(*panelPwd) < 8 {
			return fmt.Errorf("пароль панели должен быть не короче 8 символов")
		}
		if err := cfg.setPanelPassword(*panelPwd); err != nil {
			return err
		}
	}
	if err := cfg.save(*path); err != nil {
		return err
	}
	if generated != "" {
		fmt.Printf("PANEL_PASSWORD=%s\n", generated)
	}
	return nil
}

func cmdPasswd(args []string) error {
	fs := flag.NewFlagSet("passwd", flag.ExitOnError)
	path := fs.String("config", defaultConfig, "путь к config.json")
	panelPwd := fs.String("panel-password", "", "новый пароль (если пусто — сгенерируется)")
	fs.Parse(args)
	cfg, err := loadConfig(*path)
	if err != nil {
		return err
	}
	if *panelPwd == "" {
		*panelPwd = randomString(12)
		fmt.Printf("PANEL_PASSWORD=%s\n", *panelPwd)
	}
	if len(*panelPwd) < 8 {
		return fmt.Errorf("пароль должен быть не короче 8 символов")
	}
	if err := cfg.setPanelPassword(*panelPwd); err != nil {
		return err
	}
	return cfg.save(*path)
}

// cmdCheck проверяет связь с wdtt-server и главный пароль.
func cmdCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	path := fs.String("config", defaultConfig, "путь к config.json")
	fs.Parse(args)
	cfg, err := loadConfig(*path)
	if err != nil {
		return err
	}
	raw, err := callAdmin(cfg, []string{"list"})
	if err != nil {
		return err
	}
	var resp struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || !resp.OK {
		return fmt.Errorf("wdtt-server отклонил запрос: %s", resp.Message)
	}
	fmt.Println("OK: связь с wdtt-server есть, главный пароль верный")
	return nil
}
