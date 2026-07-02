package delivery

import (
	"bufio"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"servmail/pkg/storage"
)

// HandleSMTPConnection handles a single SMTP connection and appends captured email to mock repository.
func HandleSMTPConnection(conn net.Conn, mockedEmails *[]storage.MockEmail, mockedEmailsMu *sync.RWMutex) {
	defer conn.Close()
	conn.Write([]byte("220 ServMail Mock SMTP Server Ready\r\n"))

	var email storage.MockEmail
	email.Time = time.Now()

	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)

		if strings.HasPrefix(strings.ToUpper(line), "HELO") || strings.HasPrefix(strings.ToUpper(line), "EHLO") {
			conn.Write([]byte("250 Hello\r\n"))
		} else if strings.HasPrefix(strings.ToUpper(line), "MAIL FROM:") {
			email.From = strings.TrimSpace(strings.TrimPrefix(line, "MAIL FROM:"))
			conn.Write([]byte("250 2.1.0 OK\r\n"))
		} else if strings.HasPrefix(strings.ToUpper(line), "RCPT TO:") {
			email.To = strings.TrimSpace(strings.TrimPrefix(line, "RCPT TO:"))
			conn.Write([]byte("250 2.1.5 OK\r\n"))
		} else if strings.ToUpper(line) == "DATA" {
			conn.Write([]byte("354 Start mail input; end with <CRLF>.<CRLF>\r\n"))

			var bodyBuilder strings.Builder
			for {
				bodyLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if bodyLine == ".\r\n" || bodyLine == ".\n" {
					break
				}
				bodyBuilder.WriteString(bodyLine)
			}

			rawMessage := bodyBuilder.String()
			email.Body = rawMessage

			lines := strings.Split(rawMessage, "\n")
			for _, l := range lines {
				if strings.HasPrefix(strings.ToUpper(l), "SUBJECT:") {
					email.Subject = strings.TrimSpace(strings.TrimPrefix(l, "SUBJECT:"))
					break
				}
			}

			mockedEmailsMu.Lock()
			*mockedEmails = append(*mockedEmails, email)
			mockedEmailsMu.Unlock()

			log.Printf("[SMTP MOCK] Captured email to %s: %s", email.To, email.Subject)
			conn.Write([]byte("250 2.0.0 OK: Message accepted for delivery\r\n"))
		} else if strings.ToUpper(line) == "QUIT" {
			conn.Write([]byte("221 2.0.0 Bye\r\n"))
			return
		} else {
			conn.Write([]byte("250 OK\r\n"))
		}
	}
}
