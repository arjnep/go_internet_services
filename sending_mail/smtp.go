// This is just scratch net/smtp implementation.
// You will want to use awesome external libraries instead.

package main

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"log"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
)

type SMTPConfig struct {
	Host     string
	Port     string
	Username string
	Password string
}

// an email message
type Email struct {
	From mail.Address
	To   []mail.Address
	// can add cc, bcc and append them to recipient list
	Subject     string
	Body        string
	Attachments []Attachment
}

// interface for sending emails
type EmailSender interface {
	Send(config SMTPConfig, email Email) error
}

// smtp.Client wrapper
type smtpClient struct {
	*smtp.Client
}

func NewSMTPClient(config SMTPConfig) (*smtpClient, error) {
	conn, err := net.Dial("tcp", net.JoinHostPort(config.Host, config.Port))
	if err != nil {
		return nil, fmt.Errorf("failed to dial SMTP server: %w", err)
	}

	client, err := smtp.NewClient(conn, config.Host)
	if err != nil {
		return nil, fmt.Errorf("failed to create SMTP client: %w", err)
	}

	if err = client.StartTLS(&tls.Config{ServerName: config.Host}); err != nil {
		return nil, fmt.Errorf("failed to start TLS: %w", err)
	}

	auth := smtp.PlainAuth("", config.Username, config.Password, config.Host)
	if err = client.Auth(auth); err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	return &smtpClient{client}, nil
}

type SimpleSender struct{}

// implements EmailSender interface
func (s SimpleSender) Send(config SMTPConfig, email Email) error {
	auth := smtp.PlainAuth("", config.Username, config.Password, config.Host)
	msg := buildEmailMessage(email)

	to := make([]string, len(email.To))
	for i, addr := range email.To {
		to[i] = addr.Address
	}

	return smtp.SendMail(
		net.JoinHostPort(config.Host, config.Port),
		auth,
		email.From.Address,
		to,
		msg,
	)
}

type AdvancedSender struct{}

// implement EmailSender interface with manual SMTP commands
func (s AdvancedSender) Send(config SMTPConfig, email Email) error {
	client, err := NewSMTPClient(config)
	if err != nil {
		return err
	}
	defer client.Close()
	defer client.Quit()

	if err = client.Mail(config.Username); err != nil {
		return fmt.Errorf("MAIL command failed: %w", err)
	}

	for _, to := range email.To {
		if err = client.Rcpt(to.Address); err != nil {
			return fmt.Errorf("RCPT command failed for %s: %w", to.Address, err)
		}
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA command failed: %w", err)
	}
	defer writer.Close()

	msg := buildEmailMessage(email)
	_, err = writer.Write(msg)
	return err
}

// construct email message
func buildEmailMessage(email Email) []byte {
	var buf bytes.Buffer

	fmt.Fprintf(&buf, "From: %s\r\n", email.From.String())
	fmt.Fprintf(&buf, "To: %s\r\n", joinAddresses(email.To))
	fmt.Fprintf(&buf, "Subject: %s\r\n", email.Subject)
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: text/html; charset=UTF-8\r\n")
	fmt.Fprintf(&buf, "\r\n")
	buf.WriteString(email.Body)

	return buf.Bytes()
}

type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

type EliteSender struct{}

// implements the EmailSender interface with attachment support
func (s EliteSender) Send(config SMTPConfig, email Email) error {
	client, err := NewSMTPClient(config)
	if err != nil {
		return err
	}
	defer client.Close()
	defer client.Quit()

	if err = client.Mail(config.Username); err != nil {
		return fmt.Errorf("MAIL command failed: %w", err)
	}

	for _, to := range email.To {
		if err = client.Rcpt(to.Address); err != nil {
			return fmt.Errorf("RCPT command failed for %s: %w", to.Address, err)
		}
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA command failed: %w", err)
	}
	defer writer.Close()

	msg := buildMultipartMessage(email)
	_, err = writer.Write(msg)
	return err
}

