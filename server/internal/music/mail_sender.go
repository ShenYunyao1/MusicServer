package music

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type mailEnvelope struct {
	To   string `json:"to"`
	Code string `json:"code,omitempty"`
	Test bool   `json:"test,omitempty"`
}

func smtpMessage(settings MailSettings, envelope mailEnvelope, id string) []byte {
	subject := "MusicServer email verification"
	body := "Your verification code is: " + envelope.Code + "\r\nIt expires in 10 minutes. If you did not request this, ignore this email."
	if envelope.Test {
		subject = "MusicServer email delivery test"
		body = "The SMTP service accepted this test email. You can now enable account registration."
	}
	// All address/config inputs have already been normalized; id is a UUID.
	return []byte("From: " + settings.FromAddress + "\r\nTo: " + envelope.To + "\r\nSubject: " + subject + "\r\nMessage-ID: <" + id + "@" + strings.Split(settings.FromAddress, "@")[1] + ">\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body + "\r\n")
}
func sendSMTP(ctx context.Context, settings MailSettings, secret mailSecret, envelope mailEnvelope, id string, roots *x509.CertPool) error {
	address := net.JoinHostPort(settings.Host, strconv.Itoa(settings.Port))
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return errors.New("SMTP_CONNECT")
	}
	rawConn := conn
	defer rawConn.Close()
	deadline := time.Now().Add(20 * time.Second)
	if requested, ok := ctx.Deadline(); ok && requested.Before(deadline) {
		deadline = requested
	}
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	config := &tls.Config{ServerName: settings.Host, MinVersion: tls.VersionTLS12, RootCAs: roots}
	if settings.TLSMode == "tls" {
		secure := tls.Client(conn, config)
		if secure.HandshakeContext(ctx) != nil {
			return errors.New("SMTP_TLS")
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, settings.Host)
	if err != nil {
		return errors.New("SMTP_GREETING")
	}
	defer client.Close()
	if settings.TLSMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP_TLS_REQUIRED")
		}
		if client.StartTLS(config) != nil {
			return errors.New("SMTP_TLS")
		}
	}
	if client.Auth(smtp.PlainAuth("", settings.Username, secret.Password, settings.Host)) != nil {
		return errors.New("SMTP_AUTH")
	}
	if client.Mail(settings.FromAddress) != nil || client.Rcpt(envelope.To) != nil {
		return errors.New("SMTP_RECIPIENT")
	}
	writer, err := client.Data()
	if err != nil {
		return errors.New("SMTP_DATA")
	}
	_, writeErr := writer.Write(smtpMessage(settings, envelope, id))
	closeErr := writer.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("SMTP_DATA")
	}
	// Once DATA is accepted the transport has submitted the message. A lost QUIT
	// acknowledgement must not turn a successful submission into a retry.
	_ = client.Quit()
	return nil
}
func mailFailureCode(err error) string {
	if err == nil {
		return ""
	}
	switch err.Error() {
	case "SMTP_CONNECT", "SMTP_TLS", "SMTP_TLS_REQUIRED", "SMTP_GREETING", "SMTP_AUTH", "SMTP_RECIPIENT", "SMTP_DATA":
		return err.Error()
	}
	if errors.Is(err, context.Canceled) {
		return "MAIL_CANCELED"
	}
	return "MAIL_UNAVAILABLE"
}
