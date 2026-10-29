package common

import (
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"
)

// SendEmail 发送邮件。SMTP 未配置或发送失败时返回错误。
// EmailVerificationEnabled=false 时仅打印日志（联调模式），不实际发送。
func SendEmail(to string, subject string, body string) error {
	if !EmailVerificationEnabled {
		SysLog(fmt.Sprintf("[SMTP][联调模式] to=%s subject=%s body=%s", to, subject, body))
		return nil
	}
	if SmtpHost == "" || SmtpUser == "" {
		return fmt.Errorf("SMTP 未配置")
	}
	addr := fmt.Sprintf("%s:%d", SmtpHost, SmtpPort)
	from := SmtpUser
	msg := buildMailMessage(from, to, subject, body)
	auth := smtp.PlainAuth("", SmtpUser, SmtpPass, SmtpHost)

	if SmtpPort == 465 {
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: SmtpHost})
		if err != nil {
			return err
		}
		client, err := smtp.NewClient(conn, SmtpHost)
		if err != nil {
			return err
		}
		defer client.Close()
		if err := client.Auth(auth); err != nil {
			return err
		}
		if err := client.Mail(from); err != nil {
			return err
		}
		if err := client.Rcpt(to); err != nil {
			return err
		}
		w, err := client.Data()
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(msg))
		if err != nil {
			return err
		}
		return w.Close()
	}
	return smtp.SendMail(addr, auth, from, []string{to}, []byte(msg))
}

func buildMailMessage(from string, to string, subject string, body string) string {
	var sb strings.Builder
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: " + to + "\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return sb.String()
}