// construct MIME multipart message
func buildMultipartMessage(email Email) []byte {
	var buf bytes.Buffer
	boundary := fmt.Sprintf("%d", os.Getpid())

	fmt.Fprintf(&buf, "From: %s\r\n", email.From.String())
	fmt.Fprintf(&buf, "To: %s\r\n", joinAddresses(email.To))
	fmt.Fprintf(&buf, "Subject: %s\r\n", email.Subject)
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=%s\r\n", boundary)
	fmt.Fprintf(&buf, "\r\n")

	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	fmt.Fprintf(&buf, "Content-Type: text/html; charset=UTF-8\r\n")
	fmt.Fprintf(&buf, "Content-Transfer-Encoding: 7bit\r\n")
	fmt.Fprintf(&buf, "\r\n")
	buf.WriteString(email.Body)
	buf.WriteString("\r\n")

	for _, att := range email.Attachments {
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		fmt.Fprintf(&buf, "Content-Type: %s\r\n", att.ContentType)
		fmt.Fprintf(&buf, "Content-Transfer-Encoding: base64\r\n")
		fmt.Fprintf(&buf, "Content-Disposition: attachment; filename=\"%s\"\r\n", att.Filename)
		fmt.Fprintf(&buf, "\r\n")

		// encode attachment in base64
		encoder := base64.NewEncoder(base64.StdEncoding, &buf)
		_, err := encoder.Write(att.Data)
		if err != nil {
			log.Printf("Error encoding attachment %s: %v", att.Filename, err)
		}
		encoder.Close()
		buf.WriteString("\r\n")
	}

	fmt.Fprintf(&buf, "--%s--\r\n", boundary)

	return buf.Bytes()
}

// create attachment from a file path
func NewAttachmentFromFile(filePath string) (Attachment, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return Attachment{}, fmt.Errorf("failed to read file: %w", err)
	}

	contentType := mime.TypeByExtension(filepath.Ext(filePath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	return Attachment{
		Filename:    filepath.Base(filePath),
		ContentType: contentType,
		Data:        data,
	}, nil
}

// []mail.Address to a comma separated string
func joinAddresses(addrs []mail.Address) string {
	var result []string
	for _, addr := range addrs {
		result = append(result, addr.String())
	}
	return strings.Join(result, ", ")
}

func main() {
	config := SMTPConfig{
		Host:     "", // eg. smtp.gmail.com
		Port:     "", // usually 587
		Username: "", // eg. someone@gmail.com
		Password: "", // eg. google's app password
	}

	recipients := []mail.Address{
		{Name: "Recipient Name 1", Address: "john@gmail.com"},
		{Name: "Recipient Name 2", Address: "doe@example.com"},
	}

	email := Email{
		From:    mail.Address{Name: "Sender Name", Address: config.Username},
		To:      recipients,
		Subject: "This is the mail Subject",
		Body: `<!DOCTYPE html>
	<html>
	<body>
	    <div style="border: 2px solid black; padding: 10px;">
	        <h1>This is a heading</h1>
	        <p>This is a paragraph</p>
	        <p style="color: blue; background-color: #f0f0f0;">This is a styled paragraph</p>
	    </div>
	</body>
	</html>`,
	}

	// SimpleSender
	simpleSender := SimpleSender{}
	if err := simpleSender.Send(config, email); err != nil {
		log.Printf("failed to send simple mail: %v", err)
	} else {
		log.Println("simple mail sent")
	}

	// Using AdvancedSender
	advancedSender := AdvancedSender{}
	if err := advancedSender.Send(config, email); err != nil {
		log.Printf("failed to send advanced mail: %v", err)
	} else {
		log.Println("advanced mail sent")
	}

	// Create some example attachments
	attachment1, err := NewAttachmentFromFile("photo.jpg")
	if err != nil {
		log.Printf("failed to create attachment: %v", err)
	}

	attachment2 := Attachment{
		Filename:    "test.txt",
		ContentType: "text/plain",
		Data:        []byte("This is a test attachment content"),
	}

	emailElite := Email{
		From:    mail.Address{Name: "Sender Name", Address: config.Username},
		To:      recipients,
		Subject: "Email with Attachments",
		Body: `<!DOCTYPE html>
<html>
<body>
    <div style="border: 2px solid black; padding: 10px;">
        <h1>Important Message</h1>
        <p>Please find the attached files below.</p>
    </div>
</body>
</html>`,
		Attachments: []Attachment{attachment1, attachment2},
	}

	sender := EliteSender{}
	if err := sender.Send(config, emailElite); err != nil {
		log.Printf("failed to send elite mail: %v", err)
	} else {
		log.Println("elite mail sent")
	}
}
