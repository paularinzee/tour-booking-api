package email

import (
	"fmt"
	"log"
)

type EmailSender interface {
	SendPasswordResetEmail(to, token string) error
}

type MockEmailSender struct{}

func NewMockEmailSender() *MockEmailSender {
	return &MockEmailSender{}
}

func (s *MockEmailSender) SendPasswordResetEmail(to, token string) error {
	// In production, integrate with SendGrid, Mailgun, SMTP, etc.
	resetURL := fmt.Sprintf("http://localhost:3000/reset-password/%s", token)
	log.Printf("Password reset email would be sent to: %s", to)
	log.Printf("Reset URL: %s", resetURL)
	log.Printf("Token: %s", token)
	return nil
}

// For production with SMTP
type SMTPSender struct {
	host     string
	port     int
	username string
	password string
	from     string
}

func NewSMTPSender(host string, port int, username, password, from string) *SMTPSender {
	return &SMTPSender{
		host:     host,
		port:     port,
		username: username,
		password: password,
		from:     from,
	}
}

func (s *SMTPSender) SendPasswordResetEmail(to, token string) error {
	// Implementation for real email sending
	// Using net/smtp package
	return nil
}
