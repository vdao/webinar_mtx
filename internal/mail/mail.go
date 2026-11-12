// Package mail sends one-time login codes over SMTP (stdlib only).
package mail

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/vdao/webinar_mtx/internal/config"
)

// Sender sends login code emails.
type Sender struct {
	cfg config.SMTPConfig
}

// New creates a Sender; it is a no-op when SMTP_HOST is not configured.
func New(cfg config.SMTPConfig) *Sender { return &Sender{cfg: cfg} }

// Enabled reports whether mail can be sent.
func (s *Sender) Enabled() bool { return s.cfg.Host != "" }

// SendLoginCode sends the one-time code to the participant.
func (s *Sender) SendLoginCode(to, code, webinarTitle string) error {
	if !s.Enabled() {
		return fmt.Errorf("SMTP is not configured")
	}
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	subject := "Код для входа на вебинар «" + webinarTitle + "»"
	body := "Здравствуйте!\n\n" +
		"Ваш код для входа на вебинар «" + webinarTitle + "»: " + code + "\n\n" +
		"Код действителен 10 минут. Если вы не запрашивали код — просто проигнорируйте это письмо.\n"

	msg := strings.Join([]string{
		"From: " + s.cfg.From,
		"To: " + to,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Date: " + time.Now().Format(time.RFC1123Z),
		"",
		body,
	}, "\r\n")

	conn, err := tlsOrPlain(s.cfg, addr)
	if err != nil {
		return fmt.Errorf("connect SMTP: %w", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		return err
	}

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("SMTP handshake: %w", err)
	}
	defer c.Close()

	// Opportunistic STARTTLS on plain connections.
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: s.cfg.Host}); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	if s.cfg.User != "" {
		if ok, mech := c.Extension("AUTH"); ok {
			var auth smtp.Auth
			switch {
			case strings.Contains(mech, "PLAIN"):
				auth = smtp.PlainAuth("", s.cfg.User, s.cfg.Pass, s.cfg.Host)
			case strings.Contains(mech, "CRAM-MD5"):
				auth = smtp.CRAMMD5Auth(s.cfg.User, s.cfg.Pass)
			default:
				return fmt.Errorf("SMTP server supports no known AUTH mechanism (%s)", mech)
			}
			if err := c.Auth(auth); err != nil {
				return fmt.Errorf("SMTP auth: %w", err)
			}
		}
	}
	if err := c.Mail(s.cfg.From); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// tlsOrPlain dials with implicit TLS on port 465, plain TCP otherwise.
func tlsOrPlain(cfg config.SMTPConfig, addr string) (net.Conn, error) {
	if cfg.Port == 465 {
		return tls.Dial("tcp", addr, &tls.Config{ServerName: cfg.Host})
	}
	return net.DialTimeout("tcp", addr, 10*time.Second)
}
