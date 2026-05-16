package service

import (
	"fmt"
	"net/smtp"
)

type EmailService struct {
	smtpHost     string
	smtpPort     string
	smtpUser     string
	smtpPassword string
	fromName     string
	fromAddr     string
}

func NewEmailService(smtpHost, smtpPort, smtpUser, smtpPassword, fromName, fromAddr string) *EmailService {
	return &EmailService{
		smtpHost:     smtpHost,
		smtpPort:     smtpPort,
		smtpUser:     smtpUser,
		smtpPassword: smtpPassword,
		fromName:     fromName,
		fromAddr:     fromAddr,
	}
}

func (s *EmailService) SendOTP(toEmail, otp string) error {
	subject := fmt.Sprintf("%s is your Latch verification code", otp)
	return s.send(toEmail, subject, fmt.Sprintf(otpTemplate, otp))
}

func (s *EmailService) SendRecoveryOTP(toEmail, otp string) error {
	subject := fmt.Sprintf("%s is your Latch account recovery code", otp)
	return s.send(toEmail, subject, fmt.Sprintf(recoveryTemplate, otp))
}

func (s *EmailService) send(to, subject, html string) error {
	auth := smtp.PlainAuth("", s.smtpUser, s.smtpPassword, s.smtpHost)

	msg := []byte(
		"From: " + s.fromName + " <" + s.fromAddr + ">\r\n" +
			"To: " + to + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"MIME-Version: 1.0\r\n" +
			"Content-Type: text/html; charset=UTF-8\r\n" +
			"\r\n" +
			html,
	)

	addr := s.smtpHost + ":" + s.smtpPort
	if err := smtp.SendMail(addr, auth, s.fromAddr, []string{to}, msg); err != nil {
		return fmt.Errorf("smtp send to %s: %w", to, err)
	}
	return nil
}

