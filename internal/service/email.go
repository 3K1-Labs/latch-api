package service

import (
	"fmt"

	"github.com/resend/resend-go/v2"
)

type EmailService struct {
	client   *resend.Client
	fromName string
	fromAddr string
}

func NewEmailService(apiKey, fromName, fromAddr string) *EmailService {
	return &EmailService{
		client:   resend.NewClient(apiKey),
		fromName: fromName,
		fromAddr: fromAddr,
	}
}

func (s *EmailService) SendOTP(toEmail, otp string) error {
	return s.send(toEmail,
		fmt.Sprintf("%s is your Latch verification code", otp),
		fmt.Sprintf(otpTemplate, otp),
	)
}

func (s *EmailService) SendRecoveryOTP(toEmail, otp string) error {
	return s.send(toEmail,
		fmt.Sprintf("%s is your Latch account recovery code", otp),
		fmt.Sprintf(recoveryTemplate, otp),
	)
}

func (s *EmailService) send(to, subject, html string) error {
	params := &resend.SendEmailRequest{
		From:    fmt.Sprintf("%s <%s>", s.fromName, s.fromAddr),
		To:      []string{to},
		Subject: subject,
		Html:    html,
	}
	if _, err := s.client.Emails.Send(params); err != nil {
		return fmt.Errorf("resend send to %s: %w", to, err)
	}
	return nil
}
