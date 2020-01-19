// Copyright (c) 2016, Janoš Guljaš <janos@resenje.org>
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package email

import (
	"bufio"
	"bytes"
	"io/ioutil"
	"net"
	"net/mail"
	"strings"
	"sync"
	"testing"
)

type smtpRecorder struct {
	Port    int
	message *smtpMessage
	mu      sync.Mutex
}

func newSMTPRecorder(t *testing.T) (*smtpRecorder, error) {
	l, err := net.Listen("tcp", "")
	if err != nil {
		return nil, err
	}

	recorder := &smtpRecorder{
		Port: l.Addr().(*net.TCPAddr).Port,
	}

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				panic(err)
			}
			go func(conn net.Conn) {
				defer conn.Close()

				reader := bufio.NewReader(conn)
				writer := bufio.NewWriter(conn)

				if _, err := writer.WriteString("220 Welcome\r\n"); err != nil {
					panic(err)
				}
				writer.Flush()

				s, err := reader.ReadString('\n')
				if err != nil {
					panic(err)
				}
				t.Log(strings.TrimSpace(s))

				if _, err := writer.WriteString("250 Hello\r\n"); err != nil {
					panic(err)
				}
				writer.Flush()

				s, err = reader.ReadString('\n')
				if err != nil {
					panic(err)
				}
				t.Log(strings.TrimSpace(s))

				if _, err := writer.WriteString("250 Sender\r\n"); err != nil {
					panic(err)
				}
				writer.Flush()

				s, err = reader.ReadString('\n')
				if err != nil {
					panic(err)
				}
				t.Log(strings.TrimSpace(s))

				for {
					if _, err := writer.WriteString("250 Recipient\r\n"); err != nil {
						panic(err)
					}
					writer.Flush()

					s, err = reader.ReadString('\n')
					if err != nil {
						panic(err)
					}
					s = strings.TrimSpace(s)
					t.Log(s)

					if s == "DATA" {
						break
					}
				}

				if _, err := writer.WriteString("354 OK send data ending with <CRLF>.<CRLF>\r\n"); err != nil {
					panic(err)
				}
				writer.Flush()
				data := []byte{}
				for {
					d, err := reader.ReadSlice('\n')
					if err != nil {
						panic(err)
					}
					if d[0] == 46 && d[1] == 13 && d[2] == 10 {
						break
					}
					data = append(data, d...)
				}

				if _, err := writer.WriteString("250 Server has transmitted the message\n\r"); err != nil {
					panic(err)
				}
				writer.Flush()

				m, err := mail.ReadMessage(bytes.NewReader(data))
				if err != nil {
					panic(err)
				}

				t.Log("Date:", m.Header.Get("Date"))
				t.Log("From:", m.Header.Get("From"))
				t.Log("To:", m.Header.Get("To"))
				t.Log("Reply-To:", m.Header.Get("Reply-To"))
				t.Log("Subject:", m.Header.Get("Subject"))

				body, err := ioutil.ReadAll(m.Body)
				if err != nil {
					panic(err)
				}
				t.Logf("%s", body)

				message := smtpMessage{}
				from, err := m.Header.AddressList("From")
				if err != nil {
					panic(err)
				}
				if len(from) > 0 {
					message.From = from[0]
				}
				message.To, err = m.Header.AddressList("To")
				if err != nil {
					panic(err)
				}
				message.ReplyTo, err = m.Header.AddressList("Reply-To")
				if err != nil && err != mail.ErrHeaderNotPresent {
					panic(err)
				}
				message.Subject = m.Header.Get("Subject")
				message.Body = string(body)

				recorder.SetMessage(&message)
			}(conn)
		}
	}()

	return recorder, nil
}

func (r *smtpRecorder) Message() *smtpMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.message
}

func (r *smtpRecorder) SetMessage(m *smtpMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.message = m
}

type smtpMessage struct {
	From    *mail.Address
	To      []*mail.Address
	ReplyTo []*mail.Address
	Subject string
	Body    string
}

