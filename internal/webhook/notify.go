package webhook

import (
	"fmt"
	"net/smtp"
	"strings"
)

// SMTPConfig configures optional failure notification email (Standard Webhooks operational guidance).
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

// SendFailureNotification emails the subscriber when delivery is disabled or exhausted.
func SendFailureNotification(cfg SMTPConfig, to string, subject, body string) error {
	to = strings.TrimSpace(to)
	if to == "" || cfg.Host == "" || cfg.From == "" {
		return nil
	}
	port := cfg.Port
	if port == 0 {
		port = 587
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, port)
	msg := strings.Join([]string{
		"From: " + cfg.From,
		"To: " + to,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}, "\r\n")
	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}
	return smtp.SendMail(addr, auth, cfg.From, []string{to}, []byte(msg))
}