func TestService(t *testing.T) {
	recorder, err := newSMTPRecorder(t)
	if err != nil {
		t.Fatalf("smtp listen: %s", err)
	}

	from := `"Gopher" <gopher@gopherpit.com>`
	defaultFrom := `noreply@gopherpit.com`
	to := []string{`"GopherPit Support" <support@gopherpit.com>`, "contact@gopherpit.com"}
	replyTo := []string{`"GopherPit Operations" <operations@gopherpit.com>`, "archive@gopherpit.com"}
	notifyTo := []string{`"GopherPit Operations" <operations@gopherpit.com>`}
	subject := "test subject"
	body := "test body"

	service := Service{
		SMTPHost:        "localhost",
		SMTPPort:        recorder.Port,
		SMTPSkipVerify:  true,
		NotifyAddresses: notifyTo,
		DefaultFrom:     defaultFrom,
	}

	t.Run("SendEmail", func(t *testing.T) {
		if err := service.SendEmail(from, to, subject, body); err != nil {
			t.Errorf("send email: %s", err)
		}

		recordedFrom := recorder.Message().From.String()
		if recordedFrom != from && recordedFrom != "<"+defaultFrom+">" {
			t.Errorf("message from: expected %s, got %s", from, recordedFrom)
		}

		for _, pt := range to {
			found := false
			for _, rt := range recorder.Message().To {
				if pt == rt.String() || "<"+pt+">" == rt.String() {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("recipient not found %s", pt)
			}
		}

		recordedSubject := recorder.Message().Subject
		if recordedSubject != subject {
			t.Errorf(`message subject: expected "%s", got "%s"`, subject, recordedSubject)
		}

		recordedBody := recorder.Message().Body
		if recordedBody != body+"\r\n" {
			t.Errorf(`message body: expected "%v", got "%v"`, body, recordedBody)
		}
	})

	t.Run("SendEmailWithHeaders", func(t *testing.T) {
		if err := service.SendEmailWithHeaders(from, to, subject, body, map[string][]string{
			"Reply-To": replyTo,
		}); err != nil {
			t.Errorf("send email: %s", err)
		}

		recordedFrom := recorder.Message().From.String()
		if recordedFrom != from && recordedFrom != "<"+defaultFrom+">" {
			t.Errorf("message from: expected %s, got %s", from, recordedFrom)
		}

		for _, pt := range to {
			found := false
			for _, rt := range recorder.Message().To {
				if pt == rt.String() || "<"+pt+">" == rt.String() {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("recipient not found %s", pt)
			}
		}

		for _, pt := range replyTo {
			found := false
			for _, rt := range recorder.Message().ReplyTo {
				if pt == rt.String() || "<"+pt+">" == rt.String() {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("reply to recipient not found %s", pt)
			}
		}

		recordedSubject := recorder.Message().Subject
		if recordedSubject != subject {
			t.Errorf(`message subject: expected "%s", got "%s"`, subject, recordedSubject)
		}

		recordedBody := recorder.Message().Body
		if recordedBody != body+"\r\n" {
			t.Errorf(`message body: expected "%v", got "%v"`, body, recordedBody)
		}
	})

	t.Run("Notify", func(t *testing.T) {
		if err := service.Notify(subject, body); err != nil {
			t.Errorf("send email: %s", err)
		}

		recordedFrom := recorder.Message().From.String()
		if recordedFrom != defaultFrom && recordedFrom != "<"+defaultFrom+">" {
			t.Errorf("message from: expected %s, got %s", defaultFrom, recordedFrom)
		}

		for _, pt := range notifyTo {
			found := false
			for _, rt := range recorder.Message().To {
				if pt == rt.String() || "<"+pt+">" == rt.String() {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("recipient not found %s", pt)
			}
		}

		recordedSubject := recorder.Message().Subject
		if recordedSubject != subject {
			t.Errorf(`message subject: expected "%s", got "%s"`, subject, recordedSubject)
		}

		recordedBody := recorder.Message().Body
		if recordedBody != body+"\r\n" {
			t.Errorf(`message body: expected "%v", got "%v"`, body, recordedBody)
		}
	})

	t.Run("NotifyWithHeaders", func(t *testing.T) {
		if err := service.NotifyWithHeaders(subject, body, map[string][]string{
			"Reply-To": replyTo,
		}); err != nil {
			t.Errorf("send email: %s", err)
		}

		recordedFrom := recorder.Message().From.String()
		if recordedFrom != defaultFrom && recordedFrom != "<"+defaultFrom+">" {
			t.Errorf("message from: expected %s, got %s", defaultFrom, recordedFrom)
		}

		for _, pt := range notifyTo {
			found := false
			for _, rt := range recorder.Message().To {
				if pt == rt.String() || "<"+pt+">" == rt.String() {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("recipient not found %s", pt)
			}
		}

		for _, pt := range notifyTo {
			found := false
			for _, rt := range recorder.Message().To {
				if pt == rt.String() || "<"+pt+">" == rt.String() {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("recipient not found %s", pt)
			}
		}

		recordedSubject := recorder.Message().Subject
		if recordedSubject != subject {
			t.Errorf(`message subject: expected "%s", got "%s"`, subject, recordedSubject)
		}

		recordedBody := recorder.Message().Body
		if recordedBody != body+"\r\n" {
			t.Errorf(`message body: expected "%v", got "%v"`, body, recordedBody)
		}
	})

	t.Run("NotifyNoOp", func(t *testing.T) {
		recorder.SetMessage(nil)
		service.NotifyAddresses = nil
		if err := service.Notify(subject, body); err != nil {
			t.Errorf("send email: %s", err)
		}
		if recorder.Message() != nil {
			t.Errorf("expected no-op, but message %#v has been recorded", recorder.Message())
		}
	})
}
